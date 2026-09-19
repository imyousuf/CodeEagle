// Package jevtest replays interactions recorded from the real Jev service.
//
// Testing a client against hand-written responses tests what someone believed
// the service does. That belief goes stale silently: the fake keeps returning
// what it always did, the tests keep passing, and the service has moved. The
// corpus here was captured from live calls, and several of its fixtures
// contradict both the vendor's documentation and notes taken from earlier
// exploration -- refusals arrive with three different shapes, a missing key is
// a different status from an invalid one, and the size ceiling is not a round
// number.
//
// Replay is exact and offline: the transport has no network path at all, so a
// test cannot quietly start calling a paid service. A request with no matching
// fixture fails loudly rather than falling through, and a fixture that no test
// asked for fails too -- drift in the other direction, where the corpus still
// carries a case nothing sends any more.
//
// Re-record with `make jev-record` when the service changes, rather than
// hand-editing assertions.
package jevtest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Fixture is one recorded exchange.
type Fixture struct {
	Schema         int    `json:"schema"`
	Name           string `json:"name"`
	Note           string `json:"note,omitempty"`
	RecordedAt     string `json:"recorded_at"`
	APIVersion     string `json:"api_version"`
	ModelRequested string `json:"model_requested"`
	ModelAnswered  string `json:"model_answered,omitempty"`

	Request struct {
		Method string          `json:"method"`
		Path   string          `json:"path"`
		Body   json.RawMessage `json:"body"`
	} `json:"request"`

	Response struct {
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
		Body    json.RawMessage   `json:"body"`
		// Text carries a body that was not JSON, such as an HTML error page.
		Text string `json:"body_text,omitempty"`
	} `json:"response"`

	LatencyMS int    `json:"latency_ms"`
	Key       string `json:"key"`

	// path is where this was loaded from, for messages.
	path string
}

// Recorder replays a group of recorded interactions as an http.RoundTripper.
//
// It is safe for concurrent use: a fan-out test may issue several requests at
// once, and each fixture is still served once.
type Recorder struct {
	t     testing.TB
	group string
	dir   string

	mu     sync.Mutex
	byKey  map[string][]*Fixture
	served map[string]int
	allow  bool
}

// Option configures a Recorder.
type Option func(*Recorder)

// WithDir overrides where recordings are read from.
func WithDir(dir string) Option { return func(r *Recorder) { r.dir = dir } }

// AllowUnused stops unserved fixtures failing the test.
//
// The default is deliberate: a fixture nothing asks for means the corpus and
// the tests have drifted apart, which is worth knowing even though it breaks
// nothing at runtime.
func AllowUnused() Option { return func(r *Recorder) { r.allow = true } }

// NewRecorder loads one group of recordings.
func NewRecorder(t testing.TB, group string, opts ...Option) *Recorder {
	t.Helper()

	r := &Recorder{
		t:      t,
		group:  group,
		dir:    filepath.Join("testdata", "recordings"),
		byKey:  make(map[string][]*Fixture),
		served: make(map[string]int),
	}
	for _, opt := range opts {
		opt(r)
	}

	for _, f := range Load(t, r.group, WithDir(r.dir)) {
		key := f.Key
		if computed, err := RequestKey(f.Request.Method, f.Request.Path, f.Request.Body); err == nil {
			if key != "" && key != computed {
				// The stored key is advisory. A mismatch means the request
				// body was edited by hand, which defeats re-recording.
				t.Errorf("jevtest: %s: stored key does not match its request body; "+
					"re-record rather than editing fixtures", f.path)
			}
			key = computed
		}
		r.byKey[key] = append(r.byKey[key], f)
	}

	t.Cleanup(func() {
		if r.allow {
			return
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		var unused []string
		for key, fixtures := range r.byKey {
			for i := r.served[key]; i < len(fixtures); i++ {
				unused = append(unused, fixtures[i].Name)
			}
		}
		if len(unused) > 0 {
			sort.Strings(unused)
			t.Errorf("jevtest: %d recording(s) in group %q were never requested: %s",
				len(unused), r.group, strings.Join(unused, ", "))
		}
	})

	return r
}

// Load reads a group's fixtures, sorted by name so that the interactions of
// one case are served in the order they were recorded.
func Load(t testing.TB, group string, opts ...Option) []*Fixture {
	t.Helper()

	r := &Recorder{dir: filepath.Join("testdata", "recordings")}
	for _, opt := range opts {
		opt(r)
	}

	dir := filepath.Join(r.dir, group)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("jevtest: no recordings for group %q: %v", group, err)
	}

	var out []*Fixture
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("jevtest: read %s: %v", path, err)
		}
		var f Fixture
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatalf("jevtest: parse %s: %v", path, err)
		}
		f.path = path
		out = append(out, &f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(out) == 0 {
		t.Fatalf("jevtest: group %q is empty", group)
	}
	return out
}

// RoundTrip serves a recorded response, or fails the test.
func (r *Recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("jevtest: read request body: %w", err)
		}
		_ = req.Body.Close()
	}

	key, err := RequestKey(req.Method, req.URL.Path, body)
	if err != nil {
		return nil, fmt.Errorf("jevtest: key request: %w", err)
	}

	r.mu.Lock()
	fixtures := r.byKey[key]
	i := r.served[key]
	var f *Fixture
	if i < len(fixtures) {
		f = fixtures[i]
		r.served[key] = i + 1
	}
	r.mu.Unlock()

	if f == nil {
		// Deliberately not a fallback to the network: a test that silently
		// started making paid calls is worse than one that fails.
		err := fmt.Errorf("jevtest: no recording for %s %s (%s) in group %q; "+
			"re-record with: make jev-record GROUP=%s",
			req.Method, req.URL.Path, key, r.group, r.group)
		r.t.Error(err)
		return nil, err
	}

	payload := []byte(f.Response.Text)
	if len(f.Response.Body) > 0 {
		payload = f.Response.Body
	}

	resp := &http.Response{
		StatusCode: f.Response.Status,
		Header:     make(http.Header, len(f.Response.Headers)),
		Body:       io.NopCloser(bytes.NewReader(payload)),
		Request:    req,
	}
	for name, value := range f.Response.Headers {
		resp.Header.Set(name, value)
	}
	return resp, nil
}

// Client returns an http.Client that serves this group's recordings.
func (r *Recorder) Client() *http.Client {
	return &http.Client{Transport: r}
}

// RequestKey identifies a request by what it asks, not how it was written.
//
// The body is re-encoded with sorted keys and no whitespace, on both the
// incoming request and the stored fixture, so the key survives a change of
// struct field order, of whitespace, or of the language that wrote the file.
// The model is inside the body, so a recording made against a pinned version
// never answers a request for a rolling alias -- the alias can move, and
// silently replaying the old answer would hide that.
func RequestKey(method, path string, body []byte) (string, error) {
	canonical, err := canonicalJSON(body)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(method + " " + path + "\n" + canonical))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func canonicalJSON(body []byte) (string, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return "", nil
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		// Not JSON: key on the bytes as they are.
		return string(body), nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}
