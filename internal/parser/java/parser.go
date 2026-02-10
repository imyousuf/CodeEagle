package java

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/java"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

// JavaParser extracts knowledge graph nodes and edges from Java source files.
type JavaParser struct{}

// NewParser creates a new Java parser.
func NewParser() *JavaParser {
	return &JavaParser{}
}

func (p *JavaParser) Language() parser.Language {
	return parser.LangJava
}

func (p *JavaParser) Extensions() []string {
	return parser.FileExtensions[parser.LangJava]
}

func (p *JavaParser) ParseFile(filePath string, content []byte) (*parser.ParseResult, error) {
	lang := java.GetLanguage()
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
		Language: parser.LangJava,
	}, nil
}

// extractor walks a tree-sitter Java AST and builds graph nodes and edges.
type extractor struct {
	filePath string
	content  []byte
	tree     *sitter.Tree
	nodes    []*graph.Node
	edges    []*graph.Edge

	pkgNodeID  string
	fileNodeID string
	pkgName    string
	isTestFile bool

	// Lookup maps for function call resolution (built after walkProgram)
	importMap      map[string]string            // simple class name → dep node ID
	classMethodMap map[string]map[string]string // className → methodName → node ID
}

func (e *extractor) extract() {
	e.extractFileNode()

	root := e.tree.RootNode()
	// First pass: extract all declarations (classes, methods, fields, imports)
	e.walkProgram(root)
	// Build lookup maps from extracted declarations
	e.buildCallMaps()
	// Second pass: walk method bodies for HTTP client calls and function calls
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
		Language: string(parser.LangJava),
	})
}

// isTestFilename returns true if the filename matches Java test file patterns.
func isTestFilename(base string) bool {
	if !strings.HasSuffix(base, ".java") {
		return false
	}
	name := strings.TrimSuffix(base, ".java")
	return strings.HasSuffix(name, "Test") ||
		strings.HasSuffix(name, "Tests") ||
		strings.HasPrefix(name, "Test") ||
		strings.HasSuffix(name, "IT")
}

func (e *extractor) walkProgram(root *sitter.Node) {
	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		switch child.Type() {
		case "package_declaration":
			e.extractPackage(child)
		case "import_declaration":
			e.extractImport(child)
		case "class_declaration":
			e.extractClass(child, e.parentID())
		case "interface_declaration":
			e.extractInterface(child, e.parentID())
		case "enum_declaration":
			e.extractEnum(child, e.parentID())
		}
	}
}

func (e *extractor) parentID() string {
	if e.pkgNodeID != "" {
		return e.pkgNodeID
	}
	return e.fileNodeID
}

func (e *extractor) extractPackage(node *sitter.Node) {
	// package_declaration contains a scoped_identifier or identifier
	name := ""
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "scoped_identifier" || child.Type() == "identifier" {
			name = e.nodeText(child)
		}
	}
	if name == "" {
		return
	}

	e.pkgName = name
	e.pkgNodeID = graph.NewNodeID(string(graph.NodePackage), e.filePath, name)

	e.nodes = append(e.nodes, &graph.Node{
		ID:       e.pkgNodeID,
		Type:     graph.NodePackage,
		Name:     name,
		FilePath: e.filePath,
		Line:     int(node.StartPoint().Row) + 1,
		Language: string(parser.LangJava),
		Package:  name,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.fileNodeID, e.pkgNodeID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.fileNodeID,
		TargetID: e.pkgNodeID,
	})
}

func (e *extractor) extractImport(node *sitter.Node) {
	// import_declaration contains a scoped_identifier
	name := ""
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "scoped_identifier" || child.Type() == "identifier" {
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
		Language: string(parser.LangJava),
		Package:  e.pkgName,
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
	var superClass string
	var interfaces []string
	var annotations []string
	modifiers := ""

	docComment := e.extractJavadoc(node)

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier":
			name = e.nodeText(child)
		case "modifiers":
			modifiers, annotations = e.extractModifiers(child)
		case "superclass":
			superClass = e.extractSuperclass(child)
		case "super_interfaces":
			interfaces = e.extractSuperInterfaces(child)
		case "class_body":
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
	if superClass != "" {
		props["extends"] = superClass
	}
	if len(interfaces) > 0 {
		props["implements"] = strings.Join(interfaces, ",")
	}

	qualifiedName := name
	if e.pkgName != "" {
		qualifiedName = e.pkgName + "." + name
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:            classID,
		Type:          graph.NodeClass,
		Name:          name,
		QualifiedName: qualifiedName,
		FilePath:      e.filePath,
		Line:          startLine,
		EndLine:       endLine,
		Package:       e.pkgName,
		Language:      string(parser.LangJava),
		Exported:      strings.Contains(modifiers, "public"),
		DocComment:    docComment,
		Properties:    props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, classID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: classID,
	})

	// Implements edges
	for _, iface := range interfaces {
		ifaceID := graph.NewNodeID(string(graph.NodeInterface), e.filePath, iface)
		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(classID, ifaceID, string(graph.EdgeImplements)),
			Type:     graph.EdgeImplements,
			SourceID: classID,
			TargetID: ifaceID,
		})
	}

	// Walk class body
	if bodyNode != nil {
		e.walkClassBody(bodyNode, classID, name)
	}
}

