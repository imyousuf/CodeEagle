package decide

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/jev"
)

func TestYesNoVerdictRestatesTheProbability(t *testing.T) {
	tests := []struct {
		p    float64
		want Verdict
	}{
		{0.7, Verdict{Choice: True, Confidence: 0.7, RunnerUp: False, RunnerUpShare: 0.30000000000000004, P: 0.7}},
		{0.2, Verdict{Choice: False, Confidence: 0.8, RunnerUp: True, RunnerUpShare: 0.2, P: 0.2}},
		{0.5, Verdict{Choice: True, Confidence: 0.5, RunnerUp: False, RunnerUpShare: 0.5, P: 0.5}},
	}
	for _, tt := range tests {
		if got := YesNoVerdict(tt.p); got != tt.want {
			t.Errorf("YesNoVerdict(%v) = %+v, want %+v", tt.p, got, tt.want)
		}
	}
	if q := YesNo("Is it?", "", ""); !q.IsYesNo() {
		t.Error("YesNo built a choice")
	}
	if q := (Question{Ask: "Which?", Options: map[string]string{"a": "first"}}); q.IsYesNo() {
		t.Error("a question with options is not yes/no")
	}
}

// recordingJev answers every noul in a request with the probability its
// name maps to, and keeps each request body.
type recordingJev struct {
	srv    *httptest.Server
	bodies [][]byte
}

func newRecordingJev(t *testing.T, reply string) *recordingJev {
	t.Helper()
	r := &recordingJev{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.bodies = append(r.bodies, body)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *recordingJev) client(t *testing.T) *jev.Client {
	t.Helper()
	c, err := jev.New("apikey_test", jev.WithBaseURL(r.srv.URL), jev.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestJevJudgeAsksYesNoExactlyAsJevDoes pins that a yes/no question reaches
// Jev byte for byte as a direct NoulWithCriteria or Noul would, so callers
// that move from a jev.Asker to a decide.Judge send what they always sent.
func TestJevJudgeAsksYesNoExactlyAsJevDoes(t *testing.T) {
	const reply = `{"model":"jev-1.13.0","answers":{
		"related":{"type":"noul","noul":0.83},
		"same":{"type":"noul","noul":0.12}},
		"usage":{"input_tokens":120,"output_tokens":7}}`
	state := map[string]any{"a": "Retry budget", "b": "Retry cap", "facts": []string{"x < y & z"}}

	direct := newRecordingJev(t, reply)
	if _, err := direct.client(t).Ask(context.Background(), state, jev.Questions{
		"related": jev.NoulWithCriteria("Are these about one thing?", "They concern one subject.", "They do not."),
		"same":    jev.Noul("Are these the same topic?"),
	}); err != nil {
		t.Fatal(err)
	}

	viaJudge := newRecordingJev(t, reply)
	verdicts, err := NewJevJudge(viaJudge.client(t)).Decide(context.Background(), state, map[string]Question{
		"related": YesNo("Are these about one thing?", "They concern one subject.", "They do not."),
		"same":    YesNo("Are these the same topic?", "", ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(direct.bodies) != 1 || len(viaJudge.bodies) != 1 {
		t.Fatalf("requests: direct %d, judge %d; want one each", len(direct.bodies), len(viaJudge.bodies))
	}
	if string(direct.bodies[0]) != string(viaJudge.bodies[0]) {
		t.Errorf("request differs:\ndirect %s\njudge  %s", direct.bodies[0], viaJudge.bodies[0])
	}
	if verdicts["related"] != YesNoVerdict(0.83) || verdicts["same"] != YesNoVerdict(0.12) {
		t.Errorf("verdicts = %+v", verdicts)
	}
}

func TestJevJudgeWithoutAnAsker(t *testing.T) {
	if j := Optional(nil); j != nil {
		t.Errorf("Optional(nil) = %v, want no judge", j)
	}
	var none *jev.Client
	if j := Optional(none); j != nil {
		t.Errorf("Optional(typed nil client) = %v, want no judge", j)
	}
	judge := NewJevJudge(nil)
	if got, err := judge.Decide(context.Background(), nil, nil); err != nil || len(got) != 0 {
		t.Errorf("no questions: %v, %v; want an empty answer", got, err)
	}
	_, err := judge.Decide(context.Background(), nil, map[string]Question{"q": YesNo("Is it?", "", "")})
	if !errors.Is(err, ErrNoAsker) {
		t.Errorf("Decide without an asker = %v, want ErrNoAsker", err)
	}
	if got := judge.Name(); got != "jev/"+jev.DefaultModel {
		t.Errorf("Name() = %q, want the default model", got)
	}
}

// askerFunc is a jev.Asker with no Model method, like a caller's wrapper.
type askerFunc func(ctx context.Context, state any, q jev.Questions) (*jev.Response, error)

func (f askerFunc) Ask(ctx context.Context, state any, q jev.Questions) (*jev.Response, error) {
	return f(ctx, state, q)
}

func TestJevJudgeOverAnotherAsker(t *testing.T) {
	tooBig := askerFunc(func(context.Context, any, jev.Questions) (*jev.Response, error) {
		return nil, jev.ErrTooLarge
	})
	if got := Optional(tooBig, WithModelName("jev-9.9.9")).Name(); got != "jev/jev-9.9.9" {
		t.Errorf("Name() = %q, want the configured model", got)
	}
	_, err := NewJevJudge(tooBig).Decide(context.Background(), nil, map[string]Question{"q": YesNo("Is it?", "", "")})
	if !errors.Is(err, jev.ErrTooLarge) {
		t.Errorf("Decide = %v; the asker's error must come back so callers can trim", err)
	}
}
