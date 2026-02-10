package rust

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/rust"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

// RustParser extracts knowledge graph nodes and edges from Rust source files.
type RustParser struct{}

// NewParser creates a new Rust parser.
func NewParser() *RustParser {
	return &RustParser{}
}

func (p *RustParser) Language() parser.Language {
	return parser.LangRust
}

func (p *RustParser) Extensions() []string {
	return parser.FileExtensions[parser.LangRust]
}

func (p *RustParser) ParseFile(filePath string, content []byte) (*parser.ParseResult, error) {
	lang := rust.GetLanguage()
	sitterParser := sitter.NewParser()
	sitterParser.SetLanguage(lang)

	tree, err := sitterParser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filePath, err)
	}

	e := &extractor{
		filePath: filePath,
		content:  content,
		tree:     tree,
	}
	e.extract()

	return &parser.ParseResult{
		Nodes:    e.nodes,
		Edges:    e.edges,
		FilePath: filePath,
		Language: parser.LangRust,
	}, nil
}

// extractor walks a tree-sitter Rust AST and builds graph nodes and edges.
type extractor struct {
	filePath string
	content  []byte
	tree     *sitter.Tree
	nodes    []*graph.Node
	edges    []*graph.Edge

	fileNodeID string
	modName    string // current module name (from mod declarations or file name)
	isTestFile bool

	// Lookup maps for function call resolution (built after first pass)
	funcMap map[string]string // funcName -> node ID
}

func (e *extractor) extract() {
	e.extractFileNode()

	root := e.tree.RootNode()
	// First pass: extract all declarations
	e.walkRoot(root)
	// Build call maps
	e.buildCallMaps()
	// Second pass: walk function bodies for call edges
	e.walkBodiesForCalls(root)
}

func (e *extractor) extractFileNode() {
	base := filepath.Base(e.filePath)
	e.isTestFile = isTestFilePath(e.filePath)

	fileType := graph.NodeFile
	if e.isTestFile {
		fileType = graph.NodeTestFile
	}

	e.fileNodeID = graph.NewNodeID(string(fileType), e.filePath, e.filePath)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       e.fileNodeID,
		Type:     fileType,
		Name:     e.filePath,
		FilePath: e.filePath,
		Language: string(parser.LangRust),
	})

	// Derive module name from filename (without extension)
	e.modName = strings.TrimSuffix(base, ".rs")
}

// isTestFilePath returns true if the file path matches Rust test file patterns.
func isTestFilePath(filePath string) bool {
	// Files in a tests/ directory are integration test files
	parts := strings.Split(filepath.ToSlash(filePath), "/")
	for _, p := range parts {
		if p == "tests" {
			return true
		}
	}
	return false
}

func (e *extractor) walkRoot(root *sitter.Node) {
	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		e.extractDeclaration(child, e.fileNodeID)
	}
}

func (e *extractor) extractDeclaration(node *sitter.Node, parentID string) {
	switch node.Type() {
	case "function_item":
		e.extractFunction(node, parentID)
	case "struct_item":
		e.extractStruct(node, parentID)
	case "trait_item":
		e.extractTrait(node, parentID)
	case "enum_item":
		e.extractEnum(node, parentID)
	case "impl_item":
		e.extractImpl(node, parentID)
	case "use_declaration":
		e.extractUse(node, parentID)
	case "mod_item":
		e.extractMod(node, parentID)
	case "const_item":
		e.extractConst(node, parentID)
	case "static_item":
		e.extractStatic(node, parentID)
	case "type_item":
		e.extractTypeAlias(node, parentID)
	}
}

