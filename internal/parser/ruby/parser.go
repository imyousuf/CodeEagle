package ruby

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/ruby"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

// RubyParser extracts knowledge graph nodes and edges from Ruby source files.
type RubyParser struct{}

// NewParser creates a new Ruby parser.
func NewParser() *RubyParser {
	return &RubyParser{}
}

func (p *RubyParser) Language() parser.Language {
	return parser.LangRuby
}

func (p *RubyParser) Extensions() []string {
	return parser.FileExtensions[parser.LangRuby]
}

func (p *RubyParser) ParseFile(filePath string, content []byte) (*parser.ParseResult, error) {
	lang := ruby.GetLanguage()
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
		Language: parser.LangRuby,
	}, nil
}

// extractor walks a tree-sitter Ruby AST and builds graph nodes and edges.
type extractor struct {
	filePath string
	content  []byte
	tree     *sitter.Tree
	nodes    []*graph.Node
	edges    []*graph.Edge

	fileNodeID string
	isTestFile bool
	isRoutes   bool

	// Current module namespace stack for qualified names.
	moduleStack []string

	// Visibility tracking per class/module scope.
	// When a bare `private` or `protected` call is encountered, subsequent
	// methods default to that visibility until the scope ends.
	currentVisibility string

	// Lookup maps for function call resolution.
	classMethodMap map[string]map[string]string // className -> methodName -> node ID
}

func (e *extractor) extract() {
	e.extractFileNode()
	e.isRoutes = isRoutesFile(e.filePath)

	root := e.tree.RootNode()
	e.walkProgram(root, e.fileNodeID)

	// Build call maps and do a second pass for calls.
	e.buildCallMaps()
	e.walkForCallsRoot(root)
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
		Language: string(parser.LangRuby),
	})
}

// isTestFilename returns true if the filename matches Ruby test file patterns.
func isTestFilename(base string) bool {
	if !strings.HasSuffix(base, ".rb") {
		return false
	}
	name := strings.TrimSuffix(base, ".rb")
	return strings.HasSuffix(name, "_spec") ||
		strings.HasSuffix(name, "_test") ||
		strings.HasPrefix(name, "test_")
}

// isRoutesFile checks if this is a Rails routes file.
func isRoutesFile(filePath string) bool {
	base := filepath.Base(filePath)
	return base == "routes.rb" || strings.Contains(filePath, "config/routes")
}

// httpMethods are Rails route DSL method names.
var httpMethods = map[string]string{
	"get":    "GET",
	"post":   "POST",
	"put":    "PUT",
	"delete": "DELETE",
	"patch":  "PATCH",
}

// activeRecordBases lists base classes that indicate an ActiveRecord model.
var activeRecordBases = map[string]bool{
	"ApplicationRecord":  true,
	"ActiveRecord::Base": true,
}

// controllerSuffix is the naming convention for Rails controllers.
const controllerSuffix = "Controller"

func (e *extractor) currentNamespace() string {
	return strings.Join(e.moduleStack, "::")
}

func (e *extractor) qualifiedName(name string) string {
	ns := e.currentNamespace()
	if ns != "" {
		return ns + "::" + name
	}
	return name
}

func (e *extractor) walkProgram(root *sitter.Node, parentID string) {
	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		e.walkNode(child, parentID)
	}
}

func (e *extractor) walkNode(node *sitter.Node, parentID string) {
	switch node.Type() {
	case "module":
		e.extractModule(node, parentID)
	case "class":
		e.extractClass(node, parentID)
	case "method":
		e.extractMethod(node, parentID, "")
	case "singleton_method":
		e.extractSingletonMethod(node, parentID, "")
	case "call":
		handled := e.extractCall(node, parentID)
		// Walk into do_block for unhandled calls (e.g., Rails.application.routes.draw do ... end).
		// Skip for require/route/RSpec calls that already handle their own block walking.
		if !handled {
			for i := 0; i < int(node.NamedChildCount()); i++ {
				child := node.NamedChild(i)
				if child.Type() == "do_block" || child.Type() == "block" {
					e.walkDoBlock(child, parentID)
				}
			}
		}
	case "assignment":
		e.extractConstant(node, parentID)
	}
}

