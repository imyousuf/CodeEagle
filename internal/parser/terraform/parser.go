package terraform

import (
	"context"
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/hcl"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

// TerraformParser extracts knowledge graph nodes and edges from Terraform files.
type TerraformParser struct{}

// NewParser creates a new Terraform parser.
func NewParser() *TerraformParser {
	return &TerraformParser{}
}

func (p *TerraformParser) Language() parser.Language {
	return parser.LangTerraform
}

func (p *TerraformParser) Extensions() []string {
	return parser.FileExtensions[parser.LangTerraform]
}

func (p *TerraformParser) ParseFile(filePath string, content []byte) (*parser.ParseResult, error) {
	lang := hcl.GetLanguage()
	sitterParser := sitter.NewParser()
	sitterParser.SetLanguage(lang)

	tree, err := sitterParser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filePath, err)
	}

	e := &extractor{
		filePath:    filePath,
		content:     content,
		tree:        tree,
		providerIDs: make(map[string]string),
	}
	e.extract()

	return &parser.ParseResult{
		Nodes:    e.nodes,
		Edges:    e.edges,
		FilePath: filePath,
		Language: parser.LangTerraform,
	}, nil
}

type extractor struct {
	filePath string
	content  []byte
	tree     *sitter.Tree
	nodes    []*graph.Node
	edges    []*graph.Edge

	fileNodeID  string
	providerIDs map[string]string // provider name -> node ID
}

func (e *extractor) extract() {
	e.extractFileNode()

	root := e.tree.RootNode()
	// config_file -> body -> blocks
	if root.ChildCount() == 0 {
		return
	}
	body := root.Child(0)
	if body == nil || body.Type() != "body" {
		return
	}

	e.walkBody(body)
}

func (e *extractor) extractFileNode() {
	e.fileNodeID = graph.NewNodeID(string(graph.NodeFile), e.filePath, e.filePath)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       e.fileNodeID,
		Type:     graph.NodeFile,
		Name:     e.filePath,
		FilePath: e.filePath,
		Language: string(parser.LangTerraform),
	})
}

func (e *extractor) walkBody(body *sitter.Node) {
	for i := 0; i < int(body.ChildCount()); i++ {
		child := body.Child(i)
		if child.Type() == "block" {
			e.extractBlock(child)
		} else if child.Type() == "attribute" {
			// Top-level attributes in .tfvars files.
			e.extractTfvar(child)
		}
	}
}

func (e *extractor) extractBlock(block *sitter.Node) {
	// A block has: identifier [string_lit]* block_start body block_end
	blockType := ""
	var labels []string
	var bodyNode *sitter.Node

	for i := 0; i < int(block.ChildCount()); i++ {
		child := block.Child(i)
		switch child.Type() {
		case "identifier":
			if blockType == "" {
				blockType = e.nodeText(child)
			}
		case "string_lit":
			labels = append(labels, extractStringLit(child, e.content))
		case "body":
			bodyNode = child
		}
	}

	startLine := int(block.StartPoint().Row) + 1
	endLine := int(block.EndPoint().Row) + 1

	switch blockType {
	case "resource":
		e.extractResource(labels, bodyNode, startLine, endLine)
	case "data":
		e.extractDataSource(labels, bodyNode, startLine, endLine)
	case "module":
		e.extractModule(labels, bodyNode, startLine, endLine)
	case "variable":
		e.extractVariable(labels, bodyNode, startLine, endLine)
	case "output":
		e.extractOutput(labels, bodyNode, startLine, endLine)
	case "provider":
		e.extractProvider(labels, bodyNode, startLine, endLine)
	case "locals":
		e.extractLocals(bodyNode, startLine)
	case "terraform":
		e.extractTerraformBlock(bodyNode, startLine)
	}
}

func (e *extractor) extractResource(labels []string, body *sitter.Node, startLine, endLine int) {
	if len(labels) < 2 {
		return
	}
	resourceType := labels[0]
	resourceName := labels[1]
	fullName := resourceType + "." + resourceName

	attrs := extractAttributes(body, e.content)

	nodeID := graph.NewNodeID(string(graph.NodeStruct), e.filePath, fullName)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       nodeID,
		Type:     graph.NodeStruct,
		Name:     fullName,
		FilePath: e.filePath,
		Line:     startLine,
		EndLine:  endLine,
		Language: string(parser.LangTerraform),
		Exported: true,
		Properties: map[string]string{
			"kind":          "resource",
			"resource_type": resourceType,
		},
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.fileNodeID, nodeID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.fileNodeID,
		TargetID: nodeID,
	})

	// Link resource to its provider (implicit from type prefix).
	providerName := strings.SplitN(resourceType, "_", 2)[0]
	e.linkToProvider(nodeID, providerName, attrs)
}

