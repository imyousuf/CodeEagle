package typescript

import (
	"context"
	"fmt"
	"path/filepath"
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
	isTestFile   bool

	// Lookup maps for function call graph extraction, built by buildCallMaps().
	importNames      map[string]string            // imported module simple name → dep node ID
	funcNames        map[string]string            // function name → node ID
	classMethodNames map[string]map[string]string // className → methodName → node ID
}

func (e *extractor) extract() {
	e.extractFileNode()
	e.extractModuleNode()
	e.walkChildren(e.root)
	if e.isTestFile {
		e.extractTestFunctions(e.root)
	}
	e.buildCallMaps()
	e.walkAllNodes(e.root)
}

func (e *extractor) extractFileNode() {
	base := filepath.Base(e.filePath)
	e.isTestFile = isTestFilename(base)

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
		Language: string(parser.LangTypeScript),
	})
}

// isTestFilename returns true if the filename matches TypeScript test file patterns.
func isTestFilename(base string) bool {
	return strings.HasSuffix(base, ".test.ts") ||
		strings.HasSuffix(base, ".spec.ts") ||
		strings.HasSuffix(base, ".test.tsx") ||
		strings.HasSuffix(base, ".spec.tsx")
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
		Properties: map[string]string{
			"kind": "import",
		},
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

// Test function detection

// tsTestCallNames is the set of function names that define test cases in JS/TS test frameworks.
var tsTestCallNames = map[string]bool{
	"describe": true, "it": true, "test": true,
}

// extractTestFunctions walks the AST to find describe/it/test call expressions
// in test files and creates NodeTestFunction nodes for them.
func (e *extractor) extractTestFunctions(node *sitter.Node) {
	if node.Type() == "call_expression" {
		fnNode := e.findChildByFieldName(node, "function")
		if fnNode != nil && fnNode.Type() == "identifier" {
			fnName := e.nodeText(fnNode)
			if tsTestCallNames[fnName] {
				args := e.findChildByFieldName(node, "arguments")
				if args != nil {
					testName := e.extractFirstStringArg(args)
					if testName != "" {
						qualifiedName := fnName + ":" + testName
						testFuncID := graph.NewNodeID(string(graph.NodeTestFunction), e.filePath, qualifiedName)
						e.nodes = append(e.nodes, &graph.Node{
							ID:            testFuncID,
							Type:          graph.NodeTestFunction,
							Name:          testName,
							QualifiedName: e.filePath + "." + qualifiedName,
							FilePath:      e.filePath,
							Line:          startLine(node),
							EndLine:       endLine(node),
							Language:      string(parser.LangTypeScript),
							Properties: map[string]string{
								"test_type": fnName,
							},
						})
						e.edges = append(e.edges, &graph.Edge{
							ID:       edgeID(e.moduleNodeID, testFuncID, string(graph.EdgeContains)),
							Type:     graph.EdgeContains,
							SourceID: e.moduleNodeID,
							TargetID: testFuncID,
						})
					}
				}
			}
		}
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		e.extractTestFunctions(node.Child(i))
	}
}

// extractFirstStringArg returns the text of the first string or template_string
// argument in an arguments node, with quotes stripped.
func (e *extractor) extractFirstStringArg(argsNode *sitter.Node) string {
	for i := 0; i < int(argsNode.ChildCount()); i++ {
		child := argsNode.Child(i)
		if child.Type() == "string" || child.Type() == "template_string" {
			return stripQuotes(e.nodeText(child))
		}
	}
	return ""
}

// Express route detection

var expressHTTPMethods = map[string]bool{
	"get": true, "post": true, "put": true, "patch": true, "delete": true, "all": true,
}

func (e *extractor) walkAllNodes(node *sitter.Node) {
	e.checkForExpressRoute(node)
	if !e.checkForHTTPClientCall(node) {
		e.checkForFunctionCall(node)
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		e.walkAllNodes(node.Child(i))
	}
}

func (e *extractor) checkForExpressRoute(node *sitter.Node) {
	if node.Type() != "call_expression" {
		return
	}

	// Look for member_expression as the function: router.get, app.post, etc.
	fnNode := e.findChildByFieldName(node, "function")
	if fnNode == nil || fnNode.Type() != "member_expression" {
		return
	}

	objectNode := e.findChildByFieldName(fnNode, "object")
	propertyNode := e.findChildByFieldName(fnNode, "property")
	if objectNode == nil || propertyNode == nil {
		return
	}

	methodName := e.nodeText(propertyNode)
	args := e.findChildByFieldName(node, "arguments")
	if args == nil {
		return
	}

	// Collect argument nodes (skip punctuation).
	var argNodes []*sitter.Node
	for i := 0; i < int(args.ChildCount()); i++ {
		child := args.Child(i)
		if child.Type() != "(" && child.Type() != ")" && child.Type() != "," {
			argNodes = append(argNodes, child)
		}
	}

	if len(argNodes) == 0 {
		return
	}

	// Check for app.use("/prefix", router) — router mount pattern.
	if methodName == "use" && len(argNodes) >= 2 {
		firstArg := argNodes[0]
		if firstArg.Type() == "string" || firstArg.Type() == "template_string" {
			path := stripQuotes(e.nodeText(firstArg))
			secondArg := argNodes[1]
			handlerName := e.nodeText(secondArg)
			varID := graph.NewNodeID(string(graph.NodeVariable), e.filePath, "mount:"+path)
			e.nodes = append(e.nodes, &graph.Node{
				ID:       varID,
				Type:     graph.NodeVariable,
				Name:     "mount " + path,
				FilePath: e.filePath,
				Line:     startLine(node),
				Language: string(parser.LangTypeScript),
				Properties: map[string]string{
					"kind":    "router_mount",
					"prefix":  path,
					"handler": handlerName,
				},
			})
			e.edges = append(e.edges, &graph.Edge{
				ID:       edgeID(e.moduleNodeID, varID, string(graph.EdgeContains)),
				Type:     graph.EdgeContains,
				SourceID: e.moduleNodeID,
				TargetID: varID,
			})
			return
		}
	}

	// Check for route definitions: router.get("/path", handler).
	if !expressHTTPMethods[methodName] {
		return
	}

	firstArg := argNodes[0]
	if firstArg.Type() != "string" && firstArg.Type() != "template_string" {
		return
	}
	path := stripQuotes(e.nodeText(firstArg))

	// Determine handler name from the last non-path argument.
	handlerName := ""
	if len(argNodes) >= 2 {
		lastArg := argNodes[len(argNodes)-1]
		switch lastArg.Type() {
		case "identifier":
			handlerName = e.nodeText(lastArg)
		case "member_expression":
			handlerName = e.nodeText(lastArg)
		default:
			handlerName = "anonymous"
		}
	}

	httpMethod := strings.ToUpper(methodName)
	endpointID := graph.NewNodeID(string(graph.NodeAPIEndpoint), e.filePath, httpMethod+":"+path)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       endpointID,
		Type:     graph.NodeAPIEndpoint,
		Name:     httpMethod + " " + path,
		FilePath: e.filePath,
		Line:     startLine(node),
		Language: string(parser.LangTypeScript),
		Properties: map[string]string{
			"http_method": httpMethod,
			"path":        path,
			"framework":   "express",
			"handler":     handlerName,
		},
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.moduleNodeID, endpointID, string(graph.EdgeExposes)),
		Type:     graph.EdgeExposes,
		SourceID: e.moduleNodeID,
		TargetID: endpointID,
	})
}

