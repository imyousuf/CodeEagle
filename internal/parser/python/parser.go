package python

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/python"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

// PythonParser extracts knowledge graph nodes and edges from Python source files.
type PythonParser struct{}

// NewParser creates a new Python parser.
func NewParser() *PythonParser {
	return &PythonParser{}
}

func (p *PythonParser) Language() parser.Language {
	return parser.LangPython
}

func (p *PythonParser) Extensions() []string {
	return parser.FileExtensions[parser.LangPython]
}

func (p *PythonParser) ParseFile(filePath string, content []byte) (*parser.ParseResult, error) {
	lang := python.GetLanguage()
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
		Language: parser.LangPython,
	}, nil
}

// extractor walks a tree-sitter Python AST and builds graph nodes and edges.
type extractor struct {
	filePath string
	content  []byte
	tree     *sitter.Tree
	nodes    []*graph.Node
	edges    []*graph.Edge

	moduleNodeID string
	fileNodeID   string
}

func (e *extractor) extract() {
	e.extractFileNode()
	e.extractModule()

	root := e.tree.RootNode()
	e.walkTopLevel(root)
}

func (e *extractor) extractFileNode() {
	e.fileNodeID = graph.NewNodeID(string(graph.NodeFile), e.filePath, e.filePath)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       e.fileNodeID,
		Type:     graph.NodeFile,
		Name:     e.filePath,
		FilePath: e.filePath,
		Language: string(parser.LangPython),
	})
}

func (e *extractor) extractModule() {
	// Derive module name from file path (e.g., "sample" from "testdata/sample.py")
	base := filepath.Base(e.filePath)
	moduleName := strings.TrimSuffix(base, filepath.Ext(base))

	e.moduleNodeID = graph.NewNodeID(string(graph.NodeModule), e.filePath, moduleName)

	// Extract module-level docstring
	docComment := ""
	root := e.tree.RootNode()
	if root.NamedChildCount() > 0 {
		first := root.NamedChild(0)
		if first.Type() == "expression_statement" && first.NamedChildCount() > 0 {
			expr := first.NamedChild(0)
			if expr.Type() == "string" {
				docComment = cleanDocstring(e.nodeText(expr))
			}
		}
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:         e.moduleNodeID,
		Type:       graph.NodeModule,
		Name:       moduleName,
		FilePath:   e.filePath,
		Line:       1,
		Language:   string(parser.LangPython),
		Package:    moduleName,
		DocComment: docComment,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.fileNodeID, e.moduleNodeID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.fileNodeID,
		TargetID: e.moduleNodeID,
	})
}

func (e *extractor) walkTopLevel(root *sitter.Node) {
	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		switch child.Type() {
		case "import_statement":
			e.extractImport(child)
		case "import_from_statement":
			e.extractFromImport(child)
		case "class_definition":
			e.extractClass(child, e.moduleNodeID)
		case "function_definition", "decorated_definition":
			e.extractFunctionOrDecorated(child, e.moduleNodeID, "")
		case "expression_statement":
			e.extractAssignment(child)
		}
	}
}

func (e *extractor) extractImport(node *sitter.Node) {
	// import X, Y, Z
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "dotted_name" || child.Type() == "aliased_import" {
			name := e.nodeText(child)
			if child.Type() == "aliased_import" {
				// Get the module name from the aliased import
				if child.NamedChildCount() > 0 {
					name = e.nodeText(child.NamedChild(0))
				}
			}
			e.addDependency(name, int(node.StartPoint().Row)+1)
		}
	}
}

func (e *extractor) extractFromImport(node *sitter.Node) {
	// from X import Y, Z
	moduleName := ""
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "dotted_name" || child.Type() == "relative_import" {
			moduleName = e.nodeText(child)
			break
		}
	}
	if moduleName != "" {
		e.addDependency(moduleName, int(node.StartPoint().Row)+1)
	}
}

