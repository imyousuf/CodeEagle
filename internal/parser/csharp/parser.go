package csharp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/csharp"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

// CSharpParser extracts knowledge graph nodes and edges from C# source files.
type CSharpParser struct{}

// NewParser creates a new C# parser.
func NewParser() *CSharpParser {
	return &CSharpParser{}
}

func (p *CSharpParser) Language() parser.Language {
	return parser.LangCSharp
}

func (p *CSharpParser) Extensions() []string {
	return parser.FileExtensions[parser.LangCSharp]
}

func (p *CSharpParser) ParseFile(filePath string, content []byte) (*parser.ParseResult, error) {
	lang := csharp.GetLanguage()
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
		Language: parser.LangCSharp,
	}, nil
}

// extractor walks a tree-sitter C# AST and builds graph nodes and edges.
type extractor struct {
	filePath string
	content  []byte
	tree     *sitter.Tree
	nodes    []*graph.Node
	edges    []*graph.Edge

	nsNodeID   string
	fileNodeID string
	nsName     string
	isTestFile bool

	// Lookup maps for function call resolution (built after walkProgram)
	importMap      map[string]string            // simple class name -> dep node ID
	classMethodMap map[string]map[string]string // className -> methodName -> node ID
}

func (e *extractor) extract() {
	e.extractFileNode()

	root := e.tree.RootNode()
	// First pass: extract all declarations
	e.walkProgram(root)
	// Build lookup maps
	e.buildCallMaps()
	// Second pass: walk method bodies for function calls and HTTP client calls
	e.walkMethodBodies(root)
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
		Language: string(parser.LangCSharp),
	})
}

// isTestFilename returns true if the filename matches C# test file patterns.
func isTestFilename(base string) bool {
	if !strings.HasSuffix(base, ".cs") {
		return false
	}
	name := strings.TrimSuffix(base, ".cs")
	return strings.HasSuffix(name, "Test") ||
		strings.HasSuffix(name, "Tests") ||
		strings.HasPrefix(name, "Test")
}

func (e *extractor) walkProgram(root *sitter.Node) {
	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		switch child.Type() {
		case "using_directive":
			e.extractUsing(child)
		case "namespace_declaration":
			e.extractNamespace(child)
		case "file_scoped_namespace_declaration":
			e.extractFileScopedNamespace(child)
		case "class_declaration":
			e.extractClass(child, e.parentID())
		case "interface_declaration":
			e.extractInterface(child, e.parentID())
		case "struct_declaration":
			e.extractStruct(child, e.parentID())
		case "enum_declaration":
			e.extractEnum(child, e.parentID())
		}
	}
}

func (e *extractor) parentID() string {
	if e.nsNodeID != "" {
		return e.nsNodeID
	}
	return e.fileNodeID
}

func (e *extractor) extractNamespace(node *sitter.Node) {
	name := ""
	var bodyNode *sitter.Node
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier", "qualified_name":
			name = e.nodeText(child)
		case "declaration_list":
			bodyNode = child
		}
	}
	if name == "" {
		return
	}

	e.nsName = name
	e.nsNodeID = graph.NewNodeID(string(graph.NodePackage), e.filePath, name)

	e.nodes = append(e.nodes, &graph.Node{
		ID:       e.nsNodeID,
		Type:     graph.NodePackage,
		Name:     name,
		FilePath: e.filePath,
		Line:     int(node.StartPoint().Row) + 1,
		Language: string(parser.LangCSharp),
		Package:  name,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.fileNodeID, e.nsNodeID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.fileNodeID,
		TargetID: e.nsNodeID,
	})

	if bodyNode != nil {
		e.walkDeclarationList(bodyNode)
	}
}

func (e *extractor) extractFileScopedNamespace(node *sitter.Node) {
	name := ""
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier", "qualified_name":
			name = e.nodeText(child)
		}
	}
	if name == "" {
		return
	}

	e.nsName = name
	e.nsNodeID = graph.NewNodeID(string(graph.NodePackage), e.filePath, name)

	e.nodes = append(e.nodes, &graph.Node{
		ID:       e.nsNodeID,
		Type:     graph.NodePackage,
		Name:     name,
		FilePath: e.filePath,
		Line:     int(node.StartPoint().Row) + 1,
		Language: string(parser.LangCSharp),
		Package:  name,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.fileNodeID, e.nsNodeID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.fileNodeID,
		TargetID: e.nsNodeID,
	})

	// File-scoped namespace: remaining declarations are children of this node
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "using_directive":
			e.extractUsing(child)
		case "class_declaration":
			e.extractClass(child, e.nsNodeID)
		case "interface_declaration":
			e.extractInterface(child, e.nsNodeID)
		case "struct_declaration":
			e.extractStruct(child, e.nsNodeID)
		case "enum_declaration":
			e.extractEnum(child, e.nsNodeID)
		}
	}
}

