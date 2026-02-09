package java

import (
	"context"
	"fmt"
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
}

func (e *extractor) extract() {
	e.extractFileNode()

	root := e.tree.RootNode()
	e.walkProgram(root)
}

func (e *extractor) extractFileNode() {
	e.fileNodeID = graph.NewNodeID(string(graph.NodeFile), e.filePath, e.filePath)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       e.fileNodeID,
		Type:     graph.NodeFile,
		Name:     e.filePath,
		FilePath: e.filePath,
		Language: string(parser.LangJava),
	})
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
