package golang

import (
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"strings"
	"unicode"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

// GoParser extracts knowledge graph nodes and edges from Go source files.
type GoParser struct{}

// NewParser creates a new Go parser.
func NewParser() *GoParser {
	return &GoParser{}
}

func (p *GoParser) Language() parser.Language {
	return parser.LangGo
}

func (p *GoParser) Extensions() []string {
	return parser.FileExtensions[parser.LangGo]
}

func (p *GoParser) ParseFile(filePath string, content []byte) (*parser.ParseResult, error) {
	fset := token.NewFileSet()
	file, err := goparser.ParseFile(fset, filePath, content, goparser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filePath, err)
	}

	e := &extractor{
		fset:     fset,
		file:     file,
		filePath: filePath,
	}
	e.extract()

	return &parser.ParseResult{
		Nodes:    e.nodes,
		Edges:    e.edges,
		FilePath: filePath,
		Language: parser.LangGo,
	}, nil
}

// testFuncPrefixes lists prefixes that identify Go test functions.
var testFuncPrefixes = []string{"Test", "Benchmark", "Example", "Fuzz"}

// extractor walks a Go AST and builds graph nodes and edges.
type extractor struct {
	fset     *token.FileSet
	file     *ast.File
	filePath string
	nodes    []*graph.Node
	edges    []*graph.Edge

	pkgNodeID  string
	fileNodeID string
	isTestFile bool

	// Track interfaces and struct methods for Implements edge detection.
	interfaces    map[string]map[string]bool // interface name -> set of method names
	structMethods map[string]map[string]bool // struct name -> set of method names

	// Struct field types: struct name -> field name -> type string.
	structFieldTypes map[string]map[string]string

	// Lookup maps for function call extraction, built by buildCallMaps().
	importAliasMap    map[string]string            // import alias → dep node ID
	funcNameMap       map[string]string            // function name → node ID
	methodsByReceiver map[string]map[string]string // receiver type → method name → node ID
}

func (e *extractor) extract() {
	e.interfaces = make(map[string]map[string]bool)
	e.structMethods = make(map[string]map[string]bool)
	e.structFieldTypes = make(map[string]map[string]string)

	e.extractFileNode()
	e.extractPackage()
	e.extractImports()
	e.extractDeclarations()
	e.extractHTTPRoutes()
	e.extractHTTPClientCalls()
	e.extractImplementsEdges()
	e.buildCallMaps()
	e.extractFunctionCalls()
}

func (e *extractor) extractFileNode() {
	nodeType := graph.NodeFile
	if strings.HasSuffix(e.filePath, "_test.go") {
		nodeType = graph.NodeTestFile
		e.isTestFile = true
	}
	e.fileNodeID = graph.NewNodeID(string(nodeType), e.filePath, e.filePath)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       e.fileNodeID,
		Type:     nodeType,
		Name:     e.filePath,
		FilePath: e.filePath,
		Language: string(parser.LangGo),
	})
}

func (e *extractor) extractPackage() {
	pkgName := e.file.Name.Name
	e.pkgNodeID = graph.NewNodeID(string(graph.NodePackage), e.filePath, pkgName)

	docComment := ""
	if e.file.Doc != nil {
		docComment = e.file.Doc.Text()
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:         e.pkgNodeID,
		Type:       graph.NodePackage,
		Name:       pkgName,
		FilePath:   e.filePath,
		Line:       e.pos(e.file.Package),
		Language:   string(parser.LangGo),
		Package:    pkgName,
		DocComment: docComment,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.fileNodeID, e.pkgNodeID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.fileNodeID,
		TargetID: e.pkgNodeID,
	})
}

func (e *extractor) extractImports() {
	for _, imp := range e.file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		depID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, path)

		e.nodes = append(e.nodes, &graph.Node{
			ID:       depID,
			Type:     graph.NodeDependency,
			Name:     path,
			FilePath: e.filePath,
			Line:     e.pos(imp.Pos()),
			Language: string(parser.LangGo),
			Package:  e.file.Name.Name,
			Properties: map[string]string{
				"kind": "import",
			},
		})

		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(e.pkgNodeID, depID, string(graph.EdgeImports)),
			Type:     graph.EdgeImports,
			SourceID: e.pkgNodeID,
			TargetID: depID,
		})
	}
}

func (e *extractor) extractDeclarations() {
	for _, decl := range e.file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			e.extractFuncDecl(d)
		case *ast.GenDecl:
			e.extractGenDecl(d)
		}
	}
}