func (e *extractor) extractInterface(node *sitter.Node, parentID string) {
	name := ""
	var bodyNode *sitter.Node
	var annotations []string
	modifiers := ""

	docComment := e.extractJavadoc(node)

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier":
			name = e.nodeText(child)
		case "modifiers":
			modifiers, annotations = e.extractModifiers(child)
		case "interface_body":
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
	if e.pkgName != "" {
		qualifiedName = e.pkgName + "." + name
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:            ifaceID,
		Type:          graph.NodeInterface,
		Name:          name,
		QualifiedName: qualifiedName,
		FilePath:      e.filePath,
		Line:          startLine,
		EndLine:       endLine,
		Package:       e.pkgName,
		Language:      string(parser.LangJava),
		Exported:      strings.Contains(modifiers, "public"),
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

func (e *extractor) extractEnum(node *sitter.Node, parentID string) {
	name := ""
	var annotations []string
	modifiers := ""

	docComment := e.extractJavadoc(node)

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier":
			name = e.nodeText(child)
		case "modifiers":
			modifiers, annotations = e.extractModifiers(child)
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

	// Extract enum constants
	var constants []string
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "enum_body" {
			for j := 0; j < int(child.NamedChildCount()); j++ {
				enumChild := child.NamedChild(j)
				if enumChild.Type() == "enum_constant" {
					for k := 0; k < int(enumChild.NamedChildCount()); k++ {
						id := enumChild.NamedChild(k)
						if id.Type() == "identifier" {
							constants = append(constants, e.nodeText(id))
						}
					}
				}
			}
		}
	}
	if len(constants) > 0 {
		props["constants"] = strings.Join(constants, ",")
	}

	qualifiedName := name
	if e.pkgName != "" {
		qualifiedName = e.pkgName + "." + name
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:            enumID,
		Type:          graph.NodeEnum,
		Name:          name,
		QualifiedName: qualifiedName,
		FilePath:      e.filePath,
		Line:          startLine,
		EndLine:       endLine,
		Package:       e.pkgName,
		Language:      string(parser.LangJava),
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

func (e *extractor) walkClassBody(body *sitter.Node, classID, className string) {
	for i := 0; i < int(body.NamedChildCount()); i++ {
		child := body.NamedChild(i)
		switch child.Type() {
		case "method_declaration":
			e.extractMethod(child, classID, className)
		case "constructor_declaration":
			e.extractConstructor(child, classID, className)
		case "field_declaration":
			e.extractField(child, classID, className)
		case "class_declaration":
			e.extractClass(child, classID)
		case "interface_declaration":
			e.extractInterface(child, classID)
		case "enum_declaration":
			e.extractEnum(child, classID)
		}
	}
}

func (e *extractor) walkInterfaceBody(body *sitter.Node, ifaceID, ifaceName string) {
	for i := 0; i < int(body.NamedChildCount()); i++ {
		child := body.NamedChild(i)
		if child.Type() == "method_declaration" {
			e.extractMethod(child, ifaceID, ifaceName)
		}
	}
}

// javaTestAnnotations are the JUnit/TestNG annotations that indicate a test method.
var javaTestAnnotations = map[string]bool{
	"Test": true, "ParameterizedTest": true, "RepeatedTest": true,
}

// hasTestAnnotation returns true if the annotations list contains a test annotation.
func hasTestAnnotation(annotations []string) bool {
	for _, ann := range annotations {
		// Strip any arguments: "ParameterizedTest(name = ...)" -> "ParameterizedTest"
		name := ann
		if idx := strings.Index(ann, "("); idx > 0 {
			name = ann[:idx]
		}
		if javaTestAnnotations[name] {
			return true
		}
	}
	return false
}

func (e *extractor) extractMethod(node *sitter.Node, parentID, className string) {
	name := ""
	returnType := ""
	params := ""
	var annotations []string
	modifiers := ""

	docComment := e.extractJavadoc(node)

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier":
			name = e.nodeText(child)
		case "modifiers":
			modifiers, annotations = e.extractModifiers(child)
		case "formal_parameters":
			params = e.nodeText(child)
		case "type_identifier", "void_type", "generic_type", "array_type",
			"integral_type", "floating_point_type", "boolean_type", "scoped_type_identifier":
			returnType = e.nodeText(child)
		}
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

	// Determine if this is a test method (only in test files with test annotations).
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
		Package:       e.pkgName,
		Language:      string(parser.LangJava),
		Exported:      strings.Contains(modifiers, "public"),
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

func (e *extractor) extractConstructor(node *sitter.Node, parentID, className string) {
	name := ""
	params := ""
	var annotations []string
	modifiers := ""

	docComment := e.extractJavadoc(node)

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier":
			name = e.nodeText(child)
		case "modifiers":
			modifiers, annotations = e.extractModifiers(child)
		case "formal_parameters":
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
		Package:       e.pkgName,
		Language:      string(parser.LangJava),
		Exported:      strings.Contains(modifiers, "public"),
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

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "modifiers":
			modifiers, annotations = e.extractModifiers(child)
		case "type_identifier", "generic_type", "array_type", "integral_type",
			"floating_point_type", "boolean_type", "scoped_type_identifier":
			fieldType = e.nodeText(child)
		case "variable_declarator":
			name := ""
			for j := 0; j < int(child.NamedChildCount()); j++ {
				id := child.NamedChild(j)
				if id.Type() == "identifier" {
					name = e.nodeText(id)
					break
				}
			}
			if name == "" {
				continue
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
			if fieldType != "" {
				props["type"] = fieldType
			}
			props["class"] = className

			varID := graph.NewNodeID(string(graph.NodeVariable), e.filePath, qualifiedName)

			e.nodes = append(e.nodes, &graph.Node{
				ID:            varID,
				Type:          graph.NodeVariable,
				Name:          name,
				QualifiedName: qualifiedName,
				FilePath:      e.filePath,
				Line:          line,
				Package:       e.pkgName,
				Language:      string(parser.LangJava),
				Exported:      strings.Contains(modifiers, "public"),
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

func (e *extractor) extractModifiers(node *sitter.Node) (string, []string) {
	var mods []string
	var annotations []string
	// Iterate all children (named and unnamed) to get keyword modifiers and annotations
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "marker_annotation", "annotation":
			ann := e.nodeText(child)
			ann = strings.TrimPrefix(ann, "@")
			annotations = append(annotations, ann)
		default:
			text := e.nodeText(child)
			switch text {
			case "public", "private", "protected", "static", "final", "abstract",
				"synchronized", "volatile", "transient", "native", "default":
				mods = append(mods, text)
			}
		}
	}

	return strings.Join(mods, " "), annotations
}

func (e *extractor) extractSuperclass(node *sitter.Node) string {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "type_identifier" || child.Type() == "generic_type" || child.Type() == "scoped_type_identifier" {
			return e.nodeText(child)
		}
	}
	return ""
}

func (e *extractor) extractSuperInterfaces(node *sitter.Node) []string {
	var ifaces []string
	// super_interfaces contains type_list which contains type identifiers
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "type_list" {
			for j := 0; j < int(child.NamedChildCount()); j++ {
				typeChild := child.NamedChild(j)
				ifaces = append(ifaces, e.nodeText(typeChild))
			}
		} else if child.Type() == "type_identifier" || child.Type() == "generic_type" {
			ifaces = append(ifaces, e.nodeText(child))
		}
	}
	return ifaces
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

func (e *extractor) extractJavadoc(node *sitter.Node) string {
	// Look for a block_comment immediately preceding the node.
	// In tree-sitter Java, comments are siblings (children of the same parent).
	parent := node.Parent()
	if parent == nil {
		return ""
	}

	// Find the index of this node among ALL children (not just named)
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

	// Walk backward to find a block_comment (skipping whitespace/non-named nodes)
	for j := idx - 1; j >= 0; j-- {
		prev := parent.Child(j)
		if prev.Type() == "block_comment" {
			text := e.nodeText(prev)
			if strings.HasPrefix(text, "/**") {
				return cleanJavadoc(text)
			}
			return ""
		}
		// Skip line comments but stop at any other node type
		if prev.Type() != "line_comment" {
			break
		}
	}

	return ""
}

// buildCallMaps populates lookup maps from already-extracted nodes so that
// walkForCalls can resolve call targets.
func (e *extractor) buildCallMaps() {
	e.importMap = make(map[string]string)
	e.classMethodMap = make(map[string]map[string]string)

	for _, n := range e.nodes {
		switch n.Type {
		case graph.NodeDependency:
			if n.Properties != nil && n.Properties["kind"] == "import" {
				// Store by the short name (last segment of the fully qualified import)
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

// walkMethodBodies traverses the AST a second time, walking into class/method
// bodies to detect HTTP client calls and function call graph edges.
func (e *extractor) walkMethodBodies(root *sitter.Node) {
	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		if child.Type() == "class_declaration" {
			e.walkClassBodiesForCalls(child)
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
		case "class_body":
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
			// Look up the method ID from classMethodMap (handles both Method and TestFunction).
			methodID := ""
			if methods, ok := e.classMethodMap[className]; ok {
				methodID = methods[methodName]
			}
			if methodID == "" {
				qualifiedName := className + "." + methodName
				methodID = graph.NewNodeID(string(graph.NodeMethod), e.filePath, qualifiedName)
			}
			// Walk the method body for calls
			for j := 0; j < int(child.NamedChildCount()); j++ {
				bodyChild := child.NamedChild(j)
				if bodyChild.Type() == "block" || bodyChild.Type() == "constructor_body" {
					e.walkForCalls(bodyChild, methodID, className)
				}
			}
		case "class_declaration":
			// Nested class
			e.walkClassBodiesForCalls(child)
		}
	}
}

// walkForCalls recursively walks a node tree looking for method_invocation and
// object_creation_expression nodes to detect HTTP client calls and function calls.
func (e *extractor) walkForCalls(node *sitter.Node, methodID string, className string) {
	if node == nil {
		return
	}

	switch node.Type() {
	case "method_invocation":
		if !e.checkHTTPClientCall(node, methodID) {
			e.checkFunctionCall(node, methodID, className)
		}
	case "object_creation_expression":
		e.checkObjectCreationHTTP(node, methodID)
	}

	for i := 0; i < int(node.NamedChildCount()); i++ {
		e.walkForCalls(node.NamedChild(i), methodID, className)
	}
}

// RestTemplate method name → HTTP method mapping.
var restTemplateMethods = map[string]string{
	"getForObject":   "GET",
	"getForEntity":   "GET",
	"postForObject":  "POST",
	"postForEntity":  "POST",
	"put":            "PUT",
	"delete":         "DELETE",
	"exchange":       "UNKNOWN",
	"patchForObject": "PATCH",
}

// WebClient builder methods → HTTP method mapping.
var webClientMethods = map[string]string{
	"get":    "GET",
	"post":   "POST",
	"put":    "PUT",
	"delete": "DELETE",
	"patch":  "PATCH",
}

// checkHTTPClientCall checks if a method_invocation node represents an HTTP
// client call (RestTemplate, WebClient, HttpClient, OkHttp) and creates
// appropriate dependency nodes and call edges. Returns true if it matched.
func (e *extractor) checkHTTPClientCall(node *sitter.Node, methodID string) bool {
	// Extract the full text of the invocation for chain analysis
	fullText := e.nodeText(node)
	fullTextLower := strings.ToLower(fullText)

	// Try to identify the object and method from the invocation
	objectName, methodName := e.extractInvocationParts(node)
	objectLower := strings.ToLower(objectName)

	// 1. Spring RestTemplate
	if strings.Contains(objectLower, "resttemplate") {
		if httpMethod, ok := restTemplateMethods[methodName]; ok {
			path := e.extractFirstStringArg(node)
			if path != "" {
				e.addHTTPCallDep(node, methodID, httpMethod, path, "spring-resttemplate")
				return true
			}
		}
	}

	// 2. Spring WebClient: webClient.get().uri("/path")
	if strings.Contains(objectLower, "webclient") || strings.Contains(fullTextLower, "webclient") {
		for wcMethod, httpMethod := range webClientMethods {
			if strings.Contains(fullTextLower, "."+wcMethod+"()") || strings.Contains(fullTextLower, "."+wcMethod+"(") {
				path := e.extractURIArg(node)
				if path != "" {
					e.addHTTPCallDep(node, methodID, httpMethod, path, "spring-webclient")
					return true
				}
			}
		}
	}

	// 3. Java 11+ HttpClient: HttpRequest.newBuilder().uri(URI.create("/path"))
	if strings.Contains(fullTextLower, "httprequest") && strings.Contains(fullTextLower, "uri") {
		path := e.extractURICreateArg(node)
		if path != "" {
			e.addHTTPCallDep(node, methodID, "UNKNOWN", path, "java.net.http")
			return true
		}
	}

	// 4. OkHttp: new Request.Builder().url("/path")
	if methodName == "url" && strings.Contains(fullTextLower, "request") {
		path := e.extractFirstStringArg(node)
		if path != "" {
			e.addHTTPCallDep(node, methodID, "UNKNOWN", path, "okhttp")
			return true
		}
	}

	return false
}

// checkObjectCreationHTTP checks object_creation_expression for HTTP patterns
// like new URL("http://...").openConnection().
func (e *extractor) checkObjectCreationHTTP(node *sitter.Node, methodID string) {
	fullText := e.nodeText(node)
	if !strings.Contains(fullText, "URL") {
		return
	}

	// Look for new URL("http://...")
	path := ""
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "argument_list" {
			for j := 0; j < int(child.NamedChildCount()); j++ {
				arg := child.NamedChild(j)
				if arg.Type() == "string_literal" {
					path = cleanJavaString(e.nodeText(arg))
					break
				}
			}
		}
	}

	if path != "" {
		e.addHTTPCallDep(node, methodID, "UNKNOWN", path, "java.net")
	}
}

// addHTTPCallDep creates a NodeDependency with kind=api_call and an EdgeCalls.
func (e *extractor) addHTTPCallDep(node *sitter.Node, methodID, httpMethod, path, framework string) {
	line := int(node.StartPoint().Row) + 1
	depID := graph.NewNodeID(string(graph.NodeDependency), e.filePath,
		"api_call:"+httpMethod+":"+path+":"+fmt.Sprintf("%d", line))

	e.nodes = append(e.nodes, &graph.Node{
		ID:       depID,
		Type:     graph.NodeDependency,
		Name:     httpMethod + " " + path,
		FilePath: e.filePath,
		Line:     line,
		Language: string(parser.LangJava),
		Properties: map[string]string{
			"kind":        "api_call",
			"http_method": httpMethod,
			"path":        path,
			"framework":   framework,
		},
	})

	if methodID != "" {
		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(methodID, depID, string(graph.EdgeCalls)),
			Type:     graph.EdgeCalls,
			SourceID: methodID,
			TargetID: depID,
		})
	}
}

// extractInvocationParts returns the object name and method name from a
// method_invocation node. E.g., "restTemplate.getForObject(...)" returns
// ("restTemplate", "getForObject").
//
// In Java tree-sitter, method_invocation children are:
//
//	[identifier|field_access|method_invocation] "." identifier argument_list
//
// The first named child before "." is the object; the identifier after "." is
// the method name.
func (e *extractor) extractInvocationParts(node *sitter.Node) (string, string) {
	// Collect all named children in order, excluding argument_list and type_arguments.
	var identifiers []string
	var hasObject bool
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "argument_list" {
			break
		}
		if child.Type() == "." {
			continue
		}
		if child.Type() == "type_arguments" {
			continue
		}
		if child.IsNamed() {
			identifiers = append(identifiers, e.nodeText(child))
			if child.Type() != "identifier" {
				// field_access, method_invocation, etc. — always an object
				hasObject = true
			}
		}
	}

	switch len(identifiers) {
	case 0:
		return "", ""
	case 1:
		// Just a method name, no object (unqualified call)
		if hasObject {
			return identifiers[0], ""
		}
		return "", identifiers[0]
	default:
		// First is the object expression, last is the method name
		return identifiers[0], identifiers[len(identifiers)-1]
	}
}

// extractFirstStringArg extracts the first string literal argument from a
// method_invocation's argument list.
func (e *extractor) extractFirstStringArg(node *sitter.Node) string {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "argument_list" {
			return e.findFirstString(child)
		}
	}
	return ""
}

// findFirstString recursively finds the first string_literal in a subtree.
func (e *extractor) findFirstString(node *sitter.Node) string {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "string_literal" {
			return cleanJavaString(e.nodeText(child))
		}
		// Check binary expressions like "/api/users/" + id
		if child.Type() == "binary_expression" {
			path := e.extractConcatPath(child)
			if path != "" {
				return path
			}
		}
	}
	return ""
}

// extractConcatPath extracts string parts from a binary expression (string concatenation).
func (e *extractor) extractConcatPath(node *sitter.Node) string {
	if node.Type() == "string_literal" {
		return cleanJavaString(e.nodeText(node))
	}
	if node.Type() != "binary_expression" {
		return ""
	}

	var parts []string
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "string_literal":
			parts = append(parts, cleanJavaString(e.nodeText(child)))
		case "binary_expression":
			sub := e.extractConcatPath(child)
			if sub != "" {
				parts = append(parts, sub)
			}
		default:
			// Variable reference: replace with wildcard
			parts = append(parts, "*")
		}
	}
	return strings.Join(parts, "")
}

