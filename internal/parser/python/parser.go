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

	// Lookup maps for function call resolution (built after walkTopLevel)
	importNames      map[string]string            // module/alias name → dep node ID
	funcNames        map[string]string            // function name → node ID
	classMethodNames map[string]map[string]string // className → methodName → node ID
}

func (e *extractor) extract() {
	e.extractFileNode()
	e.extractModule()

	root := e.tree.RootNode()
	e.walkTopLevel(root)
	e.buildCallMaps()
	e.walkForCalls(root, e.moduleNodeID, "")
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
			e.detectIncludeRouter(child)
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

// decoratorInfo holds a decorator's name and its string arguments.
type decoratorInfo struct {
	name string
	args []string
}

func (e *extractor) extractFunctionOrDecorated(node *sitter.Node, parentID, className string) {
	if node.Type() == "decorated_definition" {
		var decoratorNames []string
		var decoratorInfos []decoratorInfo
		var funcNode *sitter.Node
		for i := 0; i < int(node.NamedChildCount()); i++ {
			child := node.NamedChild(i)
			switch child.Type() {
			case "decorator":
				info := e.extractDecoratorInfo(child)
				decoratorNames = append(decoratorNames, info.name)
				decoratorInfos = append(decoratorInfos, info)
			case "function_definition":
				funcNode = child
			case "class_definition":
				e.extractClass(child, parentID)
				return
			}
		}
		if funcNode != nil {
			funcID := e.extractFunction(funcNode, parentID, className, decoratorNames, node)
			if funcID != "" {
				e.detectHTTPEndpoints(funcNode, funcID, decoratorInfos)
			}
		}
		return
	}
	e.extractFunction(node, parentID, className, nil, node)
}

// extractDecoratorInfo extracts both the decorator name and its positional string arguments.
func (e *extractor) extractDecoratorInfo(node *sitter.Node) decoratorInfo {
	// decorator node: "@" identifier | "@" dotted_name | "@" call
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier":
			return decoratorInfo{name: e.nodeText(child)}
		case "dotted_name":
			return decoratorInfo{name: e.nodeText(child)}
		case "call":
			// e.g., @router.get("/path") or @decorator(args)
			info := decoratorInfo{}
			if child.NamedChildCount() > 0 {
				fn := child.NamedChild(0)
				info.name = e.nodeText(fn)
			}
			// Extract string arguments from argument_list
			for j := 0; j < int(child.NamedChildCount()); j++ {
				argChild := child.NamedChild(j)
				if argChild.Type() == "argument_list" {
					for k := 0; k < int(argChild.NamedChildCount()); k++ {
						arg := argChild.NamedChild(k)
						if arg.Type() == "string" {
							info.args = append(info.args, cleanStringLiteral(e.nodeText(arg)))
						}
					}
				}
			}
			return info
		}
	}
	return decoratorInfo{}
}

func (e *extractor) extractFunction(node *sitter.Node, parentID, className string, decorators []string, outerNode *sitter.Node) string {
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
		return ""
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

	return funcID
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

// HTTP route method names recognized for FastAPI and Flask.
var httpDecoratorMethods = map[string]string{
	"get":    "GET",
	"post":   "POST",
	"put":    "PUT",
	"patch":  "PATCH",
	"delete": "DELETE",
}

// detectHTTPEndpoints checks if a decorated function has HTTP route decorators
// (FastAPI or Flask) and creates NodeAPIEndpoint + EdgeExposes.
func (e *extractor) detectHTTPEndpoints(funcNode *sitter.Node, funcID string, decorators []decoratorInfo) {
	// Extract function name for the handler property.
	handlerName := ""
	for i := 0; i < int(funcNode.NamedChildCount()); i++ {
		child := funcNode.NamedChild(i)
		if child.Type() == "identifier" {
			handlerName = e.nodeText(child)
			break
		}
	}

	for _, dec := range decorators {
		httpMethod, path, framework := classifyHTTPDecorator(dec)
		if httpMethod == "" {
			continue
		}

		endpointID := graph.NewNodeID(string(graph.NodeAPIEndpoint), e.filePath, httpMethod+":"+path)
		e.nodes = append(e.nodes, &graph.Node{
			ID:       endpointID,
			Type:     graph.NodeAPIEndpoint,
			Name:     httpMethod + " " + path,
			FilePath: e.filePath,
			Line:     int(funcNode.StartPoint().Row) + 1,
			Language: string(parser.LangPython),
			Properties: map[string]string{
				"http_method": httpMethod,
				"path":        path,
				"framework":   framework,
				"handler":     handlerName,
			},
		})

		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(funcID, endpointID, string(graph.EdgeExposes)),
			Type:     graph.EdgeExposes,
			SourceID: funcID,
			TargetID: endpointID,
		})
	}
}