func (e *extractor) walkDeclarationList(body *sitter.Node) {
	for i := 0; i < int(body.NamedChildCount()); i++ {
		child := body.NamedChild(i)
		switch child.Type() {
		case "class_declaration":
			e.extractClass(child, e.parentID())
		case "interface_declaration":
			e.extractInterface(child, e.parentID())
		case "struct_declaration":
			e.extractStruct(child, e.parentID())
		case "enum_declaration":
			e.extractEnum(child, e.parentID())
		}
	}
}

func (e *extractor) extractUsing(node *sitter.Node) {
	name := ""
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier", "qualified_name":
			name = e.nodeText(child)
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
		Language: string(parser.LangCSharp),
		Package:  e.nsName,
		Properties: map[string]string{
			"kind": "import",
		},
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.parentID(), depID, string(graph.EdgeImports)),
		Type:     graph.EdgeImports,
		SourceID: e.parentID(),
		TargetID: depID,
	})
}

func (e *extractor) extractClass(node *sitter.Node, parentID string) {
	name := ""
	var bodyNode *sitter.Node
	var baseTypes []string
	var annotations []string
	modifiers := ""

	docComment := e.extractDocComment(node)

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier":
			name = e.nodeText(child)
		case "modifier":
			if modifiers != "" {
				modifiers += " "
			}
			modifiers += e.nodeText(child)
		case "attribute_list":
			annotations = append(annotations, e.extractAttributes(child)...)
		case "base_list":
			baseTypes = e.extractBaseList(child)
		case "declaration_list":
			bodyNode = child
		}
	}

	if name == "" {
		return
	}

	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1

	classID := graph.NewNodeID(string(graph.NodeClass), e.filePath, name)

	props := make(map[string]string)
	if modifiers != "" {
		props["modifiers"] = modifiers
	}
	if len(annotations) > 0 {
		props["annotations"] = strings.Join(annotations, ",")
	}

	// Separate base class and interfaces.
	// In C#, the first base type could be a class or interface.
	// Convention: interfaces start with "I" (IDisposable, IService, etc.)
	var implements []string
	var extends string
	for _, bt := range baseTypes {
		if strings.HasPrefix(bt, "I") && len(bt) > 1 && bt[1] >= 'A' && bt[1] <= 'Z' {
			implements = append(implements, bt)
		} else if extends == "" {
			extends = bt
		} else {
			implements = append(implements, bt)
		}
	}

	if extends != "" {
		props["extends"] = extends
	}
	if len(implements) > 0 {
		props["implements"] = strings.Join(implements, ",")
	}

	qualifiedName := name
	if e.nsName != "" {
		qualifiedName = e.nsName + "." + name
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:            classID,
		Type:          graph.NodeClass,
		Name:          name,
		QualifiedName: qualifiedName,
		FilePath:      e.filePath,
		Line:          startLine,
		EndLine:       endLine,
		Package:       e.nsName,
		Language:      string(parser.LangCSharp),
		Exported:      isPublicOrInternal(modifiers),
		DocComment:    docComment,
		Properties:    props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, classID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: classID,
	})

	// Implements edges for interfaces
	for _, iface := range implements {
		ifaceID := graph.NewNodeID(string(graph.NodeInterface), e.filePath, iface)
		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(classID, ifaceID, string(graph.EdgeImplements)),
			Type:     graph.EdgeImplements,
			SourceID: classID,
			TargetID: ifaceID,
		})
	}

	// Check if this is an ASP.NET controller
	isController := hasAnnotation(annotations, "ApiController") ||
		strings.HasSuffix(name, "Controller") ||
		extends == "ControllerBase" || extends == "Controller"

	// Walk class body
	if bodyNode != nil {
		e.walkClassBody(bodyNode, classID, name, isController)
	}
}