// HTTP client call detection

// axiosMethodNames maps axios method names to HTTP methods.
var axiosMethodNames = map[string]string{
	"get": "GET", "post": "POST", "put": "PUT", "patch": "PATCH",
	"delete": "DELETE", "head": "HEAD", "options": "OPTIONS",
}

func (e *extractor) checkForHTTPClientCall(node *sitter.Node) bool {
	if node.Type() != "call_expression" {
		return false
	}

	fnNode := e.findChildByFieldName(node, "function")
	if fnNode == nil {
		return false
	}

	args := e.findChildByFieldName(node, "arguments")
	if args == nil {
		return false
	}

	var argNodes []*sitter.Node
	for i := 0; i < int(args.ChildCount()); i++ {
		child := args.Child(i)
		if child.Type() != "(" && child.Type() != ")" && child.Type() != "," {
			argNodes = append(argNodes, child)
		}
	}
	if len(argNodes) == 0 {
		return false
	}

	var httpMethod, path, framework string

	switch fnNode.Type() {
	case "identifier":
		fnName := e.nodeText(fnNode)
		switch fnName {
		case "fetch":
			framework = "fetch"
			httpMethod = "UNKNOWN"
			path = e.extractURLFromArg(argNodes[0])
		case "useSWR", "useQuery":
			framework = "swr"
			httpMethod = "GET"
			path = e.extractURLFromArg(argNodes[0])
		default:
			// Direct call like axios("/path") — check if it matches known client names.
			if fnName == "axios" {
				framework = "axios"
				httpMethod = "UNKNOWN"
				path = e.extractURLFromArg(argNodes[0])
			}
		}
	case "member_expression":
		objectNode := e.findChildByFieldName(fnNode, "object")
		propertyNode := e.findChildByFieldName(fnNode, "property")
		if objectNode == nil || propertyNode == nil {
			return false
		}
		objName := e.nodeText(objectNode)
		methodName := e.nodeText(propertyNode)

		// axios.get, axios.post, etc.
		if objName == "axios" {
			if method, ok := axiosMethodNames[methodName]; ok {
				framework = "axios"
				httpMethod = method
				path = e.extractURLFromArg(argNodes[0])
			}
		}
		// http.get, httpClient.get, client.get, api.get, etc.
		if framework == "" {
			if method, ok := axiosMethodNames[methodName]; ok {
				// Heuristic: if the object looks like an HTTP client variable.
				lower := strings.ToLower(objName)
				if strings.Contains(lower, "http") || strings.Contains(lower, "client") ||
					strings.Contains(lower, "api") || strings.Contains(lower, "axios") {
					framework = "http_client"
					httpMethod = method
					path = e.extractURLFromArg(argNodes[0])
				}
			}
		}
	}

	if framework == "" || path == "" {
		return false
	}

	depID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, framework+":"+httpMethod+":"+path)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       depID,
		Type:     graph.NodeDependency,
		Name:     httpMethod + " " + path,
		FilePath: e.filePath,
		Line:     startLine(node),
		Language: string(parser.LangTypeScript),
		Properties: map[string]string{
			"kind":        "api_call",
			"http_method": httpMethod,
			"path":        path,
			"framework":   framework,
		},
	})

	// Find containing function and create EdgeCalls.
	containerID := e.findContainingFunctionID(node)
	if containerID != "" {
		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(containerID, depID, string(graph.EdgeCalls)),
			Type:     graph.EdgeCalls,
			SourceID: containerID,
			TargetID: depID,
		})
	} else {
		// Module-level call.
		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(e.moduleNodeID, depID, string(graph.EdgeCalls)),
			Type:     graph.EdgeCalls,
			SourceID: e.moduleNodeID,
			TargetID: depID,
		})
	}
	return true
}

