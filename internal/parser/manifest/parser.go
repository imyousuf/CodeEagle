package manifest

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

// ManifestParser extracts knowledge graph nodes and edges from project manifest
// files (pyproject.toml, requirements.txt, package.json, go.mod).
type ManifestParser struct{}

// NewParser creates a new manifest file parser.
func NewParser() *ManifestParser {
	return &ManifestParser{}
}

func (p *ManifestParser) Language() parser.Language {
	return parser.LangManifest
}

func (p *ManifestParser) Extensions() []string {
	return parser.FileExtensions[parser.LangManifest]
}

func (p *ManifestParser) Filenames() []string {
	return []string{"pyproject.toml", "requirements.txt", "setup.py", "package.json", "go.mod"}
}

func (p *ManifestParser) ParseFile(filePath string, content []byte) (*parser.ParseResult, error) {
	base := filepath.Base(filePath)
	switch base {
	case "pyproject.toml":
		return parsePyprojectToml(filePath, content)
	case "requirements.txt":
		return parseRequirementsTxt(filePath, content)
	case "package.json":
		return parsePackageJson(filePath, content)
	case "go.mod":
		return parseGoMod(filePath, content)
	default:
		return &parser.ParseResult{FilePath: filePath, Language: parser.LangManifest}, nil
	}
}

// --- pyproject.toml ---

type pyprojectFile struct {
	Project struct {
		Name         string   `toml:"name"`
		Version      string   `toml:"version"`
		Dependencies []string `toml:"dependencies"`
	} `toml:"project"`
}

func parsePyprojectToml(filePath string, content []byte) (*parser.ParseResult, error) {
	var pf pyprojectFile
	if err := toml.Unmarshal(content, &pf); err != nil {
		return nil, err
	}

	e := &extractor{filePath: filePath, ecosystem: "python"}
	e.addFileNode()

	serviceName := pf.Project.Name
	if serviceName == "" {
		serviceName = filepath.Base(filepath.Dir(filePath))
	}
	e.addServiceNode(serviceName, pf.Project.Version)

	// Parse dependencies with line numbers.
	lines := strings.Split(string(content), "\n")
	for _, depStr := range pf.Project.Dependencies {
		name, version := parsePythonDep(depStr)
		line := findLine(lines, name)
		e.addDependencyNode(name, version, line)
	}

	return e.result(), nil
}

// parsePythonDep parses a PEP 508 dependency string like "fastapi>=0.100.0".
var pythonDepRe = regexp.MustCompile(`^([A-Za-z0-9_.-]+(?:\[[A-Za-z0-9_,.-]+\])?)(.*)$`)

func parsePythonDep(dep string) (name, version string) {
	dep = strings.TrimSpace(dep)
	m := pythonDepRe.FindStringSubmatch(dep)
	if m == nil {
		return dep, ""
	}
	return m[1], strings.TrimSpace(m[2])
}

// --- requirements.txt ---

func parseRequirementsTxt(filePath string, content []byte) (*parser.ParseResult, error) {
	e := &extractor{filePath: filePath, ecosystem: "python"}
	e.addFileNode()

	// requirements.txt doesn't define a service name; derive from directory.
	serviceName := filepath.Base(filepath.Dir(filePath))
	e.addServiceNode(serviceName, "")

	lines := strings.Split(string(content), "\n")
	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)

		// Skip empty lines and comments.
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// -r include directives.
		if strings.HasPrefix(trimmed, "-r ") {
			includePath := strings.TrimSpace(strings.TrimPrefix(trimmed, "-r "))
			e.addDependencyNode(includePath, "", lineNum)
			// Mark as include rather than manifest_dep.
			last := e.nodes[len(e.nodes)-1]
			last.Properties["kind"] = "include"
			continue
		}

		// Skip other pip flags (-i, --index-url, -e, etc.).
		if strings.HasPrefix(trimmed, "-") {
			continue
		}

		name, version := parsePythonDep(trimmed)
		e.addDependencyNode(name, version, lineNum)
	}

	return e.result(), nil
}

// --- package.json ---