func (e *extractor) extractInterface(node *sitter.Node, parentID string) {
	name := ""
	var bodyNode *sitter.Node
	var annotations []string
	modifiers := ""

	docComment := e.extractDocComment(node)

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier":
			name = e.nodeText(child)
		case "modifier":
			if modifiers != "" {
				modifiers += " "
			}
			modifiers += e.nodeText(child)
		case "attribute_list":
			annotations = append(annotations, e.extractAttributes(child)...)
		case "declaration_list":
			bodyNode = child
		}
	}

	if name == "" {
		return
	}

	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1

	ifaceID := graph.NewNodeID(string(graph.NodeInterface), e.filePath, name)

	props := make(map[string]string)
	if modifiers != "" {
		props["modifiers"] = modifiers
	}
	if len(annotations) > 0 {
		props["annotations"] = strings.Join(annotations, ",")
	}

	// Extract method names from interface body
	var methodNames []string
	if bodyNode != nil {
		for i := 0; i < int(bodyNode.NamedChildCount()); i++ {
			child := bodyNode.NamedChild(i)
			if child.Type() == "method_declaration" {
				mn := e.getMethodName(child)
				if mn != "" {
					methodNames = append(methodNames, mn)
				}
			}
		}
	}
	if len(methodNames) > 0 {
		props["methods"] = strings.Join(methodNames, ",")
	}

	qualifiedName := name
	if e.nsName != "" {
		qualifiedName = e.nsName + "." + name
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:            ifaceID,
		Type:          graph.NodeInterface,
		Name:          name,
		QualifiedName: qualifiedName,
		FilePath:      e.filePath,
		Line:          startLine,
		EndLine:       endLine,
		Package:       e.nsName,
		Language:      string(parser.LangCSharp),
		Exported:      isPublicOrInternal(modifiers),
		DocComment:    docComment,
		Properties:    props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, ifaceID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: ifaceID,
	})

	// Extract methods from interface body
	if bodyNode != nil {
		e.walkInterfaceBody(bodyNode, ifaceID, name)
	}
}

func (e *extractor) extractStruct(node *sitter.Node, parentID string) {
	name := ""
	var bodyNode *sitter.Node
	var baseTypes []string
	var annotations []string
	modifiers := ""

	docComment := e.extractDocComment(node)

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier":
			name = e.nodeText(child)
		case "modifier":
			if modifiers != "" {
				modifiers += " "
			}
			modifiers += e.nodeText(child)
		case "attribute_list":
			annotations = append(annotations, e.extractAttributes(child)...)
		case "base_list":
			baseTypes = e.extractBaseList(child)
		case "declaration_list":
			bodyNode = child
		}
	}

	if name == "" {
		return
	}

	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1

	structID := graph.NewNodeID(string(graph.NodeStruct), e.filePath, name)

	props := make(map[string]string)
	if modifiers != "" {
		props["modifiers"] = modifiers
	}
	if len(annotations) > 0 {
		props["annotations"] = strings.Join(annotations, ",")
	}
	if len(baseTypes) > 0 {
		props["implements"] = strings.Join(baseTypes, ",")
	}

	qualifiedName := name
	if e.nsName != "" {
		qualifiedName = e.nsName + "." + name
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:            structID,
		Type:          graph.NodeStruct,
		Name:          name,
		QualifiedName: qualifiedName,
		FilePath:      e.filePath,
		Line:          startLine,
		EndLine:       endLine,
		Package:       e.nsName,
		Language:      string(parser.LangCSharp),
		Exported:      isPublicOrInternal(modifiers),
		DocComment:    docComment,
		Properties:    props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, structID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: structID,
	})

	// Implements edges
	for _, iface := range baseTypes {
		ifaceID := graph.NewNodeID(string(graph.NodeInterface), e.filePath, iface)
		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(structID, ifaceID, string(graph.EdgeImplements)),
			Type:     graph.EdgeImplements,
			SourceID: structID,
			TargetID: ifaceID,
		})
	}

	if bodyNode != nil {
		e.walkClassBody(bodyNode, structID, name, false)
	}
}

