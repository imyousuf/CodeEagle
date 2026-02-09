package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/imyousuf/CodeEagle/pkg/llm"
)

const (
	defaultCLITimeout = 5 * time.Minute
)

func init() {
	llm.RegisterProvider("claude-cli", newClaudeCLIClient)
}

// claudeCLIClient implements llm.Client by invoking the Claude CLI binary.
type claudeCLIClient struct {
	executable string
	model      string // normalized: "opus", "sonnet", "haiku", or "" (CLI default)
	timeout    time.Duration
	verbose    bool
	configFile string // forwarded to MCP serve subprocess
}

// newClaudeCLIClient creates a new Claude CLI client.
func newClaudeCLIClient(cfg llm.Config) (llm.Client, error) {
	exe := FindClaudeCLI()
	if exe == "" {
		return nil, fmt.Errorf("claude CLI not found; install Claude Code or set a different LLM provider")
	}

	model := normalizeClaudeModel(cfg.Model)

	return &claudeCLIClient{
		executable: exe,
		model:      model,
		timeout:    defaultCLITimeout,
	}, nil
}

// claudeJSONResponse is the JSON output from `claude -p ... --output-format json`.
type claudeJSONResponse struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Result  string `json:"result"`
	Usage   *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage,omitempty"`
}

// Chat sends a prompt to the Claude CLI and returns the response.
func (c *claudeCLIClient) Chat(ctx context.Context, systemPrompt string, messages []llm.Message) (*llm.Response, error) {
	prompt := buildPrompt(systemPrompt, messages)

	args := []string{"-p", prompt, "--output-format", "json"}
	if c.model != "" {
		args = append(args, "--model", c.model)
	}
	if c.verbose {
		args = append(args, "--verbose")
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, c.executable, args...)

	output, err := cmd.Output()
	if err != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("claude CLI timed out after %v", c.timeout)
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("claude CLI exited with error: %w\nstderr: %s", err, exitErr.Stderr)
		}
		return nil, fmt.Errorf("claude CLI execution failed: %w", err)
	}

	resp, err := parseClaudeResponse(output)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// Model returns the model identifier.
func (c *claudeCLIClient) Model() string {
	if c.model == "" {
		return "claude-cli"
	}
	return "claude-cli:" + c.model
}

// Provider returns the provider name.
func (c *claudeCLIClient) Provider() string {
	return "claude-cli"
}

// ChatWithTools invokes the Claude CLI with MCP configuration so that
// Claude can autonomously call tools via an MCP server subprocess.
// The full agentic loop happens inside the CLI; the returned response
// contains only the final text (no pending tool calls).
func (c *claudeCLIClient) ChatWithTools(ctx context.Context, systemPrompt string, messages []llm.Message, tools []llm.Tool) (*llm.Response, error) {
	prompt := buildPrompt(systemPrompt, messages)

	mcpConfig, cleanupFn, err := c.buildMCPConfig()
	if err != nil {
		return nil, fmt.Errorf("build MCP config: %w", err)
	}
	defer cleanupFn()

	allowedTools := c.getAllowedTools(tools)

	args := []string{"-p", prompt, "--output-format", "json"}
	if c.model != "" {
		args = append(args, "--model", c.model)
	}
	args = append(args, "--mcp-config", mcpConfig)
	for _, tool := range allowedTools {
		args = append(args, "--allowedTools", tool)
	}
	args = append(args, "--dangerously-skip-permissions")

	if c.verbose {
		args = append(args, "--verbose")
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, c.executable, args...)
	output, err := cmd.Output()
	if err != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("claude CLI timed out after %v", c.timeout)
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("claude CLI exited with error: %w\nstderr: %s", err, exitErr.Stderr)
		}
		return nil, fmt.Errorf("claude CLI execution failed: %w", err)
	}

	return parseClaudeResponse(output)
}