func (e *extractor) extractModule(node *sitter.Node, parentID string) {
	name := ""
	var bodyNode *sitter.Node

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "constant", "scope_resolution":
			name = e.nodeText(child)
		case "body_statement":
			bodyNode = child
		}
	}
	if name == "" {
		return
	}

	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1

	qname := e.qualifiedName(name)
	modID := graph.NewNodeID(string(graph.NodeModule), e.filePath, qname)

	e.nodes = append(e.nodes, &graph.Node{
		ID:            modID,
		Type:          graph.NodeModule,
		Name:          name,
		QualifiedName: qname,
		FilePath:      e.filePath,
		Line:          startLine,
		EndLine:       endLine,
		Language:      string(parser.LangRuby),
		Package:       qname,
		Exported:      true,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, modID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: modID,
	})

	if bodyNode != nil {
		e.moduleStack = append(e.moduleStack, name)
		savedVisibility := e.currentVisibility
		e.currentVisibility = "public"
		e.walkProgram(bodyNode, modID)
		e.currentVisibility = savedVisibility
		e.moduleStack = e.moduleStack[:len(e.moduleStack)-1]
	}
}

func (e *extractor) extractClass(node *sitter.Node, parentID string) {
	name := ""
	superclass := ""
	var bodyNode *sitter.Node

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "constant", "scope_resolution":
			name = e.nodeText(child)
		case "superclass":
			superclass = e.extractSuperclass(child)
		case "body_statement":
			bodyNode = child
		}
	}
	if name == "" {
		return
	}

	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1

	qname := e.qualifiedName(name)

	// Determine if this is an ActiveRecord model.
	isDBModel := activeRecordBases[superclass]
	nodeType := graph.NodeClass
	if isDBModel {
		nodeType = graph.NodeDBModel
	}

	classID := graph.NewNodeID(string(nodeType), e.filePath, qname)

	props := make(map[string]string)
	if superclass != "" {
		props["bases"] = superclass
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:            classID,
		Type:          nodeType,
		Name:          name,
		QualifiedName: qname,
		FilePath:      e.filePath,
		Line:          startLine,
		EndLine:       endLine,
		Language:      string(parser.LangRuby),
		Package:       e.currentNamespace(),
		Exported:      true,
		Properties:    props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, classID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: classID,
	})

	if bodyNode != nil {
		e.moduleStack = append(e.moduleStack, name)
		savedVisibility := e.currentVisibility
		e.currentVisibility = "public"
		e.walkClassBody(bodyNode, classID, name)
		e.currentVisibility = savedVisibility
		e.moduleStack = e.moduleStack[:len(e.moduleStack)-1]
	}
}

func (e *extractor) walkClassBody(body *sitter.Node, classID, className string) {
	var includes []string

	for i := 0; i < int(body.NamedChildCount()); i++ {
		child := body.NamedChild(i)
		switch child.Type() {
		case "method":
			e.extractMethod(child, classID, className)
		case "singleton_method":
			e.extractSingletonMethod(child, classID, className)
		case "class":
			e.extractClass(child, classID)
		case "module":
			e.extractModule(child, classID)
		case "call":
			e.handleClassLevelCall(child, classID, className, &includes)
		case "assignment":
			e.extractConstant(child, classID)
		case "identifier":
			// Bare visibility keywords like `private`, `protected`, `public`.
			text := e.nodeText(child)
			switch text {
			case "private", "protected", "public":
				e.currentVisibility = text
			}
		}
	}

	// Add includes to class properties.
	if len(includes) > 0 {
		for _, n := range e.nodes {
			if n.ID == classID {
				if n.Properties == nil {
					n.Properties = make(map[string]string)
				}
				n.Properties["includes"] = strings.Join(includes, ",")
				break
			}
		}
	}
}

// handleClassLevelCall processes calls at class body level: include, extend,
// attr_reader/writer/accessor, private/protected, and Rails route methods.
func (e *extractor) handleClassLevelCall(node *sitter.Node, classID, className string, includes *[]string) {
	methodName := ""
	var argsNode *sitter.Node

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier":
			methodName = e.nodeText(child)
		case "argument_list":
			argsNode = child
		}
	}

	switch methodName {
	case "include":
		if argsNode != nil {
			modName := e.extractFirstConstantArg(argsNode)
			if modName != "" {
				*includes = append(*includes, modName)
				// Create EdgeImplements for mixin.
				modNodeID := graph.NewNodeID(string(graph.NodeModule), e.filePath, modName)
				e.edges = append(e.edges, &graph.Edge{
					ID:       edgeID(classID, modNodeID, string(graph.EdgeImplements)),
					Type:     graph.EdgeImplements,
					SourceID: classID,
					TargetID: modNodeID,
				})
			}
		}
	case "extend":
		if argsNode != nil {
			modName := e.extractFirstConstantArg(argsNode)
			if modName != "" {
				*includes = append(*includes, modName)
			}
		}
	case "attr_reader", "attr_writer", "attr_accessor":
		if argsNode != nil {
			e.extractAttrAccessors(argsNode, classID, className, methodName)
		}
	case "private", "protected", "public":
		e.currentVisibility = methodName
	}
}