func (e *extractor) extractURLFromArg(arg *sitter.Node) string {
	switch arg.Type() {
	case "string":
		return stripQuotes(e.nodeText(arg))
	case "template_string":
		return e.extractTemplateLiteralPath(arg)
	case "binary_expression":
		// Handle string concatenation like '/api/users/' + id.
		// Extract the left-most string literal and append * for dynamic parts.
		return e.extractConcatPath(arg)
	}
	return ""
}

// extractConcatPath extracts a URL path from a binary expression (string concatenation).
// It walks the left side to find string literals and treats the rest as wildcards.
// Returns "" if no string literal is found (e.g., variable + variable).
func (e *extractor) extractConcatPath(node *sitter.Node) string {
	if node.Type() == "string" {
		return stripQuotes(e.nodeText(node))
	}
	if node.Type() != "binary_expression" {
		// Non-string, non-binary node (variable, member access, etc.) — no path info.
		return ""
	}
	left := e.findChildByFieldName(node, "left")
	if left == nil && node.ChildCount() > 0 {
		left = node.Child(0)
	}
	if left == nil {
		return ""
	}
	leftPath := e.extractConcatPath(left)
	if leftPath == "" {
		return ""
	}
	return leftPath + "*"
}

func (e *extractor) extractTemplateLiteralPath(node *sitter.Node) string {
	// Walk template string children: template_substitution nodes become "*".
	var parts []string
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "string_fragment":
			parts = append(parts, e.nodeText(child))
		case "template_substitution":
			parts = append(parts, "*")
		case "`":
			// Skip backtick delimiters.
		default:
			parts = append(parts, e.nodeText(child))
		}
	}
	return strings.Join(parts, "")
}

