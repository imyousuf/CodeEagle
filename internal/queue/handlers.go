package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/docs"
	"github.com/imyousuf/CodeEagle/internal/graph"
	genericparser "github.com/imyousuf/CodeEagle/internal/parser/generic"
)

// DocExtractHandler handles document text extraction and topic analysis.
type DocExtractHandler struct {
	provider docs.Provider
	cache    *docs.Cache
	store    graph.Store
}

// NewDocExtractHandler creates a handler for doc-extract jobs.
func NewDocExtractHandler(provider docs.Provider, cache *docs.Cache, store graph.Store) *DocExtractHandler {
	return &DocExtractHandler{
		provider: provider,
		cache:    cache,
		store:    store,
	}
}

// Handle processes a doc-extract job.
func (h *DocExtractHandler) Handle(ctx context.Context, job *Job) (json.RawMessage, error) {
	if len(job.FilePaths) == 0 {
		return nil, fmt.Errorf("no file paths in job")
	}

	// Check cache first.
	if h.cache != nil {
		cached, err := h.cache.Check(job.FilePaths[0], job.ContentHash)
		if err == nil && cached != nil {
			h.updateNodes(ctx, job, cached)
			return marshalResult(cached)
		}
		if h.cache.IsSkipped(job.ContentHash) {
			return nil, nil
		}
	}

	// Read file content.
	content, err := os.ReadFile(job.FilePaths[0])
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	// Extract text based on file type.
	fileClass := genericparser.Classify(job.FilePaths[0], nil)
	var text string
	switch fileClass {
	case genericparser.FileClassDocument:
		text, err = genericparser.ExtractDocument(job.FilePaths[0], content)
		if err != nil {
			if h.cache != nil {
				_ = h.cache.MarkSkipped(job.FilePaths[0], job.ContentHash)
			}
			return nil, fmt.Errorf("extract document: %w", err)
		}
	default:
		text = genericparser.ExtractText(job.FilePaths[0], content)
	}

	if strings.TrimSpace(text) == "" {
		return nil, nil
	}

	// Call LLM provider.
	if h.provider == nil {
		return nil, nil
	}

	result, err := h.provider.ExtractTopics(ctx, text)
	if err != nil {
		if h.cache != nil {
			_ = h.cache.MarkSkipped(job.FilePaths[0], job.ContentHash)
		}
		return nil, fmt.Errorf("extract topics: %w", err)
	}

	// Store in cache.
	if h.cache != nil {
		_ = h.cache.Store(job.FilePaths[0], job.ContentHash, result)
	}

	// Update graph nodes.
	h.updateNodes(ctx, job, result)
	return marshalResult(result)
}

// updateNodes updates all NodeDocument nodes sharing this content hash with extraction results.
func (h *DocExtractHandler) updateNodes(ctx context.Context, job *Job, result *docs.ExtractionResult) {
	for _, filePath := range job.FilePaths {
		nodes, err := h.store.QueryNodes(ctx, graph.NodeFilter{
			Type:     graph.NodeDocument,
			FilePath: filePath,
		})
		if err != nil || len(nodes) == 0 {
			continue
		}
		for _, node := range nodes {
			applyExtraction(ctx, h.store, node, result)
		}
	}
}

// ImageDescribeHandler handles image description via LLM.
type ImageDescribeHandler struct {
	provider    docs.Provider
	cache       *docs.Cache
	store       graph.Store
	maxImageRes int
}

// NewImageDescribeHandler creates a handler for image-describe jobs.
func NewImageDescribeHandler(provider docs.Provider, cache *docs.Cache, store graph.Store, maxImageRes int) *ImageDescribeHandler {
	if maxImageRes <= 0 {
		maxImageRes = 1024
	}
	return &ImageDescribeHandler{
		provider:    provider,
		cache:       cache,
		store:       store,
		maxImageRes: maxImageRes,
	}
}