func (e *extractor) extractFunction(node *sitter.Node, parentID string) {
	name := ""
	params := ""
	returnType := ""
	isPublic := false
	isTest := false

	docComment := e.extractDocComment(node)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "visibility_modifier":
			isPublic = true
		case "identifier":
			if name == "" {
				name = e.nodeText(child)
			}
		case "parameters":
			params = e.nodeText(child)
		case "type_identifier", "generic_type", "reference_type", "tuple_type",
			"array_type", "primitive_type", "scoped_type_identifier", "unit_type":
			returnType = e.nodeText(child)
		}
	}

	// Check for return type after ->
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if e.nodeText(child) == "->" {
			if i+1 < int(node.ChildCount()) {
				next := node.Child(i + 1)
				if next.IsNamed() {
					returnType = e.nodeText(next)
				}
			}
		}
	}

	if name == "" {
		return
	}

	// Check for #[test] attribute
	isTest = e.hasTestAttribute(node)

	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1

	sig := "fn " + name + params
	if returnType != "" {
		sig += " -> " + returnType
	}

	nodeType := graph.NodeFunction
	if isTest {
		nodeType = graph.NodeTestFunction
	}

	funcID := graph.NewNodeID(string(nodeType), e.filePath, name)

	props := make(map[string]string)
	if isTest {
		props["test"] = "true"
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:            funcID,
		Type:          nodeType,
		Name:          name,
		QualifiedName: e.qualifiedName(name),
		FilePath:      e.filePath,
		Line:          startLine,
		EndLine:       endLine,
		Package:       e.modName,
		Language:      string(parser.LangRust),
		Exported:      isPublic,
		Signature:     sig,
		DocComment:    docComment,
		Properties:    props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, funcID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: funcID,
	})
}

func (e *extractor) extractStruct(node *sitter.Node, parentID string) {
	name := ""
	isPublic := false

	docComment := e.extractDocComment(node)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "visibility_modifier":
			isPublic = true
		case "type_identifier":
			name = e.nodeText(child)
		}
	}

	if name == "" {
		return
	}

	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1

	structID := graph.NewNodeID(string(graph.NodeStruct), e.filePath, name)

	// Extract field names
	var fields []string
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "field_declaration_list" {
			for j := 0; j < int(child.NamedChildCount()); j++ {
				field := child.NamedChild(j)
				if field.Type() == "field_declaration" {
					for k := 0; k < int(field.NamedChildCount()); k++ {
						fc := field.NamedChild(k)
						if fc.Type() == "field_identifier" {
							fields = append(fields, e.nodeText(fc))
						}
					}
				}
			}
		}
	}

	props := make(map[string]string)
	if len(fields) > 0 {
		props["fields"] = strings.Join(fields, ",")
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:            structID,
		Type:          graph.NodeStruct,
		Name:          name,
		QualifiedName: e.qualifiedName(name),
		FilePath:      e.filePath,
		Line:          startLine,
		EndLine:       endLine,
		Package:       e.modName,
		Language:      string(parser.LangRust),
		Exported:      isPublic,
		DocComment:    docComment,
		Properties:    props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, structID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: structID,
	})
}

func (e *extractor) extractTrait(node *sitter.Node, parentID string) {
	name := ""
	isPublic := false
	var bodyNode *sitter.Node

	docComment := e.extractDocComment(node)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "visibility_modifier":
			isPublic = true
		case "type_identifier":
			name = e.nodeText(child)
		case "declaration_list":
			bodyNode = child
		}
	}

	if name == "" {
		return
	}

	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1

	traitID := graph.NewNodeID(string(graph.NodeInterface), e.filePath, name)

	// Extract method names from trait body
	var methodNames []string
	if bodyNode != nil {
		for i := 0; i < int(bodyNode.NamedChildCount()); i++ {
			child := bodyNode.NamedChild(i)
			if child.Type() == "function_item" || child.Type() == "function_signature_item" {
				mn := e.getFuncName(child)
				if mn != "" {
					methodNames = append(methodNames, mn)
				}
			}
		}
	}

	props := make(map[string]string)
	if len(methodNames) > 0 {
		props["methods"] = strings.Join(methodNames, ",")
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:            traitID,
		Type:          graph.NodeInterface,
		Name:          name,
		QualifiedName: e.qualifiedName(name),
		FilePath:      e.filePath,
		Line:          startLine,
		EndLine:       endLine,
		Package:       e.modName,
		Language:      string(parser.LangRust),
		Exported:      isPublic,
		DocComment:    docComment,
		Properties:    props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, traitID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: traitID,
	})
}

