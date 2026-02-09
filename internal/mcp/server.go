// Package mcp implements a JSON-RPC 2.0 over stdio MCP server
// that exposes CodeEagle tools to Claude CLI and other MCP clients.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/imyousuf/CodeEagle/internal/agents"
)

const (
	protocolVersion = "2024-11-05"
	serverName      = "codeeagle"
	serverVersion   = "1.0.0"
)

// jsonRPCRequest is a JSON-RPC 2.0 request or notification.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // nil for notifications
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonRPCResponse is a JSON-RPC 2.0 response.
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

// jsonRPCError is a JSON-RPC 2.0 error object.
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// initializeResult is the response to the initialize method.
type initializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	ServerInfo      serverInfo     `json:"serverInfo"`
	Capabilities    capabilitiesOb `json:"capabilities"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type capabilitiesOb struct {
	Tools map[string]any `json:"tools"`
}

// toolDefinition is a tool definition for tools/list response.
type toolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// toolCallResult is the result of a tools/call response.
type toolCallResult struct {
	Content []toolCallContent `json:"content"`
	IsError bool              `json:"isError"`
}

type toolCallContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// toolCallParams are the parameters for the tools/call method.
type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// Server handles JSON-RPC 2.0 requests over stdio for MCP.
type Server struct {
	registry *agents.Registry
	scanner  *bufio.Scanner
	writer   io.Writer
}

// NewServer creates an MCP server that reads from stdin and writes to stdout.
func NewServer(registry *agents.Registry) *Server {
	return NewServerWithIO(registry, os.Stdin, os.Stdout)
}

// NewServerWithIO creates an MCP server with custom I/O (for testing).
func NewServerWithIO(registry *agents.Registry, reader io.Reader, writer io.Writer) *Server {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	return &Server{
		registry: registry,
		scanner:  scanner,
		writer:   writer,
	}
}

// Run reads JSON-RPC requests from stdin line-by-line and dispatches them.
func (s *Server) Run(ctx context.Context) error {
	for s.scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		line := s.scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.writeError(nil, -32700, "Parse error: "+err.Error())
			continue
		}

		s.dispatch(ctx, &req)
	}

	if err := s.scanner.Err(); err != nil {
		return fmt.Errorf("scanner error: %w", err)
	}
	return nil
}

// dispatch routes a request to the appropriate handler.
func (s *Server) dispatch(ctx context.Context, req *jsonRPCRequest) {
	switch req.Method {
	case "initialize":
		s.handleInitialize(req)
	case "initialized":
		// No-op notification; nothing to respond.
	case "tools/list":
		s.handleToolsList(req)
	case "tools/call":
		s.handleToolsCall(ctx, req)
	default:
		if req.ID != nil {
			s.writeError(req.ID, -32601, "Method not found: "+req.Method)
		}
	}
}

// handleInitialize responds with server capabilities.
func (s *Server) handleInitialize(req *jsonRPCRequest) {
	s.writeResult(req.ID, initializeResult{
		ProtocolVersion: protocolVersion,
		ServerInfo: serverInfo{
			Name:    serverName,
			Version: serverVersion,
		},
		Capabilities: capabilitiesOb{
			Tools: map[string]any{},
		},
	})
}

// handleToolsList returns the list of available tools.
func (s *Server) handleToolsList(req *jsonRPCRequest) {
	defs := s.registry.Definitions()
	tools := make([]toolDefinition, len(defs))
	for i, d := range defs {
		tools[i] = toolDefinition{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: d.Parameters,
		}
	}
	s.writeResult(req.ID, map[string]any{"tools": tools})
}

// handleToolsCall executes the requested tool.
func (s *Server) handleToolsCall(ctx context.Context, req *jsonRPCRequest) {
	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.writeError(req.ID, -32602, "Invalid params: "+err.Error())
		return
	}

	result, success, err := s.registry.Execute(ctx, params.Name, params.Arguments)
	if err != nil {
		s.writeResult(req.ID, toolCallResult{
			Content: []toolCallContent{{Type: "text", Text: err.Error()}},
			IsError: true,
		})
		return
	}

	s.writeResult(req.ID, toolCallResult{
		Content: []toolCallContent{{Type: "text", Text: result}},
		IsError: !success,
	})
}

// writeResult sends a successful JSON-RPC response.
func (s *Server) writeResult(id json.RawMessage, result any) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	data, _ := json.Marshal(resp)
	fmt.Fprintf(s.writer, "%s\n", data)
}

// writeError sends a JSON-RPC error response.
func (s *Server) writeError(id json.RawMessage, code int, message string) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonRPCError{Code: code, Message: message},
	}
	data, _ := json.Marshal(resp)
	fmt.Fprintf(s.writer, "%s\n", data)
}