func (e *extractor) extractEnum(node *sitter.Node, parentID string) {
	name := ""
	var annotations []string
	modifiers := ""

	docComment := e.extractDocComment(node)

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier":
			name = e.nodeText(child)
		case "modifier":
			if modifiers != "" {
				modifiers += " "
			}
			modifiers += e.nodeText(child)
		case "attribute_list":
			annotations = append(annotations, e.extractAttributes(child)...)
		}
	}

	if name == "" {
		return
	}

	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1

	enumID := graph.NewNodeID(string(graph.NodeEnum), e.filePath, name)

	props := make(map[string]string)
	if modifiers != "" {
		props["modifiers"] = modifiers
	}
	if len(annotations) > 0 {
		props["annotations"] = strings.Join(annotations, ",")
	}

	// Extract enum members
	var members []string
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "enum_member_declaration_list" {
			for j := 0; j < int(child.NamedChildCount()); j++ {
				member := child.NamedChild(j)
				if member.Type() == "enum_member_declaration" {
					for k := 0; k < int(member.NamedChildCount()); k++ {
						id := member.NamedChild(k)
						if id.Type() == "identifier" {
							members = append(members, e.nodeText(id))
						}
					}
				}
			}
		}
	}
	if len(members) > 0 {
		props["constants"] = strings.Join(members, ",")
	}

	qualifiedName := name
	if e.nsName != "" {
		qualifiedName = e.nsName + "." + name
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:            enumID,
		Type:          graph.NodeEnum,
		Name:          name,
		QualifiedName: qualifiedName,
		FilePath:      e.filePath,
		Line:          startLine,
		EndLine:       endLine,
		Package:       e.nsName,
		Language:      string(parser.LangCSharp),
		Exported:      !strings.Contains(modifiers, "private"),
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

func (e *extractor) walkClassBody(body *sitter.Node, ownerID, className string, isController bool) {
	for i := 0; i < int(body.NamedChildCount()); i++ {
		child := body.NamedChild(i)
		switch child.Type() {
		case "method_declaration":
			e.extractMethod(child, ownerID, className, isController)
		case "constructor_declaration":
			e.extractConstructor(child, ownerID, className)
		case "field_declaration":
			e.extractField(child, ownerID, className)
		case "property_declaration":
			e.extractProperty(child, ownerID, className)
		case "class_declaration":
			e.extractClass(child, ownerID)
		case "interface_declaration":
			e.extractInterface(child, ownerID)
		case "struct_declaration":
			e.extractStruct(child, ownerID)
		case "enum_declaration":
			e.extractEnum(child, ownerID)
		}
	}
}

func (e *extractor) walkInterfaceBody(body *sitter.Node, ifaceID, ifaceName string) {
	for i := 0; i < int(body.NamedChildCount()); i++ {
		child := body.NamedChild(i)
		if child.Type() == "method_declaration" {
			e.extractMethod(child, ifaceID, ifaceName, false)
		}
	}
}

// csharpTestAnnotations maps C# test framework attributes that indicate test methods.
var csharpTestAnnotations = map[string]bool{
	"Test": true, "TestMethod": true, "Fact": true, "Theory": true,
	"TestCase": true, "TestCaseSource": true,
}

// hasTestAnnotation returns true if the annotations list contains a test annotation.
func hasTestAnnotation(annotations []string) bool {
	for _, ann := range annotations {
		// Strip any arguments: "TestCase(1)" -> "TestCase"
		name := ann
		if idx := strings.Index(ann, "("); idx > 0 {
			name = ann[:idx]
		}
		if csharpTestAnnotations[name] {
			return true
		}
	}
	return false
}

// aspnetHTTPAttributes maps ASP.NET controller action attributes to HTTP methods.
var aspnetHTTPAttributes = map[string]string{
	"HttpGet":    "GET",
	"HttpPost":   "POST",
	"HttpPut":    "PUT",
	"HttpDelete": "DELETE",
	"HttpPatch":  "PATCH",
}

func (e *extractor) extractMethod(node *sitter.Node, parentID, className string, isController bool) {
	name := ""
	returnType := ""
	params := ""
	var annotations []string
	modifiers := ""

	docComment := e.extractDocComment(node)

	// In C# tree-sitter, method_declaration children include:
	// modifier*, attribute_list*, return_type, identifier (name), parameter_list, body
	// We need to identify the method name (the identifier that comes after a type).
	// Strategy: collect identifiers; the last one before parameter_list is the name.
	var identifiers []struct {
		text string
		idx  int
	}

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier":
			identifiers = append(identifiers, struct {
				text string
				idx  int
			}{e.nodeText(child), i})
		case "modifier":
			if modifiers != "" {
				modifiers += " "
			}
			modifiers += e.nodeText(child)
		case "attribute_list":
			annotations = append(annotations, e.extractAttributes(child)...)
		case "parameter_list":
			params = e.nodeText(child)
		case "predefined_type", "generic_name", "nullable_type",
			"array_type", "qualified_name":
			returnType = e.nodeText(child)
		case "void_keyword":
			returnType = "void"
		}
	}

	// The last identifier is the method name; if there's a previous identifier
	// and no explicit return type, the previous identifier is the return type.
	if len(identifiers) == 0 {
		return
	}
	name = identifiers[len(identifiers)-1].text
	if returnType == "" && len(identifiers) > 1 {
		returnType = identifiers[0].text
	}

	if name == "" {
		return
	}

	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1
	qualifiedName := className + "." + name

	sig := returnType + " " + name + params

	props := make(map[string]string)
	if modifiers != "" {
		props["modifiers"] = modifiers
	}
	if len(annotations) > 0 {
		props["annotations"] = strings.Join(annotations, ",")
	}
	props["class"] = className

	// Determine if this is a test method
	nodeType := graph.NodeMethod
	if e.isTestFile && hasTestAnnotation(annotations) {
		nodeType = graph.NodeTestFunction
	}

	methodID := graph.NewNodeID(string(nodeType), e.filePath, qualifiedName)

	e.nodes = append(e.nodes, &graph.Node{
		ID:            methodID,
		Type:          nodeType,
		Name:          name,
		QualifiedName: qualifiedName,
		FilePath:      e.filePath,
		Line:          startLine,
		EndLine:       endLine,
		Package:       e.nsName,
		Language:      string(parser.LangCSharp),
		Exported:      isPublicOrInternal(modifiers),
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

	// Extract ASP.NET API endpoints from controller methods
	if isController {
		e.extractAPIEndpoints(node, annotations, methodID, className, name, startLine)
	}
}

func (e *extractor) extractConstructor(node *sitter.Node, parentID, className string) {
	name := ""
	params := ""
	var annotations []string
	modifiers := ""

	docComment := e.extractDocComment(node)

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier":
			name = e.nodeText(child)
		case "modifier":
			if modifiers != "" {
				modifiers += " "
			}
			modifiers += e.nodeText(child)
		case "attribute_list":
			annotations = append(annotations, e.extractAttributes(child)...)
		case "parameter_list":
			params = e.nodeText(child)
		}
	}

	if name == "" {
		return
	}

	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1
	qualifiedName := className + "." + name

	sig := name + params

	props := make(map[string]string)
	if modifiers != "" {
		props["modifiers"] = modifiers
	}
	if len(annotations) > 0 {
		props["annotations"] = strings.Join(annotations, ",")
	}
	props["class"] = className
	props["constructor"] = "true"

	methodID := graph.NewNodeID(string(graph.NodeMethod), e.filePath, qualifiedName)

	e.nodes = append(e.nodes, &graph.Node{
		ID:            methodID,
		Type:          graph.NodeMethod,
		Name:          name,
		QualifiedName: qualifiedName,
		FilePath:      e.filePath,
		Line:          startLine,
		EndLine:       endLine,
		Package:       e.nsName,
		Language:      string(parser.LangCSharp),
		Exported:      isPublicOrInternal(modifiers),
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

func (e *extractor) extractField(node *sitter.Node, parentID, className string) {
	modifiers := ""
	var annotations []string
	fieldType := ""
	isConst := false

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "modifier":
			text := e.nodeText(child)
			if text == "const" {
				isConst = true
			}
			if modifiers != "" {
				modifiers += " "
			}
			modifiers += text
		case "attribute_list":
			annotations = append(annotations, e.extractAttributes(child)...)
		case "variable_declaration":
			// variable_declaration contains the type and variable_declarator(s)
			for j := 0; j < int(child.NamedChildCount()); j++ {
				varChild := child.NamedChild(j)
				switch varChild.Type() {
				case "predefined_type", "generic_name", "identifier", "nullable_type",
					"array_type", "qualified_name":
					if fieldType == "" {
						fieldType = e.nodeText(varChild)
					}
				case "variable_declarator":
					varName := ""
					for k := 0; k < int(varChild.NamedChildCount()); k++ {
						id := varChild.NamedChild(k)
						if id.Type() == "identifier" {
							varName = e.nodeText(id)
							break
						}
					}
					if varName == "" {
						continue
					}

					line := int(node.StartPoint().Row) + 1
					qualifiedName := className + "." + varName

					props := make(map[string]string)
					if modifiers != "" {
						props["modifiers"] = modifiers
					}
					if len(annotations) > 0 {
						props["annotations"] = strings.Join(annotations, ",")
					}
					if fieldType != "" {
						props["type"] = fieldType
					}
					props["class"] = className

					nodeType := graph.NodeVariable
					if isConst {
						nodeType = graph.NodeConstant
					}

					varID := graph.NewNodeID(string(nodeType), e.filePath, qualifiedName)

					e.nodes = append(e.nodes, &graph.Node{
						ID:            varID,
						Type:          nodeType,
						Name:          varName,
						QualifiedName: qualifiedName,
						FilePath:      e.filePath,
						Line:          line,
						Package:       e.nsName,
						Language:      string(parser.LangCSharp),
						Exported:      isPublicOrInternal(modifiers),
						Properties:    props,
					})

					e.edges = append(e.edges, &graph.Edge{
						ID:       edgeID(parentID, varID, string(graph.EdgeContains)),
						Type:     graph.EdgeContains,
						SourceID: parentID,
						TargetID: varID,
					})
				}
			}
		}
	}
}