func (e *extractor) extractEnum(node *sitter.Node, parentID string) {
	name := ""
	isPublic := false

	docComment := e.extractDocComment(node)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "visibility_modifier":
			isPublic = true
		case "type_identifier":
			name = e.nodeText(child)
		}
	}

	if name == "" {
		return
	}

	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1

	enumID := graph.NewNodeID(string(graph.NodeEnum), e.filePath, name)

	// Extract variant names
	var variants []string
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "enum_variant_list" {
			for j := 0; j < int(child.NamedChildCount()); j++ {
				variant := child.NamedChild(j)
				if variant.Type() == "enum_variant" {
					for k := 0; k < int(variant.NamedChildCount()); k++ {
						vc := variant.NamedChild(k)
						if vc.Type() == "identifier" {
							variants = append(variants, e.nodeText(vc))
						}
					}
				}
			}
		}
	}

	props := make(map[string]string)
	if len(variants) > 0 {
		props["variants"] = strings.Join(variants, ",")
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:            enumID,
		Type:          graph.NodeEnum,
		Name:          name,
		QualifiedName: e.qualifiedName(name),
		FilePath:      e.filePath,
		Line:          startLine,
		EndLine:       endLine,
		Package:       e.modName,
		Language:      string(parser.LangRust),
		Exported:      isPublic,
		DocComment:    docComment,
		Properties:    props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, enumID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: enumID,
	})
}

func (e *extractor) extractImpl(node *sitter.Node, parentID string) {
	// impl_item can be:
	//   impl Type { ... }           (inherent impl)
	//   impl Trait for Type { ... } (trait impl)
	var traitName string
	var typeName string
	var bodyNode *sitter.Node

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "type_identifier", "generic_type", "scoped_type_identifier":
			if typeName == "" {
				typeName = e.nodeText(child)
			} else if traitName == "" {
				// If we already have typeName and see another type, then
				// the first was the trait and this is the type.
				traitName = typeName
				typeName = e.nodeText(child)
			}
		case "declaration_list":
			bodyNode = child
		}
	}

	// Check for "for" keyword to distinguish "impl Trait for Type" from "impl Type"
	hasFor := false
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if !child.IsNamed() && e.nodeText(child) == "for" {
			hasFor = true
			break
		}
	}

	if !hasFor {
		// inherent impl: typeName is correct, traitName should be empty
		traitName = ""
	}

	if typeName == "" {
		return
	}

	// Create implements edge if this is a trait impl
	if traitName != "" {
		structID := graph.NewNodeID(string(graph.NodeStruct), e.filePath, typeName)
		traitID := graph.NewNodeID(string(graph.NodeInterface), e.filePath, traitName)

		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(structID, traitID, string(graph.EdgeImplements)),
			Type:     graph.EdgeImplements,
			SourceID: structID,
			TargetID: traitID,
			Properties: map[string]string{
				"implements": traitName,
			},
		})
	}

	// Extract methods from impl body
	if bodyNode != nil {
		e.walkImplBody(bodyNode, parentID, typeName, traitName)
	}
}

func (e *extractor) walkImplBody(body *sitter.Node, parentID, typeName, traitName string) {
	for i := 0; i < int(body.NamedChildCount()); i++ {
		child := body.NamedChild(i)
		if child.Type() == "function_item" {
			e.extractMethod(child, parentID, typeName, traitName)
		}
	}
}

func (e *extractor) extractMethod(node *sitter.Node, parentID, typeName, traitName string) {
	name := ""
	params := ""
	returnType := ""
	isPublic := false
	isTest := false

	docComment := e.extractDocComment(node)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "visibility_modifier":
			isPublic = true
		case "identifier":
			if name == "" {
				name = e.nodeText(child)
			}
		case "parameters":
			params = e.nodeText(child)
		}
	}

	// Check for return type after ->
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if e.nodeText(child) == "->" {
			if i+1 < int(node.ChildCount()) {
				next := node.Child(i + 1)
				if next.IsNamed() {
					returnType = e.nodeText(next)
				}
			}
		}
	}

	if name == "" {
		return
	}

	isTest = e.hasTestAttribute(node)

	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1

	qualifiedName := typeName + "." + name
	sig := "fn " + name + params
	if returnType != "" {
		sig += " -> " + returnType
	}

	nodeType := graph.NodeMethod
	if isTest {
		nodeType = graph.NodeTestFunction
	}

	methodID := graph.NewNodeID(string(nodeType), e.filePath, qualifiedName)

	props := make(map[string]string)
	props["struct"] = typeName
	if traitName != "" {
		props["trait"] = traitName
	}
	if isTest {
		props["test"] = "true"
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:            methodID,
		Type:          nodeType,
		Name:          name,
		QualifiedName: qualifiedName,
		FilePath:      e.filePath,
		Line:          startLine,
		EndLine:       endLine,
		Package:       e.modName,
		Language:      string(parser.LangRust),
		Exported:      isPublic,
		Signature:     sig,
		DocComment:    docComment,
		Properties:    props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, methodID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: methodID,
	})
}