// extractURIArg extracts a string from a .uri("path") call in a method chain.
func (e *extractor) extractURIArg(node *sitter.Node) string {
	fullText := e.nodeText(node)
	// Look for .uri("...") pattern
	idx := strings.Index(fullText, ".uri(")
	if idx < 0 {
		return ""
	}
	sub := fullText[idx+5:] // after ".uri("
	return extractQuotedString(sub)
}

// extractURICreateArg extracts a string from URI.create("path") in the AST.
func (e *extractor) extractURICreateArg(node *sitter.Node) string {
	fullText := e.nodeText(node)
	idx := strings.Index(fullText, "URI.create(")
	if idx < 0 {
		return ""
	}
	sub := fullText[idx+11:] // after "URI.create("
	return extractQuotedString(sub)
}

// Java builtin method names to skip in function call analysis.
var javaBuiltins = map[string]bool{
	"toString": true, "hashCode": true, "equals": true, "getClass": true,
	"notify": true, "notifyAll": true, "wait": true, "clone": true, "finalize": true,
	"valueOf": true, "length": true, "size": true, "get": true, "set": true,
	"add": true, "remove": true, "contains": true, "isEmpty": true,
	"iterator": true, "compareTo": true, "println": true, "print": true,
	"printf": true, "format": true, "append": true, "substring": true,
	"trim": true, "split": true, "replace": true, "matches": true,
	"charAt": true, "indexOf": true, "lastIndexOf": true, "toUpperCase": true,
	"toLowerCase": true, "startsWith": true, "endsWith": true, "toArray": true,
	"stream": true, "collect": true, "map": true, "filter": true,
	"forEach": true, "of": true, "asList": true, "sort": true,
	"getName": true, "getBytes": true, "close": true, "flush": true,
	"read": true, "write": true, "next": true, "hasNext": true,
	"put": true, "containsKey": true, "keySet": true, "values": true,
	"entrySet": true, "getOrDefault": true, "putIfAbsent": true,
}