func (e *extractor) extractDataSource(labels []string, body *sitter.Node, startLine, endLine int) {
	if len(labels) < 2 {
		return
	}
	dataType := labels[0]
	dataName := labels[1]
	fullName := "data." + dataType + "." + dataName

	attrs := extractAttributes(body, e.content)

	nodeID := graph.NewNodeID(string(graph.NodeStruct), e.filePath, fullName)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       nodeID,
		Type:     graph.NodeStruct,
		Name:     fullName,
		FilePath: e.filePath,
		Line:     startLine,
		EndLine:  endLine,
		Language: string(parser.LangTerraform),
		Exported: true,
		Properties: map[string]string{
			"kind":          "data_source",
			"resource_type": dataType,
		},
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.fileNodeID, nodeID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.fileNodeID,
		TargetID: nodeID,
	})

	// Link data source to its provider.
	providerName := strings.SplitN(dataType, "_", 2)[0]
	e.linkToProvider(nodeID, providerName, attrs)
}

func (e *extractor) extractModule(labels []string, body *sitter.Node, startLine, endLine int) {
	if len(labels) < 1 {
		return
	}
	moduleName := labels[0]

	attrs := extractAttributes(body, e.content)
	source := attrs["source"]

	props := map[string]string{
		"kind": "module",
	}
	if source != "" {
		props["source"] = source
	}

	nodeID := graph.NewNodeID(string(graph.NodeModule), e.filePath, moduleName)
	e.nodes = append(e.nodes, &graph.Node{
		ID:         nodeID,
		Type:       graph.NodeModule,
		Name:       moduleName,
		FilePath:   e.filePath,
		Line:       startLine,
		EndLine:    endLine,
		Language:   string(parser.LangTerraform),
		Exported:   true,
		Properties: props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.fileNodeID, nodeID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.fileNodeID,
		TargetID: nodeID,
	})

	// Link module to its source.
	if source != "" {
		if strings.HasPrefix(source, ".") {
			// Local module reference.
			depID := graph.NewNodeID(string(graph.NodeModule), e.filePath, "module_source:"+source)
			e.nodes = append(e.nodes, &graph.Node{
				ID:       depID,
				Type:     graph.NodeModule,
				Name:     source,
				FilePath: e.filePath,
				Line:     startLine,
				Language: string(parser.LangTerraform),
				Properties: map[string]string{
					"kind": "module_source",
				},
			})
			e.edges = append(e.edges, &graph.Edge{
				ID:       edgeID(nodeID, depID, string(graph.EdgeDependsOn)),
				Type:     graph.EdgeDependsOn,
				SourceID: nodeID,
				TargetID: depID,
			})
		} else {
			// Remote module reference (registry, git, etc.).
			depID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, "module_source:"+source)
			e.nodes = append(e.nodes, &graph.Node{
				ID:       depID,
				Type:     graph.NodeDependency,
				Name:     source,
				FilePath: e.filePath,
				Line:     startLine,
				Language: string(parser.LangTerraform),
				Properties: map[string]string{
					"kind": "module_source",
				},
			})
			e.edges = append(e.edges, &graph.Edge{
				ID:       edgeID(nodeID, depID, string(graph.EdgeDependsOn)),
				Type:     graph.EdgeDependsOn,
				SourceID: nodeID,
				TargetID: depID,
			})
		}
	}
}

func (e *extractor) extractVariable(labels []string, body *sitter.Node, startLine, endLine int) {
	if len(labels) < 1 {
		return
	}
	varName := labels[0]

	attrs := extractAttributes(body, e.content)

	props := map[string]string{
		"kind": "terraform_var",
	}
	if v, ok := attrs["type"]; ok {
		props["var_type"] = v
	}
	if v, ok := attrs["default"]; ok {
		props["default"] = v
	}

	desc := ""
	if v, ok := attrs["description"]; ok {
		desc = v
	}

	nodeID := graph.NewNodeID(string(graph.NodeVariable), e.filePath, varName)
	e.nodes = append(e.nodes, &graph.Node{
		ID:         nodeID,
		Type:       graph.NodeVariable,
		Name:       varName,
		FilePath:   e.filePath,
		Line:       startLine,
		EndLine:    endLine,
		Language:   string(parser.LangTerraform),
		Exported:   true,
		DocComment: desc,
		Properties: props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.fileNodeID, nodeID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.fileNodeID,
		TargetID: nodeID,
	})
}

