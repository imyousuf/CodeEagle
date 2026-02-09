package llm

import (
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/llm"
)

func TestClaudeCLIProviderRegistration(t *testing.T) {
	if !llm.IsProviderRegistered("claude-cli") {
		t.Fatal("expected 'claude-cli' provider to be registered via init()")
	}
}

func TestNormalizeClaudeModel(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"sonnet", "sonnet"},
		{"opus", "opus"},
		{"haiku", "haiku"},
		{"claude-sonnet-4-5-20250929", "sonnet"},
		{"claude-opus-4-20250115", "opus"},
		{"claude-haiku-4-5-20251001", "haiku"},
		{"Claude-Sonnet-4-5-20250929", "sonnet"},
		{"OPUS", "opus"},
		{"claude-sonnet", "sonnet"},
		{"", ""},
		{"gpt-4", ""},
		{"unknown-model", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeClaudeModel(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeClaudeModel(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestBuildPrompt(t *testing.T) {
	tests := []struct {
		name         string
		systemPrompt string
		messages     []llm.Message
		expected     string
	}{
		{
			name:         "system prompt only",
			systemPrompt: "You are a helpful assistant.",
			messages:     nil,
			expected:     "[System]\nYou are a helpful assistant.",
		},
		{
			name:         "messages only",
			systemPrompt: "",
			messages: []llm.Message{
				{Role: llm.RoleUser, Content: "Hello"},
			},
			expected: "[User]\nHello",
		},
		{
			name:         "system and messages",
			systemPrompt: "Be concise.",
			messages: []llm.Message{
				{Role: llm.RoleUser, Content: "What is Go?"},
				{Role: llm.RoleAssistant, Content: "A programming language."},
				{Role: llm.RoleUser, Content: "Tell me more."},
			},
			expected: "[System]\nBe concise.\n\n[User]\nWhat is Go?\n\n[Assistant]\nA programming language.\n\n[User]\nTell me more.",
		},
		{
			name:         "empty",
			systemPrompt: "",
			messages:     nil,
			expected:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildPrompt(tt.systemPrompt, tt.messages)
			if result != tt.expected {
				t.Errorf("buildPrompt() =\n%q\nwant\n%q", result, tt.expected)
			}
		})
	}
}

func TestParseClaudeResponse(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantContent string
		wantInput   int
		wantOutput  int
		wantErr     bool
	}{
		{
			name:        "success with usage",
			input:       `{"type":"result","subtype":"success","result":"Hello world","usage":{"input_tokens":10,"output_tokens":5}}`,
			wantContent: "Hello world",
			wantInput:   10,
			wantOutput:  5,
		},
		{
			name:        "success without usage",
			input:       `{"type":"result","subtype":"success","result":"Hello"}`,
			wantContent: "Hello",
			wantInput:   0,
			wantOutput:  0,
		},
		{
			name:    "malformed JSON",
			input:   `not json at all`,
			wantErr: true,
		},
		{
			name:    "empty response",
			input:   `{}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := parseClaudeResponse([]byte(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.Content != tt.wantContent {
				t.Errorf("content = %q, want %q", resp.Content, tt.wantContent)
			}
			if resp.Usage.InputTokens != tt.wantInput {
				t.Errorf("input tokens = %d, want %d", resp.Usage.InputTokens, tt.wantInput)
			}
			if resp.Usage.OutputTokens != tt.wantOutput {
				t.Errorf("output tokens = %d, want %d", resp.Usage.OutputTokens, tt.wantOutput)
			}
		})
	}
}

func TestFindClaudeCLI(t *testing.T) {
	// FindClaudeCLI may or may not find the binary depending on the environment.
	// We just verify it returns a string and doesn't panic.
	path := FindClaudeCLI()
	t.Logf("FindClaudeCLI() returned %q", path)
}

func TestIsExecutable(t *testing.T) {
	// Non-existent path should return false.
	if isExecutable("/nonexistent/path/to/binary") {
		t.Error("expected false for non-existent path")
	}

	// A directory should return false.
	if isExecutable("/tmp") {
		t.Error("expected false for a directory")
	}
}

func TestClaudeCLIClientModel(t *testing.T) {
	client := &claudeCLIClient{
		executable: "/usr/bin/claude",
		model:      "sonnet",
	}
	if client.Model() != "claude-cli:sonnet" {
		t.Errorf("Model() = %q, want %q", client.Model(), "claude-cli:sonnet")
	}

	client2 := &claudeCLIClient{
		executable: "/usr/bin/claude",
		model:      "",
	}
	if client2.Model() != "claude-cli" {
		t.Errorf("Model() = %q, want %q", client2.Model(), "claude-cli")
	}
}

func TestClaudeCLIClientProvider(t *testing.T) {
	client := &claudeCLIClient{executable: "/usr/bin/claude"}
	if client.Provider() != "claude-cli" {
		t.Errorf("Provider() = %q, want %q", client.Provider(), "claude-cli")
	}
}

func TestClaudeCLIClientClose(t *testing.T) {
	client := &claudeCLIClient{executable: "/usr/bin/claude"}
	if err := client.Close(); err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}