func (e *extractor) extractProperty(node *sitter.Node, parentID, className string) {
	name := ""
	propType := ""
	modifiers := ""
	var annotations []string

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier":
			name = e.nodeText(child)
		case "modifier":
			if modifiers != "" {
				modifiers += " "
			}
			modifiers += e.nodeText(child)
		case "attribute_list":
			annotations = append(annotations, e.extractAttributes(child)...)
		case "predefined_type", "generic_name", "nullable_type", "array_type", "qualified_name":
			if propType == "" {
				propType = e.nodeText(child)
			}
		}
	}

	if name == "" {
		return
	}

	line := int(node.StartPoint().Row) + 1
	qualifiedName := className + "." + name

	props := make(map[string]string)
	if modifiers != "" {
		props["modifiers"] = modifiers
	}
	if len(annotations) > 0 {
		props["annotations"] = strings.Join(annotations, ",")
	}
	if propType != "" {
		props["type"] = propType
	}
	props["class"] = className
	props["property"] = "true"

	varID := graph.NewNodeID(string(graph.NodeVariable), e.filePath, qualifiedName)

	e.nodes = append(e.nodes, &graph.Node{
		ID:            varID,
		Type:          graph.NodeVariable,
		Name:          name,
		QualifiedName: qualifiedName,
		FilePath:      e.filePath,
		Line:          line,
		Package:       e.nsName,
		Language:      string(parser.LangCSharp),
		Exported:      isPublicOrInternal(modifiers),
		Properties:    props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, varID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: varID,
	})
}

