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

// extractor walks a Go AST and builds graph nodes and edges.
type extractor struct {
	fset     *token.FileSet
	file     *ast.File
	filePath string
	nodes    []*graph.Node
	edges    []*graph.Edge

	pkgNodeID  string
	fileNodeID string

	// Track interfaces and struct methods for Implements edge detection.
	interfaces    map[string]map[string]bool // interface name -> set of method names
	structMethods map[string]map[string]bool // struct name -> set of method names
}

func (e *extractor) extract() {
	e.interfaces = make(map[string]map[string]bool)
	e.structMethods = make(map[string]map[string]bool)

	e.extractFileNode()
	e.extractPackage()
	e.extractImports()
	e.extractDeclarations()
	e.extractImplementsEdges()
}

func (e *extractor) extractFileNode() {
	e.fileNodeID = graph.NewNodeID(string(graph.NodeFile), e.filePath, e.filePath)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       e.fileNodeID,
		Type:     graph.NodeFile,
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
		// Function
		funcID := graph.NewNodeID(string(graph.NodeFunction), e.filePath, name)

		e.nodes = append(e.nodes, &graph.Node{
			ID:            funcID,
			Type:          graph.NodeFunction,
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

func (e *extractor) extractStruct(name string, exported bool, doc string, startLine, endLine int, st *ast.StructType) {
	structID := graph.NewNodeID(string(graph.NodeStruct), e.filePath, name)

	props := make(map[string]string)
	if st.Fields != nil {
		fields := make([]string, 0, len(st.Fields.List))
		for _, f := range st.Fields.List {
			if len(f.Names) > 0 {
				for _, n := range f.Names {
					fields = append(fields, n.Name)
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