func (e *extractor) extractFuncDecl(fn *ast.FuncDecl) {
	name := fn.Name.Name
	exported := isExported(name)
	sig := funcSignature(fn)
	doc := ""
	if fn.Doc != nil {
		doc = fn.Doc.Text()
	}
	startLine := e.pos(fn.Pos())
	endLine := e.pos(fn.End())

	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		// Method
		recvType := receiverTypeName(fn.Recv.List[0].Type)
		methodID := graph.NewNodeID(string(graph.NodeMethod), e.filePath, recvType+"."+name)

		e.nodes = append(e.nodes, &graph.Node{
			ID:            methodID,
			Type:          graph.NodeMethod,
			Name:          name,
			QualifiedName: recvType + "." + name,
			FilePath:      e.filePath,
			Line:          startLine,
			EndLine:       endLine,
			Package:       e.file.Name.Name,
			Language:      string(parser.LangGo),
			Exported:      exported,
			Signature:     sig,
			DocComment:    doc,
			Properties: map[string]string{
				"receiver": recvType,
			},
		})

		// Edge: package contains method
		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(e.pkgNodeID, methodID, string(graph.EdgeContains)),
			Type:     graph.EdgeContains,
			SourceID: e.pkgNodeID,
			TargetID: methodID,
		})

		// Track for Implements detection
		if e.structMethods[recvType] == nil {
			e.structMethods[recvType] = make(map[string]bool)
		}
		e.structMethods[recvType][name] = true
	} else {
		// Function — detect test functions in test files.
		nodeType := graph.NodeFunction
		if e.isTestFile && isTestFuncName(name) {
			nodeType = graph.NodeTestFunction
		}
		funcID := graph.NewNodeID(string(nodeType), e.filePath, name)

		e.nodes = append(e.nodes, &graph.Node{
			ID:            funcID,
			Type:          nodeType,
			Name:          name,
			QualifiedName: e.file.Name.Name + "." + name,
			FilePath:      e.filePath,
			Line:          startLine,
			EndLine:       endLine,
			Package:       e.file.Name.Name,
			Language:      string(parser.LangGo),
			Exported:      exported,
			Signature:     sig,
			DocComment:    doc,
		})

		// Edge: package contains function
		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(e.pkgNodeID, funcID, string(graph.EdgeContains)),
			Type:     graph.EdgeContains,
			SourceID: e.pkgNodeID,
			TargetID: funcID,
		})
	}
}

func (e *extractor) extractGenDecl(decl *ast.GenDecl) {
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			e.extractTypeSpec(s, decl)
		case *ast.ValueSpec:
			e.extractValueSpec(s, decl)
		}
	}
}

func (e *extractor) extractTypeSpec(ts *ast.TypeSpec, decl *ast.GenDecl) {
	name := ts.Name.Name
	exported := isExported(name)
	doc := ""
	if ts.Doc != nil {
		doc = ts.Doc.Text()
	} else if decl.Doc != nil {
		doc = decl.Doc.Text()
	}
	startLine := e.pos(ts.Pos())
	endLine := e.pos(ts.End())

	switch t := ts.Type.(type) {
	case *ast.StructType:
		e.extractStruct(name, exported, doc, startLine, endLine, t)
	case *ast.InterfaceType:
		e.extractInterface(name, exported, doc, startLine, endLine, t)
	default:
		// Type alias or definition
		typeID := graph.NewNodeID(string(graph.NodeType_), e.filePath, name)
		e.nodes = append(e.nodes, &graph.Node{
			ID:            typeID,
			Type:          graph.NodeType_,
			Name:          name,
			QualifiedName: e.file.Name.Name + "." + name,
			FilePath:      e.filePath,
			Line:          startLine,
			EndLine:       endLine,
			Package:       e.file.Name.Name,
			Language:      string(parser.LangGo),
			Exported:      exported,
			DocComment:    doc,
		})
		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(e.pkgNodeID, typeID, string(graph.EdgeContains)),
			Type:     graph.EdgeContains,
			SourceID: e.pkgNodeID,
			TargetID: typeID,
		})
	}
}

func (e *extractor) extractStruct(structName string, exported bool, doc string, startLine, endLine int, st *ast.StructType) {
	structID := graph.NewNodeID(string(graph.NodeStruct), e.filePath, structName)

	props := make(map[string]string)
	if st.Fields != nil {
		fields := make([]string, 0, len(st.Fields.List))
		for _, f := range st.Fields.List {
			if len(f.Names) > 0 {
				typeStr := typeExprString(f.Type)
				for _, n := range f.Names {
					fields = append(fields, n.Name)
					// Store field type for chained call resolution.
					if e.structFieldTypes[structName] == nil {
						e.structFieldTypes[structName] = make(map[string]string)
					}
					e.structFieldTypes[structName][n.Name] = typeStr
				}
			} else {
				// Embedded field
				fields = append(fields, typeExprString(f.Type))
			}
		}
		props["fields"] = strings.Join(fields, ",")
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:            structID,
		Type:          graph.NodeStruct,
		Name:          structName,
		QualifiedName: e.file.Name.Name + "." + structName,
		FilePath:      e.filePath,
		Line:          startLine,
		EndLine:       endLine,
		Package:       e.file.Name.Name,
		Language:      string(parser.LangGo),
		Exported:      exported,
		DocComment:    doc,
		Properties:    props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.pkgNodeID, structID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.pkgNodeID,
		TargetID: structID,
	})
}

