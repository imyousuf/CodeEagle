package typescript

import (
	"context"
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	tsgrammar "github.com/smacker/go-tree-sitter/typescript/typescript"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

// TypeScriptParser extracts knowledge graph nodes and edges from TypeScript source files.
type TypeScriptParser struct{}

// NewParser creates a new TypeScript parser.
func NewParser() *TypeScriptParser {
	return &TypeScriptParser{}
}

func (p *TypeScriptParser) Language() parser.Language {
	return parser.LangTypeScript
}

func (p *TypeScriptParser) Extensions() []string {
	return parser.FileExtensions[parser.LangTypeScript]
}

func (p *TypeScriptParser) ParseFile(filePath string, content []byte) (*parser.ParseResult, error) {
	lang := tsgrammar.GetLanguage()
	psr := sitter.NewParser()
	psr.SetLanguage(lang)

	tree, err := psr.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filePath, err)
	}
	defer tree.Close()

	e := &extractor{
		filePath: filePath,
		content:  content,
		root:     tree.RootNode(),
	}
	e.extract()

	return &parser.ParseResult{
		Nodes:    e.nodes,
		Edges:    e.edges,
		FilePath: filePath,
		Language: parser.LangTypeScript,
	}, nil
}

// extractor walks a tree-sitter TypeScript AST and builds graph nodes and edges.
type extractor struct {
	filePath string
	content  []byte
	root     *sitter.Node
	nodes    []*graph.Node
	edges    []*graph.Edge

	fileNodeID   string
	moduleNodeID string
}

func (e *extractor) extract() {
	e.extractFileNode()
	e.extractModuleNode()
	e.walkChildren(e.root)
}

func (e *extractor) extractFileNode() {
	e.fileNodeID = graph.NewNodeID(string(graph.NodeFile), e.filePath, e.filePath)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       e.fileNodeID,
		Type:     graph.NodeFile,
		Name:     e.filePath,
		FilePath: e.filePath,
		Language: string(parser.LangTypeScript),
	})
}

func (e *extractor) extractModuleNode() {
	// Use the file path as the module name (TypeScript files are modules).
	e.moduleNodeID = graph.NewNodeID(string(graph.NodeModule), e.filePath, e.filePath)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       e.moduleNodeID,
		Type:     graph.NodeModule,
		Name:     e.filePath,
		FilePath: e.filePath,
		Language: string(parser.LangTypeScript),
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.fileNodeID, e.moduleNodeID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.fileNodeID,
		TargetID: e.moduleNodeID,
	})
}

func (e *extractor) walkChildren(node *sitter.Node) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		e.visitNode(child)
	}
}

func (e *extractor) visitNode(node *sitter.Node) {
	switch node.Type() {
	case "import_statement":
		e.extractImport(node)
	case "export_statement":
		e.extractExportStatement(node)
	case "class_declaration":
		e.extractClass(node, false)
	case "abstract_class_declaration":
		e.extractClass(node, false)
	case "interface_declaration":
		e.extractInterface(node, false)
	case "type_alias_declaration":
		e.extractTypeAlias(node, false)
	case "enum_declaration":
		e.extractEnum(node, false)
	case "function_declaration":
		e.extractFunction(node, false)
	case "lexical_declaration":
		e.extractLexicalDeclaration(node, false)
	case "module", "internal_module":
		e.extractNamespace(node, false)
	}
}

func (e *extractor) extractImport(node *sitter.Node) {
	// Find the source string (the module path).
	source := e.findChildByType(node, "string")
	if source == nil {
		return
	}
	modulePath := stripQuotes(e.nodeText(source))

	depID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, modulePath)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       depID,
		Type:     graph.NodeDependency,
		Name:     modulePath,
		FilePath: e.filePath,
		Line:     startLine(node),
		Language: string(parser.LangTypeScript),
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.moduleNodeID, depID, string(graph.EdgeImports)),
		Type:     graph.EdgeImports,
		SourceID: e.moduleNodeID,
		TargetID: depID,
	})
}

func (e *extractor) extractExportStatement(node *sitter.Node) {
	// An export_statement wraps a declaration. Walk its children to find the
	// actual declaration and mark it as exported.
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "class_declaration", "abstract_class_declaration":
			e.extractClass(child, true)
		case "interface_declaration":
			e.extractInterface(child, true)
		case "type_alias_declaration":
			e.extractTypeAlias(child, true)
		case "enum_declaration":
			e.extractEnum(child, true)
		case "function_declaration":
			e.extractFunction(child, true)
		case "lexical_declaration":
			e.extractLexicalDeclaration(child, true)
		case "module", "internal_module":
			e.extractNamespace(child, true)
		}
	}
}

