package yaml

import (
	"fmt"
	"strings"

	yamlv3 "go.yaml.in/yaml/v3"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

// YAML dialect identifiers.
const (
	DialectGitHubActions = "github_actions"
	DialectAnsible       = "ansible"
	DialectGeneric       = "generic"
)

// YAMLParser extracts knowledge graph nodes and edges from YAML files.
type YAMLParser struct{}

// NewParser creates a new YAML parser.
func NewParser() *YAMLParser {
	return &YAMLParser{}
}

func (p *YAMLParser) Language() parser.Language {
	return parser.LangYAML
}

func (p *YAMLParser) Extensions() []string {
	return parser.FileExtensions[parser.LangYAML]
}

func (p *YAMLParser) ParseFile(filePath string, content []byte) (*parser.ParseResult, error) {
	var root yamlv3.Node
	if err := yamlv3.Unmarshal(content, &root); err != nil {
		return nil, fmt.Errorf("parsing YAML %s: %w", filePath, err)
	}

	e := &extractor{
		filePath: filePath,
		content:  content,
	}

	dialect := e.detectDialect(filePath, &root)
	e.dialect = dialect

	e.extractFileNode()

	switch dialect {
	case DialectGitHubActions:
		e.extractGitHubActions(&root)
	case DialectAnsible:
		e.extractAnsiblePlaybook(&root)
	default:
		e.extractGenericYAML(&root)
	}

	return &parser.ParseResult{
		Nodes:    e.nodes,
		Edges:    e.edges,
		FilePath: filePath,
		Language: parser.LangYAML,
	}, nil
}

type extractor struct {
	filePath   string
	content    []byte
	dialect    string
	nodes      []*graph.Node
	edges      []*graph.Edge
	fileNodeID string
}

func (e *extractor) extractFileNode() {
	e.fileNodeID = graph.NewNodeID(string(graph.NodeFile), e.filePath, e.filePath)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       e.fileNodeID,
		Type:     graph.NodeFile,
		Name:     e.filePath,
		FilePath: e.filePath,
		Language: string(parser.LangYAML),
		Properties: map[string]string{
			"yaml_dialect": e.dialect,
		},
	})
}

// detectDialect determines the YAML dialect from file path and content.
func (e *extractor) detectDialect(filePath string, root *yamlv3.Node) string {
	// Check path for GitHub Actions.
	if strings.Contains(filePath, ".github/workflows/") || strings.Contains(filePath, ".github\\workflows\\") {
		return DialectGitHubActions
	}

	// The root node for Unmarshal is a document node wrapping the actual content.
	if root == nil || len(root.Content) == 0 {
		return DialectGeneric
	}
	doc := root.Content[0]

	// Mapping-based detection for GitHub Actions.
	if doc.Kind == yamlv3.MappingNode {
		keys := mappingKeys(doc)
		if keys["on"] && keys["jobs"] {
			return DialectGitHubActions
		}
	}

	// Sequence-based detection for Ansible.
	if doc.Kind == yamlv3.SequenceNode && len(doc.Content) > 0 {
		firstItem := doc.Content[0]
		if firstItem.Kind == yamlv3.MappingNode {
			keys := mappingKeys(firstItem)
			if keys["hosts"] {
				return DialectAnsible
			}
			// Tasks file: has name + known Ansible module keys.
			if keys["name"] && hasAnsibleModuleKey(keys) {
				return DialectAnsible
			}
		}
	}

	return DialectGeneric
}

// mappingKeys returns a set of keys from a YAML mapping node.
func mappingKeys(node *yamlv3.Node) map[string]bool {
	keys := make(map[string]bool)
	if node.Kind != yamlv3.MappingNode {
		return keys
	}
	for i := 0; i < len(node.Content)-1; i += 2 {
		keys[node.Content[i].Value] = true
	}
	return keys
}

