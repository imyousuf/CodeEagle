package javascript

import (
	"context"
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"
	jsgrammar "github.com/smacker/go-tree-sitter/javascript"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

// JavaScriptParser extracts knowledge graph nodes and edges from JavaScript source files.
type JavaScriptParser struct{}

// NewParser creates a new JavaScript parser.
func NewParser() *JavaScriptParser {
	return &JavaScriptParser{}
}

func (p *JavaScriptParser) Language() parser.Language {
	return parser.LangJavaScript
}

func (p *JavaScriptParser) Extensions() []string {
	return parser.FileExtensions[parser.LangJavaScript]
}

func (p *JavaScriptParser) ParseFile(filePath string, content []byte) (*parser.ParseResult, error) {
	lang := jsgrammar.GetLanguage()
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
		Language: parser.LangJavaScript,
	}, nil
}

// extractor walks a tree-sitter JavaScript AST and builds graph nodes and edges.
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
		Language: string(parser.LangJavaScript),
	})
}

func (e *extractor) extractModuleNode() {
	e.moduleNodeID = graph.NewNodeID(string(graph.NodeModule), e.filePath, e.filePath)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       e.moduleNodeID,
		Type:     graph.NodeModule,
		Name:     e.filePath,
		FilePath: e.filePath,
		Language: string(parser.LangJavaScript),
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
	case "function_declaration":
		e.extractFunction(node, false)
	case "lexical_declaration":
		e.extractLexicalDeclaration(node, false)
	case "variable_declaration":
		e.extractVariableDeclaration(node)
	case "expression_statement":
		e.extractExpressionStatement(node)
	}
}

func (e *extractor) extractImport(node *sitter.Node) {
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
		Language: string(parser.LangJavaScript),
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.moduleNodeID, depID, string(graph.EdgeImports)),
		Type:     graph.EdgeImports,
		SourceID: e.moduleNodeID,
		TargetID: depID,
	})
}

func (e *extractor) extractExportStatement(node *sitter.Node) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "class_declaration":
			e.extractClass(child, true)
		case "function_declaration":
			e.extractFunction(child, true)
		case "lexical_declaration":
			e.extractLexicalDeclaration(child, true)
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

	// Check for extends (class heritage).
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "class_heritage" {
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				if gc.Type() == "identifier" || gc.Type() == "member_expression" {
					props["extends"] = e.nodeText(gc)
				}
			}
		}
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
		Language:      string(parser.LangJavaScript),
		Exported:      exported,
		Properties:    props,
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.moduleNodeID, classID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.moduleNodeID,
		TargetID: classID,
	})

	// Extract class methods.
	body := e.findChildByType(node, "class_body")
	if body != nil {
		e.extractClassMembers(body, name, classID)
	}
}

func (e *extractor) extractClassMembers(body *sitter.Node, className, classID string) {
	for i := 0; i < int(body.ChildCount()); i++ {
		child := body.Child(i)
		if child.Type() == "method_definition" {
			e.extractMethod(child, className, classID)
		}
	}
}

func (e *extractor) extractMethod(node *sitter.Node, className, classID string) {
	nameNode := e.findChildByFieldName(node, "name")
	if nameNode == nil {
		return
	}
	name := e.nodeText(nameNode)

	sig := e.buildFuncSignature(node, name)

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
		Language:      string(parser.LangJavaScript),
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

	// Check for JSX (React component).
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
		Language:      string(parser.LangJavaScript),
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
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "variable_declarator" {
			// Check for require() first.
			valueNode := e.findChildByFieldName(child, "value")
			if valueNode != nil && e.isRequireCall(valueNode) {
				modulePath := e.extractRequireModulePath(valueNode)
				if modulePath != "" {
					depID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, modulePath)
					e.nodes = append(e.nodes, &graph.Node{
						ID:       depID,
						Type:     graph.NodeDependency,
						Name:     modulePath,
						FilePath: e.filePath,
						Line:     startLine(child),
						Language: string(parser.LangJavaScript),
						Properties: map[string]string{
							"system": "commonjs",
						},
					})
					e.edges = append(e.edges, &graph.Edge{
						ID:       edgeID(e.moduleNodeID, depID, string(graph.EdgeImports)),
						Type:     graph.EdgeImports,
						SourceID: e.moduleNodeID,
						TargetID: depID,
					})
				}
				continue
			}
			e.extractVariableDeclarator(child, exported)
		}
	}
}