func (e *extractor) extractClass(node *sitter.Node, exported bool) {
	nameNode := e.findChildByFieldName(node, "name")
	if nameNode == nil {
		return
	}
	name := e.nodeText(nameNode)

	props := make(map[string]string)

	// Check for heritage clause (extends, implements).
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "class_heritage" {
			e.parseClassHeritage(child, props)
		}
	}

	// Check for decorators.
	decorators := e.collectDecorators(node)
	if len(decorators) > 0 {
		props["decorators"] = strings.Join(decorators, ",")
	}

	classID := graph.NewNodeID(string(graph.NodeClass), e.filePath, name)
	e.nodes = append(e.nodes, &graph.Node{
		ID:            classID,
		Type:          graph.NodeClass,
		Name:          name,
		QualifiedName: e.filePath + "." + name,
		FilePath:      e.filePath,
		Line:          startLine(node),
		EndLine:       endLine(node),
		Language:      string(parser.LangTypeScript),
		Exported:      exported,
		Properties:    props,
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.moduleNodeID, classID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.moduleNodeID,
		TargetID: classID,
	})

	// Extract methods inside the class body.
	body := e.findChildByType(node, "class_body")
	if body != nil {
		e.extractClassMembers(body, name, classID)
	}

	// Generate Implements edges.
	if implStr, ok := props["implements"]; ok {
		for _, iface := range strings.Split(implStr, ",") {
			iface = strings.TrimSpace(iface)
			if iface == "" {
				continue
			}
			ifaceID := graph.NewNodeID(string(graph.NodeInterface), e.filePath, iface)
			e.edges = append(e.edges, &graph.Edge{
				ID:       edgeID(classID, ifaceID, string(graph.EdgeImplements)),
				Type:     graph.EdgeImplements,
				SourceID: classID,
				TargetID: ifaceID,
			})
		}
	}
}

func (e *extractor) parseClassHeritage(node *sitter.Node, props map[string]string) {
	text := e.nodeText(node)
	if strings.Contains(text, "extends") || strings.Contains(text, "implements") {
		var extendsList []string
		var implList []string
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(i)
			if child.Type() == "extends_clause" {
				for j := 0; j < int(child.ChildCount()); j++ {
					gc := child.Child(j)
					if gc.Type() == "identifier" || gc.Type() == "member_expression" {
						extendsList = append(extendsList, e.nodeText(gc))
					}
				}
			}
			if child.Type() == "implements_clause" {
				for j := 0; j < int(child.ChildCount()); j++ {
					gc := child.Child(j)
					if gc.Type() == "type_identifier" || gc.Type() == "generic_type" {
						implList = append(implList, extractBaseTypeName(e.nodeText(gc)))
					}
				}
			}
		}
		if len(extendsList) > 0 {
			props["extends"] = strings.Join(extendsList, ",")
		}
		if len(implList) > 0 {
			props["implements"] = strings.Join(implList, ",")
		}
	}
}

func (e *extractor) collectDecorators(node *sitter.Node) []string {
	var decorators []string
	// Check preceding siblings for decorator nodes.
	if node.Parent() != nil {
		parent := node.Parent()
		for i := 0; i < int(parent.ChildCount()); i++ {
			child := parent.Child(i)
			if child == node {
				break
			}
			if child.Type() == "decorator" {
				decorators = append(decorators, e.nodeText(child))
			}
		}
	}
	return decorators
}

func (e *extractor) extractClassMembers(body *sitter.Node, className, classID string) {
	for i := 0; i < int(body.ChildCount()); i++ {
		child := body.Child(i)
		switch child.Type() {
		case "method_definition":
			e.extractMethod(child, className, classID)
		case "public_field_definition":
			// Skip field declarations for now.
		}
	}
}