// knownAnsibleModules is a subset of common Ansible module names for detection.
var knownAnsibleModules = map[string]bool{
	"command": true, "shell": true, "copy": true, "file": true,
	"template": true, "service": true, "apt": true, "yum": true,
	"pip": true, "git": true, "debug": true, "set_fact": true,
	"include_tasks": true, "import_tasks": true, "include_role": true,
	"import_role": true, "block": true, "lineinfile": true,
	"ansible.builtin.command": true, "ansible.builtin.shell": true,
	"ansible.builtin.copy": true, "ansible.builtin.file": true,
	"ansible.builtin.template": true, "ansible.builtin.service": true,
	"ansible.builtin.debug": true, "ansible.builtin.set_fact": true,
}

func hasAnsibleModuleKey(keys map[string]bool) bool {
	for k := range keys {
		if knownAnsibleModules[k] {
			return true
		}
	}
	return false
}

// --- GitHub Actions extraction ---

func (e *extractor) extractGitHubActions(root *yamlv3.Node) {
	if root == nil || len(root.Content) == 0 {
		return
	}
	doc := root.Content[0]
	if doc.Kind != yamlv3.MappingNode {
		return
	}

	// Create workflow document node.
	workflowName := ""
	var triggerNode, jobsNode *yamlv3.Node

	for i := 0; i < len(doc.Content)-1; i += 2 {
		key := doc.Content[i].Value
		val := doc.Content[i+1]
		switch key {
		case "name":
			workflowName = val.Value
		case "on":
			triggerNode = val
		case "jobs":
			jobsNode = val
		}
	}

	if workflowName == "" {
		workflowName = e.filePath
	}

	docNodeID := graph.NewNodeID(string(graph.NodeDocument), e.filePath, "workflow:"+workflowName)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       docNodeID,
		Type:     graph.NodeDocument,
		Name:     workflowName,
		FilePath: e.filePath,
		Line:     1,
		Language: string(parser.LangYAML),
		Properties: map[string]string{
			"kind": "workflow",
		},
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.fileNodeID, docNodeID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.fileNodeID,
		TargetID: docNodeID,
	})

	// Extract triggers.
	if triggerNode != nil {
		e.extractGHATriggers(triggerNode, docNodeID)
	}

	// Extract jobs.
	if jobsNode != nil && jobsNode.Kind == yamlv3.MappingNode {
		e.extractGHAJobs(jobsNode, docNodeID)
	}
}

func (e *extractor) extractGHATriggers(node *yamlv3.Node, parentID string) {
	var events []string

	switch node.Kind {
	case yamlv3.MappingNode:
		for i := 0; i < len(node.Content)-1; i += 2 {
			events = append(events, node.Content[i].Value)
		}
	case yamlv3.SequenceNode:
		for _, item := range node.Content {
			if item.Kind == yamlv3.ScalarNode {
				events = append(events, item.Value)
			}
		}
	case yamlv3.ScalarNode:
		events = append(events, node.Value)
	}

	for _, event := range events {
		triggerID := graph.NewNodeID(string(graph.NodeVariable), e.filePath, "trigger:"+event)
		e.nodes = append(e.nodes, &graph.Node{
			ID:       triggerID,
			Type:     graph.NodeVariable,
			Name:     event,
			FilePath: e.filePath,
			Line:     node.Line,
			Language: string(parser.LangYAML),
			Exported: true,
			Properties: map[string]string{
				"kind": "gha_trigger",
			},
		})
		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(parentID, triggerID, string(graph.EdgeContains)),
			Type:     graph.EdgeContains,
			SourceID: parentID,
			TargetID: triggerID,
		})
	}
}