func (e *extractor) addDependency(name string, line int) {
	depID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, name)

	e.nodes = append(e.nodes, &graph.Node{
		ID:       depID,
		Type:     graph.NodeDependency,
		Name:     name,
		FilePath: e.filePath,
		Line:     line,
		Language: string(parser.LangPython),
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.moduleNodeID, depID, string(graph.EdgeImports)),
		Type:     graph.EdgeImports,
		SourceID: e.moduleNodeID,
		TargetID: depID,
	})
}

func (e *extractor) extractClass(node *sitter.Node, parentID string) {
	name := ""
	var bodyNode *sitter.Node
	var bases []string

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier":
			name = e.nodeText(child)
		case "argument_list":
			bases = e.extractBaseClasses(child)
		case "block":
			bodyNode = child
		}
	}

	if name == "" {
		return
	}

	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1
	exported := isExported(name)

	classID := graph.NewNodeID(string(graph.NodeClass), e.filePath, name)

	props := make(map[string]string)
	if len(bases) > 0 {
		props["bases"] = strings.Join(bases, ",")
	}

	// Extract docstring from class body
	docComment := ""
	if bodyNode != nil {
		docComment = e.extractDocstring(bodyNode)
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:            classID,
		Type:          graph.NodeClass,
		Name:          name,
		QualifiedName: name,
		FilePath:      e.filePath,
		Line:          startLine,
		EndLine:       endLine,
		Language:      string(parser.LangPython),
		Exported:      exported,
		DocComment:    docComment,
		Properties:    props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, classID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: classID,
	})

	// Extract methods and nested entities from body
	if bodyNode != nil {
		e.walkClassBody(bodyNode, classID, name)
	}
}

func (e *extractor) extractBaseClasses(node *sitter.Node) []string {
	var bases []string
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		bases = append(bases, e.nodeText(child))
	}
	return bases
}

func (e *extractor) walkClassBody(body *sitter.Node, classID, className string) {
	for i := 0; i < int(body.NamedChildCount()); i++ {
		child := body.NamedChild(i)
		switch child.Type() {
		case "function_definition", "decorated_definition":
			e.extractFunctionOrDecorated(child, classID, className)
		}
	}
}

func (e *extractor) extractFunctionOrDecorated(node *sitter.Node, parentID, className string) {
	if node.Type() == "decorated_definition" {
		var decorators []string
		var funcNode *sitter.Node
		for i := 0; i < int(node.NamedChildCount()); i++ {
			child := node.NamedChild(i)
			switch child.Type() {
			case "decorator":
				decorators = append(decorators, e.extractDecoratorName(child))
			case "function_definition":
				funcNode = child
			case "class_definition":
				e.extractClass(child, parentID)
				return
			}
		}
		if funcNode != nil {
			e.extractFunction(funcNode, parentID, className, decorators, node)
		}
		return
	}
	e.extractFunction(node, parentID, className, nil, node)
}

func (e *extractor) extractDecoratorName(node *sitter.Node) string {
	// decorator node: "@" identifier | "@" dotted_name | "@" call
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier":
			return e.nodeText(child)
		case "dotted_name":
			return e.nodeText(child)
		case "call":
			// e.g., @decorator(args)
			if child.NamedChildCount() > 0 {
				return e.nodeText(child.NamedChild(0))
			}
		}
	}
	return ""
}

