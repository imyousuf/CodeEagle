package decide

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/jev"
)

// newTestJudge builds a judge against a test server, with retries off so a
// refusal ends the call at once instead of backing off.
func newTestJudge(t *testing.T, url string) *JevJudge {
	t.Helper()
	client, err := jev.New("apikey_test", jev.WithBaseURL(url), jev.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	return NewJevJudge(client)
}

// twoWay is a question with the two options the client insists on.
var twoWay = map[string]Question{
	"q": {Ask: "Which?", Options: map[string]string{"A": "the first", "B": "the second"}},
}

// TestJevJudgeUsageAccumulates covers spend tracking across calls: each
// successful round trip adds one request and its tokens, and a refused one
// adds nothing, since a caller budgeting on Usage must not be charged for a
// reply that never came.
func TestJevJudgeUsageAccumulates(t *testing.T) {
	usages := []string{
		`{"input_tokens": 100, "output_tokens": 10}`,
		`{"input_tokens": 250, "output_tokens": 0}`,
		`{"input_tokens": 7, "output_tokens": 3}`,
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := int(calls.Add(1)) - 1
		if n >= len(usages) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"detail": "refused"}`))
			return
		}
		fmt.Fprintf(w, `{"model": "jev-1.13.0", "answers": {"q": {"type": "choice",
			"choice": "A", "confidence": 0.9, "probabilities": {"A": 0.9, "B": 0.1}}},
			"usage": %s}`, usages[n])
	}))
	defer srv.Close()

	judge := newTestJudge(t, srv.URL)
	if got := judge.Usage(); got != (Usage{}) {
		t.Fatalf("fresh judge Usage() = %+v, want zero", got)
	}

	want := []Usage{
		{Requests: 1, InputTokens: 100, OutputTokens: 10},
		{Requests: 2, InputTokens: 350, OutputTokens: 10},
		{Requests: 3, InputTokens: 357, OutputTokens: 13},
	}
	for i, w := range want {
		if _, err := judge.Decide(context.Background(), "state", twoWay); err != nil {
			t.Fatalf("Decide #%d: %v", i+1, err)
		}
		if got := judge.Usage(); got != w {
			t.Errorf("after call %d Usage() = %+v, want %+v", i+1, got, w)
		}
	}

	// The fourth call is refused; usage stays where it was.
	if _, err := judge.Decide(context.Background(), "state", twoWay); err == nil {
		t.Fatal("Decide succeeded against a refusal")
	}
	if got := judge.Usage(); got != want[len(want)-1] {
		t.Errorf("after a refused call Usage() = %+v, want %+v", got, want[len(want)-1])
	}

	// No questions means no request, and no usage either.
	if _, err := judge.Decide(context.Background(), "state", nil); err != nil {
		t.Fatalf("Decide with no questions: %v", err)
	}
	if got := judge.Usage(); got != want[len(want)-1] {
		t.Errorf("after an empty Decide Usage() = %+v, want %+v", got, want[len(want)-1])
	}
	if got := calls.Load(); got != 4 {
		t.Errorf("server saw %d requests, want 4", got)
	}
}

// TestJevJudgeUsageConcurrent covers the documented concurrency guarantee:
// parallel Decide calls each count once. Run under -race this also checks the
// counter is guarded.
func TestJevJudgeUsageConcurrent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"answers": {"q": {"type": "choice", "choice": "B",
			"confidence": 0.8, "probabilities": {"A": 0.2, "B": 0.8}}},
			"usage": {"input_tokens": 11, "output_tokens": 2}}`))
	}))
	defer srv.Close()

	judge := newTestJudge(t, srv.URL)
	const workers = 8
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := judge.Decide(context.Background(), "state", twoWay); err != nil {
				t.Errorf("Decide: %v", err)
			}
		}()
	}
	wg.Wait()

	want := Usage{Requests: workers, InputTokens: 11 * workers, OutputTokens: 2 * workers}
	if got := judge.Usage(); got != want {
		t.Errorf("Usage() = %+v, want %+v", got, want)
	}
}

