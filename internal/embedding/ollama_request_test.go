package embedding_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/embedding"
)

// These tests pin the Ollama provider's wire behaviour through the public
// registry only, so they survive the provider moving packages: the exact
// request each call sends, and how each kind of reply is turned into a
// result or an error.

// capturedRequest is one request as the fake server saw it.
type capturedRequest struct {
	Method      string
	Path        string
	ContentType string
	Body        string // canonical JSON: keys sorted, no whitespace
}

// fakeOllama records every request and answers with a fixed status and body.
type fakeOllama struct {
	srv *httptest.Server

	mu       sync.Mutex
	requests []capturedRequest

	status int
	reply  string
}

func newFakeOllama(t *testing.T, status int, reply string) *fakeOllama {
	t.Helper()
	f := &fakeOllama{status: status, reply: reply}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		f.mu.Lock()
		f.requests = append(f.requests, capturedRequest{
			Method:      r.Method,
			Path:        r.URL.Path,
			ContentType: r.Header.Get("Content-Type"),
			Body:        canonicalJSON(t, raw),
		})
		f.mu.Unlock()
		w.WriteHeader(f.status)
		_, _ = io.WriteString(w, f.reply)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeOllama) captured() []capturedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]capturedRequest(nil), f.requests...)
}

// canonicalJSON re-encodes a JSON document so two bodies compare equal exactly
// when they carry the same fields and values, whatever their key order.
func canonicalJSON(t *testing.T, raw []byte) string {
	t.Helper()
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Errorf("request body is not JSON: %v: %q", err, raw)
		return string(raw)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("re-encode body: %v", err)
	}
	return string(out)
}

func newOllama(t *testing.T, cfg embedding.Config) embedding.Provider {
	t.Helper()
	cfg.Provider = "ollama"
	p, err := embedding.NewProvider(cfg)
	if err != nil {
		t.Fatalf("NewProvider(ollama): %v", err)
	}
	return p
}

func TestOllamaProviderMetadata(t *testing.T) {
	tests := []struct {
		name      string
		cfg       embedding.Config
		wantModel string
		wantDims  int
	}{
		{
			name:      "defaults",
			cfg:       embedding.Config{},
			wantModel: "nomic-embed-text-v2-moe",
			wantDims:  768,
		},
		{
			name:      "overrides",
			cfg:       embedding.Config{Model: "mxbai-embed-large", Dimensions: 1024},
			wantModel: "mxbai-embed-large",
			wantDims:  1024,
		},
		{
			// Dimensions is reported, never sent or checked against replies.
			name:      "dimensions only",
			cfg:       embedding.Config{Dimensions: 256},
			wantModel: "nomic-embed-text-v2-moe",
			wantDims:  256,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newOllama(t, tc.cfg)
			if got := p.Name(); got != "ollama" {
				t.Errorf("Name() = %q, want %q", got, "ollama")
			}
			if got := p.ModelName(); got != tc.wantModel {
				t.Errorf("ModelName() = %q, want %q", got, tc.wantModel)
			}
			if got := p.Dimensions(); got != tc.wantDims {
				t.Errorf("Dimensions() = %d, want %d", got, tc.wantDims)
			}
		})
	}
}

func TestOllamaEmbedRequest(t *testing.T) {
	f := newFakeOllama(t, http.StatusOK, `{"embeddings":[[0.5,-1.25],[2,0.125]]}`)
	p := newOllama(t, embedding.Config{OllamaBaseURL: f.srv.URL, Model: "test-model"})

	got, err := p.Embed(context.Background(), []string{"hello world", "  spaced\ttext  "})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	want := [][]float32{{0.5, -1.25}, {2, 0.125}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Embed() = %v, want %v", got, want)
	}

	// Every text gets the document prefix verbatim; the text itself is not
	// trimmed or otherwise touched.
	wantReqs := []capturedRequest{{
		Method:      http.MethodPost,
		Path:        "/api/embed",
		ContentType: "application/json",
		Body:        `{"input":["search_document: hello world","search_document:   spaced\ttext  "],"model":"test-model"}`,
	}}
	if reqs := f.captured(); !reflect.DeepEqual(reqs, wantReqs) {
		t.Errorf("requests =\n%+v\nwant\n%+v", reqs, wantReqs)
	}
}

func TestOllamaEmbedQueryRequest(t *testing.T) {
	f := newFakeOllama(t, http.StatusOK, `{"embeddings":[[1,2,3]]}`)
	p := newOllama(t, embedding.Config{OllamaBaseURL: f.srv.URL})

	got, err := p.EmbedQuery(context.Background(), "what is auth")
	if err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	if want := []float32{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Errorf("EmbedQuery() = %v, want %v", got, want)
	}

	// The query prefix differs from the document prefix, and the default
	// model is sent when none is configured.
	wantReqs := []capturedRequest{{
		Method:      http.MethodPost,
		Path:        "/api/embed",
		ContentType: "application/json",
		Body:        `{"input":["search_query: what is auth"],"model":"nomic-embed-text-v2-moe"}`,
	}}
	if reqs := f.captured(); !reflect.DeepEqual(reqs, wantReqs) {
		t.Errorf("requests =\n%+v\nwant\n%+v", reqs, wantReqs)
	}
}