func (e *extractor) extractAttrAccessors(argsNode *sitter.Node, classID, className, kind string) {
	for i := 0; i < int(argsNode.NamedChildCount()); i++ {
		child := argsNode.NamedChild(i)
		if child.Type() == "simple_symbol" {
			attrName := strings.TrimPrefix(e.nodeText(child), ":")
			line := int(child.StartPoint().Row) + 1
			qname := className + "#" + attrName

			methodID := graph.NewNodeID(string(graph.NodeMethod), e.filePath, qname)
			e.nodes = append(e.nodes, &graph.Node{
				ID:            methodID,
				Type:          graph.NodeMethod,
				Name:          attrName,
				QualifiedName: qname,
				FilePath:      e.filePath,
				Line:          line,
				Language:      string(parser.LangRuby),
				Package:       e.currentNamespace(),
				Exported:      true,
				Properties: map[string]string{
					"kind":  kind,
					"class": className,
				},
			})

			e.edges = append(e.edges, &graph.Edge{
				ID:       edgeID(classID, methodID, string(graph.EdgeContains)),
				Type:     graph.EdgeContains,
				SourceID: classID,
				TargetID: methodID,
			})
		}
	}
}

func (e *extractor) extractMethod(node *sitter.Node, parentID, className string) {
	name := ""
	var bodyNode *sitter.Node
	var paramsNode *sitter.Node

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier":
			name = e.nodeText(child)
		case "method_parameters":
			paramsNode = child
		case "body_statement":
			bodyNode = child
		}
	}
	if name == "" {
		return
	}

	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1

	visibility := e.currentVisibility
	if visibility == "" {
		visibility = "public"
	}

	// Build signature.
	sig := "def " + name
	if paramsNode != nil {
		sig += e.nodeText(paramsNode)
	}

	// Determine node type.
	nodeType := graph.NodeMethod
	if className == "" {
		nodeType = graph.NodeFunction
	}
	if e.isTestFile && isTestMethodName(name, e.filePath) {
		nodeType = graph.NodeTestFunction
	}

	qname := name
	if className != "" {
		qname = className + "#" + name
	}

	methodID := graph.NewNodeID(string(nodeType), e.filePath, qname)

	props := map[string]string{
		"visibility": visibility,
	}
	if className != "" {
		props["class"] = className
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:            methodID,
		Type:          nodeType,
		Name:          name,
		QualifiedName: qname,
		FilePath:      e.filePath,
		Line:          startLine,
		EndLine:       endLine,
		Language:      string(parser.LangRuby),
		Package:       e.currentNamespace(),
		Exported:      visibility == "public",
		Signature:     sig,
		Properties:    props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, methodID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: methodID,
	})

	// If this is a controller action, also check for route connection.
	if strings.HasSuffix(className, controllerSuffix) {
		e.extractControllerAction(name, className, methodID, startLine)
	}

	_ = bodyNode // body is used in second-pass call extraction
}

func (e *extractor) extractSingletonMethod(node *sitter.Node, parentID, className string) {
	// singleton_method: def self.method_name ... end
	name := ""
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "identifier" {
			name = e.nodeText(child)
		}
	}
	if name == "" {
		return
	}

	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1

	qname := name
	if className != "" {
		qname = className + "." + name
	}

	methodID := graph.NewNodeID(string(graph.NodeMethod), e.filePath, qname)

	props := map[string]string{
		"visibility": "public",
		"static":     "true",
	}
	if className != "" {
		props["class"] = className
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:            methodID,
		Type:          graph.NodeMethod,
		Name:          name,
		QualifiedName: qname,
		FilePath:      e.filePath,
		Line:          startLine,
		EndLine:       endLine,
		Language:      string(parser.LangRuby),
		Package:       e.currentNamespace(),
		Exported:      true,
		Signature:     "def self." + name,
		Properties:    props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, methodID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: methodID,
	})
}