func (e *extractor) extractInterface(name string, exported bool, doc string, startLine, endLine int, it *ast.InterfaceType) {
	ifaceID := graph.NewNodeID(string(graph.NodeInterface), e.filePath, name)

	methods := make(map[string]bool)
	methodNames := make([]string, 0)
	if it.Methods != nil {
		for _, m := range it.Methods.List {
			if len(m.Names) > 0 {
				for _, n := range m.Names {
					methods[n.Name] = true
					methodNames = append(methodNames, n.Name)
				}
			}
		}
	}
	e.interfaces[name] = methods

	props := make(map[string]string)
	if len(methodNames) > 0 {
		props["methods"] = strings.Join(methodNames, ",")
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:            ifaceID,
		Type:          graph.NodeInterface,
		Name:          name,
		QualifiedName: e.file.Name.Name + "." + name,
		FilePath:      e.filePath,
		Line:          startLine,
		EndLine:       endLine,
		Package:       e.file.Name.Name,
		Language:      string(parser.LangGo),
		Exported:      exported,
		DocComment:    doc,
		Properties:    props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.pkgNodeID, ifaceID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.pkgNodeID,
		TargetID: ifaceID,
	})
}

func (e *extractor) extractValueSpec(vs *ast.ValueSpec, decl *ast.GenDecl) {
	doc := ""
	if vs.Doc != nil {
		doc = vs.Doc.Text()
	} else if decl.Doc != nil {
		doc = decl.Doc.Text()
	}

	isConst := decl.Tok == token.CONST
	nodeType := graph.NodeVariable
	if isConst {
		nodeType = graph.NodeConstant
	}

	for _, ident := range vs.Names {
		name := ident.Name
		if name == "_" {
			continue
		}
		exported := isExported(name)
		nodeID := graph.NewNodeID(string(nodeType), e.filePath, name)
		line := e.pos(ident.Pos())

		e.nodes = append(e.nodes, &graph.Node{
			ID:            nodeID,
			Type:          nodeType,
			Name:          name,
			QualifiedName: e.file.Name.Name + "." + name,
			FilePath:      e.filePath,
			Line:          line,
			Package:       e.file.Name.Name,
			Language:      string(parser.LangGo),
			Exported:      exported,
			DocComment:    doc,
		})

		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(e.pkgNodeID, nodeID, string(graph.EdgeContains)),
			Type:     graph.EdgeContains,
			SourceID: e.pkgNodeID,
			TargetID: nodeID,
		})
	}
}

// httpRouteMethod maps capitalized Gin-style method names to HTTP verbs.
var ginMethods = map[string]bool{
	"GET":    true,
	"POST":   true,
	"PUT":    true,
	"PATCH":  true,
	"DELETE": true,
	"Handle": true,
	"Any":    true,
}

// routeInfo holds a detected HTTP route.
type routeInfo struct {
	method    string // HTTP method (GET, POST, etc.)
	path      string // Route path
	framework string // "gin", "net/http", "gorilla/mux"
	handler   string // Handler function/identifier name
	line      int    // Source line
}

func (e *extractor) extractHTTPRoutes() {
	for _, decl := range e.file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		enclosingNodeID := e.enclosingFuncNodeID(fn)

		// Collect group prefix assignments: variable name -> prefix path.
		groupPrefixes := make(map[string]string)
		e.collectGroupPrefixes(fn.Body, groupPrefixes)

		// Track inner calls consumed by chained .Methods() to avoid duplicates.
		consumedCalls := make(map[*ast.CallExpr]bool)

		// First pass: find .Methods() chains and mark inner HandleFunc calls as consumed.
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Methods" {
				return true
			}
			if innerCall, ok := sel.X.(*ast.CallExpr); ok {
				consumedCalls[innerCall] = true
			}
			return true
		})

		// Second pass: match all route registrations.
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if consumedCalls[call] {
				return true
			}

			routes := e.matchRouteCall(call, groupPrefixes)
			for _, r := range routes {
				e.addRouteNode(r, enclosingNodeID)
			}

			return true
		})
	}
}