func (e *extractor) extractUse(node *sitter.Node, parentID string) {
	// use_declaration has an "argument" child (scoped_identifier, use_wildcard, etc.)
	name := ""
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		// The argument is the path part of the use statement
		if child.Type() != "visibility_modifier" {
			name = e.nodeText(child)
			break
		}
	}
	if name == "" {
		return
	}

	depID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, name)

	e.nodes = append(e.nodes, &graph.Node{
		ID:       depID,
		Type:     graph.NodeDependency,
		Name:     name,
		FilePath: e.filePath,
		Line:     int(node.StartPoint().Row) + 1,
		Language: string(parser.LangRust),
		Package:  e.modName,
		Properties: map[string]string{
			"kind": "use",
		},
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, depID, string(graph.EdgeImports)),
		Type:     graph.EdgeImports,
		SourceID: parentID,
		TargetID: depID,
	})
}

func (e *extractor) extractMod(node *sitter.Node, parentID string) {
	name := ""
	var bodyNode *sitter.Node

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier":
			name = e.nodeText(child)
		case "declaration_list":
			bodyNode = child
		}
	}

	if name == "" {
		return
	}

	modID := graph.NewNodeID(string(graph.NodePackage), e.filePath, name)

	e.nodes = append(e.nodes, &graph.Node{
		ID:       modID,
		Type:     graph.NodePackage,
		Name:     name,
		FilePath: e.filePath,
		Line:     int(node.StartPoint().Row) + 1,
		Language: string(parser.LangRust),
		Package:  e.modName,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, modID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: modID,
	})

	// If the mod has an inline body, walk its declarations
	if bodyNode != nil {
		for i := 0; i < int(bodyNode.NamedChildCount()); i++ {
			child := bodyNode.NamedChild(i)
			e.extractDeclaration(child, modID)
		}
	}
}

func (e *extractor) extractConst(node *sitter.Node, parentID string) {
	name := ""
	constType := ""
	isPublic := false

	docComment := e.extractDocComment(node)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "visibility_modifier":
			isPublic = true
		case "identifier":
			if name == "" {
				name = e.nodeText(child)
			}
		case "type_identifier", "primitive_type", "generic_type", "reference_type",
			"scoped_type_identifier":
			constType = e.nodeText(child)
		}
	}

	if name == "" {
		return
	}

	constID := graph.NewNodeID(string(graph.NodeConstant), e.filePath, name)

	props := make(map[string]string)
	props["kind"] = "const"
	if constType != "" {
		props["type"] = constType
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:            constID,
		Type:          graph.NodeConstant,
		Name:          name,
		QualifiedName: e.qualifiedName(name),
		FilePath:      e.filePath,
		Line:          int(node.StartPoint().Row) + 1,
		Package:       e.modName,
		Language:      string(parser.LangRust),
		Exported:      isPublic,
		DocComment:    docComment,
		Properties:    props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, constID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: constID,
	})
}

func (e *extractor) extractStatic(node *sitter.Node, parentID string) {
	name := ""
	staticType := ""
	isPublic := false

	docComment := e.extractDocComment(node)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "visibility_modifier":
			isPublic = true
		case "identifier":
			if name == "" {
				name = e.nodeText(child)
			}
		case "type_identifier", "primitive_type", "generic_type", "reference_type",
			"scoped_type_identifier":
			staticType = e.nodeText(child)
		}
	}

	if name == "" {
		return
	}

	constID := graph.NewNodeID(string(graph.NodeConstant), e.filePath, name)

	props := make(map[string]string)
	props["kind"] = "static"
	if staticType != "" {
		props["type"] = staticType
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:            constID,
		Type:          graph.NodeConstant,
		Name:          name,
		QualifiedName: e.qualifiedName(name),
		FilePath:      e.filePath,
		Line:          int(node.StartPoint().Row) + 1,
		Package:       e.modName,
		Language:      string(parser.LangRust),
		Exported:      isPublic,
		DocComment:    docComment,
		Properties:    props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, constID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: constID,
	})
}

func (e *extractor) extractTypeAlias(node *sitter.Node, parentID string) {
	name := ""
	isPublic := false

	docComment := e.extractDocComment(node)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "visibility_modifier":
			isPublic = true
		case "type_identifier":
			if name == "" {
				name = e.nodeText(child)
			}
		}
	}

	if name == "" {
		return
	}

	typeID := graph.NewNodeID(string(graph.NodeType_), e.filePath, name)

	e.nodes = append(e.nodes, &graph.Node{
		ID:            typeID,
		Type:          graph.NodeType_,
		Name:          name,
		QualifiedName: e.qualifiedName(name),
		FilePath:      e.filePath,
		Line:          int(node.StartPoint().Row) + 1,
		Package:       e.modName,
		Language:      string(parser.LangRust),
		Exported:      isPublic,
		DocComment:    docComment,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, typeID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: typeID,
	})
}