// checkFunctionCall checks if a method_invocation represents a function call
// within the project (same-class, this-qualified, or import-qualified) and
// creates EdgeCalls edges.
func (e *extractor) checkFunctionCall(node *sitter.Node, methodID string, className string) {
	if methodID == "" {
		return
	}

	objectName, calledMethod := e.extractInvocationParts(node)
	if calledMethod == "" || javaBuiltins[calledMethod] {
		return
	}

	// Case 1: no object or "this" → same-class call
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

	// Case 2: ClassName.method() → static/import-qualified call
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

func (e *extractor) nodeText(node *sitter.Node) string {
	return node.Content(e.content)
}

// Helper functions

func edgeID(sourceID, targetID, edgeType string) string {
	return graph.NewNodeID(edgeType, sourceID, targetID)
}

func cleanJavadoc(raw string) string {
	// Remove /** and */ wrapping
	s := strings.TrimPrefix(raw, "/**")
	s = strings.TrimSuffix(s, "*/")

	// Clean up lines: remove leading " * " prefixes
	lines := strings.Split(s, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "* ")
		line = strings.TrimPrefix(line, "*")
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n")
}

// cleanJavaString removes surrounding quotes from a Java string literal.
func cleanJavaString(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// extractQuotedString extracts a double-quoted string from a text fragment.
// E.g., from `"/api/users")` it extracts `/api/users`.
func extractQuotedString(s string) string {
	start := strings.Index(s, `"`)
	if start < 0 {
		return ""
	}
	end := strings.Index(s[start+1:], `"`)
	if end < 0 {
		return ""
	}
	return s[start+1 : start+1+end]
}