// extractAPIEndpoints extracts ASP.NET API endpoint nodes from controller method attributes.
func (e *extractor) extractAPIEndpoints(node *sitter.Node, annotations []string, methodID, className, methodName string, line int) {
	// Find the class-level [Route] attribute for base path
	classRoute := e.findClassRoute(node)

	for _, ann := range annotations {
		// Parse annotation name and argument
		annName := ann
		annArg := ""
		if idx := strings.Index(ann, "("); idx > 0 {
			annName = ann[:idx]
			end := strings.LastIndex(ann, ")")
			if end > idx {
				annArg = ann[idx+1 : end]
				annArg = strings.Trim(annArg, "\"")
			}
		}

		httpMethod, ok := aspnetHTTPAttributes[annName]
		if !ok {
			continue
		}

		// Build the route path
		path := classRoute
		if annArg != "" {
			if path != "" && !strings.HasSuffix(path, "/") {
				path += "/"
			}
			path += annArg
		}
		if path == "" {
			path = "/" + methodName
		}

		// Normalize path to start with /
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}

		endpointName := httpMethod + " " + path
		endpointID := graph.NewNodeID(string(graph.NodeAPIEndpoint), e.filePath,
			endpointName+":"+fmt.Sprintf("%d", line))

		e.nodes = append(e.nodes, &graph.Node{
			ID:       endpointID,
			Type:     graph.NodeAPIEndpoint,
			Name:     endpointName,
			FilePath: e.filePath,
			Line:     line,
			Package:  e.nsName,
			Language: string(parser.LangCSharp),
			Properties: map[string]string{
				"http_method": httpMethod,
				"path":        path,
				"controller":  className,
				"action":      methodName,
			},
		})

		// EdgeExposes: method -> endpoint
		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(methodID, endpointID, string(graph.EdgeExposes)),
			Type:     graph.EdgeExposes,
			SourceID: methodID,
			TargetID: endpointID,
		})
	}
}

// findClassRoute walks up to the class node and extracts the [Route] attribute value.
func (e *extractor) findClassRoute(methodNode *sitter.Node) string {
	// Walk up to find the class_declaration parent
	parent := methodNode.Parent()
	for parent != nil {
		if parent.Type() == "class_declaration" {
			break
		}
		parent = parent.Parent()
	}
	if parent == nil {
		return ""
	}

	// Look for [Route] attribute on the class
	for i := 0; i < int(parent.NamedChildCount()); i++ {
		child := parent.NamedChild(i)
		if child.Type() == "attribute_list" {
			attrs := e.extractAttributes(child)
			for _, attr := range attrs {
				if strings.HasPrefix(attr, "Route(") {
					end := strings.LastIndex(attr, ")")
					if end > 6 {
						route := attr[6:end]
						route = strings.Trim(route, "\"")
						return route
					}
				}
			}
		}
	}

	return ""
}

func (e *extractor) extractAttributes(node *sitter.Node) []string {
	var attrs []string
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "attribute" {
			text := e.nodeText(child)
			attrs = append(attrs, text)
		}
	}
	return attrs
}

func (e *extractor) extractBaseList(node *sitter.Node) []string {
	var bases []string
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier", "generic_name", "qualified_name":
			bases = append(bases, e.nodeText(child))
		default:
			// Try extracting text from any type-like children
			text := e.nodeText(child)
			if text != "" && text != ":" {
				bases = append(bases, text)
			}
		}
	}
	return bases
}