func (e *extractor) extractGHAJobs(jobsNode *yamlv3.Node, parentID string) {
	for i := 0; i < len(jobsNode.Content)-1; i += 2 {
		jobKey := jobsNode.Content[i].Value
		jobVal := jobsNode.Content[i+1]
		if jobVal.Kind != yamlv3.MappingNode {
			continue
		}

		runsOn := ""
		var stepsNode *yamlv3.Node

		for j := 0; j < len(jobVal.Content)-1; j += 2 {
			k := jobVal.Content[j].Value
			v := jobVal.Content[j+1]
			switch k {
			case "runs-on":
				runsOn = nodeScalarValue(v)
			case "steps":
				stepsNode = v
			}
		}

		props := map[string]string{
			"kind": "gha_job",
		}
		if runsOn != "" {
			props["runs_on"] = runsOn
		}

		jobID := graph.NewNodeID(string(graph.NodeFunction), e.filePath, "job:"+jobKey)
		e.nodes = append(e.nodes, &graph.Node{
			ID:         jobID,
			Type:       graph.NodeFunction,
			Name:       jobKey,
			FilePath:   e.filePath,
			Line:       jobsNode.Content[i].Line,
			Language:   string(parser.LangYAML),
			Exported:   true,
			Properties: props,
		})
		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(parentID, jobID, string(graph.EdgeContains)),
			Type:     graph.EdgeContains,
			SourceID: parentID,
			TargetID: jobID,
		})

		// Extract steps.
		if stepsNode != nil && stepsNode.Kind == yamlv3.SequenceNode {
			e.extractGHASteps(stepsNode, jobID)
		}
	}
}

func (e *extractor) extractGHASteps(stepsNode *yamlv3.Node, jobID string) {
	for _, step := range stepsNode.Content {
		if step.Kind != yamlv3.MappingNode {
			continue
		}

		stepName := ""
		usesAction := ""
		hasRun := false

		for j := 0; j < len(step.Content)-1; j += 2 {
			k := step.Content[j].Value
			v := step.Content[j+1]
			switch k {
			case "name":
				stepName = v.Value
			case "uses":
				usesAction = v.Value
			case "run":
				hasRun = true
			}
		}

		if usesAction != "" {
			// External action dependency.
			actionID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, "action:"+usesAction)
			e.nodes = append(e.nodes, &graph.Node{
				ID:       actionID,
				Type:     graph.NodeDependency,
				Name:     usesAction,
				FilePath: e.filePath,
				Line:     step.Line,
				Language: string(parser.LangYAML),
				Properties: map[string]string{
					"kind": "gha_action",
				},
			})
			e.edges = append(e.edges, &graph.Edge{
				ID:       edgeID(jobID, actionID, string(graph.EdgeDependsOn)),
				Type:     graph.EdgeDependsOn,
				SourceID: jobID,
				TargetID: actionID,
			})
		} else if hasRun {
			// Inline run step.
			name := stepName
			if name == "" {
				name = fmt.Sprintf("step:%d", step.Line)
			}
			stepID := graph.NewNodeID(string(graph.NodeFunction), e.filePath, "step:"+name+fmt.Sprintf(":%d", step.Line))
			e.nodes = append(e.nodes, &graph.Node{
				ID:       stepID,
				Type:     graph.NodeFunction,
				Name:     name,
				FilePath: e.filePath,
				Line:     step.Line,
				Language: string(parser.LangYAML),
				Properties: map[string]string{
					"kind": "gha_step",
				},
			})
			e.edges = append(e.edges, &graph.Edge{
				ID:       edgeID(jobID, stepID, string(graph.EdgeContains)),
				Type:     graph.EdgeContains,
				SourceID: jobID,
				TargetID: stepID,
			})
		}
	}
}

// --- Ansible extraction ---

func (e *extractor) extractAnsiblePlaybook(root *yamlv3.Node) {
	if root == nil || len(root.Content) == 0 {
		return
	}
	doc := root.Content[0]

	if doc.Kind != yamlv3.SequenceNode {
		return
	}

	for idx, item := range doc.Content {
		if item.Kind != yamlv3.MappingNode {
			continue
		}
		keys := mappingKeys(item)
		if keys["hosts"] {
			e.extractAnsiblePlay(item, idx)
		} else if keys["name"] && hasAnsibleModuleKey(keys) {
			// Tasks-only file.
			e.extractAnsibleTask(item, e.fileNodeID)
		}
	}
}