// collectGroupPrefixes scans for Gin router group assignments like:
//
//	v1 := r.Group("/api/v1")
//	api := router.Group("/api")
func (e *extractor) collectGroupPrefixes(body *ast.BlockStmt, prefixes map[string]string) {
	for _, stmt := range body.List {
		assign, ok := stmt.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) == 0 || len(assign.Rhs) == 0 {
			continue
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Group" {
			continue
		}
		if len(call.Args) < 1 {
			continue
		}
		pathLit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || pathLit.Kind != token.STRING {
			continue
		}
		prefix := strings.Trim(pathLit.Value, `"`)

		// Check if the receiver itself is a known group variable.
		if recvIdent, ok := sel.X.(*ast.Ident); ok {
			if parentPrefix, exists := prefixes[recvIdent.Name]; exists {
				prefix = strings.TrimRight(parentPrefix, "/") + prefix
			}
		}

		// Store for each LHS identifier.
		for _, lhs := range assign.Lhs {
			if ident, ok := lhs.(*ast.Ident); ok {
				prefixes[ident.Name] = prefix
			}
		}
	}
}

// matchRouteCall attempts to match a call expression as an HTTP route registration.
// Returns nil if it doesn't match.
func (e *extractor) matchRouteCall(call *ast.CallExpr, groupPrefixes map[string]string) []routeInfo {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	methodName := sel.Sel.Name

	// Case 1: Gin routes — r.GET("/path", handler)
	if ginMethods[methodName] {
		return e.matchGinRoute(call, sel, methodName, groupPrefixes)
	}

	// Case 2: net/http or gorilla/mux — mux.HandleFunc("/path", handler) or http.Handle("/path", handler)
	if methodName == "HandleFunc" || methodName == "Handle" {
		return e.matchHandleFuncRoute(call, sel)
	}

	// Case 3: gorilla/mux chained — r.HandleFunc("/path", handler).Methods("GET")
	// This is handled when we see the outer Methods() call.
	if methodName == "Methods" {
		return e.matchGorillaMethodsChain(call, sel, groupPrefixes)
	}

	return nil
}

func (e *extractor) matchGinRoute(call *ast.CallExpr, sel *ast.SelectorExpr, methodName string, groupPrefixes map[string]string) []routeInfo {
	if len(call.Args) < 1 {
		return nil
	}

	pathLit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || pathLit.Kind != token.STRING {
		return nil
	}
	path := strings.Trim(pathLit.Value, `"`)

	// Check if receiver is a group variable with a known prefix.
	if recvIdent, ok := sel.X.(*ast.Ident); ok {
		if prefix, exists := groupPrefixes[recvIdent.Name]; exists {
			path = strings.TrimRight(prefix, "/") + path
		}
	}

	httpMethod := methodName
	if methodName == "Handle" || methodName == "Any" {
		httpMethod = methodName
	}

	handler := e.extractHandlerName(call, 1)

	return []routeInfo{{
		method:    httpMethod,
		path:      path,
		framework: "gin",
		handler:   handler,
		line:      e.pos(call.Pos()),
	}}
}

func (e *extractor) matchHandleFuncRoute(call *ast.CallExpr, sel *ast.SelectorExpr) []routeInfo {
	if len(call.Args) < 1 {
		return nil
	}

	pathLit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || pathLit.Kind != token.STRING {
		return nil
	}
	path := strings.Trim(pathLit.Value, `"`)

	// Determine framework: if receiver is "http" package selector, it's net/http.
	// Otherwise, assume gorilla/mux or net/http (both use HandleFunc).
	framework := "net/http"
	if recvIdent, ok := sel.X.(*ast.Ident); ok {
		if recvIdent.Name != "http" {
			// Could be gorilla/mux or custom mux — mark as net/http for plain HandleFunc.
			framework = "net/http"
		}
	}

	handler := e.extractHandlerName(call, 1)

	return []routeInfo{{
		method:    "ANY",
		path:      path,
		framework: framework,
		handler:   handler,
		line:      e.pos(call.Pos()),
	}}
}

// matchGorillaMethodsChain handles gorilla/mux chained pattern:
//
//	r.HandleFunc("/path", handler).Methods("GET")
func (e *extractor) matchGorillaMethodsChain(call *ast.CallExpr, sel *ast.SelectorExpr, groupPrefixes map[string]string) []routeInfo {
	// sel.X should be the HandleFunc call expression.
	innerCall, ok := sel.X.(*ast.CallExpr)
	if !ok {
		return nil
	}
	innerSel, ok := innerCall.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	if innerSel.Sel.Name != "HandleFunc" && innerSel.Sel.Name != "Handle" {
		return nil
	}
	if len(innerCall.Args) < 1 {
		return nil
	}

	pathLit, ok := innerCall.Args[0].(*ast.BasicLit)
	if !ok || pathLit.Kind != token.STRING {
		return nil
	}
	path := strings.Trim(pathLit.Value, `"`)

	// Extract the HTTP method from Methods() args.
	httpMethod := "ANY"
	if len(call.Args) >= 1 {
		if methodLit, ok := call.Args[0].(*ast.BasicLit); ok && methodLit.Kind == token.STRING {
			httpMethod = strings.Trim(methodLit.Value, `"`)
		}
	}

	handler := e.extractHandlerName(innerCall, 1)

	return []routeInfo{{
		method:    httpMethod,
		path:      path,
		framework: "gorilla/mux",
		handler:   handler,
		line:      e.pos(innerCall.Pos()),
	}}
}