func (e *extractor) getMethodName(node *sitter.Node) string {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "identifier" {
			return e.nodeText(child)
		}
	}
	return ""
}

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

	// Walk backward to find comment nodes
	var commentLines []string
	for j := idx - 1; j >= 0; j-- {
		prev := parent.Child(j)
		text := e.nodeText(prev)
		if prev.Type() == "comment" {
			if strings.HasPrefix(text, "///") {
				line := strings.TrimPrefix(text, "///")
				line = strings.TrimSpace(line)
				commentLines = append([]string{line}, commentLines...)
				continue
			}
			// Regular // comment, stop
			break
		}
		// Skip whitespace between comments
		if prev.IsNamed() {
			break
		}
	}

	if len(commentLines) > 0 {
		return cleanXMLDoc(strings.Join(commentLines, "\n"))
	}

	return ""
}

// buildCallMaps populates lookup maps from already-extracted nodes.
func (e *extractor) buildCallMaps() {
	e.importMap = make(map[string]string)
	e.classMethodMap = make(map[string]map[string]string)

	for _, n := range e.nodes {
		switch n.Type {
		case graph.NodeDependency:
			if n.Properties != nil && n.Properties["kind"] == "import" {
				parts := strings.Split(n.Name, ".")
				shortName := parts[len(parts)-1]
				e.importMap[shortName] = n.ID
			}
		case graph.NodeMethod, graph.NodeTestFunction:
			className := n.Properties["class"]
			if className != "" {
				if e.classMethodMap[className] == nil {
					e.classMethodMap[className] = make(map[string]string)
				}
				e.classMethodMap[className][n.Name] = n.ID
			}
		}
	}
}

// walkMethodBodies traverses the AST a second time for function call detection.
func (e *extractor) walkMethodBodies(root *sitter.Node) {
	e.walkForClassBodies(root)
}

func (e *extractor) walkForClassBodies(node *sitter.Node) {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "class_declaration", "struct_declaration":
			e.walkClassBodiesForCalls(child)
		case "namespace_declaration":
			for j := 0; j < int(child.NamedChildCount()); j++ {
				nsChild := child.NamedChild(j)
				if nsChild.Type() == "declaration_list" {
					e.walkForClassBodies(nsChild)
				}
			}
		case "file_scoped_namespace_declaration":
			e.walkForClassBodies(child)
		}
	}
}

func (e *extractor) walkClassBodiesForCalls(classNode *sitter.Node) {
	className := ""
	var bodyNode *sitter.Node
	for i := 0; i < int(classNode.NamedChildCount()); i++ {
		child := classNode.NamedChild(i)
		switch child.Type() {
		case "identifier":
			className = e.nodeText(child)
		case "declaration_list":
			bodyNode = child
		}
	}
	if className == "" || bodyNode == nil {
		return
	}

	for i := 0; i < int(bodyNode.NamedChildCount()); i++ {
		child := bodyNode.NamedChild(i)
		switch child.Type() {
		case "method_declaration", "constructor_declaration":
			methodName := e.getMethodName(child)
			if methodName == "" {
				continue
			}
			methodID := ""
			if methods, ok := e.classMethodMap[className]; ok {
				methodID = methods[methodName]
			}
			if methodID == "" {
				qualifiedName := className + "." + methodName
				methodID = graph.NewNodeID(string(graph.NodeMethod), e.filePath, qualifiedName)
			}
			// Walk the method body for calls
			e.walkNodeForCalls(child, methodID, className)
		case "class_declaration", "struct_declaration":
			e.walkClassBodiesForCalls(child)
		}
	}
}

func (e *extractor) walkNodeForCalls(node *sitter.Node, methodID, className string) {
	if node == nil {
		return
	}

	if node.Type() == "invocation_expression" {
		e.checkFunctionCall(node, methodID, className)
	}

	for i := 0; i < int(node.NamedChildCount()); i++ {
		e.walkNodeForCalls(node.NamedChild(i), methodID, className)
	}
}