func (e *extractor) findContainingFunctionID(node *sitter.Node) string {
	current := node.Parent()
	for current != nil {
		switch current.Type() {
		case "function_declaration":
			nameNode := e.findChildByFieldName(current, "name")
			if nameNode != nil {
				name := e.nodeText(nameNode)
				return graph.NewNodeID(string(graph.NodeFunction), e.filePath, name)
			}
		case "method_definition":
			nameNode := e.findChildByFieldName(current, "name")
			if nameNode != nil {
				methodName := e.nodeText(nameNode)
				// Find the class name by looking for the class_declaration ancestor.
				className := e.findAncestorClassName(current)
				if className != "" {
					return graph.NewNodeID(string(graph.NodeMethod), e.filePath, className+"."+methodName)
				}
			}
		case "arrow_function", "function":
			// Check if this is assigned to a variable (const foo = () => ...).
			parent := current.Parent()
			if parent != nil && parent.Type() == "variable_declarator" {
				nameNode := e.findChildByFieldName(parent, "name")
				if nameNode != nil {
					name := e.nodeText(nameNode)
					return graph.NewNodeID(string(graph.NodeFunction), e.filePath, name)
				}
			}
		}
		current = current.Parent()
	}
	return ""
}

func (e *extractor) findAncestorClassName(node *sitter.Node) string {
	current := node.Parent()
	for current != nil {
		if current.Type() == "class_declaration" || current.Type() == "abstract_class_declaration" {
			nameNode := e.findChildByFieldName(current, "name")
			if nameNode != nil {
				return e.nodeText(nameNode)
			}
		}
		current = current.Parent()
	}
	return ""
}

// Function call graph extraction

// tsBuiltins is the set of built-in names to skip when resolving function calls.
var tsBuiltins = map[string]bool{
	"console": true, "setTimeout": true, "setInterval": true, "clearTimeout": true,
	"clearInterval": true, "parseInt": true, "parseFloat": true, "isNaN": true,
	"isFinite": true, "Array": true, "Object": true, "String": true, "Number": true,
	"Boolean": true, "JSON": true, "Math": true, "Date": true, "Promise": true,
	"Error": true, "RegExp": true, "Map": true, "Set": true, "Symbol": true,
	"require": true, "undefined": true, "NaN": true, "Infinity": true,
}

func (e *extractor) buildCallMaps() {
	e.importNames = make(map[string]string)
	e.funcNames = make(map[string]string)
	e.classMethodNames = make(map[string]map[string]string)

	// Build a map from module path to dependency node ID.
	depByModule := make(map[string]string)
	for _, n := range e.nodes {
		switch n.Type {
		case graph.NodeDependency:
			if n.Properties["kind"] == "import" {
				depByModule[n.Name] = n.ID
				// Store full module path as key.
				e.importNames[n.Name] = n.ID
				// Also store the last path component for matching.
				simpleName := lastPathComponent(n.Name)
				if simpleName != n.Name {
					e.importNames[simpleName] = n.ID
				}
			}
		case graph.NodeFunction, graph.NodeTestFunction:
			e.funcNames[n.Name] = n.ID
		case graph.NodeMethod:
			if n.Properties != nil && n.Properties["receiver"] != "" {
				className := n.Properties["receiver"]
				if e.classMethodNames[className] == nil {
					e.classMethodNames[className] = make(map[string]string)
				}
				e.classMethodNames[className][n.Name] = n.ID
			}
		}
	}

	// Walk AST import statements to map local binding names to dependency node IDs.
	e.extractImportBindings(e.root, depByModule)
}

// extractImportBindings walks import_statement nodes and maps each imported local
// name (default import, named import specifier, namespace import) to the dep node ID.
func (e *extractor) extractImportBindings(node *sitter.Node, depByModule map[string]string) {
	if node.Type() == "import_statement" {
		source := e.findChildByType(node, "string")
		if source == nil {
			return
		}
		modulePath := stripQuotes(e.nodeText(source))
		depID, ok := depByModule[modulePath]
		if !ok {
			return
		}
		// Walk children to find import clause / specifiers.
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(i)
			switch child.Type() {
			case "import_clause":
				e.extractImportClauseBindings(child, depID)
			}
		}
		return
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		e.extractImportBindings(node.Child(i), depByModule)
	}
}