func (e *extractor) extractMethod(node *sitter.Node, className, classID string) {
	nameNode := e.findChildByFieldName(node, "name")
	if nameNode == nil {
		return
	}
	name := e.nodeText(nameNode)

	// Build signature.
	sig := e.buildFuncSignature(node, name)

	// Check for async.
	props := make(map[string]string)
	props["receiver"] = className
	if e.hasChildWithValue(node, "async") {
		props["async"] = "true"
	}

	methodID := graph.NewNodeID(string(graph.NodeMethod), e.filePath, className+"."+name)
	e.nodes = append(e.nodes, &graph.Node{
		ID:            methodID,
		Type:          graph.NodeMethod,
		Name:          name,
		QualifiedName: className + "." + name,
		FilePath:      e.filePath,
		Line:          startLine(node),
		EndLine:       endLine(node),
		Language:      string(parser.LangTypeScript),
		Signature:     sig,
		Properties:    props,
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(classID, methodID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: classID,
		TargetID: methodID,
	})
}

func (e *extractor) extractInterface(node *sitter.Node, exported bool) {
	nameNode := e.findChildByFieldName(node, "name")
	if nameNode == nil {
		return
	}
	name := e.nodeText(nameNode)

	props := make(map[string]string)

	// Check for extends.
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "extends_type_clause" {
			var extendsList []string
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				if gc.Type() == "type_identifier" || gc.Type() == "generic_type" {
					extendsList = append(extendsList, extractBaseTypeName(e.nodeText(gc)))
				}
			}
			if len(extendsList) > 0 {
				props["extends"] = strings.Join(extendsList, ",")
			}
		}
	}

	// Count methods in the interface body.
	body := e.findChildByType(node, "interface_body")
	if body != nil {
		var methods []string
		for i := 0; i < int(body.ChildCount()); i++ {
			child := body.Child(i)
			if child.Type() == "method_signature" || child.Type() == "property_signature" {
				mName := e.findChildByFieldName(child, "name")
				if mName != nil {
					methods = append(methods, e.nodeText(mName))
				}
			}
		}
		if len(methods) > 0 {
			props["methods"] = strings.Join(methods, ",")
		}
	}

	ifaceID := graph.NewNodeID(string(graph.NodeInterface), e.filePath, name)
	e.nodes = append(e.nodes, &graph.Node{
		ID:            ifaceID,
		Type:          graph.NodeInterface,
		Name:          name,
		QualifiedName: e.filePath + "." + name,
		FilePath:      e.filePath,
		Line:          startLine(node),
		EndLine:       endLine(node),
		Language:      string(parser.LangTypeScript),
		Exported:      exported,
		Properties:    props,
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.moduleNodeID, ifaceID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.moduleNodeID,
		TargetID: ifaceID,
	})
}

func (e *extractor) extractTypeAlias(node *sitter.Node, exported bool) {
	nameNode := e.findChildByFieldName(node, "name")
	if nameNode == nil {
		return
	}
	name := e.nodeText(nameNode)

	typeID := graph.NewNodeID(string(graph.NodeType_), e.filePath, name)
	e.nodes = append(e.nodes, &graph.Node{
		ID:            typeID,
		Type:          graph.NodeType_,
		Name:          name,
		QualifiedName: e.filePath + "." + name,
		FilePath:      e.filePath,
		Line:          startLine(node),
		EndLine:       endLine(node),
		Language:      string(parser.LangTypeScript),
		Exported:      exported,
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.moduleNodeID, typeID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.moduleNodeID,
		TargetID: typeID,
	})
}

func (e *extractor) extractEnum(node *sitter.Node, exported bool) {
	nameNode := e.findChildByFieldName(node, "name")
	if nameNode == nil {
		return
	}
	name := e.nodeText(nameNode)

	// Collect enum members.
	props := make(map[string]string)
	body := e.findChildByType(node, "enum_body")
	if body != nil {
		var members []string
		for i := 0; i < int(body.ChildCount()); i++ {
			child := body.Child(i)
			if child.Type() == "enum_assignment" || child.Type() == "property_identifier" {
				mName := e.findChildByFieldName(child, "name")
				if mName != nil {
					members = append(members, e.nodeText(mName))
				} else if child.Type() == "property_identifier" {
					members = append(members, e.nodeText(child))
				}
			}
		}
		if len(members) > 0 {
			props["members"] = strings.Join(members, ",")
		}
	}

	enumID := graph.NewNodeID(string(graph.NodeEnum), e.filePath, name)
	e.nodes = append(e.nodes, &graph.Node{
		ID:            enumID,
		Type:          graph.NodeEnum,
		Name:          name,
		QualifiedName: e.filePath + "." + name,
		FilePath:      e.filePath,
		Line:          startLine(node),
		EndLine:       endLine(node),
		Language:      string(parser.LangTypeScript),
		Exported:      exported,
		Properties:    props,
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.moduleNodeID, enumID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.moduleNodeID,
		TargetID: enumID,
	})
}

func (e *extractor) extractFunction(node *sitter.Node, exported bool) {
	nameNode := e.findChildByFieldName(node, "name")
	if nameNode == nil {
		return
	}
	name := e.nodeText(nameNode)

	sig := e.buildFuncSignature(node, name)

	props := make(map[string]string)
	if e.hasChildWithValue(node, "async") {
		props["async"] = "true"
	}

	// Check if it returns JSX (React component heuristic).
	if e.containsJSXReturn(node) {
		props["component"] = "true"
	}

	funcID := graph.NewNodeID(string(graph.NodeFunction), e.filePath, name)
	e.nodes = append(e.nodes, &graph.Node{
		ID:            funcID,
		Type:          graph.NodeFunction,
		Name:          name,
		QualifiedName: e.filePath + "." + name,
		FilePath:      e.filePath,
		Line:          startLine(node),
		EndLine:       endLine(node),
		Language:      string(parser.LangTypeScript),
		Exported:      exported,
		Signature:     sig,
		Properties:    props,
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.moduleNodeID, funcID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.moduleNodeID,
		TargetID: funcID,
	})
}