// C# builtin method names to skip in function call analysis.
var csharpBuiltins = map[string]bool{
	"ToString": true, "GetHashCode": true, "Equals": true, "GetType": true,
	"ReferenceEquals": true, "MemberwiseClone": true,
	"Add": true, "Remove": true, "Contains": true, "Count": true,
	"Any": true, "All": true, "Select": true, "Where": true,
	"FirstOrDefault": true, "First": true, "Last": true, "LastOrDefault": true,
	"OrderBy": true, "OrderByDescending": true, "GroupBy": true, "ToList": true,
	"ToArray": true, "ToDictionary": true, "Aggregate": true,
	"WriteLine": true, "Write": true, "ReadLine": true, "Read": true,
	"Format": true, "Join": true, "Split": true, "Trim": true,
	"StartsWith": true, "EndsWith": true, "Replace": true, "Substring": true,
	"IndexOf": true, "ToLower": true, "ToUpper": true, "Concat": true,
	"Parse": true, "TryParse": true, "IsNullOrEmpty": true, "IsNullOrWhiteSpace": true,
	"Dispose": true, "Close": true, "Flush": true,
	"ContainsKey": true, "TryGetValue": true, "Keys": true, "Values": true,
}

func (e *extractor) checkFunctionCall(node *sitter.Node, methodID, className string) {
	if methodID == "" {
		return
	}

	objectName, calledMethod := e.extractInvocationParts(node)
	if calledMethod == "" || csharpBuiltins[calledMethod] {
		return
	}

	// Case 1: no object or "this" -> same-class call
	if objectName == "" || objectName == "this" {
		if methods, ok := e.classMethodMap[className]; ok {
			if targetID, ok := methods[calledMethod]; ok {
				e.edges = append(e.edges, &graph.Edge{
					ID:       edgeID(methodID, targetID, string(graph.EdgeCalls)),
					Type:     graph.EdgeCalls,
					SourceID: methodID,
					TargetID: targetID,
					Properties: map[string]string{
						"callee": calledMethod,
					},
				})
			}
		}
		return
	}

	// Case 2: ClassName.Method() -> import-qualified call
	if targetID, ok := e.importMap[objectName]; ok {
		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(methodID, targetID, string(graph.EdgeCalls)),
			Type:     graph.EdgeCalls,
			SourceID: methodID,
			TargetID: targetID,
			Properties: map[string]string{
				"callee": calledMethod,
			},
		})
	}
}

// extractInvocationParts extracts the object name and method name from an
// invocation_expression node. E.g., "service.DoWork()" returns ("service", "DoWork").
func (e *extractor) extractInvocationParts(node *sitter.Node) (string, string) {
	// In C# tree-sitter, invocation_expression has:
	//   member_access_expression argument_list
	// OR
	//   identifier argument_list
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "member_access_expression":
			// member_access_expression has: expression "." identifier
			parts := e.extractMemberAccessParts(child)
			if len(parts) >= 2 {
				return parts[0], parts[len(parts)-1]
			}
			if len(parts) == 1 {
				return "", parts[0]
			}
		case "identifier":
			return "", e.nodeText(child)
		}
	}
	return "", ""
}

func (e *extractor) extractMemberAccessParts(node *sitter.Node) []string {
	var parts []string
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier", "predefined_type", "this_expression":
			parts = append(parts, e.nodeText(child))
		case "member_access_expression":
			// Nested: a.b.c
			subParts := e.extractMemberAccessParts(child)
			parts = append(parts, subParts...)
		case "invocation_expression":
			// Chained method call: a.Foo().Bar()
			subParts := e.extractInvocationExpressionParts(child)
			parts = append(parts, subParts...)
		}
	}
	return parts
}

func (e *extractor) extractInvocationExpressionParts(node *sitter.Node) []string {
	var parts []string
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "member_access_expression":
			return e.extractMemberAccessParts(child)
		case "identifier":
			parts = append(parts, e.nodeText(child))
		}
	}
	return parts
}

func (e *extractor) nodeText(node *sitter.Node) string {
	return node.Content(e.content)
}

// Helper functions

func edgeID(sourceID, targetID, edgeType string) string {
	return graph.NewNodeID(edgeType, sourceID, targetID)
}

func isPublicOrInternal(modifiers string) bool {
	return strings.Contains(modifiers, "public") || strings.Contains(modifiers, "internal")
}

func hasAnnotation(annotations []string, name string) bool {
	for _, ann := range annotations {
		annName := ann
		if idx := strings.Index(ann, "("); idx > 0 {
			annName = ann[:idx]
		}
		if annName == name {
			return true
		}
	}
	return false
}

// cleanXMLDoc strips XML tags from C# XML documentation comments.
func cleanXMLDoc(s string) string {
	// Remove XML tags like <summary>, </summary>, <param>, etc.
	var result strings.Builder
	inTag := false
	for _, ch := range s {
		if ch == '<' {
			inTag = true
			continue
		}
		if ch == '>' {
			inTag = false
			continue
		}
		if !inTag {
			result.WriteRune(ch)
		}
	}

	// Clean up lines
	lines := strings.Split(result.String(), "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n")
}