func (e *extractor) extractImportClauseBindings(node *sitter.Node, depID string) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			// Default import: import axios from 'axios'
			e.importNames[e.nodeText(child)] = depID
		case "named_imports":
			// Named imports: import { format, parse } from './utils'
			for j := 0; j < int(child.ChildCount()); j++ {
				spec := child.Child(j)
				if spec.Type() == "import_specifier" {
					// The local name is the "name" field, or if aliased, the "alias" field.
					alias := e.findChildByFieldName(spec, "alias")
					if alias != nil {
						e.importNames[e.nodeText(alias)] = depID
					} else {
						nameNode := e.findChildByFieldName(spec, "name")
						if nameNode != nil {
							e.importNames[e.nodeText(nameNode)] = depID
						}
					}
				}
			}
		case "namespace_import":
			// Namespace import: import * as utils from './utils'
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				if gc.Type() == "identifier" {
					e.importNames[e.nodeText(gc)] = depID
				}
			}
		}
	}
}

func (e *extractor) checkForFunctionCall(node *sitter.Node) {
	if node.Type() != "call_expression" {
		return
	}

	fnNode := e.findChildByFieldName(node, "function")
	if fnNode == nil {
		return
	}

	callerID := e.findContainingFunctionID(node)
	if callerID == "" {
		callerID = e.moduleNodeID
	}

	switch fnNode.Type() {
	case "identifier":
		name := e.nodeText(fnNode)
		if tsBuiltins[name] {
			return
		}
		// Match against same-file functions.
		if targetID, ok := e.funcNames[name]; ok {
			e.edges = append(e.edges, &graph.Edge{
				ID:       edgeID(callerID, targetID, string(graph.EdgeCalls)),
				Type:     graph.EdgeCalls,
				SourceID: callerID,
				TargetID: targetID,
			})
			return
		}
		// Match against imports (e.g., named import used as direct call).
		if targetID, ok := e.importNames[name]; ok {
			e.edges = append(e.edges, &graph.Edge{
				ID:       edgeID(callerID, targetID, string(graph.EdgeCalls)),
				Type:     graph.EdgeCalls,
				SourceID: callerID,
				TargetID: targetID,
				Properties: map[string]string{
					"callee": name,
				},
			})
		}

	case "member_expression":
		objectNode := e.findChildByFieldName(fnNode, "object")
		propertyNode := e.findChildByFieldName(fnNode, "property")
		if objectNode == nil || propertyNode == nil {
			return
		}
		objName := e.nodeText(objectNode)
		methodName := e.nodeText(propertyNode)

		if tsBuiltins[objName] {
			return
		}

		// this.method() — match against class methods of the enclosing class.
		if objName == "this" {
			className := e.findAncestorClassName(node)
			if className != "" {
				if methods, ok := e.classMethodNames[className]; ok {
					if targetID, ok := methods[methodName]; ok {
						e.edges = append(e.edges, &graph.Edge{
							ID:       edgeID(callerID, targetID, string(graph.EdgeCalls)),
							Type:     graph.EdgeCalls,
							SourceID: callerID,
							TargetID: targetID,
						})
					}
				}
			}
			return
		}

		// obj.method() — match obj against imports.
		if targetID, ok := e.importNames[objName]; ok {
			e.edges = append(e.edges, &graph.Edge{
				ID:       edgeID(callerID, targetID, string(graph.EdgeCalls)),
				Type:     graph.EdgeCalls,
				SourceID: callerID,
				TargetID: targetID,
				Properties: map[string]string{
					"callee": methodName,
				},
			})
		}
	}
}

// lastPathComponent extracts the last segment of a module path.
// e.g., "./utils" -> "utils", "axios" -> "axios", "@scope/pkg" -> "pkg".
func lastPathComponent(modulePath string) string {
	for i := len(modulePath) - 1; i >= 0; i-- {
		if modulePath[i] == '/' {
			return modulePath[i+1:]
		}
	}
	return modulePath
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