func (e *extractor) extractOutput(labels []string, body *sitter.Node, startLine, endLine int) {
	if len(labels) < 1 {
		return
	}
	outputName := labels[0]

	attrs := extractAttributes(body, e.content)

	desc := ""
	if v, ok := attrs["description"]; ok {
		desc = v
	}

	props := map[string]string{
		"kind": "output",
	}

	nodeID := graph.NewNodeID(string(graph.NodeConstant), e.filePath, outputName)
	e.nodes = append(e.nodes, &graph.Node{
		ID:         nodeID,
		Type:       graph.NodeConstant,
		Name:       outputName,
		FilePath:   e.filePath,
		Line:       startLine,
		EndLine:    endLine,
		Language:   string(parser.LangTerraform),
		Exported:   true,
		DocComment: desc,
		Properties: props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.fileNodeID, nodeID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.fileNodeID,
		TargetID: nodeID,
	})
}

func (e *extractor) extractProvider(labels []string, body *sitter.Node, startLine, endLine int) {
	if len(labels) < 1 {
		return
	}
	providerName := labels[0]

	attrs := extractAttributes(body, e.content)

	props := map[string]string{
		"kind": "provider",
	}
	if v, ok := attrs["version"]; ok {
		props["version"] = v
	}
	if v, ok := attrs["region"]; ok {
		props["region"] = v
	}
	// Check for alias.
	if v, ok := attrs["alias"]; ok {
		props["alias"] = v
	}

	nodeID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, "provider:"+providerName)
	e.providerIDs[providerName] = nodeID

	e.nodes = append(e.nodes, &graph.Node{
		ID:         nodeID,
		Type:       graph.NodeDependency,
		Name:       providerName,
		FilePath:   e.filePath,
		Line:       startLine,
		EndLine:    endLine,
		Language:   string(parser.LangTerraform),
		Exported:   true,
		Properties: props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.fileNodeID, nodeID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.fileNodeID,
		TargetID: nodeID,
	})
}

func (e *extractor) extractLocals(body *sitter.Node, startLine int) {
	if body == nil {
		return
	}
	for i := 0; i < int(body.ChildCount()); i++ {
		child := body.Child(i)
		if child.Type() != "attribute" {
			continue
		}
		name := ""
		value := ""
		for j := 0; j < int(child.ChildCount()); j++ {
			attr := child.Child(j)
			switch attr.Type() {
			case "identifier":
				name = e.nodeText(attr)
			case "expression":
				value = e.nodeText(attr)
			}
		}
		if name == "" {
			continue
		}

		line := int(child.StartPoint().Row) + 1
		props := map[string]string{
			"kind": "local",
		}
		if value != "" {
			props["value"] = value
		}

		nodeID := graph.NewNodeID(string(graph.NodeVariable), e.filePath, "local."+name)
		e.nodes = append(e.nodes, &graph.Node{
			ID:         nodeID,
			Type:       graph.NodeVariable,
			Name:       "local." + name,
			FilePath:   e.filePath,
			Line:       line,
			Language:   string(parser.LangTerraform),
			Exported:   true,
			Properties: props,
		})

		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(e.fileNodeID, nodeID, string(graph.EdgeContains)),
			Type:     graph.EdgeContains,
			SourceID: e.fileNodeID,
			TargetID: nodeID,
		})
	}
}

func (e *extractor) extractTerraformBlock(body *sitter.Node, startLine int) {
	if body == nil {
		return
	}
	// Look for required_providers sub-block.
	for i := 0; i < int(body.ChildCount()); i++ {
		child := body.Child(i)
		if child.Type() != "block" {
			continue
		}
		blockType := ""
		var subBody *sitter.Node
		for j := 0; j < int(child.ChildCount()); j++ {
			sub := child.Child(j)
			switch sub.Type() {
			case "identifier":
				if blockType == "" {
					blockType = e.nodeText(sub)
				}
			case "body":
				subBody = sub
			}
		}
		if blockType == "required_providers" && subBody != nil {
			e.extractRequiredProviders(subBody, startLine)
		}
	}
}

