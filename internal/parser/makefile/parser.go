package makefile

import (
	"regexp"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

// MakefileParser extracts knowledge graph nodes and edges from Makefiles.
type MakefileParser struct{}

// NewParser creates a new Makefile parser.
func NewParser() *MakefileParser {
	return &MakefileParser{}
}

func (p *MakefileParser) Language() parser.Language {
	return parser.LangMakefile
}

func (p *MakefileParser) Extensions() []string {
	return parser.FileExtensions[parser.LangMakefile]
}

func (p *MakefileParser) Filenames() []string {
	return []string{"Makefile", "makefile", "GNUmakefile"}
}

func (p *MakefileParser) ParseFile(filePath string, content []byte) (*parser.ParseResult, error) {
	e := &extractor{
		filePath: filePath,
		lines:    strings.Split(string(content), "\n"),
	}
	e.extract()

	return &parser.ParseResult{
		Nodes:    e.nodes,
		Edges:    e.edges,
		FilePath: filePath,
		Language: parser.LangMakefile,
	}, nil
}

// Regex patterns for Makefile elements.
var (
	// target: prerequisites
	targetRe = regexp.MustCompile(`^([a-zA-Z0-9_./%+-]+(?:\s+[a-zA-Z0-9_./%+-]+)*):\s*(.*)$`)
	// VAR = value, VAR := value, VAR ?= value, VAR += value
	variableRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*([?:+]?=)\s*(.*)$`)
	// include file.mk, -include file.mk
	includeRe = regexp.MustCompile(`^-?include\s+(.+)$`)
	// .PHONY: target1 target2
	phonyRe = regexp.MustCompile(`^\.PHONY:\s*(.+)$`)
	// ## comment (doc comment for the next target)
	docCommentRe = regexp.MustCompile(`^##\s*(.+)$`)
)

type extractor struct {
	filePath string
	lines    []string
	nodes    []*graph.Node
	edges    []*graph.Edge

	fileNodeID string
	phonySet   map[string]bool
	targetIDs  map[string]string // target name -> node ID
}

func (e *extractor) extract() {
	e.phonySet = make(map[string]bool)
	e.targetIDs = make(map[string]string)

	e.extractFileNode()

	// First pass: collect .PHONY declarations.
	for _, line := range e.lines {
		line = strings.TrimSpace(line)
		if m := phonyRe.FindStringSubmatch(line); m != nil {
			for _, name := range strings.Fields(m[1]) {
				e.phonySet[name] = true
			}
		}
	}

	// Second pass: extract targets, variables, includes.
	var pendingDoc string
	for i, line := range e.lines {
		lineNum := i + 1

		// Skip empty lines and reset pending doc.
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			pendingDoc = ""
			continue
		}

		// Doc comments (lines starting with ##).
		if m := docCommentRe.FindStringSubmatch(trimmed); m != nil {
			if pendingDoc != "" {
				pendingDoc += "\n" + m[1]
			} else {
				pendingDoc = m[1]
			}
			continue
		}

		// Regular comments.
		if strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Recipe lines (start with tab) are part of target body, skip.
		if strings.HasPrefix(line, "\t") {
			continue
		}

		// Include directives.
		if m := includeRe.FindStringSubmatch(trimmed); m != nil {
			e.addInclude(m[1], lineNum)
			pendingDoc = ""
			continue
		}

		// .PHONY declarations (already processed in first pass).
		if phonyRe.MatchString(trimmed) {
			pendingDoc = ""
			continue
		}

		// Variable assignments (must check before targets since VAR=val looks like target).
		if m := variableRe.FindStringSubmatch(trimmed); m != nil {
			e.addVariable(m[1], m[2], m[3], lineNum)
			pendingDoc = ""
			continue
		}

		// Target definitions.
		if m := targetRe.FindStringSubmatch(trimmed); m != nil {
			// Skip lines that look like they might be inside conditionals.
			targetNames := strings.Fields(m[1])
			prereqs := strings.TrimSpace(m[2])
			for _, name := range targetNames {
				// Skip special targets other than .PHONY.
				if strings.HasPrefix(name, ".") && name != ".DEFAULT" {
					continue
				}
				e.addTarget(name, prereqs, pendingDoc, lineNum)
			}
			pendingDoc = ""
			continue
		}

		// Anything else resets doc.
		pendingDoc = ""
	}

	// Post-process: add DependsOn edges for target prerequisites.
	e.linkPrerequisites()

	// Mark phony targets.
	for _, node := range e.nodes {
		if node.Properties != nil && node.Properties["kind"] == "target" {
			if e.phonySet[node.Name] {
				node.Properties["phony"] = "true"
			}
		}
	}
}

