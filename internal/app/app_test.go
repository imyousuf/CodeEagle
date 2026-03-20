//go:build app

package app

import (
	"fmt"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/pkg/llm"
)

func TestNewApp_NoState(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "test"},
	}
	a := NewApp(cfg, nil, "", nil, nil)
	if a == nil {
		t.Fatal("NewApp returned nil")
	}
	if a.cfg != cfg {
		t.Error("cfg not set")
	}
}

func TestWithGraph_NoConfigDir(t *testing.T) {
	cfg := &config.Config{}
	a := NewApp(cfg, nil, "", nil, nil)

	err := a.withGraph(func(gr *graphResources) error {
		return nil
	})
	if err == nil {
		t.Fatal("withGraph should fail with no config dir")
	}
}

func TestWithGraph_CreatesDBIfMissing(t *testing.T) {
	cfg := &config.Config{
		ConfigDir: t.TempDir(),
		Graph:     config.GraphConfig{Storage: "embedded"},
	}
	a := NewApp(cfg, nil, "", nil, nil)

	var called bool
	err := a.withGraph(func(gr *graphResources) error {
		called = true
		if gr == nil {
			t.Error("graphResources should not be nil")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("withGraph should succeed (creates DB): %v", err)
	}
	if !called {
		t.Error("callback should have been called")
	}
}

func TestWithGraph_IndependentCalls(t *testing.T) {
	cfg := &config.Config{
		ConfigDir: t.TempDir(),
	}
	a := NewApp(cfg, nil, "", nil, nil)

	// Two calls should both succeed independently (per-request open).
	callCount := 0
	for i := 0; i < 2; i++ {
		if err := a.withGraph(func(gr *graphResources) error {
			callCount++
			if gr == nil {
				t.Error("graphResources should not be nil")
			}
			return nil
		}); err != nil {
			t.Fatalf("withGraph call %d: %v", i+1, err)
		}
	}
	if callCount != 2 {
		t.Errorf("callback called %d times, want 2", callCount)
	}
}

func TestWithGraph_PropagatesError(t *testing.T) {
	cfg := &config.Config{
		ConfigDir: t.TempDir(),
	}
	a := NewApp(cfg, nil, "", nil, nil)

	want := fmt.Errorf("test error")
	err := a.withGraph(func(gr *graphResources) error {
		return want
	})
	if err != want {
		t.Errorf("withGraph error = %v, want %v", err, want)
	}
}

func TestOpenLLM_NoFactory(t *testing.T) {
	cfg := &config.Config{}
	a := NewApp(cfg, nil, "", nil, nil)

	client, closer, err := a.openLLM()
	if err == nil {
		t.Fatal("openLLM should fail with no factory")
	}
	if client != nil {
		t.Error("client should be nil on error")
	}
	if closer != nil {
		t.Error("closer should be nil on error")
	}
}

func TestOpenLLM_FactoryError(t *testing.T) {
	cfg := &config.Config{}
	factory := func(c *config.Config) (llm.Client, error) {
		return nil, fmt.Errorf("unknown provider")
	}
	a := NewApp(cfg, nil, "", factory, nil)

	client, closer, err := a.openLLM()
	if err == nil {
		t.Fatal("openLLM should propagate factory error")
	}
	if client != nil {
		t.Error("client should be nil on error")
	}
	if closer != nil {
		t.Error("closer should be nil on error")
	}
}

func TestOpenLLM_FactoryCalledEachTime(t *testing.T) {
	cfg := &config.Config{}
	callCount := 0
	factory := func(c *config.Config) (llm.Client, error) {
		callCount++
		return nil, fmt.Errorf("unknown provider")
	}
	a := NewApp(cfg, nil, "", factory, nil)

	_, _, _ = a.openLLM()
	_, _, _ = a.openLLM()
	if callCount != 2 {
		t.Errorf("factory called %d times, want 2 (per-request)", callCount)
	}
}

func TestOpenVector_NilBranchStore(t *testing.T) {
	cfg := &config.Config{}
	a := NewApp(cfg, nil, "", nil, nil)

	vs, closer := a.openVector(nil, "main")
	if vs != nil {
		t.Error("vectorStore should be nil without branchStore")
	}
	// closer should be a noop, not nil.
	closer()
}

func TestGetStatus_NoResources(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "test-project"},
	}
	a := NewApp(cfg, nil, "main", nil, nil)

	status := a.GetStatus()
	if status.ProjectName != "test-project" {
		t.Errorf("ProjectName = %q, want %q", status.ProjectName, "test-project")
	}
	if status.GraphReady {
		t.Error("GraphReady should be false")
	}
	if status.VectorReady {
		t.Error("VectorReady should be false")
	}
	if status.LLMReady {
		t.Error("LLMReady should be false")
	}
}

func TestShutdown_NoResources(t *testing.T) {
	cfg := &config.Config{}
	a := NewApp(cfg, nil, "", nil, nil)

	// Should not panic.
	a.Shutdown(nil)
}
