package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// basetenServer stands in for the API, recording what it was sent.
type basetenServer struct {
	*httptest.Server
	requests atomic.Int32
	// last holds the most recent decoded request body.
	last basetenRequest
}

// newBasetenServer starts a server whose handler is called with each request.
func newBasetenServer(t *testing.T, handler func(req basetenRequest, w http.ResponseWriter, n int)) *basetenServer {
	t.Helper()
	bs := &basetenServer{}
	bs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req basetenRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("malformed request body: %v", err)
		}
		bs.last = req
		n := int(bs.requests.Add(1))
		if got := r.Header.Get("Authorization"); got != "Api-Key test-key" {
			t.Errorf("Authorization = %q, want %q", got, "Api-Key test-key")
		}
		w.Header().Set("Content-Type", "application/json")
		handler(req, w, n)
	}))
	t.Cleanup(bs.Close)
	return bs
}

// reply writes a well-formed completion.
func reply(w http.ResponseWriter, content, finishReason string, reasoning string) {
	resp := map[string]any{
		"choices": []map[string]any{{
			"message":       map[string]any{"content": content, "reasoning_content": reasoning},
			"finish_reason": finishReason,
		}},
		"usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 50},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func newTestClient(t *testing.T, url string) llm.Client {
	t.Helper()
	c, err := llm.NewClient(llm.Config{
		Provider: "baseten",
		Model:    "test-model",
		APIKey:   "test-key",
		BaseURL:  url,
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	return c
}

func TestBasetenRequiresAPIKey(t *testing.T) {
	if _, err := llm.NewClient(llm.Config{Provider: "baseten", Model: "m"}); err == nil {
		t.Error("expected an error when no API key is supplied")
	}
}

func TestBasetenDefaults(t *testing.T) {
	c, err := newBasetenClient(llm.Config{APIKey: "k"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if c.Model() != DefaultBasetenModel {
		t.Errorf("model = %q, want the default %q", c.Model(), DefaultBasetenModel)
	}
	if c.Provider() != "baseten" {
		t.Errorf("provider = %q, want baseten", c.Provider())
	}
}

func TestBasetenChat(t *testing.T) {
	srv := newBasetenServer(t, func(_ basetenRequest, w http.ResponseWriter, _ int) {
		reply(w, "hello there", "stop", "")
	})
	c := newTestClient(t, srv.URL)

	resp, err := c.Chat(context.Background(), "be brief", []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if resp.Content != "hello there" {
		t.Errorf("content = %q", resp.Content)
	}
	if resp.Usage.InputTokens != 100 || resp.Usage.OutputTokens != 50 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	// The system prompt must lead the message list.
	if len(srv.last.Messages) != 2 || srv.last.Messages[0].Role != "system" {
		t.Errorf("messages = %+v", srv.last.Messages)
	}
}

func TestBasetenChatJSONSendsSchema(t *testing.T) {
	srv := newBasetenServer(t, func(_ basetenRequest, w http.ResponseWriter, _ int) {
		reply(w, `{"speakers":[]}`, "stop", "")
	})
	c := newTestClient(t, srv.URL).(llm.StructuredClient)

	schema := &llm.JSONSchema{
		Name:   "speaker_identities",
		Strict: true,
		Schema: map[string]any{"type": "object"},
	}
	if _, err := c.ChatJSON(context.Background(), "sys", []llm.Message{{Role: llm.RoleUser, Content: "x"}}, schema); err != nil {
		t.Fatalf("chat json: %v", err)
	}

	rf := srv.last.ResponseFormat
	if rf == nil || rf.Type != "json_schema" {
		t.Fatalf("response_format = %+v, want json_schema", rf)
	}
	if rf.JSONSchema == nil || rf.JSONSchema.Name != "speaker_identities" || !rf.JSONSchema.Strict {
		t.Errorf("json_schema = %+v", rf.JSONSchema)
	}
}

func TestBasetenSupportsStructured(t *testing.T) {
	c, _ := newBasetenClient(llm.Config{APIKey: "k"})
	if !llm.SupportsStructured(c) {
		t.Error("baseten client should implement StructuredClient")
	}
	if !llm.SupportsTools(c) {
		t.Error("baseten client should implement ToolCapableClient")
	}
}

func TestBasetenReasoningEffortIsSent(t *testing.T) {
	srv := newBasetenServer(t, func(_ basetenRequest, w http.ResponseWriter, _ int) {
		reply(w, "ok", "stop", "")
	})
	c, err := llm.NewClient(llm.Config{
		Provider: "baseten", Model: "m", APIKey: "test-key",
		BaseURL: srv.URL, ReasoningEffort: "low",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := c.Chat(context.Background(), "", []llm.Message{{Role: llm.RoleUser, Content: "x"}}); err != nil {
		t.Fatalf("chat: %v", err)
	}
	if srv.last.ReasoningEffort != "low" {
		t.Errorf("reasoning_effort = %q, want low", srv.last.ReasoningEffort)
	}
}

func TestBasetenFallsBackWhenReasoningEatsTheBudget(t *testing.T) {
	// The model deliberates until nothing is left for the answer. Retrying the
	// identical request would fail identically, so reasoning is switched off.
	srv := newBasetenServer(t, func(req basetenRequest, w http.ResponseWriter, n int) {
		if n == 1 {
			reply(w, "", "length", strings.Repeat("thinking ", 4000))
			return
		}
		reply(w, `{"ok":true}`, "stop", "")
	})
	c := newTestClient(t, srv.URL).(llm.StructuredClient)

	resp, err := c.ChatJSON(context.Background(), "sys", []llm.Message{{Role: llm.RoleUser, Content: "x"}}, nil)
	if err != nil {
		t.Fatalf("chat json: %v", err)
	}
	if resp.Content != `{"ok":true}` {
		t.Errorf("content = %q", resp.Content)
	}
	if srv.requests.Load() != 2 {
		t.Errorf("requests = %d, want 2 (one failure, one fallback)", srv.requests.Load())
	}
	if v, ok := srv.last.ChatTemplateKwargs["thinking"]; !ok || v != false {
		t.Errorf("fallback did not disable reasoning: %+v", srv.last.ChatTemplateKwargs)
	}
}

func TestBasetenFallsBackOnTruncatedJSON(t *testing.T) {
	// A half-written JSON document will not parse, so truncation must be
	// treated as a failure rather than returned as a partial answer.
	srv := newBasetenServer(t, func(req basetenRequest, w http.ResponseWriter, n int) {
		if n == 1 {
			reply(w, `{"topics":[{"name":"half a doc`, "length", "")
			return
		}
		reply(w, `{"topics":[]}`, "stop", "")
	})
	c := newTestClient(t, srv.URL).(llm.StructuredClient)

	resp, err := c.ChatJSON(context.Background(), "sys", []llm.Message{{Role: llm.RoleUser, Content: "x"}}, nil)
	if err != nil {
		t.Fatalf("chat json: %v", err)
	}
	if resp.Content != `{"topics":[]}` {
		t.Errorf("content = %q, want the complete retry", resp.Content)
	}
}

func TestBasetenKeepsTruncatedProse(t *testing.T) {
	// Plain chat has no parsing requirement, so a cut-off answer is still
	// worth returning rather than retrying.
	srv := newBasetenServer(t, func(_ basetenRequest, w http.ResponseWriter, _ int) {
		reply(w, "a long answer that was cut", "length", "")
	})
	c := newTestClient(t, srv.URL)

	resp, err := c.Chat(context.Background(), "", []llm.Message{{Role: llm.RoleUser, Content: "x"}})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if resp.Content != "a long answer that was cut" {
		t.Errorf("content = %q", resp.Content)
	}
	if srv.requests.Load() != 1 {
		t.Errorf("requests = %d, want 1 (no retry for prose)", srv.requests.Load())
	}
}

func TestBasetenRetriesRateLimit(t *testing.T) {
	srv := newBasetenServer(t, func(_ basetenRequest, w http.ResponseWriter, n int) {
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
			return
		}
		reply(w, "recovered", "stop", "")
	})
	c := newTestClient(t, srv.URL)

	resp, err := c.Chat(context.Background(), "", []llm.Message{{Role: llm.RoleUser, Content: "x"}})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if resp.Content != "recovered" {
		t.Errorf("content = %q", resp.Content)
	}
}

func TestBasetenDoesNotRetryClientErrors(t *testing.T) {
	// A bad request will fail the same way every time; retrying wastes a
	// batch's time and money.
	srv := newBasetenServer(t, func(_ basetenRequest, w http.ResponseWriter, _ int) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"unknown model"}}`))
	})
	c := newTestClient(t, srv.URL)

	_, err := c.Chat(context.Background(), "", []llm.Message{{Role: llm.RoleUser, Content: "x"}})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "unknown model") {
		t.Errorf("error should carry the API message, got: %v", err)
	}
	if srv.requests.Load() != 1 {
		t.Errorf("requests = %d, want 1", srv.requests.Load())
	}
}

func TestBasetenToolCalls(t *testing.T) {
	srv := newBasetenServer(t, func(_ basetenRequest, w http.ResponseWriter, _ int) {
		resp := map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": "calling",
					"tool_calls": []map[string]any{{
						"id":       "call_1",
						"type":     "function",
						"function": map[string]any{"name": "search", "arguments": `{"q":"auth"}`},
					}},
				},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	c := newTestClient(t, srv.URL).(llm.ToolCapableClient)

	resp, err := c.ChatWithTools(context.Background(), "", []llm.Message{{Role: llm.RoleUser, Content: "x"}},
		[]llm.Tool{{Name: "search", Description: "find things", Parameters: map[string]any{"type": "object"}}})
	if err != nil {
		t.Fatalf("chat with tools: %v", err)
	}
	if !resp.HasToolCalls() {
		t.Fatal("expected a tool call")
	}
	tc := resp.ToolCalls[0]
	if tc.Name != "search" || tc.Arguments["q"] != "auth" {
		t.Errorf("tool call = %+v", tc)
	}
	if len(srv.last.Tools) != 1 || srv.last.Tools[0].Function.Name != "search" {
		t.Errorf("tools sent = %+v", srv.last.Tools)
	}
}

func TestBasetenContextCancellation(t *testing.T) {
	srv := newBasetenServer(t, func(_ basetenRequest, w http.ResponseWriter, _ int) {
		reply(w, "", "length", "reasoning")
	})
	c := newTestClient(t, srv.URL).(llm.StructuredClient)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ChatJSON(ctx, "", []llm.Message{{Role: llm.RoleUser, Content: "x"}}, nil); err == nil {
		t.Error("expected an error from a cancelled context")
	}
}