// hasTestAttribute checks if a function_item node has a #[test] or #[cfg(test)] attribute.
func (e *extractor) hasTestAttribute(node *sitter.Node) bool {
	parent := node.Parent()
	if parent == nil {
		return false
	}

	// Look for attribute_item nodes immediately preceding this node in the parent
	idx := -1
	for i := 0; i < int(parent.ChildCount()); i++ {
		if parent.Child(i) == node {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}

	// Walk backward to find attribute items
	for j := idx - 1; j >= 0; j-- {
		prev := parent.Child(j)
		if prev.Type() == "attribute_item" {
			text := e.nodeText(prev)
			if strings.Contains(text, "#[test]") || strings.Contains(text, "#[tokio::test]") {
				return true
			}
		} else if prev.Type() == "line_comment" || prev.Type() == "block_comment" {
			// Skip comments between attributes and function
			continue
		} else {
			break
		}
	}

	return false
}

// extractDocComment looks for /// or //! comments immediately preceding a node.
func (e *extractor) extractDocComment(node *sitter.Node) string {
	parent := node.Parent()
	if parent == nil {
		return ""
	}

	idx := -1
	for i := 0; i < int(parent.ChildCount()); i++ {
		if parent.Child(i) == node {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return ""
	}

	var lines []string
	for j := idx - 1; j >= 0; j-- {
		prev := parent.Child(j)
		if prev.Type() == "line_comment" {
			text := e.nodeText(prev)
			if strings.HasPrefix(text, "///") {
				line := strings.TrimPrefix(text, "///")
				line = strings.TrimPrefix(line, " ")
				lines = append([]string{line}, lines...)
			} else if strings.HasPrefix(text, "//!") {
				line := strings.TrimPrefix(text, "//!")
				line = strings.TrimPrefix(line, " ")
				lines = append([]string{line}, lines...)
			} else {
				break
			}
		} else if prev.Type() == "attribute_item" {
			// Skip attributes between doc comments and function
			continue
		} else {
			break
		}
	}

	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

// buildCallMaps populates lookup maps from extracted nodes.
func (e *extractor) buildCallMaps() {
	e.funcMap = make(map[string]string)
	for _, n := range e.nodes {
		switch n.Type {
		case graph.NodeFunction, graph.NodeMethod, graph.NodeTestFunction:
			e.funcMap[n.Name] = n.ID
		}
	}
}

// walkBodiesForCalls walks function bodies looking for call expressions.
func (e *extractor) walkBodiesForCalls(root *sitter.Node) {
	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		switch child.Type() {
		case "function_item":
			name := e.getFuncName(child)
			if name == "" {
				continue
			}
			funcID := e.funcMap[name]
			if funcID == "" {
				continue
			}
			e.walkForCalls(child, funcID)
		case "impl_item":
			e.walkImplBodiesForCalls(child)
		case "mod_item":
			// Recurse into inline mod bodies
			for j := 0; j < int(child.NamedChildCount()); j++ {
				gc := child.NamedChild(j)
				if gc.Type() == "declaration_list" {
					e.walkBodiesForCalls(gc)
				}
			}
		}
	}
}

func (e *extractor) walkImplBodiesForCalls(implNode *sitter.Node) {
	typeName := ""
	var bodyNode *sitter.Node

	for i := 0; i < int(implNode.ChildCount()); i++ {
		child := implNode.Child(i)
		switch child.Type() {
		case "type_identifier", "generic_type", "scoped_type_identifier":
			// Last type_identifier before "for" or declaration_list is the implementing type
			typeName = e.nodeText(child)
		case "declaration_list":
			bodyNode = child
		}
	}

	if bodyNode == nil {
		return
	}

	for i := 0; i < int(bodyNode.NamedChildCount()); i++ {
		child := bodyNode.NamedChild(i)
		if child.Type() == "function_item" {
			name := e.getFuncName(child)
			if name == "" || typeName == "" {
				continue
			}
			qualifiedName := typeName + "." + name
			methodID := ""
			// Try both Method and TestFunction IDs
			for _, nt := range []graph.NodeType{graph.NodeMethod, graph.NodeTestFunction} {
				candidateID := graph.NewNodeID(string(nt), e.filePath, qualifiedName)
				if _, ok := e.funcMap[name]; ok {
					methodID = candidateID
					break
				}
				// Check if this ID is in our nodes
				for _, n := range e.nodes {
					if n.ID == candidateID {
						methodID = candidateID
						break
					}
				}
				if methodID != "" {
					break
				}
			}
			if methodID == "" {
				methodID = graph.NewNodeID(string(graph.NodeMethod), e.filePath, qualifiedName)
			}
			e.walkForCalls(child, methodID)
		}
	}
}

// Rust builtins/standard method names to skip in call graph analysis.
var rustBuiltins = map[string]bool{
	"clone": true, "to_string": true, "to_owned": true, "as_ref": true,
	"as_mut": true, "into": true, "from": true, "default": true,
	"unwrap": true, "expect": true, "is_some": true, "is_none": true,
	"is_ok": true, "is_err": true, "ok": true, "err": true,
	"map": true, "and_then": true, "or_else": true, "unwrap_or": true,
	"unwrap_or_else": true, "unwrap_or_default": true,
	"len": true, "is_empty": true, "push": true, "pop": true,
	"iter": true, "into_iter": true, "collect": true, "filter": true,
	"for_each": true, "enumerate": true, "zip": true, "take": true,
	"skip": true, "chain": true, "flat_map": true, "fold": true,
	"any": true, "all": true, "find": true, "position": true,
	"count": true, "sum": true, "min": true, "max": true,
	"sort": true, "sort_by": true, "reverse": true,
	"insert": true, "remove": true, "contains": true, "get": true,
	"contains_key": true, "keys": true, "values": true, "entry": true,
	"or_insert": true, "or_insert_with": true,
	"fmt": true, "write": true, "read": true, "flush": true,
	"println": true, "print": true, "eprintln": true, "eprint": true,
	"format": true, "panic": true, "assert": true, "assert_eq": true,
	"assert_ne": true, "debug_assert": true,
	"new": true, "with_capacity": true, "capacity": true,
}

// walkForCalls recursively walks a subtree looking for call_expression nodes.
func (e *extractor) walkForCalls(node *sitter.Node, callerID string) {
	if node == nil {
		return
	}

	if node.Type() == "call_expression" {
		e.checkFunctionCall(node, callerID)
	}

	for i := 0; i < int(node.NamedChildCount()); i++ {
		e.walkForCalls(node.NamedChild(i), callerID)
	}
}

// checkFunctionCall checks if a call_expression is a call to a known function.
func (e *extractor) checkFunctionCall(node *sitter.Node, callerID string) {
	if callerID == "" {
		return
	}

	// call_expression has a "function" child which is either an identifier
	// or a field_expression (for method calls).
	funcNode := node.ChildByFieldName("function")
	if funcNode == nil {
		return
	}

	var calledName string

	switch funcNode.Type() {
	case "identifier":
		calledName = e.nodeText(funcNode)
	case "field_expression":
		// receiver.method() - extract the method name (field)
		field := funcNode.ChildByFieldName("field")
		if field != nil {
			calledName = e.nodeText(field)
		}
	case "scoped_identifier":
		// Type::method() - extract the last segment
		text := e.nodeText(funcNode)
		parts := strings.Split(text, "::")
		calledName = parts[len(parts)-1]
	}

	if calledName == "" || rustBuiltins[calledName] {
		return
	}

	// Look up target in our function map
	if targetID, ok := e.funcMap[calledName]; ok {
		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(callerID, targetID, string(graph.EdgeCalls)),
			Type:     graph.EdgeCalls,
			SourceID: callerID,
			TargetID: targetID,
			Properties: map[string]string{
				"callee": calledName,
			},
		})
	}
}

func (e *extractor) getFuncName(node *sitter.Node) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "identifier" {
			return e.nodeText(child)
		}
	}
	return ""
}

func (e *extractor) nodeText(node *sitter.Node) string {
	return node.Content(e.content)
}

func (e *extractor) qualifiedName(name string) string {
	if e.modName != "" {
		return e.modName + "::" + name
	}
	return name
}

// Helper functions

func edgeID(sourceID, targetID, edgeType string) string {
	return graph.NewNodeID(edgeType, sourceID, targetID)
}