func (e *extractor) extractControllerAction(actionName, className, methodID string, line int) {
	// Controller actions are API endpoints.
	controllerBase := strings.TrimSuffix(className, controllerSuffix)
	controllerBase = strings.ToLower(controllerBase)
	// Conventional path: /controller_base/action
	path := "/" + controllerBase + "/" + actionName

	endpointID := graph.NewNodeID(string(graph.NodeAPIEndpoint), e.filePath, path)

	e.nodes = append(e.nodes, &graph.Node{
		ID:       endpointID,
		Type:     graph.NodeAPIEndpoint,
		Name:     path,
		FilePath: e.filePath,
		Line:     line,
		Language: string(parser.LangRuby),
		Properties: map[string]string{
			"controller": className,
			"action":     actionName,
		},
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(methodID, endpointID, string(graph.EdgeExposes)),
		Type:     graph.EdgeExposes,
		SourceID: methodID,
		TargetID: endpointID,
	})
}

// extractCall processes top-level call nodes (require, routes, RSpec).
// Returns true if the call was handled (caller should not walk into do_block).
func (e *extractor) extractCall(node *sitter.Node, parentID string) bool {
	methodName := ""
	var argsNode *sitter.Node
	receiver := ""

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "identifier":
			methodName = e.nodeText(child)
		case "constant":
			receiver = e.nodeText(child)
		case "argument_list":
			argsNode = child
		}
	}

	// Handle require/require_relative.
	if methodName == "require" || methodName == "require_relative" {
		e.extractRequire(node, parentID, methodName, argsNode)
		return true
	}

	// Handle Rails routes: get/post/put/delete/patch.
	if e.isRoutes {
		if httpMethod, ok := httpMethods[methodName]; ok {
			e.extractRouteEndpoint(node, parentID, httpMethod, argsNode)
			return true
		}
	}

	// Handle RSpec describe/it blocks for test extraction.
	if e.isTestFile {
		if methodName == "describe" || methodName == "context" || methodName == "it" {
			e.extractRSpecBlock(node, parentID, methodName, receiver, argsNode)
			return true
		}
	}

	return false
}

func (e *extractor) extractRequire(node *sitter.Node, parentID, kind string, argsNode *sitter.Node) {
	if argsNode == nil {
		return
	}

	name := e.extractFirstStringArg(argsNode)
	if name == "" {
		return
	}

	line := int(node.StartPoint().Row) + 1
	depID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, name)

	e.nodes = append(e.nodes, &graph.Node{
		ID:       depID,
		Type:     graph.NodeDependency,
		Name:     name,
		FilePath: e.filePath,
		Line:     line,
		Language: string(parser.LangRuby),
		Properties: map[string]string{
			"kind": kind,
		},
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, depID, string(graph.EdgeImports)),
		Type:     graph.EdgeImports,
		SourceID: parentID,
		TargetID: depID,
	})
}

func (e *extractor) extractRouteEndpoint(node *sitter.Node, parentID, httpMethod string, argsNode *sitter.Node) {
	if argsNode == nil {
		return
	}

	path := e.extractFirstStringArg(argsNode)
	if path == "" {
		return
	}

	line := int(node.StartPoint().Row) + 1
	endpointName := httpMethod + " " + path

	endpointID := graph.NewNodeID(string(graph.NodeAPIEndpoint), e.filePath, endpointName)

	props := map[string]string{
		"http_method": httpMethod,
		"path":        path,
	}

	// Extract controller#action from `to:` option.
	controller := e.extractToOption(argsNode)
	if controller != "" {
		props["controller"] = controller
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:         endpointID,
		Type:       graph.NodeAPIEndpoint,
		Name:       endpointName,
		FilePath:   e.filePath,
		Line:       line,
		Language:   string(parser.LangRuby),
		Properties: props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, endpointID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: endpointID,
	})
}

func (e *extractor) extractRSpecBlock(node *sitter.Node, parentID, kind, receiver string, argsNode *sitter.Node) {
	// For RSpec: `describe`, `context`, `it` blocks.
	blockName := ""
	if argsNode != nil {
		// Try constant first (e.g., `describe UserService`)
		blockName = e.extractFirstConstantArg(argsNode)
		if blockName == "" {
			// Then string (e.g., `it 'does something'`)
			blockName = e.extractFirstStringArg(argsNode)
		}
	}
	if blockName == "" {
		return
	}

	// `it` blocks become test functions.
	if kind == "it" {
		line := int(node.StartPoint().Row) + 1
		endLine := int(node.EndPoint().Row) + 1

		funcID := graph.NewNodeID(string(graph.NodeTestFunction), e.filePath, blockName+fmt.Sprintf(":%d", line))

		e.nodes = append(e.nodes, &graph.Node{
			ID:            funcID,
			Type:          graph.NodeTestFunction,
			Name:          blockName,
			QualifiedName: blockName,
			FilePath:      e.filePath,
			Line:          line,
			EndLine:       endLine,
			Language:      string(parser.LangRuby),
			Properties: map[string]string{
				"kind": "rspec_it",
			},
		})

		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(parentID, funcID, string(graph.EdgeContains)),
			Type:     graph.EdgeContains,
			SourceID: parentID,
			TargetID: funcID,
		})
		return
	}

	// `describe` or `context` blocks — walk into the do_block to find nested blocks.
	var doBlock *sitter.Node
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "do_block" || child.Type() == "block" {
			doBlock = child
			break
		}
	}
	if doBlock != nil {
		e.walkDoBlock(doBlock, parentID)
	}
}