type packageJsonFile struct {
	Name            string            `json:"name"`
	Version         string            `json:"version"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func parsePackageJson(filePath string, content []byte) (*parser.ParseResult, error) {
	var pj packageJsonFile
	if err := json.Unmarshal(content, &pj); err != nil {
		return nil, err
	}

	e := &extractor{filePath: filePath, ecosystem: "nodejs"}
	e.addFileNode()

	serviceName := pj.Name
	if serviceName == "" {
		serviceName = filepath.Base(filepath.Dir(filePath))
	}
	e.addServiceNode(serviceName, pj.Version)

	lines := strings.Split(string(content), "\n")

	for name, version := range pj.Dependencies {
		line := findLine(lines, name)
		e.addDependencyNode(name, version, line)
	}
	for name, version := range pj.DevDependencies {
		line := findLine(lines, name)
		dep := e.addDependencyNode(name, version, line)
		dep.Properties["scope"] = "dev"
	}

	return e.result(), nil
}

// --- go.mod ---

func parseGoMod(filePath string, content []byte) (*parser.ParseResult, error) {
	e := &extractor{filePath: filePath, ecosystem: "go"}
	e.addFileNode()

	lines := strings.Split(string(content), "\n")
	inRequireBlock := false
	isIndirect := false

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)

		// Module declaration.
		if strings.HasPrefix(trimmed, "module ") {
			moduleName := strings.TrimSpace(strings.TrimPrefix(trimmed, "module "))
			e.addServiceNode(moduleName, "")
			continue
		}

		// Require block start/end.
		if trimmed == "require (" {
			inRequireBlock = true
			// Check if the next lines are indirect by peeking (handled per-line).
			continue
		}
		if trimmed == ")" {
			inRequireBlock = false
			continue
		}

		// Single-line require.
		if strings.HasPrefix(trimmed, "require ") && !strings.Contains(trimmed, "(") {
			parts := strings.Fields(strings.TrimPrefix(trimmed, "require "))
			if len(parts) >= 2 {
				dep := e.addDependencyNode(parts[0], parts[1], lineNum)
				if strings.Contains(trimmed, "// indirect") {
					dep.Properties["scope"] = "indirect"
				}
			}
			continue
		}

		// Inside require block.
		if inRequireBlock {
			if trimmed == "" || strings.HasPrefix(trimmed, "//") {
				continue
			}
			isIndirect = strings.Contains(trimmed, "// indirect")
			// Remove trailing comment.
			entry := trimmed
			if idx := strings.Index(entry, "//"); idx >= 0 {
				entry = strings.TrimSpace(entry[:idx])
			}
			parts := strings.Fields(entry)
			if len(parts) >= 2 {
				dep := e.addDependencyNode(parts[0], parts[1], lineNum)
				if isIndirect {
					dep.Properties["scope"] = "indirect"
				}
			}
		}
	}

	return e.result(), nil
}

// --- shared helpers ---

type extractor struct {
	filePath  string
	ecosystem string

	nodes []*graph.Node
	edges []*graph.Edge

	fileNodeID    string
	serviceNodeID string
}

func (e *extractor) addFileNode() {
	e.fileNodeID = graph.NewNodeID(string(graph.NodeFile), e.filePath, e.filePath)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       e.fileNodeID,
		Type:     graph.NodeFile,
		Name:     e.filePath,
		FilePath: e.filePath,
		Language: string(parser.LangManifest),
	})
}

func (e *extractor) addServiceNode(name, version string) {
	e.serviceNodeID = graph.NewNodeID(string(graph.NodeService), e.filePath, name)
	props := map[string]string{
		"kind":      "service",
		"ecosystem": e.ecosystem,
	}
	if version != "" {
		props["version"] = version
	}

	e.nodes = append(e.nodes, &graph.Node{
		ID:         e.serviceNodeID,
		Type:       graph.NodeService,
		Name:       name,
		FilePath:   e.filePath,
		Line:       1,
		Language:   string(parser.LangManifest),
		Properties: props,
	})

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.fileNodeID, e.serviceNodeID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.fileNodeID,
		TargetID: e.serviceNodeID,
	})
}

func (e *extractor) addDependencyNode(name, version string, line int) *graph.Node {
	depID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, name)

	node := &graph.Node{
		ID:       depID,
		Type:     graph.NodeDependency,
		Name:     name,
		FilePath: e.filePath,
		Line:     line,
		Language: string(parser.LangManifest),
		Properties: map[string]string{
			"kind":      "manifest_dep",
			"version":   version,
			"ecosystem": e.ecosystem,
			"source":    filepath.Base(e.filePath),
		},
	}

	e.nodes = append(e.nodes, node)

	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.serviceNodeID, depID, string(graph.EdgeDependsOn)),
		Type:     graph.EdgeDependsOn,
		SourceID: e.serviceNodeID,
		TargetID: depID,
	})

	return node
}

func (e *extractor) result() *parser.ParseResult {
	return &parser.ParseResult{
		Nodes:    e.nodes,
		Edges:    e.edges,
		FilePath: e.filePath,
		Language: parser.LangManifest,
	}
}

func edgeID(sourceID, targetID, edgeType string) string {
	return graph.NewNodeID(edgeType, sourceID, targetID)
}

// findLine searches for the first line containing substr (1-indexed). Returns 0 if not found.
func findLine(lines []string, substr string) int {
	for i, line := range lines {
		if strings.Contains(line, substr) {
			return i + 1
		}
	}
	return 0
}