func (e *extractor) extractFunction(node *sitter.Node, parentID, className string, decorators []string, outerNode *sitter.Node) {
	name := ""
	sig := ""
	returnType := ""
	var bodyNode *sitter.Node

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier":
			name = e.nodeText(child)
		case "parameters":
			sig = e.nodeText(child)
		case "type":
			returnType = e.nodeText(child)
		case "block":
			bodyNode = child
		}
	}

	if name == "" {
		return
	}

	startLine := int(outerNode.StartPoint().Row) + 1
	endLine := int(outerNode.EndPoint().Row) + 1
	exported := isExported(name)

	isMethod := className != ""
	nodeType := graph.NodeFunction
	if isMethod {
		nodeType = graph.NodeMethod
	}

	qualifiedName := name
	if className != "" {
		qualifiedName = className + "." + name
	}

	fullSig := "def " + name + sig
	if returnType != "" {
		fullSig += " -> " + returnType
	}

	props := make(map[string]string)
	if len(decorators) > 0 {
		props["decorators"] = strings.Join(decorators, ",")
	}
	if returnType != "" {
		props["return_type"] = returnType
	}
	if isMethod {
		props["class"] = className
	}

	// Extract docstring from function body
	docComment := ""
	if bodyNode != nil {
		docComment = e.extractDocstring(bodyNode)
	}

	funcID := graph.NewNodeID(string(nodeType), e.filePath, qualifiedName)

	e.nodes = append(e.nodes, &graph.Node{
		ID:            funcID,
		Type:          nodeType,
		Name:          name,
		QualifiedName: qualifiedName,
		FilePath:      e.filePath,
		Line:          startLine,
		EndLine:       endLine,
		Language:      string(parser.LangPython),
		Exported:      exported,
		Signature:     fullSig,
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

func (e *extractor) extractAssignment(node *sitter.Node) {
	// expression_statement -> assignment
	if node.NamedChildCount() == 0 {
		return
	}
	child := node.NamedChild(0)
	if child.Type() != "assignment" {
		return
	}

	// Get the left-hand side
	if child.NamedChildCount() < 2 {
		return
	}
	lhs := child.NamedChild(0)
	if lhs.Type() != "identifier" {
		return
	}

	name := e.nodeText(lhs)
	line := int(node.StartPoint().Row) + 1

	// Determine if it's a constant (UPPER_CASE) or variable
	nodeType := graph.NodeVariable
	if isConstantName(name) {
		nodeType = graph.NodeConstant
	}

	exported := isExported(name)

	varID := graph.NewNodeID(string(nodeType), e.filePath, name)

	e.nodes = append(e.nodes, &graph.Node{
		ID:            varID,
		Type:          nodeType,
		Name:          name,
		QualifiedName: name,
		FilePath:      e.filePath,
		Line:          line,
		Language:      string(parser.LangPython),
		Exported:      exported,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.moduleNodeID, varID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.moduleNodeID,
		TargetID: varID,
	})
}

func (e *extractor) extractDocstring(body *sitter.Node) string {
	if body.NamedChildCount() == 0 {
		return ""
	}
	first := body.NamedChild(0)
	if first.Type() == "expression_statement" && first.NamedChildCount() > 0 {
		expr := first.NamedChild(0)
		if expr.Type() == "string" {
			return cleanDocstring(e.nodeText(expr))
		}
	}
	return ""
}

func (e *extractor) nodeText(node *sitter.Node) string {
	return node.Content(e.content)
}

// Helper functions

func edgeID(sourceID, targetID, edgeType string) string {
	return graph.NewNodeID(edgeType, sourceID, targetID)
}

func isExported(name string) bool {
	if name == "" {
		return false
	}
	// In Python, names starting with underscore are considered private.
	return !strings.HasPrefix(name, "_")
}

func isConstantName(name string) bool {
	// Python convention: UPPER_CASE names are constants.
	if name == "" || strings.HasPrefix(name, "_") {
		return false
	}
	for _, r := range name {
		if unicode.IsLetter(r) && !unicode.IsUpper(r) {
			return false
		}
	}
	return true
}

func cleanDocstring(raw string) string {
	// Remove surrounding triple quotes
	s := raw
	for _, prefix := range []string{`"""`, `'''`, `r"""`, `r'''`} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimPrefix(s, prefix)
			quote := prefix[len(prefix)-3:]
			s = strings.TrimSuffix(s, quote)
			break
		}
	}
	return strings.TrimSpace(s)
}
