package llm

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
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
	log        func(format string, args ...any) // verbose logger (writes to stderr)
	configFile string                           // forwarded to MCP serve subprocess
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
//
// A temp log file is always passed to the MCP subprocess via --log so that
// tool calls are unconditionally logged. When verbose is enabled, a goroutine
// tails this file in real time, forwarding lines to the verbose logger.
func (c *claudeCLIClient) ChatWithTools(ctx context.Context, systemPrompt string, messages []llm.Message, tools []llm.Tool) (*llm.Response, error) {
	prompt := buildPrompt(systemPrompt, messages)

	// Always create a log file for the MCP subprocess to write tool calls to.
	toolLogPath := newMCPLogPath()

	mcpConfig, cleanupFn, err := c.buildMCPConfig(toolLogPath)
	if err != nil {
		return nil, fmt.Errorf("build MCP config: %w", err)
	}
	defer cleanupFn()

	allowedTools := c.getAllowedTools(tools)

	args := []string{"-p", prompt}
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

	// Create the log file so the subprocess can write to it immediately.
	if f, err := os.Create(toolLogPath); err == nil {
		f.Close()
	}

	// Only tail the log file when verbose mode is active.
	var logDone chan struct{}
	if c.verbose && c.log != nil {
		logDone = make(chan struct{})
		go func() {
			defer close(logDone)
			c.tailFile(timeoutCtx, toolLogPath)
		}()
	}

	output, err := cmd.Output()

	// Wait for tailer to flush remaining lines, then clean up the log file.
	if logDone != nil {
		select {
		case <-logDone:
		case <-time.After(200 * time.Millisecond):
		}
	}
	os.Remove(toolLogPath)

	if err != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("claude CLI timed out after %v", c.timeout)
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("claude CLI exited with error: %w\nstderr: %s", err, exitErr.Stderr)
		}
		return nil, fmt.Errorf("claude CLI execution failed: %w", err)
	}

	// Claude CLI without --output-format json returns plain text.
	return &llm.Response{Content: strings.TrimSpace(string(output))}, nil
}

// tailFile reads lines from a file as they appear, forwarding to the logger.
// It polls because the file is written by a separate process (MCP subprocess).
func (c *claudeCLIClient) tailFile(ctx context.Context, path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	for {
		select {
		case <-ctx.Done():
			// Drain any remaining lines before exit.
			c.drainLines(reader)
			return
		default:
		}

		line, err := reader.ReadString('\n')
		if line != "" {
			c.log("%s", strings.TrimRight(line, "\n"))
		}
		if err != nil {
			if err == io.EOF {
				// No new data yet; poll.
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return
		}
	}
}

// drainLines reads and logs any remaining lines in the reader.
func (c *claudeCLIClient) drainLines(reader *bufio.Reader) {
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			c.log("%s", strings.TrimRight(line, "\n"))
		}
		if err != nil {
			return
		}
	}
}

// newMCPLogPath returns a unique log file path for the MCP subprocess.
func newMCPLogPath() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	id := fmt.Sprintf("%x", b)
	return filepath.Join(os.TempDir(), fmt.Sprintf("codeeagle-mcp-%s.log", id))
}

// buildMCPConfig creates a temporary MCP config file pointing to this binary's
// `mcp serve` command. Returns the config file path and a cleanup function.
// If toolLogPath is non-empty, adds --log to the MCP args so the subprocess
// writes tool call logs to a file that the parent process can tail.
func (c *claudeCLIClient) buildMCPConfig(toolLogPath string) (string, func(), error) {
	selfExe, err := os.Executable()
	if err != nil {
		return "", func() {}, fmt.Errorf("determine executable path: %w", err)
	}

	// Build args for the MCP server subprocess.
	mcpArgs := []string{"mcp", "serve"}
	if c.configFile != "" {
		mcpArgs = append(mcpArgs, "--config", c.configFile)
	}
	if toolLogPath != "" {
		mcpArgs = append(mcpArgs, "--log", toolLogPath)
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

// SetVerbose enables verbose logging. When true and used with ChatWithTools,
// the MCP subprocess writes tool call logs to a temp file which is tailed in
// real time to the provided logger.
func (c *claudeCLIClient) SetVerbose(verbose bool, logger func(format string, args ...any)) {
	c.verbose = verbose
	if logger != nil {
		c.log = logger
	} else {
		c.log = func(format string, args ...any) {}
	}
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