func (e *extractor) walkDoBlock(doBlock *sitter.Node, parentID string) {
	for i := 0; i < int(doBlock.NamedChildCount()); i++ {
		child := doBlock.NamedChild(i)
		if child.Type() == "body_statement" {
			e.walkProgram(child, parentID)
		}
	}
}

func (e *extractor) extractConstant(node *sitter.Node, parentID string) {
	// assignment: LEFT = RIGHT
	if node.NamedChildCount() < 2 {
		return
	}

	left := node.NamedChild(0)
	if left.Type() != "constant" {
		return
	}

	name := e.nodeText(left)
	line := int(node.StartPoint().Row) + 1

	constID := graph.NewNodeID(string(graph.NodeConstant), e.filePath, e.qualifiedName(name))

	e.nodes = append(e.nodes, &graph.Node{
		ID:       constID,
		Type:     graph.NodeConstant,
		Name:     name,
		FilePath: e.filePath,
		Line:     line,
		Language: string(parser.LangRuby),
		Package:  e.currentNamespace(),
		Exported: true,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, constID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: constID,
	})
}

// --- Second pass: function call extraction ---

func (e *extractor) buildCallMaps() {
	e.classMethodMap = make(map[string]map[string]string)

	for _, n := range e.nodes {
		switch n.Type {
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

func (e *extractor) walkForCallsRoot(root *sitter.Node) {
	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		switch child.Type() {
		case "class":
			e.walkClassForCalls(child)
		case "module":
			e.walkModuleForCalls(child)
		}
	}
}

func (e *extractor) walkModuleForCalls(modNode *sitter.Node) {
	for i := 0; i < int(modNode.NamedChildCount()); i++ {
		child := modNode.NamedChild(i)
		if child.Type() == "body_statement" {
			for j := 0; j < int(child.NamedChildCount()); j++ {
				inner := child.NamedChild(j)
				switch inner.Type() {
				case "class":
					e.walkClassForCalls(inner)
				case "module":
					e.walkModuleForCalls(inner)
				}
			}
		}
	}
}

func (e *extractor) walkClassForCalls(classNode *sitter.Node) {
	className := ""
	var bodyNode *sitter.Node
	for i := 0; i < int(classNode.NamedChildCount()); i++ {
		child := classNode.NamedChild(i)
		switch child.Type() {
		case "constant":
			className = e.nodeText(child)
		case "body_statement":
			bodyNode = child
		}
	}
	if className == "" || bodyNode == nil {
		return
	}

	for i := 0; i < int(bodyNode.NamedChildCount()); i++ {
		child := bodyNode.NamedChild(i)
		if child.Type() == "method" {
			methodName := ""
			var methodBody *sitter.Node
			for j := 0; j < int(child.NamedChildCount()); j++ {
				mc := child.NamedChild(j)
				switch mc.Type() {
				case "identifier":
					methodName = mc.Content(e.content)
				case "body_statement":
					methodBody = mc
				}
			}
			if methodName == "" || methodBody == nil {
				continue
			}

			// Resolve method ID.
			qname := className + "#" + methodName
			nodeType := graph.NodeMethod
			if e.isTestFile && isTestMethodName(methodName, e.filePath) {
				nodeType = graph.NodeTestFunction
			}
			methodID := graph.NewNodeID(string(nodeType), e.filePath, qname)

			e.scanForCalls(methodBody, methodID, className)
		}
	}
}

// rubyBuiltins are common Ruby methods to skip in call graph analysis.
var rubyBuiltins = map[string]bool{
	"puts": true, "print": true, "p": true, "pp": true,
	"raise": true, "require": true, "require_relative": true,
	"include": true, "extend": true, "prepend": true,
	"attr_reader": true, "attr_writer": true, "attr_accessor": true,
	"private": true, "protected": true, "public": true,
	"new": true, "initialize": true, "to_s": true, "to_i": true,
	"to_f": true, "to_a": true, "to_h": true, "inspect": true,
	"nil?": true, "empty?": true, "blank?": true, "present?": true,
	"each": true, "map": true, "select": true, "reject": true,
	"reduce": true, "inject": true, "find": true, "detect": true,
	"any?": true, "all?": true, "none?": true, "count": true,
	"length": true, "size": true, "first": true, "last": true,
	"push": true, "pop": true, "shift": true, "unshift": true,
	"keys": true, "values": true, "merge": true, "fetch": true,
	"render": true, "redirect_to": true, "respond_to": true,
	"has_many": true, "belongs_to": true, "has_one": true,
	"validates": true, "validate": true, "before_action": true,
	"after_action": true, "around_action": true,
}

func (e *extractor) scanForCalls(node *sitter.Node, methodID, className string) {
	if node == nil {
		return
	}

	switch node.Type() {
	case "call":
		e.checkFunctionCall(node, methodID, className)
	case "identifier":
		// In Ruby, bare method calls without arguments/parens are parsed as identifiers.
		e.checkBareCall(node, methodID, className)
	}

	for i := 0; i < int(node.NamedChildCount()); i++ {
		e.scanForCalls(node.NamedChild(i), methodID, className)
	}
}

// checkBareCall handles Ruby bare method calls (no parens/args) which tree-sitter
// parses as plain identifier nodes rather than call nodes.
func (e *extractor) checkBareCall(node *sitter.Node, methodID, className string) {
	if methodID == "" {
		return
	}

	calledMethod := e.nodeText(node)
	if calledMethod == "" || rubyBuiltins[calledMethod] {
		return
	}

	// Only match same-class methods.
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
}

func (e *extractor) checkFunctionCall(node *sitter.Node, methodID, className string) {
	if methodID == "" {
		return
	}

	calledMethod := ""
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "identifier" {
			calledMethod = e.nodeText(child)
		}
	}
	if calledMethod == "" || rubyBuiltins[calledMethod] {
		return
	}

	// Same-class call.
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
}

// --- Helper extraction functions ---

func (e *extractor) extractSuperclass(node *sitter.Node) string {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "constant" || child.Type() == "scope_resolution" {
			return e.nodeText(child)
		}
	}
	return ""
}