func (e *extractor) extractAnsiblePlay(play *yamlv3.Node, idx int) {
	playName := ""
	hosts := ""
	var tasksNode, handlersNode, rolesNode, varsNode *yamlv3.Node

	for i := 0; i < len(play.Content)-1; i += 2 {
		k := play.Content[i].Value
		v := play.Content[i+1]
		switch k {
		case "name":
			playName = v.Value
		case "hosts":
			hosts = nodeScalarValue(v)
		case "tasks":
			tasksNode = v
		case "handlers":
			handlersNode = v
		case "roles":
			rolesNode = v
		case "vars":
			varsNode = v
		}
	}

	if playName == "" {
		playName = fmt.Sprintf("play_%d", idx)
	}

	props := map[string]string{
		"kind": "ansible_play",
	}
	if hosts != "" {
		props["hosts"] = hosts
	}

	playID := graph.NewNodeID(string(graph.NodeFunction), e.filePath, "play:"+playName)
	e.nodes = append(e.nodes, &graph.Node{
		ID:         playID,
		Type:       graph.NodeFunction,
		Name:       playName,
		FilePath:   e.filePath,
		Line:       play.Line,
		Language:   string(parser.LangYAML),
		Exported:   true,
		Properties: props,
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(e.fileNodeID, playID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: e.fileNodeID,
		TargetID: playID,
	})

	// Extract tasks.
	if tasksNode != nil && tasksNode.Kind == yamlv3.SequenceNode {
		for _, task := range tasksNode.Content {
			e.extractAnsibleTask(task, playID)
		}
	}

	// Extract handlers.
	if handlersNode != nil && handlersNode.Kind == yamlv3.SequenceNode {
		for _, handler := range handlersNode.Content {
			e.extractAnsibleHandler(handler, playID)
		}
	}

	// Extract roles.
	if rolesNode != nil && rolesNode.Kind == yamlv3.SequenceNode {
		for _, role := range rolesNode.Content {
			e.extractAnsibleRole(role, playID)
		}
	}

	// Extract vars.
	if varsNode != nil && varsNode.Kind == yamlv3.MappingNode {
		e.extractAnsibleVars(varsNode, playID)
	}
}

func (e *extractor) extractAnsibleTask(task *yamlv3.Node, parentID string) {
	if task.Kind != yamlv3.MappingNode {
		return
	}

	taskName := ""
	moduleName := ""

	for i := 0; i < len(task.Content)-1; i += 2 {
		k := task.Content[i].Value
		v := task.Content[i+1]
		switch k {
		case "name":
			taskName = v.Value
		default:
			if knownAnsibleModules[k] && moduleName == "" {
				moduleName = k
			}
		}
	}

	if taskName == "" {
		taskName = fmt.Sprintf("task:%d", task.Line)
	}

	props := map[string]string{
		"kind": "ansible_task",
	}
	if moduleName != "" {
		props["module"] = moduleName
	}

	taskID := graph.NewNodeID(string(graph.NodeFunction), e.filePath, "task:"+taskName+fmt.Sprintf(":%d", task.Line))
	e.nodes = append(e.nodes, &graph.Node{
		ID:         taskID,
		Type:       graph.NodeFunction,
		Name:       taskName,
		FilePath:   e.filePath,
		Line:       task.Line,
		Language:   string(parser.LangYAML),
		Properties: props,
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, taskID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: taskID,
	})
}

func (e *extractor) extractAnsibleHandler(handler *yamlv3.Node, parentID string) {
	if handler.Kind != yamlv3.MappingNode {
		return
	}

	handlerName := ""
	moduleName := ""

	for i := 0; i < len(handler.Content)-1; i += 2 {
		k := handler.Content[i].Value
		v := handler.Content[i+1]
		switch k {
		case "name":
			handlerName = v.Value
		default:
			if knownAnsibleModules[k] && moduleName == "" {
				moduleName = k
			}
		}
	}

	if handlerName == "" {
		handlerName = fmt.Sprintf("handler:%d", handler.Line)
	}

	props := map[string]string{
		"kind": "ansible_handler",
	}
	if moduleName != "" {
		props["module"] = moduleName
	}

	handlerID := graph.NewNodeID(string(graph.NodeFunction), e.filePath, "handler:"+handlerName+fmt.Sprintf(":%d", handler.Line))
	e.nodes = append(e.nodes, &graph.Node{
		ID:         handlerID,
		Type:       graph.NodeFunction,
		Name:       handlerName,
		FilePath:   e.filePath,
		Line:       handler.Line,
		Language:   string(parser.LangYAML),
		Properties: props,
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, handlerID, string(graph.EdgeContains)),
		Type:     graph.EdgeContains,
		SourceID: parentID,
		TargetID: handlerID,
	})
}