// classifyHTTPDecorator determines if a decorator represents an HTTP route definition.
// Returns (httpMethod, path, framework) or ("", "", "") if not a route decorator.
func classifyHTTPDecorator(dec decoratorInfo) (string, string, string) {
	parts := strings.Split(dec.name, ".")
	if len(parts) < 2 {
		return "", "", ""
	}

	methodPart := parts[len(parts)-1]

	// Check for FastAPI-style: router.get, app.post, etc.
	if httpMethod, ok := httpDecoratorMethods[methodPart]; ok {
		path := ""
		if len(dec.args) > 0 {
			path = dec.args[0]
		}
		return httpMethod, path, "fastapi"
	}

	// Check for Flask-style: app.route, bp.route, blueprint.route
	if methodPart == "route" {
		path := ""
		if len(dec.args) > 0 {
			path = dec.args[0]
		}
		// For Flask @app.route, default method is GET
		return "GET", path, "flask"
	}

	return "", "", ""
}

// detectIncludeRouter detects FastAPI include_router calls like:
//
//	app.include_router(router, prefix="/api/v1")
func (e *extractor) detectIncludeRouter(node *sitter.Node) {
	// expression_statement -> call
	if node.NamedChildCount() == 0 {
		return
	}
	child := node.NamedChild(0)
	if child.Type() != "call" {
		return
	}

	// Check if it's *.include_router
	fn := child.NamedChild(0)
	if fn == nil || fn.Type() != "attribute" {
		return
	}
	fnText := e.nodeText(fn)
	if !strings.HasSuffix(fnText, ".include_router") {
		return
	}

	// Extract arguments: first positional (router name), prefix= keyword
	routerName := ""
	prefix := ""
	for i := 0; i < int(child.NamedChildCount()); i++ {
		argList := child.NamedChild(i)
		if argList.Type() != "argument_list" {
			continue
		}
		for j := 0; j < int(argList.NamedChildCount()); j++ {
			arg := argList.NamedChild(j)
			switch arg.Type() {
			case "identifier":
				if routerName == "" {
					routerName = e.nodeText(arg)
				}
			case "keyword_argument":
				// prefix="/api/v1"
				if arg.NamedChildCount() >= 2 {
					key := arg.NamedChild(0)
					val := arg.NamedChild(1)
					if e.nodeText(key) == "prefix" && val.Type() == "string" {
						prefix = cleanStringLiteral(e.nodeText(val))
					}
				}
			}
		}
	}

	if routerName == "" && prefix == "" {
		return
	}

	line := int(node.StartPoint().Row) + 1
	varID := graph.NewNodeID(string(graph.NodeVariable), e.filePath, "router_mount:"+routerName)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       varID,
		Type:     graph.NodeVariable,
		Name:     routerName,
		FilePath: e.filePath,
		Line:     line,
		Language: string(parser.LangPython),
		Properties: map[string]string{
			"kind":   "router_mount",
			"prefix": prefix,
			"router": routerName,
		},
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.moduleNodeID, varID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.moduleNodeID,
		TargetID: varID,
	})
}

// pythonBuiltins is the set of Python builtin function names to skip when
// detecting function calls.
var pythonBuiltins = map[string]bool{
	"print": true, "len": true, "range": true, "int": true, "str": true,
	"list": true, "dict": true, "set": true, "tuple": true, "type": true,
	"isinstance": true, "issubclass": true, "super": true, "property": true,
	"staticmethod": true, "classmethod": true, "enumerate": true, "zip": true,
	"map": true, "filter": true, "sorted": true, "reversed": true,
	"any": true, "all": true, "min": true, "max": true, "sum": true,
	"abs": true, "round": true, "open": true, "getattr": true, "setattr": true,
	"hasattr": true, "delattr": true, "input": true, "format": true,
	"repr": true, "id": true, "dir": true, "vars": true, "globals": true,
	"locals": true, "callable": true, "iter": true, "next": true, "hash": true,
	"hex": true, "oct": true, "bin": true, "ord": true, "chr": true,
	"bool": true, "bytes": true, "bytearray": true, "memoryview": true,
	"complex": true, "float": true, "frozenset": true, "object": true, "slice": true,
}