// extractHandlerName extracts the handler function/identifier name from the argIndex-th argument.
func (e *extractor) extractHandlerName(call *ast.CallExpr, argIndex int) string {
	if argIndex >= len(call.Args) {
		return ""
	}
	arg := call.Args[argIndex]
	switch h := arg.(type) {
	case *ast.Ident:
		return h.Name
	case *ast.SelectorExpr:
		return typeExprString(h)
	default:
		return ""
	}
}

func (e *extractor) addRouteNode(r routeInfo, enclosingNodeID string) {
	endpointID := graph.NewNodeID(string(graph.NodeAPIEndpoint), e.filePath, r.method+":"+r.path)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       endpointID,
		Type:     graph.NodeAPIEndpoint,
		Name:     r.method + " " + r.path,
		FilePath: e.filePath,
		Line:     r.line,
		Language: string(parser.LangGo),
		Properties: map[string]string{
			"http_method": r.method,
			"path":        r.path,
			"framework":   r.framework,
			"handler":     r.handler,
		},
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(enclosingNodeID, endpointID, string(graph.EdgeExposes)),
		Type:     graph.EdgeExposes,
		SourceID: enclosingNodeID,
		TargetID: endpointID,
	})
}

func (e *extractor) extractImplementsEdges() {
	for ifaceName, ifaceMethods := range e.interfaces {
		if len(ifaceMethods) == 0 {
			continue
		}
		ifaceID := graph.NewNodeID(string(graph.NodeInterface), e.filePath, ifaceName)
		for structName, structMethods := range e.structMethods {
			if implementsAll(structMethods, ifaceMethods) {
				structID := graph.NewNodeID(string(graph.NodeStruct), e.filePath, structName)
				e.edges = append(e.edges, &graph.Edge{
					ID:       edgeID(structID, ifaceID, string(graph.EdgeImplements)),
					Type:     graph.EdgeImplements,
					SourceID: structID,
					TargetID: ifaceID,
				})
			}
		}
	}
}

// Go HTTP client package-level functions.
var goHTTPPackageFuncs = map[string]string{
	"Get":      "GET",
	"Post":     "POST",
	"Head":     "HEAD",
	"PostForm": "POST",
}

// Go HTTP request constructors.
var goHTTPNewRequestFuncs = map[string]bool{
	"NewRequest":            true,
	"NewRequestWithContext": true,
}

// Go HTTP client method calls that carry a URL argument.
var goHTTPClientMethodsWithURL = map[string]string{
	"Get":  "GET",
	"Post": "POST",
	"Head": "HEAD",
}

// extractHTTPClientCalls walks function/method bodies for Go net/http client calls.
func (e *extractor) extractHTTPClientCalls() {
	// Build a quick map of import aliases → import path.
	httpAlias := "" // alias for "net/http"
	for _, imp := range e.file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if path == "net/http" {
			if imp.Name != nil {
				httpAlias = imp.Name.Name
			} else {
				httpAlias = "http"
			}
			break
		}
	}
	if httpAlias == "" {
		return // "net/http" not imported
	}

	for _, decl := range e.file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		enclosingNodeID := e.enclosingFuncNodeID(fn)

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			methodName := sel.Sel.Name

			// Case 1: http.Get(url), http.Post(url, ...), http.Head(url), http.PostForm(url, ...)
			if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == httpAlias {
				if httpMethod, ok := goHTTPPackageFuncs[methodName]; ok {
					path := e.extractStringArg(call, 0)
					if path != "" {
						e.addHTTPClientCallNode(httpMethod, path, "net/http", enclosingNodeID, e.pos(call.Pos()))
					}
					return true
				}

				// Case 2: http.NewRequest("METHOD", url, ...) or http.NewRequestWithContext(ctx, "METHOD", url, ...)
				if goHTTPNewRequestFuncs[methodName] {
					methodArgIdx := 0
					urlArgIdx := 1
					if methodName == "NewRequestWithContext" {
						methodArgIdx = 1
						urlArgIdx = 2
					}
					httpMethod := e.extractStringArg(call, methodArgIdx)
					path := e.extractStringArg(call, urlArgIdx)
					if httpMethod == "" {
						httpMethod = "UNKNOWN"
					}
					if path != "" {
						e.addHTTPClientCallNode(httpMethod, path, "net/http", enclosingNodeID, e.pos(call.Pos()))
					}
					return true
				}
			}

			// Case 3: client.Get(url), client.Post(url, ...), client.Head(url)
			if httpMethod, ok := goHTTPClientMethodsWithURL[methodName]; ok {
				path := e.extractStringArg(call, 0)
				if path != "" {
					e.addHTTPClientCallNode(httpMethod, path, "net/http", enclosingNodeID, e.pos(call.Pos()))
				}
				return true
			}

			// Case 4: client.Do(req) — method is on the request, mark as UNKNOWN
			if methodName == "Do" {
				e.addHTTPClientCallNode("UNKNOWN", "UNKNOWN", "net/http", enclosingNodeID, e.pos(call.Pos()))
			}

			return true
		})
	}
}