func (e *extractor) extractRequiredProviders(body *sitter.Node, startLine int) {
	for i := 0; i < int(body.ChildCount()); i++ {
		child := body.Child(i)
		if child.Type() != "attribute" {
			continue
		}
		providerName := ""
		for j := 0; j < int(child.ChildCount()); j++ {
			attr := child.Child(j)
			if attr.Type() == "identifier" {
				providerName = e.nodeText(attr)
				break
			}
		}
		if providerName == "" {
			continue
		}

		line := int(child.StartPoint().Row) + 1
		nodeID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, "required_provider:"+providerName)
		e.nodes = append(e.nodes, &graph.Node{
			ID:       nodeID,
			Type:     graph.NodeDependency,
			Name:     providerName,
			FilePath: e.filePath,
			Line:     line,
			Language: string(parser.LangTerraform),
			Exported: true,
			Properties: map[string]string{
				"kind": "required_provider",
			},
		})

		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(e.fileNodeID, nodeID, string(graph.EdgeContains)),
			Type:     graph.EdgeContains,
			SourceID: e.fileNodeID,
			TargetID: nodeID,
		})
	}
}

func (e *extractor) extractTfvar(attr *sitter.Node) {
	name := ""
	value := ""
	for i := 0; i < int(attr.ChildCount()); i++ {
		child := attr.Child(i)
		switch child.Type() {
		case "identifier":
			name = e.nodeText(child)
		case "expression":
			value = extractExpressionValue(child, e.content)
		}
	}
	if name == "" {
		return
	}

	line := int(attr.StartPoint().Row) + 1
	nodeID := graph.NewNodeID(string(graph.NodeVariable), e.filePath, name)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       nodeID,
		Type:     graph.NodeVariable,
		Name:     name,
		FilePath: e.filePath,
		Line:     line,
		Language: string(parser.LangTerraform),
		Exported: true,
		Properties: map[string]string{
			"kind":  "terraform_var",
			"value": value,
		},
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.fileNodeID, nodeID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.fileNodeID,
		TargetID: nodeID,
	})
}

func (e *extractor) linkToProvider(resourceNodeID, providerName string, attrs map[string]string) {
	// Check for explicit provider attribute.
	if explicit, ok := attrs["provider"]; ok {
		parts := strings.SplitN(explicit, ".", 2)
		providerName = parts[0]
	}

	if providerID, ok := e.providerIDs[providerName]; ok {
		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(resourceNodeID, providerID, string(graph.EdgeDependsOn)),
			Type:     graph.EdgeDependsOn,
			SourceID: resourceNodeID,
			TargetID: providerID,
		})
	}
}

// extractStringLit extracts the text content from an HCL string_lit node.
func extractStringLit(node *sitter.Node, src []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "template_literal" {
			return child.Content(src)
		}
	}
	return ""
}

// extractAttributes collects key-value pairs from an HCL body node.
func extractAttributes(body *sitter.Node, src []byte) map[string]string {
	attrs := make(map[string]string)
	if body == nil {
		return attrs
	}

	for i := 0; i < int(body.ChildCount()); i++ {
		child := body.Child(i)
		if child.Type() != "attribute" {
			continue
		}
		key := ""
		value := ""
		for j := 0; j < int(child.ChildCount()); j++ {
			attr := child.Child(j)
			switch attr.Type() {
			case "identifier":
				key = attr.Content(src)
			case "expression":
				value = extractExpressionValue(attr, src)
			}
		}
		if key != "" {
			attrs[key] = value
		}
	}
	return attrs
}

// extractExpressionValue extracts a simple value from an HCL expression.
func extractExpressionValue(expr *sitter.Node, src []byte) string {
	if expr.ChildCount() == 0 {
		return expr.Content(src)
	}
	child := expr.Child(0)
	switch child.Type() {
	case "literal_value":
		// Could be string_lit, number, bool, etc.
		text := child.Content(src)
		return strings.Trim(text, "\"")
	case "variable_expr":
		return expr.Content(src)
	default:
		return expr.Content(src)
	}
}

func (e *extractor) nodeText(node *sitter.Node) string {
	return node.Content(e.content)
}

func edgeID(sourceID, targetID, edgeType string) string {
	return graph.NewNodeID(edgeType, sourceID, targetID)
}