// buildMCPConfig creates a temporary MCP config file pointing to this binary's
// `mcp serve` command. Returns the config file path and a cleanup function.
func (c *claudeCLIClient) buildMCPConfig() (string, func(), error) {
	selfExe, err := os.Executable()
	if err != nil {
		return "", func() {}, fmt.Errorf("determine executable path: %w", err)
	}

	// Build args for the MCP server subprocess.
	mcpArgs := []string{"mcp", "serve"}
	if c.configFile != "" {
		mcpArgs = append(mcpArgs, "--config", c.configFile)
	}

	mcpConfig := map[string]any{
		"mcpServers": map[string]any{
			"codeeagle": map[string]any{
				"command": selfExe,
				"args":    mcpArgs,
			},
		},
	}

	configJSON, err := json.Marshal(mcpConfig)
	if err != nil {
		return "", func() {}, fmt.Errorf("marshal MCP config: %w", err)
	}

	tmpFile, err := os.CreateTemp("", "codeeagle-mcp-*.json")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temp MCP config: %w", err)
	}

	if _, err := tmpFile.Write(configJSON); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", func() {}, fmt.Errorf("write MCP config: %w", err)
	}
	tmpFile.Close()

	cleanup := func() {
		os.Remove(tmpFile.Name())
	}
	return tmpFile.Name(), cleanup, nil
}

// getAllowedTools maps tool names to MCP-prefixed names for the Claude CLI.
func (c *claudeCLIClient) getAllowedTools(tools []llm.Tool) []string {
	result := make([]string, len(tools))
	for i, t := range tools {
		result[i] = "mcp__codeeagle__" + t.Name
	}
	return result
}

// SetConfigFile sets the config file path to be forwarded to MCP serve subprocess.
func (c *claudeCLIClient) SetConfigFile(path string) {
	c.configFile = path
}

// Close is a no-op for the CLI client.
func (c *claudeCLIClient) Close() error {
	return nil
}

// FindClaudeCLI searches for the Claude CLI binary in common installation paths.
func FindClaudeCLI() string {
	home, _ := os.UserHomeDir()
	if home != "" {
		candidates := []string{
			filepath.Join(home, ".claude", "local", "claude"),
			filepath.Join(home, ".local", "bin", "claude"),
		}
		for _, path := range candidates {
			if isExecutable(path) {
				return path
			}
		}
	}

	systemPaths := []string{
		"/usr/local/bin/claude",
		"/opt/homebrew/bin/claude",
	}
	for _, path := range systemPaths {
		if isExecutable(path) {
			return path
		}
	}

	if path, err := exec.LookPath("claude"); err == nil {
		return path
	}

	return ""
}

// normalizeClaudeModel converts model identifiers to the short names the CLI accepts.
func normalizeClaudeModel(model string) string {
	lower := strings.ToLower(model)

	switch {
	case lower == "sonnet" || strings.HasPrefix(lower, "claude-sonnet"):
		return "sonnet"
	case lower == "opus" || strings.HasPrefix(lower, "claude-opus"):
		return "opus"
	case lower == "haiku" || strings.HasPrefix(lower, "claude-haiku"):
		return "haiku"
	default:
		return ""
	}
}

// buildPrompt formats a system prompt and message history into a single prompt string.
func buildPrompt(systemPrompt string, messages []llm.Message) string {
	var sb strings.Builder

	if systemPrompt != "" {
		sb.WriteString("[System]\n")
		sb.WriteString(systemPrompt)
		sb.WriteString("\n\n")
	}

	for _, msg := range messages {
		switch msg.Role {
		case llm.RoleUser:
			sb.WriteString("[User]\n")
		case llm.RoleAssistant:
			sb.WriteString("[Assistant]\n")
		default:
			fmt.Fprintf(&sb, "[%s]\n", string(msg.Role))
		}
		sb.WriteString(msg.Content)
		sb.WriteString("\n\n")
	}

	return strings.TrimSpace(sb.String())
}

// parseClaudeResponse parses the JSON output from the Claude CLI.
func parseClaudeResponse(data []byte) (*llm.Response, error) {
	var resp claudeJSONResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse claude CLI JSON response: %w\nraw output: %s", err, string(data))
	}

	if resp.Result == "" && resp.Type == "" {
		return nil, fmt.Errorf("empty response from claude CLI")
	}

	response := &llm.Response{
		Content: resp.Result,
	}

	if resp.Usage != nil {
		response.Usage = llm.TokenUsage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
		}
	}

	return response, nil
}

// isExecutable checks if a file exists and has execute permission.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir() && info.Mode()&0111 != 0
}