func (e *extractor) extractStringArg(call *ast.CallExpr, idx int) string {
	if idx >= len(call.Args) {
		return ""
	}
	arg := call.Args[idx]

	switch a := arg.(type) {
	case *ast.BasicLit:
		if a.Kind == token.STRING {
			return strings.Trim(a.Value, `"`)
		}
	case *ast.BinaryExpr:
		// String concatenation: extract left-most literal + wildcard
		return e.extractConcatStringArg(a)
	}
	return ""
}

func (e *extractor) extractConcatStringArg(expr *ast.BinaryExpr) string {
	if expr.Op != token.ADD {
		return ""
	}
	// Get the left-most string literal
	left := ""
	switch l := expr.X.(type) {
	case *ast.BasicLit:
		if l.Kind == token.STRING {
			left = strings.Trim(l.Value, `"`)
		}
	case *ast.BinaryExpr:
		left = e.extractConcatStringArg(l)
	}
	if left == "" {
		return ""
	}
	return left + "*"
}

func (e *extractor) addHTTPClientCallNode(httpMethod, path, framework, enclosingNodeID string, line int) {
	depID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, "api_call:"+httpMethod+":"+path+":"+fmt.Sprintf("%d", line))

	e.nodes = append(e.nodes, &graph.Node{
		ID:       depID,
		Type:     graph.NodeDependency,
		Name:     httpMethod + " " + path,
		FilePath: e.filePath,
		Line:     line,
		Language: string(parser.LangGo),
		Properties: map[string]string{
			"kind":        "api_call",
			"http_method": httpMethod,
			"path":        path,
			"framework":   framework,
		},
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(enclosingNodeID, depID, string(graph.EdgeCalls)),
		Type:     graph.EdgeCalls,
		SourceID: enclosingNodeID,
		TargetID: depID,
	})
}

// Go builtins to skip during function call extraction.
var goBuiltins = map[string]bool{
	"make": true, "len": true, "cap": true, "append": true, "copy": true,
	"delete": true, "close": true, "new": true, "panic": true, "recover": true,
	"print": true, "println": true, "error": true, "complex": true, "real": true,
	"imag": true, "clear": true, "min": true, "max": true,
}

// buildCallMaps builds lookup maps for resolving function call targets.
func (e *extractor) buildCallMaps() {
	e.importAliasMap = make(map[string]string) // alias → dep node ID
	e.funcNameMap = make(map[string]string)    // func name → node ID

	// Build import alias map
	for _, imp := range e.file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		depID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, path)

		if imp.Name != nil {
			// Explicit alias
			e.importAliasMap[imp.Name.Name] = depID
		} else {
			// Default: last component of path
			parts := strings.Split(path, "/")
			e.importAliasMap[parts[len(parts)-1]] = depID
		}
	}

	// Build function name map from already-extracted nodes
	for _, n := range e.nodes {
		if (n.Type == graph.NodeFunction || n.Type == graph.NodeTestFunction) && n.FilePath == e.filePath {
			e.funcNameMap[n.Name] = n.ID
		}
	}

	// Build method-by-receiver map
	e.methodsByReceiver = make(map[string]map[string]string) // recvType → methodName → nodeID
	for _, n := range e.nodes {
		if n.Type == graph.NodeMethod && n.FilePath == e.filePath {
			recv := n.Properties["receiver"]
			if recv != "" {
				if e.methodsByReceiver[recv] == nil {
					e.methodsByReceiver[recv] = make(map[string]string)
				}
				e.methodsByReceiver[recv][n.Name] = n.ID
			}
		}
	}
}

// enclosingFuncNodeID returns the graph node ID for the given function declaration.
func (e *extractor) enclosingFuncNodeID(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		recvType := receiverTypeName(fn.Recv.List[0].Type)
		return graph.NewNodeID(string(graph.NodeMethod), e.filePath, recvType+"."+fn.Name.Name)
	}
	nodeType := graph.NodeFunction
	if e.isTestFile && isTestFuncName(fn.Name.Name) {
		nodeType = graph.NodeTestFunction
	}
	return graph.NewNodeID(string(nodeType), e.filePath, fn.Name.Name)
}