func TestOllamaEmbedQueryEmptyText(t *testing.T) {
	// An empty query is still sent, as the bare prefix.
	f := newFakeOllama(t, http.StatusOK, `{"embeddings":[[1]]}`)
	p := newOllama(t, embedding.Config{OllamaBaseURL: f.srv.URL, Model: "m"})

	if _, err := p.EmbedQuery(context.Background(), ""); err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	reqs := f.captured()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	if want := `{"input":["search_query: "],"model":"m"}`; reqs[0].Body != want {
		t.Errorf("body = %s, want %s", reqs[0].Body, want)
	}
}

func TestOllamaEmbedEmptyInputSendsNothing(t *testing.T) {
	f := newFakeOllama(t, http.StatusOK, `{"embeddings":[]}`)
	p := newOllama(t, embedding.Config{OllamaBaseURL: f.srv.URL})

	for _, in := range [][]string{nil, {}} {
		got, err := p.Embed(context.Background(), in)
		if err != nil {
			t.Errorf("Embed(%#v): %v", in, err)
		}
		if got != nil {
			t.Errorf("Embed(%#v) = %#v, want nil", in, got)
		}
	}
	if reqs := f.captured(); len(reqs) != 0 {
		t.Errorf("got %d requests for empty input, want 0", len(reqs))
	}
}

func TestOllamaEmbedReturnsRepliesUnchecked(t *testing.T) {
	// The reply is returned as the server sent it: neither the number of
	// vectors nor their length is checked against the input or Dimensions.
	f := newFakeOllama(t, http.StatusOK, `{"embeddings":[[1,2,3]]}`)
	p := newOllama(t, embedding.Config{OllamaBaseURL: f.srv.URL, Dimensions: 768})

	got, err := p.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if want := [][]float32{{1, 2, 3}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Embed() = %v, want %v", got, want)
	}
}

func TestOllamaBaseURLIsConcatenated(t *testing.T) {
	// The base URL is joined to the path by plain concatenation, so a
	// trailing slash yields a double slash on the wire.
	f := newFakeOllama(t, http.StatusOK, `{"embeddings":[[1]]}`)
	p := newOllama(t, embedding.Config{OllamaBaseURL: f.srv.URL + "/"})

	if _, err := p.Embed(context.Background(), []string{"x"}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	reqs := f.captured()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	if reqs[0].Path != "//api/embed" {
		t.Errorf("path = %q, want %q", reqs[0].Path, "//api/embed")
	}
}

func TestOllamaDefaultBaseURL(t *testing.T) {
	// A cancelled context fails the request before anything is dialled, and
	// the error names the URL it would have used: the default base URL.
	p := newOllama(t, embedding.Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Embed(ctx, []string{"x"})
	want := `embed request: Post "http://localhost:11434/api/embed": context canceled`
	if err == nil || err.Error() != want {
		t.Errorf("Embed error = %v, want %q", err, want)
	}
}

func TestOllamaErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		reply   string
		query   bool
		wantErr string
		prefix  bool // wantErr is a prefix, the rest comes from encoding/json
	}{
		{
			name:    "not found",
			status:  http.StatusNotFound,
			reply:   `{"error":"model \"x\" not found, try pulling it first"}`,
			wantErr: `ollama embed: status 404: {"error":"model \"x\" not found, try pulling it first"}`,
		},
		{
			name:    "server error",
			status:  http.StatusInternalServerError,
			reply:   "boom",
			wantErr: "ollama embed: status 500: boom",
		},
		{
			name:    "server error on query",
			status:  http.StatusInternalServerError,
			reply:   "boom",
			query:   true,
			wantErr: "ollama embed: status 500: boom",
		},
		{
			// Only 200 is success; another 2xx is an error.
			name:    "created is not ok",
			status:  http.StatusCreated,
			reply:   `{"embeddings":[[1]]}`,
			wantErr: `ollama embed: status 201: {"embeddings":[[1]]}`,
		},
		{
			name:    "invalid json",
			status:  http.StatusOK,
			reply:   "not json",
			wantErr: "decode embed response: ",
			prefix:  true,
		},
		{
			name:    "query with no vectors",
			status:  http.StatusOK,
			reply:   `{"embeddings":[]}`,
			query:   true,
			wantErr: "ollama embed returned no results",
		},
		{
			name:    "query with missing field",
			status:  http.StatusOK,
			reply:   `{}`,
			query:   true,
			wantErr: "ollama embed returned no results",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeOllama(t, tc.status, tc.reply)
			p := newOllama(t, embedding.Config{OllamaBaseURL: f.srv.URL})

			var err error
			if tc.query {
				_, err = p.EmbedQuery(context.Background(), "q")
			} else {
				_, err = p.Embed(context.Background(), []string{"d"})
			}
			if err == nil {
				t.Fatalf("expected error %q, got nil", tc.wantErr)
			}
			if tc.prefix {
				if !strings.HasPrefix(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want prefix %q", err, tc.wantErr)
				}
			} else if err.Error() != tc.wantErr {
				t.Errorf("error = %q, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestOllamaEmbedEmptyReplyIsNotAnError(t *testing.T) {
	// Embed, unlike EmbedQuery, accepts a reply with no vectors.
	f := newFakeOllama(t, http.StatusOK, `{}`)
	p := newOllama(t, embedding.Config{OllamaBaseURL: f.srv.URL})

	got, err := p.Embed(context.Background(), []string{"d"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if got != nil {
		t.Errorf("Embed() = %#v, want nil", got)
	}
}
