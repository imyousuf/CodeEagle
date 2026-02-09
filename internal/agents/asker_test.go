package agents

import (
	"context"
	"strings"
	"testing"
)

func TestAskerName(t *testing.T) {
	mock := &mockClient{response: "test"}
	asker := NewAsker(mock, nil)
	if asker.Name() != "asker" {
		t.Errorf("expected name 'asker', got %q", asker.Name())
	}
}

func TestAskerOverviewContext(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	mock := &mockClient{response: "overview answer"}
	ctxBuilder := NewContextBuilder(store)
	asker := NewAsker(mock, ctxBuilder)

	resp, err := asker.Ask(context.Background(), "tell me about this project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "overview answer" {
		t.Errorf("expected 'overview answer', got %q", resp)
	}
	if mock.lastSystemPrompt != askerSystemPrompt {
		t.Errorf("expected asker system prompt")
	}
	// Should have context messages that include overview.
	if len(mock.lastMessages) < 2 {
		t.Fatal("expected at least 2 messages with context")
	}
	if !strings.Contains(mock.lastMessages[0].Content, "Knowledge Graph Overview") {
		t.Errorf("expected overview context, got %q", mock.lastMessages[0].Content)
	}
}

func TestAskerModelContext(t *testing.T) {
	store := setupArchTestGraph(t)
	defer store.Close()

	mock := &mockClient{response: "model answer"}
	ctxBuilder := NewContextBuilder(store)
	asker := NewAsker(mock, ctxBuilder)

	resp, err := asker.Ask(context.Background(), "tell me about the database models")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "model answer" {
		t.Errorf("expected 'model answer', got %q", resp)
	}
	// Context should include both overview and model context.
	contextMsg := mock.lastMessages[0].Content
	if !strings.Contains(contextMsg, "Knowledge Graph Overview") {
		t.Error("expected overview context in combined context")
	}
	if !strings.Contains(contextMsg, "Data Models") {
		t.Error("expected model context in combined context")
	}
}

func TestAskerFileContext(t *testing.T) {
	store := setupTestGraph(t)
	defer store.Close()

	mock := &mockClient{response: "file answer"}
	ctxBuilder := NewContextBuilder(store)
	asker := NewAsker(mock, ctxBuilder)

	resp, err := asker.Ask(context.Background(), "what is in src/handler.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "file answer" {
		t.Errorf("expected 'file answer', got %q", resp)
	}
	// Context should include file context for handler.go.
	contextMsg := mock.lastMessages[0].Content
	if !strings.Contains(contextMsg, "File: src/handler.go") {
		t.Error("expected file context for src/handler.go")
	}
	if !strings.Contains(contextMsg, "HandleRequest") {
		t.Error("expected HandleRequest symbol in file context")
	}
}

func TestAskerMultiContext(t *testing.T) {
	store := setupArchTestGraph(t)
	defer store.Close()

	mock := &mockClient{response: "multi answer"}
	ctxBuilder := NewContextBuilder(store)
	asker := NewAsker(mock, ctxBuilder)

	// A query that should trigger both architecture and model context.
	resp, err := asker.Ask(context.Background(), "describe the architecture and database schema design")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "multi answer" {
		t.Errorf("expected 'multi answer', got %q", resp)
	}
	// Context should include overview, model, and architecture sections.
	contextMsg := mock.lastMessages[0].Content
	if !strings.Contains(contextMsg, "Knowledge Graph Overview") {
		t.Error("expected overview context")
	}
	if !strings.Contains(contextMsg, "Data Models") {
		t.Error("expected model context (triggered by 'schema' and 'database')")
	}
	if !strings.Contains(contextMsg, "Architecture Overview") {
		t.Error("expected architecture context (triggered by 'architecture' and 'design')")
	}
}

func TestAskerImpactContext(t *testing.T) {
	store := setupTestGraph(t)
	defer store.Close()

	mock := &mockClient{response: "impact answer"}
	ctxBuilder := NewContextBuilder(store)
	asker := NewAsker(mock, ctxBuilder)

	resp, err := asker.Ask(context.Background(), "what would be the impact of changing HandleRequest")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "impact answer" {
		t.Errorf("expected 'impact answer', got %q", resp)
	}
	// Context should include impact analysis for HandleRequest.
	contextMsg := mock.lastMessages[0].Content
	if !strings.Contains(contextMsg, "Impact Analysis: HandleRequest") {
		t.Error("expected impact context for HandleRequest")
	}
}

func TestAskerMetricsContext(t *testing.T) {
	store := setupTestGraph(t)
	defer store.Close()

	mock := &mockClient{response: "metrics answer"}
	ctxBuilder := NewContextBuilder(store)
	asker := NewAsker(mock, ctxBuilder)

	resp, err := asker.Ask(context.Background(), "show me the complexity metrics for src/handler.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "metrics answer" {
		t.Errorf("expected 'metrics answer', got %q", resp)
	}
	// Context should include metrics for handler.go.
	contextMsg := mock.lastMessages[0].Content
	if !strings.Contains(contextMsg, "Metrics: src/handler.go") {
		t.Error("expected metrics context for src/handler.go")
	}
	if !strings.Contains(contextMsg, "cyclomatic_complexity") {
		t.Error("expected cyclomatic_complexity metric")
	}
}

func TestExtractFilePath(t *testing.T) {
	tests := []struct {
		query    string
		expected string
	}{
		{"what is in src/handler.go", "src/handler.go"},
		{"tell me about main.go", "main.go"},
		{"how does app.py work", "app.py"},
		{"describe index.ts", "index.ts"},
		{"what are the issues", ""},
		{"explain the auth/handler.go file", "auth/handler.go"},
		{"show config.yaml", "config.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			result := extractFilePath(tt.query)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}
