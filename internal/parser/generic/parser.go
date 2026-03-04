package generic

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"mime"
	"path/filepath"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/docs"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

const (
	// LangGeneric is the language identifier for generic (non-code) files.
	LangGeneric parser.Language = "generic"
)

// GenericParser handles non-code files (text and images) that have no
// registered language parser. It creates NodeDocument nodes with raw text
// as DocComment and builds directory hierarchy nodes.
//
// When a docs.Provider is available, it uses LLM-based topic extraction
// to create NodeTopic nodes and EdgeHasTopic edges.
type GenericParser struct {
	excludeExts  []string
	docsProvider docs.Provider
	docsCache    *docs.Cache
	maxImageRes  int
}

// NewGenericParser creates a new GenericParser.
// docsProvider and docsCache may be nil (graceful degradation to raw text).
// maxImageRes is the max longest-edge resolution for image downscaling (default 1024).
func NewGenericParser(excludeExts []string, docsProvider docs.Provider, docsCache *docs.Cache, maxImageRes int) *GenericParser {
	if maxImageRes <= 0 {
		maxImageRes = 1024
	}
	return &GenericParser{
		excludeExts:  excludeExts,
		docsProvider: docsProvider,
		docsCache:    docsCache,
		maxImageRes:  maxImageRes,
	}
}

// Language returns the language identifier.
func (p *GenericParser) Language() parser.Language {
	return LangGeneric
}

// Extensions returns an empty slice — the generic parser is used as a fallback,
// not matched by extension.
func (p *GenericParser) Extensions() []string {
	return nil
}

// ParseFile parses a non-code file and returns document + directory nodes.
func (p *GenericParser) ParseFile(filePath string, content []byte) (*parser.ParseResult, error) {
	class := Classify(filePath, p.excludeExts)
	if class == FileClassSkip {
		return &parser.ParseResult{FilePath: filePath, Language: LangGeneric}, nil
	}

	// Check content for binary files that passed extension check.
	if class == FileClassText && ClassifyContent(content) == FileClassSkip {
		return &parser.ParseResult{FilePath: filePath, Language: LangGeneric}, nil
	}

	result := &parser.ParseResult{
		FilePath: filePath,
		Language: LangGeneric,
	}

	// Build the document node.
	fileName := filepath.Base(filePath)
	nodeID := graph.NewNodeID(string(graph.NodeDocument), filePath, fileName)
	contentHash := fmt.Sprintf("sha256:%x", sha256.Sum256(content))

	// Determine kind and MIME type.
	kind := "text"
	mimeType := detectMIMEType(filePath)
	if class == FileClassImage {
		kind = "image"
	}

	docNode := &graph.Node{
		ID:            nodeID,
		Type:          graph.NodeDocument,
		Name:          fileName,
		QualifiedName: filePath,
		FilePath:      filePath,
		Package:       filepath.Dir(filePath),
		Properties: map[string]string{
			"content_hash":  contentHash,
			"mime_type":     mimeType,
			"kind":          kind,
			"original_size": fmt.Sprintf("%d", len(content)),
		},
	}

	// Try LLM-based extraction for text files.
	switch class {
	case FileClassText:
		extraction := p.extractTopics(filePath, contentHash, content)
		if extraction != nil {
			docNode.DocComment = extraction.Summary
			topicNodes, topicEdges := CreateTopicNodes(extraction.Topics, nodeID)
			result.Nodes = append(result.Nodes, topicNodes...)
			result.Edges = append(result.Edges, topicEdges...)
		} else {
			docNode.DocComment = ExtractText(filePath, content)
		}
	case FileClassImage:
		extraction := p.describeImage(filePath, contentHash, content, mimeType)
		if extraction != nil {
			docNode.DocComment = extraction.Summary
			topicNodes, topicEdges := CreateTopicNodes(extraction.Topics, nodeID)
			result.Nodes = append(result.Nodes, topicNodes...)
			result.Edges = append(result.Edges, topicEdges...)
		} else {
			docNode.DocComment = fmt.Sprintf("Image file: %s (%s, %d bytes)", fileName, mimeType, len(content))
		}
	}

	result.Nodes = append(result.Nodes, docNode)

	// Build directory hierarchy.
	dirSeen := make(map[string]bool)
	dirNodes, dirEdges := EnsureDirectoryHierarchy(filePath, dirSeen)
	result.Nodes = append(result.Nodes, dirNodes...)
	result.Edges = append(result.Edges, dirEdges...)

	return result, nil
}

// extractTopics tries to extract topics via LLM, using the cache for dedup.
// Returns nil if no provider is available or extraction fails.
func (p *GenericParser) extractTopics(filePath, contentHash string, content []byte) *docs.ExtractionResult {
	if p.docsProvider == nil {
		return nil
	}

	// Check cache first.
	if p.docsCache != nil {
		cached, err := p.docsCache.Check(filePath, contentHash)
		if err == nil && cached != nil {
			return cached
		}
		if p.docsCache.IsSkipped(contentHash) {
			return nil // previously failed, skip
		}
	}

	text := ExtractText(filePath, content)
	if len(strings.TrimSpace(text)) < 50 {
		return nil // too short for meaningful extraction
	}

	ctx := context.TODO()
	extraction, err := p.docsProvider.ExtractTopics(ctx, text)
	if err != nil {
		if p.docsCache != nil {
			_ = p.docsCache.MarkSkipped(filePath, contentHash)
		}
		log.Printf("[docs] extraction failed for %s: %v", filePath, err)
		return nil
	}

	// Cache the result.
	if p.docsCache != nil {
		_ = p.docsCache.Store(filePath, contentHash, extraction)
	}

	return extraction
}

// describeImage tries to describe an image via LLM, using the cache for dedup.
// Returns nil if no provider is available or description fails.
func (p *GenericParser) describeImage(filePath, contentHash string, content []byte, mimeType string) *docs.ExtractionResult {
	if p.docsProvider == nil {
		return nil
	}

	// Check cache first.
	if p.docsCache != nil {
		cached, err := p.docsCache.Check(filePath, contentHash)
		if err == nil && cached != nil {
			return cached
		}
		if p.docsCache.IsSkipped(contentHash) {
			return nil
		}
	}

	// Downscale image for LLM consumption.
	imgData, _, _, err := downscaleImage(content, mimeType, p.maxImageRes)
	if err != nil {
		log.Printf("[docs] image downscale failed for %s: %v", filePath, err)
		return nil
	}

	ctx := context.TODO()
	extraction, err := p.docsProvider.DescribeImage(ctx, imgData, "image/jpeg")
	if err != nil {
		if p.docsCache != nil {
			_ = p.docsCache.MarkSkipped(filePath, contentHash)
		}
		log.Printf("[docs] image description failed for %s: %v", filePath, err)
		return nil
	}

	// Cache the result.
	if p.docsCache != nil {
		_ = p.docsCache.Store(filePath, contentHash, extraction)
	}

	return extraction
}

// detectMIMEType returns the MIME type for a file path based on extension.
func detectMIMEType(filePath string) string {
	ext := filepath.Ext(filePath)
	if ext == "" {
		return "text/plain"
	}

	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		ext = strings.ToLower(ext)
		switch ext {
		case ".svg":
			return "image/svg+xml"
		case ".csv":
			return "text/csv"
		case ".tsv":
			return "text/tab-separated-values"
		case ".json":
			return "application/json"
		case ".yaml", ".yml":
			return "application/x-yaml"
		case ".toml":
			return "application/toml"
		case ".log":
			return "text/plain"
		default:
			return "application/octet-stream"
		}
	}
	return mimeType
}
