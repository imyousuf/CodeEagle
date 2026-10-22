package cli

import "testing"

// TestVerbToolsAreSmallAndComplete covers the MCP surface: three tools,
// each with a required argument, and descriptions short enough that adding
// them costs an agent little on every turn.
func TestVerbToolsAreSmallAndComplete(t *testing.T) {
	tools := verbTools(newVerbs(nil))
	want := map[string]string{"find": "query", "brief": "target", "show": "id"}
	if len(tools) != len(want) {
		t.Fatalf("%d tools, want %d", len(tools), len(want))
	}
	total := 0
	for _, tool := range tools {
		req, ok := want[tool.Name()]
		if !ok {
			t.Errorf("unexpected tool %q", tool.Name())
			continue
		}
		params := tool.Parameters()
		required, _ := params["required"].([]string)
		if len(required) != 1 || required[0] != req {
			t.Errorf("%s requires %v, want [%s]", tool.Name(), required, req)
		}
		total += len(tool.Description())
	}
	if total > 600 {
		t.Errorf("tool descriptions total %d characters; every request pays for them", total)
	}
}

func TestVerbArgumentsAreReadLeniently(t *testing.T) {
	args := map[string]any{"query": "retry", "budget": float64(250), "from": 3}
	if str(args, "query") != "retry" || str(args, "missing") != "" {
		t.Error("string arguments")
	}
	if num(args, "budget") != 250 || num(args, "from") != 3 || num(args, "missing") != 0 {
		t.Error("numeric arguments, which JSON delivers as float64")
	}
}