func (e *extractor) extractAnsibleRole(role *yamlv3.Node, parentID string) {
	roleName := ""
	switch role.Kind {
	case yamlv3.ScalarNode:
		roleName = role.Value
	case yamlv3.MappingNode:
		for i := 0; i < len(role.Content)-1; i += 2 {
			if role.Content[i].Value == "role" {
				roleName = role.Content[i+1].Value
				break
			}
		}
	}
	if roleName == "" {
		return
	}

	roleID := graph.NewNodeID(string(graph.NodeDependency), e.filePath, "role:"+roleName)
	e.nodes = append(e.nodes, &graph.Node{
		ID:       roleID,
		Type:     graph.NodeDependency,
		Name:     roleName,
		FilePath: e.filePath,
		Line:     role.Line,
		Language: string(parser.LangYAML),
		Properties: map[string]string{
			"kind": "ansible_role",
		},
	})
	e.edges = append(e.edges, &graph.Edge{
		ID:       edgeID(parentID, roleID, string(graph.EdgeDependsOn)),
		Type:     graph.EdgeDependsOn,
		SourceID: parentID,
		TargetID: roleID,
	})
}

func (e *extractor) extractAnsibleVars(vars *yamlv3.Node, parentID string) {
	for i := 0; i < len(vars.Content)-1; i += 2 {
		varName := vars.Content[i].Value

		varID := graph.NewNodeID(string(graph.NodeVariable), e.filePath, "var:"+varName)
		e.nodes = append(e.nodes, &graph.Node{
			ID:       varID,
			Type:     graph.NodeVariable,
			Name:     varName,
			FilePath: e.filePath,
			Line:     vars.Content[i].Line,
			Language: string(parser.LangYAML),
			Properties: map[string]string{
				"kind": "ansible_var",
			},
		})
		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(parentID, varID, string(graph.EdgeContains)),
			Type:     graph.EdgeContains,
			SourceID: parentID,
			TargetID: varID,
		})
	}
}

// --- Generic YAML extraction ---

func (e *extractor) extractGenericYAML(root *yamlv3.Node) {
	if root == nil || len(root.Content) == 0 {
		return
	}
	doc := root.Content[0]

	if doc.Kind != yamlv3.MappingNode {
		return
	}

	for i := 0; i < len(doc.Content)-1; i += 2 {
		keyNode := doc.Content[i]
		keyName := keyNode.Value

		varID := graph.NewNodeID(string(graph.NodeVariable), e.filePath, "key:"+keyName)
		e.nodes = append(e.nodes, &graph.Node{
			ID:       varID,
			Type:     graph.NodeVariable,
			Name:     keyName,
			FilePath: e.filePath,
			Line:     keyNode.Line,
			Language: string(parser.LangYAML),
			Exported: true,
			Properties: map[string]string{
				"kind": "yaml_key",
			},
		})
		e.edges = append(e.edges, &graph.Edge{
			ID:       edgeID(e.fileNodeID, varID, string(graph.EdgeContains)),
			Type:     graph.EdgeContains,
			SourceID: e.fileNodeID,
			TargetID: varID,
		})
	}
}

// nodeScalarValue extracts the scalar value from a YAML node.
// For sequences, it joins values with comma. For mappings, it returns empty.
func nodeScalarValue(node *yamlv3.Node) string {
	switch node.Kind {
	case yamlv3.ScalarNode:
		return node.Value
	case yamlv3.SequenceNode:
		var vals []string
		for _, item := range node.Content {
			if item.Kind == yamlv3.ScalarNode {
				vals = append(vals, item.Value)
			}
		}
		return strings.Join(vals, ",")
	default:
		return ""
	}
}

func edgeID(sourceID, targetID, edgeType string) string {
	return graph.NewNodeID(edgeType, sourceID, targetID)
}
