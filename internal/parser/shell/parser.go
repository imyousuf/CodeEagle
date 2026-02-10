package shell

import (
	"context"
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/bash"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

// ShellParser extracts knowledge graph nodes and edges from shell scripts.
type ShellParser struct{}

// NewParser creates a new Shell parser.
func NewParser() *ShellParser {
	return &ShellParser{}
}

func (p *ShellParser) Language() parser.Language {
	return parser.LangShell
}

func (p *ShellParser) Extensions() []string {
	return parser.FileExtensions[parser.LangShell]
}

func (p *ShellParser) ParseFile(filePath string, content []byte) (*parser.ParseResult, error) {
	lang := bash.GetLanguage()
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
		Language: parser.LangShell,
	}, nil
}

type extractor struct {
	filePath string
	content  []byte
	tree     *sitter.Tree
	nodes    []*graph.Node
	edges    []*graph.Edge

	fileNodeID string
}

func (e *extractor) extract() {
	e.extractFileNode()
	root := e.tree.RootNode()
	e.extractShebang(root)
	e.walkTopLevel(root)
}

func (e *extractor) extractFileNode() {
	e.fileNodeID = graph.NewNodeID(string(graph.NodeFile), e.filePath, e.filePath)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       e.fileNodeID,
		Type:     graph.NodeFile,
		Name:     e.filePath,
		FilePath: e.filePath,
		Language: string(parser.LangShell),
	})
}

func (e *extractor) extractShebang(root *sitter.Node) {
	if root.NamedChildCount() == 0 {
		return
	}
	first := root.Child(0) // Use Child, not NamedChild, since comments may not be named
	if first == nil {
		return
	}
	if first.Type() == "comment" {
		text := e.nodeText(first)
		if strings.HasPrefix(text, "#!") {
			// Set shebang property on file node.
			for _, node := range e.nodes {
				if node.ID == e.fileNodeID {
					if node.Properties == nil {
						node.Properties = make(map[string]string)
					}
					node.Properties["shebang"] = text
					break
				}
			}
		}
	}
}

func (e *extractor) walkTopLevel(root *sitter.Node) {
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		switch child.Type() {
		case "function_definition":
			e.extractFunction(child)
		case "variable_assignment":
			e.extractVariable(child, false)
		case "declaration_command":
			e.extractDeclaration(child)
		case "command":
			e.extractCommand(child)
		}
	}
}

func (e *extractor) extractFunction(node *sitter.Node) {
	name := ""
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "word" {
			name = e.nodeText(child)
			break
		}
	}
	if name == "" {
		return
	}

	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1

	funcID := graph.NewNodeID(string(graph.NodeFunction), e.filePath, name)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       funcID,
		Type:     graph.NodeFunction,
		Name:     name,
		FilePath: e.filePath,
		Line:     startLine,
		EndLine:  endLine,
		Language: string(parser.LangShell),
		Exported: true,
		Properties: map[string]string{
			"kind": "function",
		},
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.fileNodeID, funcID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.fileNodeID,
		TargetID: funcID,
	})
}

func (e *extractor) extractVariable(node *sitter.Node, exported bool) {
	name := ""
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "variable_name" {
			name = e.nodeText(child)
			break
		}
	}
	if name == "" {
		return
	}

	line := int(node.StartPoint().Row) + 1

	props := map[string]string{
		"kind": "shell_var",
	}
	if exported {
		props["exported"] = "true"
	}

	varID := graph.NewNodeID(string(graph.NodeVariable), e.filePath, name)
	e.nodes = append(e.nodes, &graph.Node{
		ID:         varID,
		Type:       graph.NodeVariable,
		Name:       name,
		FilePath:   e.filePath,
		Line:       line,
		Language:   string(parser.LangShell),
		Exported:   exported,
		Properties: props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.fileNodeID, varID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.fileNodeID,
		TargetID: varID,
	})
}

func (e *extractor) extractDeclaration(node *sitter.Node) {
	// declaration_command: export/readonly/declare + variable_assignment
	declType := ""
	exported := false

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "export":
			declType = "export"
			exported = true
		case "readonly":
			declType = "readonly"
		case "declare":
			declType = "declare"
		case "word":
			// Check for declare -x flag.
			text := e.nodeText(child)
			if strings.Contains(text, "x") && declType == "declare" {
				exported = true
			}
		case "variable_assignment":
			e.extractVariable(child, exported)
		}
	}
	_ = declType
}

func (e *extractor) extractCommand(node *sitter.Node) {
	// Check for source/. commands.
	if node.NamedChildCount() == 0 {
		return
	}

	cmdNameNode := node.ChildByFieldName("name")
	if cmdNameNode == nil {
		// Fallback: first named child might be the command_name.
		for i := 0; i < int(node.NamedChildCount()); i++ {
			child := node.NamedChild(i)
			if child.Type() == "command_name" {
				cmdNameNode = child
				break
			}
		}
	}
	if cmdNameNode == nil {
		return
	}

	// The command_name contains a word child.
	cmdName := ""
	if cmdNameNode.Type() == "command_name" && cmdNameNode.ChildCount() > 0 {
		cmdName = e.nodeText(cmdNameNode.Child(0))
	} else {
		cmdName = e.nodeText(cmdNameNode)
	}

	if cmdName != "source" && cmdName != "." {
		return
	}

	// Get the argument (file being sourced).
	// Arguments follow the command_name in the command node.
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "word" || child.Type() == "string" || child.Type() == "raw_string" {
			sourcePath := e.nodeText(child)
			// Clean up quotes if present.
			sourcePath = strings.Trim(sourcePath, "\"'")
			e.addSourceImport(sourcePath, int(node.StartPoint().Row)+1)
			return
		}
	}
}

func (e *extractor) addSourceImport(path string, line int) {
	depID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, "source:"+path)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       depID,
		Type:     graph.NodeDependency,
		Name:     path,
		FilePath: e.filePath,
		Line:     line,
		Language: string(parser.LangShell),
		Properties: map[string]string{
			"kind": "source",
		},
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.fileNodeID, depID, string(graph.EdgeImports)),
		Type:     graph.EdgeImports,
		SourceID: e.fileNodeID,
		TargetID: depID,
	})
}

func (e *extractor) nodeText(node *sitter.Node) string {
	return node.Content(e.content)
}

func edgeID(sourceID, targetID, edgeType string) string {
	return graph.NewNodeID(edgeType, sourceID, targetID)
}