// TestJevJudgeRunnerUp pins how the runner-up is read: it is the second entry
// of the distribution ranked by probability, ties broken by option name, and
// Confidence is the service's own figure rather than the winner's mass.
func TestJevJudgeRunnerUp(t *testing.T) {
	tests := []struct {
		name   string
		answer string
		want   Verdict
	}{
		{
			name: "clear second",
			answer: `{"type": "choice", "choice": "A", "confidence": 0.42,
				"probabilities": {"A": 0.6, "B": 0.3, "C": 0.1}}`,
			want: Verdict{Choice: "A", Confidence: 0.42, RunnerUp: "B", RunnerUpShare: 0.3},
		},
		{
			name: "tie for second breaks on name",
			answer: `{"type": "choice", "choice": "A", "confidence": 0.5,
				"probabilities": {"A": 0.5, "Zed": 0.25, "Bob": 0.25}}`,
			want: Verdict{Choice: "A", Confidence: 0.5, RunnerUp: "Bob", RunnerUpShare: 0.25},
		},
		{
			// Ranking ignores which option the service chose, so a winner
			// tied with an earlier-sorting rival is reported as its own
			// runner-up. Pinned as today's behaviour.
			name: "tied winner sorting second is its own runner-up",
			answer: `{"type": "choice", "choice": "B", "confidence": 0.5,
				"probabilities": {"A": 0.5, "B": 0.5}}`,
			want: Verdict{Choice: "B", Confidence: 0.5, RunnerUp: "B", RunnerUpShare: 0.5},
		},
		{
			name: "one option in the distribution has no runner-up",
			answer: `{"type": "choice", "choice": "A", "confidence": 1,
				"probabilities": {"A": 1}}`,
			want: Verdict{Choice: "A", Confidence: 1},
		},
		{
			name:   "no distribution has no runner-up",
			answer: `{"type": "choice", "choice": "B", "confidence": 0.7}`,
			want:   Verdict{Choice: "B", Confidence: 0.7},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(w, `{"answers": {"q": %s}, "usage": {"input_tokens": 1}}`, tt.answer)
			}))
			defer srv.Close()

			verdicts, err := newTestJudge(t, srv.URL).Decide(context.Background(), "state", twoWay)
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if len(verdicts) != 1 {
				t.Fatalf("got %d verdicts, want 1", len(verdicts))
			}
			if got := verdicts["q"]; got != tt.want {
				t.Errorf("verdict = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestJevJudgeRequestBody pins what one Decide sends: the pinned default
// model, the state as given, and each question as a choice whose criteria are
// the option descriptions.
func TestJevJudgeRequestBody(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer apikey_test" {
			t.Errorf("Authorization = %q", got)
		}
		body, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"answers": {
			"a": {"type": "choice", "choice": "x", "confidence": 1, "probabilities": {"x": 1}},
			"b": {"type": "choice", "choice": "y", "confidence": 1, "probabilities": {"y": 1}}}}`))
	}))
	defer srv.Close()

	_, err := newTestJudge(t, srv.URL).Decide(context.Background(),
		map[string]string{"transcript": "hello"},
		map[string]Question{
			"a": {Ask: "Pick a", Options: map[string]string{"x": "ex", "unresolved": "none"}},
			"b": {Ask: "Pick b", Options: map[string]string{"y": "why", "z": "zed"}},
		})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	const want = `{"model":"jev-1.13.0","state":{"transcript":"hello"},"questions":{` +
		`"a":{"type":"choice","instructions":"Pick a","criteria":{"unresolved":"none","x":"ex"}},` +
		`"b":{"type":"choice","instructions":"Pick b","criteria":{"y":"why","z":"zed"}}}}`
	if string(body) != want {
		t.Errorf("request body =\n%s\nwant\n%s", body, want)
	}
}
