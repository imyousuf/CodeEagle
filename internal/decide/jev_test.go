package decide

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/jev"
)

// TestJevJudgeMapsVerdicts covers turning a reply into verdicts, including the
// runner-up — a winner at 0.51 against one rival is a different situation from
// one at 0.51 against five, and the choice alone does not say which.
func TestJevJudgeMapsVerdicts(t *testing.T) {
	const reply = `{
	  "model": "jev-1.13.0",
	  "answers": {
	    "speaker_0": {"type": "choice", "choice": "Kevin Mitchell", "confidence": 0.98,
	                  "probabilities": {"Kevin Mitchell": 0.98, "Mona Patel": 0.01, "unresolved": 0.01}},
	    "speaker_1": {"type": "choice", "choice": "unresolved", "confidence": 0.95,
	                  "probabilities": {"unresolved": 0.95, "Kevin Mitchell": 0.05}}
	  },
	  "usage": {"input_tokens": 937, "output_tokens": 192}
	}`

	var gotQuestions int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		gotQuestions = len(body.Questions)
		_, _ = w.Write([]byte(reply))
	}))
	defer srv.Close()

	client, err := jev.New("apikey_test", jev.WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	judge := &JevJudge{client: client}

	verdicts, err := judge.Decide(context.Background(), map[string]string{"transcript": "..."},
		map[string]Question{
			"speaker_0": {Ask: "Who is Person 1?", Options: map[string]string{
				"Kevin Mitchell": "a", "Mona Patel": "b", "unresolved": "c"}},
			"speaker_1": {Ask: "Who is Person 2?", Options: map[string]string{
				"Kevin Mitchell": "a", "unresolved": "c"}},
		})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	if gotQuestions != 2 {
		t.Errorf("sent %d questions, want 2 in one request", gotQuestions)
	}
	if got := verdicts["speaker_0"]; got.Choice != "Kevin Mitchell" || got.Confidence != 0.98 {
		t.Errorf("speaker_0 = %+v, want Kevin Mitchell at 0.98", got)
	}
	if got := verdicts["speaker_0"]; got.RunnerUp == "" || got.RunnerUpShare == 0 {
		t.Errorf("speaker_0 carries no runner-up: %+v", got)
	}
	if got := verdicts["speaker_1"]; got.Choice != "unresolved" {
		t.Errorf("speaker_1 = %q, want unresolved", got.Choice)
	}

	usage := judge.Usage()
	if usage.Requests != 1 || usage.InputTokens != 937 {
		t.Errorf("usage = %+v, want 1 request and 937 input tokens", usage)
	}
}

// TestJevJudgeNoQuestions covers not spending a request on nothing.
func TestJevJudgeNoQuestions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("Decide sent a request with no questions")
	}))
	defer srv.Close()

	client, err := jev.New("apikey_test", jev.WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	judge := &JevJudge{client: client}

	verdicts, err := judge.Decide(context.Background(), "state", nil)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if len(verdicts) != 0 {
		t.Errorf("got %d verdicts from no questions", len(verdicts))
	}
}

// TestNewJevJudgeName covers reporting which model is behind a decision, so a
// graph edge written at a given confidence can be traced to the weights that
// produced it.
func TestNewJevJudgeName(t *testing.T) {
	judge, err := NewJevJudge("apikey_test", "jev-1.13.0")
	if err != nil {
		t.Fatal(err)
	}
	if got := judge.Name(); got != "jev/jev-1.13.0" {
		t.Errorf("Name() = %q, want jev/jev-1.13.0", got)
	}
	if _, err := NewJevJudge("", ""); err == nil {
		t.Error("NewJevJudge succeeded with no key")
	}
}