// buildCallMaps populates lookup maps from already-extracted nodes so that
// walkForCalls can resolve call targets.
func (e *extractor) buildCallMaps() {
	e.importNames = make(map[string]string)
	e.funcNames = make(map[string]string)
	e.classMethodNames = make(map[string]map[string]string)

	for _, n := range e.nodes {
		switch n.Type {
		case graph.NodeDependency:
			if n.Properties != nil && n.Properties["kind"] == "import" {
				// Store by the short name (last segment) and the full name
				name := n.Name
				e.importNames[name] = n.ID
				// Also register the last segment for dotted imports like "os.path"
				parts := strings.Split(name, ".")
				if len(parts) > 1 {
					e.importNames[parts[0]] = n.ID
				}
			}
		case graph.NodeFunction:
			e.funcNames[n.Name] = n.ID
		case graph.NodeMethod:
			className := n.Properties["class"]
			if className != "" {
				if e.classMethodNames[className] == nil {
					e.classMethodNames[className] = make(map[string]string)
				}
				e.classMethodNames[className][n.Name] = n.ID
			}
		}
	}
}

// HTTP client function names → HTTP methods.
var httpClientMethods = map[string]string{
	"get":    "GET",
	"post":   "POST",
	"put":    "PUT",
	"patch":  "PATCH",
	"delete": "DELETE",
	"head":   "HEAD",
}

// HTTP client module/object names.
var httpClientObjects = map[string]bool{
	"requests": true,
	"httpx":    true,
	"client":   true,
	"http":     true,
	"session":  true,
}

// walkForCalls recursively walks the AST looking for call expressions:
// HTTP client calls (requests.get, httpx.post, etc.) and general function calls.
// className tracks the enclosing class name when walking inside a class body.
func (e *extractor) walkForCalls(node *sitter.Node, parentFuncID string, className string) {
	if node == nil {
		return
	}

	currentFuncID := parentFuncID
	currentClassName := className

	switch node.Type() {
	case "class_definition":
		// Track entering a class to resolve self/cls calls and method IDs
		for i := 0; i < int(node.NamedChildCount()); i++ {
			child := node.NamedChild(i)
			if child.Type() == "identifier" {
				currentClassName = e.nodeText(child)
				break
			}
		}
	case "function_definition":
		// Get function/method name for ID lookup
		for i := 0; i < int(node.NamedChildCount()); i++ {
			child := node.NamedChild(i)
			if child.Type() == "identifier" {
				name := e.nodeText(child)
				if currentClassName != "" {
					currentFuncID = graph.NewNodeID(string(graph.NodeMethod), e.filePath, currentClassName+"."+name)
				} else {
					currentFuncID = graph.NewNodeID(string(graph.NodeFunction), e.filePath, name)
				}
				break
			}
		}
	}

	if node.Type() == "call" {
		// HTTP client check first; if it matches, skip general call check
		if !e.checkHTTPClientCall(node, currentFuncID) {
			e.checkFunctionCall(node, currentFuncID, currentClassName)
		}
	}

	for i := 0; i < int(node.NamedChildCount()); i++ {
		e.walkForCalls(node.NamedChild(i), currentFuncID, currentClassName)
	}
}

// checkHTTPClientCall checks if a call node is an HTTP client call like
// requests.get("/path") or httpx.post("/path") and creates appropriate nodes.
// Returns true if the node was recognized as an HTTP client call.
func (e *extractor) checkHTTPClientCall(node *sitter.Node, funcID string) bool {
	if node.NamedChildCount() < 2 {
		return false
	}

	fn := node.NamedChild(0)
	if fn.Type() != "attribute" {
		return false
	}

	fnText := e.nodeText(fn)
	parts := strings.Split(fnText, ".")
	if len(parts) < 2 {
		return false
	}

	objectName := strings.ToLower(parts[0])
	methodName := strings.ToLower(parts[len(parts)-1])

	// Check if this looks like an HTTP client call
	if !httpClientObjects[objectName] {
		return false
	}
	httpMethod, ok := httpClientMethods[methodName]
	if !ok {
		return false
	}

	// Extract URL path from first argument
	path := ""
	for i := 0; i < int(node.NamedChildCount()); i++ {
		argList := node.NamedChild(i)
		if argList.Type() != "argument_list" {
			continue
		}
		for j := 0; j < int(argList.NamedChildCount()); j++ {
			arg := argList.NamedChild(j)
			if arg.Type() == "string" {
				path = cleanStringLiteral(e.nodeText(arg))
				break
			}
			if arg.Type() == "concatenated_string" {
				// f-string: extract static parts and replace expressions with *
				path = extractFStringPath(e.nodeText(arg))
				break
			}
		}
		break
	}

	if path == "" {
		return false
	}

	// Determine the framework name
	framework := parts[0] // "requests", "httpx", etc.

	line := int(node.StartPoint().Row) + 1
	depID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, "api_call:"+httpMethod+":"+path+":"+fmt.Sprintf("%d", line))

	e.nodes = append(e.nodes, &graph.Node{
		ID:       depID,
		Type:     graph.NodeDependency,
		Name:     httpMethod + " " + path,
		FilePath: e.filePath,
		Line:     line,
		Language: string(parser.LangPython),
		Properties: map[string]string{
			"kind":        "api_call",
			"http_method": httpMethod,
			"path":        path,
			"framework":   framework,
		},
	})

	if funcID != "" {
		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(funcID, depID, string(graph.EdgeCalls)),
			Type:     graph.EdgeCalls,
			SourceID: funcID,
			TargetID: depID,
		})
	}

	return true
}