func (e *extractor) extractVariableDeclaration(node *sitter.Node) {
	// var declarations: check for CommonJS require() patterns.
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "variable_declarator" {
			e.extractRequireOrDeclarator(child)
		}
	}
}

func (e *extractor) extractRequireOrDeclarator(node *sitter.Node) {
	valueNode := e.findChildByFieldName(node, "value")
	if valueNode == nil {
		return
	}

	// Check if value is a require() call.
	if e.isRequireCall(valueNode) {
		modulePath := e.extractRequireModulePath(valueNode)
		if modulePath != "" {
			depID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, modulePath)
			e.nodes = append(e.nodes, &graph.Node{
				ID:       depID,
				Type:     graph.NodeDependency,
				Name:     modulePath,
				FilePath: e.filePath,
				Line:     startLine(node),
				Language: string(parser.LangJavaScript),
				Properties: map[string]string{
					"system": "commonjs",
				},
			})
			e.edges = append(e.edges, &graph.Edge{
				ID:       edgeID(e.moduleNodeID, depID, string(graph.EdgeImports)),
				Type:     graph.EdgeImports,
				SourceID: e.moduleNodeID,
				TargetID: depID,
			})
		}
		return
	}

	// Otherwise check if it's a function assigned to variable.
	nameNode := e.findChildByFieldName(node, "name")
	if nameNode == nil {
		return
	}
	name := e.nodeText(nameNode)

	switch valueNode.Type() {
	case "arrow_function", "function":
		e.extractArrowFunction(node, name, valueNode, false)
	}
}

func (e *extractor) extractVariableDeclarator(node *sitter.Node, exported bool) {
	nameNode := e.findChildByFieldName(node, "name")
	if nameNode == nil {
		return
	}
	name := e.nodeText(nameNode)

	valueNode := e.findChildByFieldName(node, "value")
	if valueNode == nil {
		return
	}

	switch valueNode.Type() {
	case "arrow_function", "function":
		e.extractArrowFunction(node, name, valueNode, exported)
	}
}

func (e *extractor) extractArrowFunction(declNode *sitter.Node, name string, fnNode *sitter.Node, exported bool) {
	props := make(map[string]string)
	props["arrow"] = "true"
	if e.hasChildWithValue(fnNode, "async") {
		props["async"] = "true"
	}

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
		Language:      string(parser.LangJavaScript),
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

func (e *extractor) extractExpressionStatement(node *sitter.Node) {
	// Look for require() calls in expression statements, e.g.:
	// const x = require('y')  -- handled by variable_declaration
	// This catches standalone require() calls if needed.
	// Also could catch module.exports patterns but we skip code modification.
}

func (e *extractor) isRequireCall(node *sitter.Node) bool {
	if node.Type() != "call_expression" {
		return false
	}
	fn := e.findChildByFieldName(node, "function")
	if fn == nil {
		return false
	}
	return e.nodeText(fn) == "require"
}

func (e *extractor) extractRequireModulePath(node *sitter.Node) string {
	args := e.findChildByFieldName(node, "arguments")
	if args == nil {
		return ""
	}
	for i := 0; i < int(args.ChildCount()); i++ {
		child := args.Child(i)
		if child.Type() == "string" {
			return stripQuotes(e.nodeText(child))
		}
	}
	return ""
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

func edgeID(sourceID, targetID, edgeType string) string {
	return graph.NewNodeID(edgeType, sourceID, targetID)
}