func (e *extractor) extractFileNode() {
	e.fileNodeID = graph.NewNodeID(string(graph.NodeFile), e.filePath, e.filePath)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       e.fileNodeID,
		Type:     graph.NodeFile,
		Name:     e.filePath,
		FilePath: e.filePath,
		Language: string(parser.LangMakefile),
	})
}

func (e *extractor) addTarget(name, prereqs, docComment string, line int) {
	targetID := graph.NewNodeID(string(graph.NodeFunction), e.filePath, name)
	e.targetIDs[name] = targetID

	props := map[string]string{
		"kind": "target",
	}
	if prereqs != "" {
		props["prerequisites"] = prereqs
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:         targetID,
		Type:       graph.NodeFunction,
		Name:       name,
		FilePath:   e.filePath,
		Line:       line,
		Language:   string(parser.LangMakefile),
		Exported:   true,
		DocComment: docComment,
		Properties: props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.fileNodeID, targetID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.fileNodeID,
		TargetID: targetID,
	})
}

func (e *extractor) addVariable(name, op, value string, line int) {
	varID := graph.NewNodeID(string(graph.NodeVariable), e.filePath, name)

	e.nodes = append(e.nodes, &graph.Node{
		ID:       varID,
		Type:     graph.NodeVariable,
		Name:     name,
		FilePath: e.filePath,
		Line:     line,
		Language: string(parser.LangMakefile),
		Exported: true,
		Properties: map[string]string{
			"kind":          "makefile_var",
			"assignment_op": op,
			"value":         value,
		},
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.fileNodeID, varID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.fileNodeID,
		TargetID: varID,
	})
}

func (e *extractor) addInclude(path string, line int) {
	// Include can have multiple files separated by spaces.
	for _, f := range strings.Fields(path) {
		depID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, "include:"+f)
		e.nodes = append(e.nodes, &graph.Node{
			ID:       depID,
			Type:     graph.NodeDependency,
			Name:     f,
			FilePath: e.filePath,
			Line:     line,
			Language: string(parser.LangMakefile),
			Properties: map[string]string{
				"kind": "include",
			},
		})

		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(e.fileNodeID, depID, string(graph.EdgeImports)),
			Type:     graph.EdgeImports,
			SourceID: e.fileNodeID,
			TargetID: depID,
		})
	}
}

func (e *extractor) linkPrerequisites() {
	for _, node := range e.nodes {
		if node.Properties == nil || node.Properties["kind"] != "target" {
			continue
		}
		prereqs := node.Properties["prerequisites"]
		if prereqs == "" {
			continue
		}
		sourceID := e.targetIDs[node.Name]
		for _, dep := range strings.Fields(prereqs) {
			if targetID, ok := e.targetIDs[dep]; ok {
				e.edges = append(e.edges, &graph.Edge{
					ID:       edgeID(sourceID, targetID, string(graph.EdgeDependsOn)),
					Type:     graph.EdgeDependsOn,
					SourceID: sourceID,
					TargetID: targetID,
				})
			}
		}
	}
}

func edgeID(sourceID, targetID, edgeType string) string {
	return graph.NewNodeID(edgeType, sourceID, targetID)
}