func (e *extractor) extractLexicalDeclaration(node *sitter.Node, exported bool) {
	// Lexical declarations: const x = ..., let x = ...
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "variable_declarator" {
			e.extractVariableDeclarator(child, exported)
		}
	}
}

func (e *extractor) extractVariableDeclarator(node *sitter.Node, exported bool) {
	nameNode := e.findChildByFieldName(node, "name")
	if nameNode == nil {
		return
	}
	name := e.nodeText(nameNode)

	// Check if the value is an arrow function or function expression.
	valueNode := e.findChildByFieldName(node, "value")
	if valueNode == nil {
		return
	}

	switch valueNode.Type() {
	case "arrow_function":
		e.extractArrowFunction(node, name, valueNode, exported)
	case "function":
		e.extractArrowFunction(node, name, valueNode, exported)
	default:
		// It's a variable assignment, not a function. Skip for now.
	}
}

func (e *extractor) extractArrowFunction(declNode *sitter.Node, name string, fnNode *sitter.Node, exported bool) {
	props := make(map[string]string)
	props["arrow"] = "true"
	if e.hasChildWithValue(fnNode, "async") {
		props["async"] = "true"
	}

	// Check for JSX return (React component).
	if e.containsJSXReturn(fnNode) {
		props["component"] = "true"
	}

	funcID := graph.NewNodeID(string(graph.NodeFunction), e.filePath, name)
	e.nodes = append(e.nodes, &graph.Node{
		ID:            funcID,
		Type:          graph.NodeFunction,
		Name:          name,
		QualifiedName: e.filePath + "." + name,
		FilePath:      e.filePath,
		Line:          startLine(declNode),
		EndLine:       endLine(declNode),
		Language:      string(parser.LangTypeScript),
		Exported:      exported,
		Properties:    props,
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.moduleNodeID, funcID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.moduleNodeID,
		TargetID: funcID,
	})
}

func (e *extractor) extractNamespace(node *sitter.Node, exported bool) {
	nameNode := e.findChildByFieldName(node, "name")
	if nameNode == nil {
		// internal_module uses identifier child without field name.
		nameNode = e.findChildByType(node, "identifier")
	}
	if nameNode == nil {
		return
	}
	name := e.nodeText(nameNode)

	nsID := graph.NewNodeID(string(graph.NodeModule), e.filePath, name)
	e.nodes = append(e.nodes, &graph.Node{
		ID:            nsID,
		Type:          graph.NodeModule,
		Name:          name,
		QualifiedName: e.filePath + "." + name,
		FilePath:      e.filePath,
		Line:          startLine(node),
		EndLine:       endLine(node),
		Language:      string(parser.LangTypeScript),
		Exported:      exported,
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.moduleNodeID, nsID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.moduleNodeID,
		TargetID: nsID,
	})
}

// Helper functions

func (e *extractor) nodeText(node *sitter.Node) string {
	return node.Content(e.content)
}

func (e *extractor) findChildByType(node *sitter.Node, typeName string) *sitter.Node {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == typeName {
			return child
		}
	}
	return nil
}

func (e *extractor) findChildByFieldName(node *sitter.Node, fieldName string) *sitter.Node {
	return node.ChildByFieldName(fieldName)
}

func (e *extractor) hasChildWithValue(node *sitter.Node, value string) bool {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if e.nodeText(child) == value {
			return true
		}
	}
	return false
}

func (e *extractor) buildFuncSignature(node *sitter.Node, name string) string {
	params := e.findChildByFieldName(node, "parameters")
	if params == nil {
		return name + "()"
	}
	return name + e.nodeText(params)
}

func (e *extractor) containsJSXReturn(node *sitter.Node) bool {
	return e.walkForJSX(node)
}

func (e *extractor) walkForJSX(node *sitter.Node) bool {
	nodeType := node.Type()
	if nodeType == "jsx_element" || nodeType == "jsx_self_closing_element" ||
		nodeType == "jsx_fragment" {
		return true
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		if e.walkForJSX(node.Child(i)) {
			return true
		}
	}
	return false
}

func startLine(node *sitter.Node) int {
	return int(node.StartPoint().Row) + 1
}

func endLine(node *sitter.Node) int {
	return int(node.EndPoint().Row) + 1
}

func stripQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func extractBaseTypeName(s string) string {
	// Remove generic parameters: "Foo<Bar>" -> "Foo"
	if idx := strings.Index(s, "<"); idx > 0 {
		return s[:idx]
	}
	return s
}

func edgeID(sourceID, targetID, edgeType string) string {
	return graph.NewNodeID(edgeType, sourceID, targetID)
}
