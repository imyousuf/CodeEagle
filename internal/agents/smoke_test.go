//go:build llm_smoke

package agents

// Tests in this file require real LLM API credentials and are NOT run in CI.
// Run manually with: go test ./internal/agents/ -tags=llm_smoke -v

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	_ "github.com/imyousuf/CodeEagle/internal/llm"
	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// setupSmokeStore creates a temporary graph store with a small set of nodes
// for smoke testing agents with real LLM calls.
func setupSmokeStore(t *testing.T) graph.Store {
	t.Helper()

	store, err := embedded.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}

	ctx := context.Background()

	nodes := []*graph.Node{
		{
			ID:       "smoke-file1",
			Type:     graph.NodeFile,
			Name:     "main.go",
			FilePath: "cmd/main.go",
			Package:  "main",
			Language: "go",
		},
		{
			ID:            "smoke-func1",
			Type:          graph.NodeFunction,
			Name:          "HandleRequest",
			QualifiedName: "main.HandleRequest",
			FilePath:      "cmd/main.go",
			Package:       "main",
			Language:      "go",
			Exported:      true,
			Line:          10,
			EndLine:       25,
		},
		{
			ID:       "smoke-svc1",
			Type:     graph.NodeService,
			Name:     "APIService",
			Package:  "api",
			Language: "go",
		},
	}
	for _, n := range nodes {
		if err := store.AddNode(ctx, n); err != nil {
			t.Fatalf("failed to add node: %v", err)
		}
	}

	edge := &graph.Edge{
		ID:       "smoke-edge1",
		Type:     graph.EdgeContains,
		SourceID: "smoke-svc1",
		TargetID: "smoke-func1",
	}
	if err := store.AddEdge(ctx, edge); err != nil {
		t.Fatalf("failed to add edge: %v", err)
	}

	return store
}

func TestPlannerSmoke(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set, skipping Planner smoke test")
	}

	client, err := llm.NewClient(llm.Config{
		Provider: "anthropic",
		APIKey:   apiKey,
	})
	if err != nil {
		t.Fatalf("failed to create LLM client: %v", err)
	}
	defer client.Close()

	store := setupSmokeStore(t)
	defer store.Close()

	ctxBuilder := NewContextBuilder(store)
	planner := NewPlanner(client, ctxBuilder)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := planner.Ask(ctx, "What is the architecture of this project?")
	if err != nil {
		t.Fatalf("Planner.Ask failed: %v", err)
	}
	if resp == "" {
		t.Fatal("expected non-empty response from Planner")
	}
	t.Logf("Planner response (first 200 chars): %.200s", resp)
}

func TestReviewerSmoke(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set, skipping Reviewer smoke test")
	}

	client, err := llm.NewClient(llm.Config{
		Provider: "anthropic",
		APIKey:   apiKey,
	})
	if err != nil {
		t.Fatalf("failed to create LLM client: %v", err)
	}
	defer client.Close()

	store := setupSmokeStore(t)
	defer store.Close()

	ctxBuilder := NewContextBuilder(store)
	reviewer := NewReviewer(client, ctxBuilder)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := reviewer.Ask(ctx, "What are the code quality concerns in this project?")
	if err != nil {
		t.Fatalf("Reviewer.Ask failed: %v", err)
	}
	if resp == "" {
		t.Fatal("expected non-empty response from Reviewer")
	}
	t.Logf("Reviewer response (first 200 chars): %.200s", resp)
}