func (e *extractor) extractFirstStringArg(argsNode *sitter.Node) string {
	for i := 0; i < int(argsNode.NamedChildCount()); i++ {
		child := argsNode.NamedChild(i)
		if child.Type() == "string" {
			return e.extractStringContent(child)
		}
	}
	return ""
}

func (e *extractor) extractFirstConstantArg(argsNode *sitter.Node) string {
	for i := 0; i < int(argsNode.NamedChildCount()); i++ {
		child := argsNode.NamedChild(i)
		if child.Type() == "constant" || child.Type() == "scope_resolution" {
			return e.nodeText(child)
		}
	}
	return ""
}

func (e *extractor) extractStringContent(strNode *sitter.Node) string {
	for i := 0; i < int(strNode.NamedChildCount()); i++ {
		child := strNode.NamedChild(i)
		if child.Type() == "string_content" {
			return e.nodeText(child)
		}
	}
	return ""
}

func (e *extractor) extractToOption(argsNode *sitter.Node) string {
	// Look for `to: 'controller#action'` pair.
	for i := 0; i < int(argsNode.NamedChildCount()); i++ {
		child := argsNode.NamedChild(i)
		if child.Type() == "pair" {
			key := ""
			val := ""
			for j := 0; j < int(child.NamedChildCount()); j++ {
				pChild := child.NamedChild(j)
				switch pChild.Type() {
				case "hash_key_symbol":
					key = e.nodeText(pChild)
				case "string":
					val = e.extractStringContent(pChild)
				}
			}
			if key == "to" && val != "" {
				return val
			}
		}
	}
	return ""
}

func (e *extractor) nodeText(node *sitter.Node) string {
	return node.Content(e.content)
}

// isTestMethodName returns true if the method name is a test method.
func isTestMethodName(name, filePath string) bool {
	base := filepath.Base(filePath)
	if !isTestFilename(base) {
		return false
	}
	return strings.HasPrefix(name, "test_")
}

// Helper functions

func edgeID(sourceID, targetID, edgeType string) string {
	return graph.NewNodeID(edgeType, sourceID, targetID)
}