// extractFunctionCalls walks all function/method bodies and creates EdgeCalls for
// detected call expressions: same-file calls and import-qualified calls.
func (e *extractor) extractFunctionCalls() {
	for _, decl := range e.file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		enclosingNodeID := e.enclosingFuncNodeID(fn)

		// Determine receiver parameter name and type for chained field access resolution.
		var recvParamName, recvTypeName_ string
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			recvTypeName_ = receiverTypeName(fn.Recv.List[0].Type)
			if len(fn.Recv.List[0].Names) > 0 {
				recvParamName = fn.Recv.List[0].Names[0].Name
			}
		}

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			switch funExpr := call.Fun.(type) {
			case *ast.SelectorExpr:
				callee := funExpr.Sel.Name

				// Try to resolve chained field access: receiver.field.Method()
				// or deeper chains like receiver.field1.field2.Method().
				if fieldTypeStr, ok := e.resolveFieldChain(funExpr.X, recvParamName, recvTypeName_); ok {
					typeName := extractTypeName(fieldTypeStr)
					qualifiedCallee := typeName + "." + callee

					pkg := extractPackagePrefix(fieldTypeStr)
					if pkg != "" {
						// Cross-package field type: create edge to the dependency node
						// with qualified callee (e.g., "Store.QueryNodes").
						if depID, ok := e.importAliasMap[pkg]; ok {
							e.edges = append(e.edges, &graph.Edge{
								ID:       edgeID(enclosingNodeID, depID, string(graph.EdgeCalls)+":"+qualifiedCallee),
								Type:     graph.EdgeCalls,
								SourceID: enclosingNodeID,
								TargetID: depID,
								Properties: map[string]string{
									"callee": qualifiedCallee,
								},
							})
							return true
						}
					}

					// Local (same-package) field type: try to resolve directly to the method node.
					if methods, ok := e.methodsByReceiver[typeName]; ok {
						if methodNodeID, ok := methods[callee]; ok {
							e.edges = append(e.edges, &graph.Edge{
								ID:       edgeID(enclosingNodeID, methodNodeID, string(graph.EdgeCalls)),
								Type:     graph.EdgeCalls,
								SourceID: enclosingNodeID,
								TargetID: methodNodeID,
								Properties: map[string]string{
									"callee": qualifiedCallee,
								},
							})
							return true
						}
					}
				}

				// pkg.Func() or receiver.Method()
				if ident, ok := funExpr.X.(*ast.Ident); ok {
					// Check if it's an import-qualified call
					if depID, ok := e.importAliasMap[ident.Name]; ok {
						e.edges = append(e.edges, &graph.Edge{
							ID:       edgeID(enclosingNodeID, depID, string(graph.EdgeCalls)+":"+callee),
							Type:     graph.EdgeCalls,
							SourceID: enclosingNodeID,
							TargetID: depID,
							Properties: map[string]string{
								"callee": callee,
							},
						})
					}
				}
			case *ast.Ident:
				// Direct call: helper()
				name := funExpr.Name
				if goBuiltins[name] {
					return true
				}
				if targetID, ok := e.funcNameMap[name]; ok {
					if targetID != enclosingNodeID { // skip self-recursion noise
						e.edges = append(e.edges, &graph.Edge{
							ID:       edgeID(enclosingNodeID, targetID, string(graph.EdgeCalls)),
							Type:     graph.EdgeCalls,
							SourceID: enclosingNodeID,
							TargetID: targetID,
						})
					}
				}
			}

			return true
		})
	}
}

// resolveFieldChain walks a chain of SelectorExpr nodes (e.g., receiver.field1.field2)
// and resolves the final field's type string through structFieldTypes. It handles
// chains of arbitrary depth starting from the receiver parameter.
// Returns the resolved type string and true if successful.
func (e *extractor) resolveFieldChain(expr ast.Expr, recvParamName, recvTypeName string) (string, bool) {
	if recvParamName == "" {
		return "", false
	}

	// Collect the chain of field names from innermost to outermost.
	// For "receiver.field1.field2", we collect ["field2", "field1"] and verify
	// the root ident is the receiver param.
	var fieldNames []string
	cur := expr
	for {
		sel, ok := cur.(*ast.SelectorExpr)
		if !ok {
			break
		}
		fieldNames = append(fieldNames, sel.Sel.Name)
		cur = sel.X
	}

	ident, ok := cur.(*ast.Ident)
	if !ok || ident.Name != recvParamName {
		return "", false
	}

	// We need at least one field in the chain (receiver.field).
	if len(fieldNames) == 0 {
		return "", false
	}

	// Walk forward through field names (reversed since we collected innermost first).
	// Start from the receiver's struct type and resolve each field.
	currentType := recvTypeName
	for i := len(fieldNames) - 1; i >= 0; i-- {
		fieldTypes, ok := e.structFieldTypes[currentType]
		if !ok {
			return "", false
		}
		fieldTypeStr, ok := fieldTypes[fieldNames[i]]
		if !ok {
			return "", false
		}
		if i == 0 {
			// Last field in the chain — return its type.
			return fieldTypeStr, true
		}
		// Intermediate field: resolve to its type name for the next lookup.
		// For cross-package types (e.g., "graph.Store"), we can't resolve further
		// since we don't have the foreign struct's field types.
		if extractPackagePrefix(fieldTypeStr) != "" {
			return "", false
		}
		currentType = extractTypeName(fieldTypeStr)
	}
	return "", false
}