// checkFunctionCall checks if a call node is a general function call and creates
// EdgeCalls edges for: import-qualified calls, same-file calls, and self/cls calls.
func (e *extractor) checkFunctionCall(node *sitter.Node, funcID string, className string) {
	if funcID == "" || node.NamedChildCount() == 0 {
		return
	}

	callee := node.NamedChild(0)

	switch callee.Type() {
	case "attribute":
		// module.func() or self.method() or cls.method()
		fnText := e.nodeText(callee)
		dotIdx := strings.Index(fnText, ".")
		if dotIdx < 0 {
			return
		}
		objectName := fnText[:dotIdx]
		methodName := fnText[dotIdx+1:]

		// self/cls method call
		if (objectName == "self" || objectName == "cls") && className != "" {
			if methods, ok := e.classMethodNames[className]; ok {
				if targetID, ok := methods[methodName]; ok {
					e.edges = append(e.edges, &graph.Edge{
						ID:       edgeID(funcID, targetID, string(graph.EdgeCalls)),
						Type:     graph.EdgeCalls,
						SourceID: funcID,
						TargetID: targetID,
						Properties: map[string]string{
							"callee": methodName,
						},
					})
				}
			}
			return
		}

		// Import-qualified call: match object against import names
		if targetID, ok := e.importNames[objectName]; ok {
			e.edges = append(e.edges, &graph.Edge{
				ID:       edgeID(funcID, targetID, string(graph.EdgeCalls)),
				Type:     graph.EdgeCalls,
				SourceID: funcID,
				TargetID: targetID,
				Properties: map[string]string{
					"callee": methodName,
				},
			})
		}

	case "identifier":
		// Same-file call: helper()
		name := e.nodeText(callee)
		if pythonBuiltins[name] {
			return
		}
		if targetID, ok := e.funcNames[name]; ok {
			e.edges = append(e.edges, &graph.Edge{
				ID:       edgeID(funcID, targetID, string(graph.EdgeCalls)),
				Type:     graph.EdgeCalls,
				SourceID: funcID,
				TargetID: targetID,
			})
		}
	}
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

// cleanStringLiteral removes surrounding quotes from a Python string literal.
func cleanStringLiteral(s string) string {
	// Remove f-string prefix
	s = strings.TrimPrefix(s, "f")
	s = strings.TrimPrefix(s, "r")
	s = strings.TrimPrefix(s, "b")

	// Remove surrounding quotes
	for _, q := range []string{`"""`, `'''`, `"`, `'`} {
		if strings.HasPrefix(s, q) && strings.HasSuffix(s, q) {
			return s[len(q) : len(s)-len(q)]
		}
	}
	return s
}

// extractFStringPath extracts a URL path from an f-string, replacing
// expression parts with wildcards. E.g.:
//
//	f"/api/v1/instances/{instance_id}/agents" → "/api/v1/instances/*/agents"
func extractFStringPath(raw string) string {
	s := cleanStringLiteral(raw)
	// Replace {expr} with *
	var result strings.Builder
	inBrace := 0
	for _, r := range s {
		switch r {
		case '{':
			inBrace++
			if inBrace == 1 {
				result.WriteByte('*')
			}
		case '}':
			inBrace--
		default:
			if inBrace == 0 {
				result.WriteRune(r)
			}
		}
	}
	return result.String()
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