// Handle processes an image-describe job.
func (h *ImageDescribeHandler) Handle(ctx context.Context, job *Job) (json.RawMessage, error) {
	if len(job.FilePaths) == 0 {
		return nil, fmt.Errorf("no file paths in job")
	}

	// Check cache first.
	if h.cache != nil {
		cached, err := h.cache.Check(job.FilePaths[0], job.ContentHash)
		if err == nil && cached != nil {
			h.updateNodes(ctx, job, cached)
			return marshalResult(cached)
		}
		if h.cache.IsSkipped(job.ContentHash) {
			return nil, nil
		}
	}

	if h.provider == nil {
		return nil, nil
	}

	// Read image file.
	content, err := os.ReadFile(job.FilePaths[0])
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	// Detect MIME type and downscale.
	mimeType := detectMIME(job.FilePaths[0])
	imgData, _, _, err := genericparser.DownscaleImage(content, mimeType, h.maxImageRes)
	if err != nil {
		if h.cache != nil {
			_ = h.cache.MarkSkipped(job.FilePaths[0], job.ContentHash)
		}
		return nil, fmt.Errorf("downscale image: %w", err)
	}

	// Call LLM provider.
	result, err := h.provider.DescribeImage(ctx, imgData, "image/jpeg")
	if err != nil {
		if h.cache != nil {
			_ = h.cache.MarkSkipped(job.FilePaths[0], job.ContentHash)
		}
		return nil, fmt.Errorf("describe image: %w", err)
	}

	// Store in cache.
	if h.cache != nil {
		_ = h.cache.Store(job.FilePaths[0], job.ContentHash, result)
	}

	// Update graph nodes.
	h.updateNodes(ctx, job, result)
	return marshalResult(result)
}

// updateNodes updates all NodeDocument nodes sharing this content hash.
func (h *ImageDescribeHandler) updateNodes(ctx context.Context, job *Job, result *docs.ExtractionResult) {
	for _, filePath := range job.FilePaths {
		nodes, err := h.store.QueryNodes(ctx, graph.NodeFilter{
			Type:     graph.NodeDocument,
			FilePath: filePath,
		})
		if err != nil || len(nodes) == 0 {
			continue
		}
		for _, node := range nodes {
			applyExtraction(ctx, h.store, node, result)
		}
	}
}

// applyExtraction updates a node with extraction results and creates topic edges.
func applyExtraction(ctx context.Context, store graph.Store, node *graph.Node, result *docs.ExtractionResult) {
	node.DocComment = result.Summary
	if node.Properties == nil {
		node.Properties = make(map[string]string)
	}
	if len(result.Topics) > 0 {
		node.Properties["topics"] = strings.Join(result.Topics, ",")
	}
	if len(result.Entities) > 0 {
		node.Properties["entities"] = strings.Join(result.Entities, ",")
	}
	node.Properties["extraction_status"] = "success"
	_ = store.UpdateNode(ctx, node)

	topicNodes, topicEdges := genericparser.CreateTopicNodes(result.Topics, node.ID)
	for _, tn := range topicNodes {
		_ = store.AddNode(ctx, tn)
	}
	for _, te := range topicEdges {
		_ = store.AddEdge(ctx, te)
	}
}

// detectMIME returns the MIME type for a file based on its extension.
func detectMIME(filePath string) string {
	ext := filepath.Ext(filePath)
	if ext == "" {
		return "application/octet-stream"
	}
	mt := mime.TypeByExtension(ext)
	if mt == "" {
		switch strings.ToLower(ext) {
		case ".webp":
			return "image/webp"
		case ".bmp":
			return "image/bmp"
		case ".tiff", ".tif":
			return "image/tiff"
		default:
			return "application/octet-stream"
		}
	}
	return mt
}

// marshalResult converts an ExtractionResult to JSON.
func marshalResult(result *docs.ExtractionResult) (json.RawMessage, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}