// Helper functions

func (e *extractor) pos(p token.Pos) int {
	return e.fset.Position(p).Line
}

func isExported(name string) bool {
	if name == "" {
		return false
	}
	return unicode.IsUpper(rune(name[0]))
}

func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return typeExprString(t.X)
	case *ast.Ident:
		return t.Name
	default:
		return typeExprString(expr)
	}
}

func typeExprString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + typeExprString(t.X)
	case *ast.SelectorExpr:
		return typeExprString(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		return "[]" + typeExprString(t.Elt)
	case *ast.MapType:
		return "map[" + typeExprString(t.Key) + "]" + typeExprString(t.Value)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.IndexExpr:
		return typeExprString(t.X) + "[" + typeExprString(t.Index) + "]"
	default:
		return fmt.Sprintf("%T", expr)
	}
}

func funcSignature(fn *ast.FuncDecl) string {
	var b strings.Builder
	b.WriteString("func ")
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		b.WriteString("(")
		b.WriteString(receiverTypeName(fn.Recv.List[0].Type))
		b.WriteString(") ")
	}
	b.WriteString(fn.Name.Name)
	b.WriteString("(")
	if fn.Type.Params != nil {
		writeFieldList(&b, fn.Type.Params)
	}
	b.WriteString(")")
	if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 {
		b.WriteString(" ")
		if len(fn.Type.Results.List) > 1 || len(fn.Type.Results.List[0].Names) > 0 {
			b.WriteString("(")
			writeFieldList(&b, fn.Type.Results)
			b.WriteString(")")
		} else {
			writeFieldList(&b, fn.Type.Results)
		}
	}
	return b.String()
}

func writeFieldList(b *strings.Builder, fl *ast.FieldList) {
	for i, f := range fl.List {
		if i > 0 {
			b.WriteString(", ")
		}
		if len(f.Names) > 0 {
			for j, n := range f.Names {
				if j > 0 {
					b.WriteString(", ")
				}
				b.WriteString(n.Name)
			}
			b.WriteString(" ")
		}
		b.WriteString(typeExprString(f.Type))
	}
}

func edgeID(sourceID, targetID, edgeType string) string {
	return graph.NewNodeID(edgeType, sourceID, targetID)
}

func implementsAll(structMethods, ifaceMethods map[string]bool) bool {
	for method := range ifaceMethods {
		if !structMethods[method] {
			return false
		}
	}
	return true
}

// isTestFuncName returns true if name matches Go test function patterns
// (Test*, Benchmark*, Example*, Fuzz*).
func isTestFuncName(name string) bool {
	for _, prefix := range testFuncPrefixes {
		if strings.HasPrefix(name, prefix) && len(name) > len(prefix) {
			return true
		}
	}
	return false
}

// extractPackagePrefix strips pointer/slice prefixes and returns the package
// part before ".". For example: "*graph.Store" -> "graph", "[]config.Item" -> "config",
// "string" -> "".
func extractPackagePrefix(typeStr string) string {
	typeStr = stripTypeDecorators(typeStr)
	if idx := strings.Index(typeStr, "."); idx >= 0 {
		return typeStr[:idx]
	}
	return ""
}

// extractTypeName strips pointer/slice prefixes and returns the type name
// after the dot, or the bare name if no dot. For example:
// "*graph.Store" -> "Store", "[]config.Item" -> "Item", "Store" -> "Store", "string" -> "string".
func extractTypeName(typeStr string) string {
	typeStr = stripTypeDecorators(typeStr)
	if idx := strings.LastIndex(typeStr, "."); idx >= 0 {
		return typeStr[idx+1:]
	}
	return typeStr
}

// stripTypeDecorators removes pointer (*) and slice ([]) prefixes from a type string.
func stripTypeDecorators(typeStr string) string {
	for strings.HasPrefix(typeStr, "*") || strings.HasPrefix(typeStr, "[]") {
		if strings.HasPrefix(typeStr, "*") {
			typeStr = typeStr[1:]
		} else {
			typeStr = typeStr[2:]
		}
	}
	return typeStr
}
