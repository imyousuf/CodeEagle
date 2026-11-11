package relate

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/jev"
	"github.com/imyousuf/CodeEagle/pkg/kit/decide"
)

// TestJevJudgeAsksEachPairAsNoulsWithCriteria drives the Relater through
// decide.JevJudge against a Jev endpoint on localhost: one request per pair,
// every question a noul with both criteria, and the probabilities back on
// the verdict.
func TestJevJudgeAsksEachPairAsNoulsWithCriteria(t *testing.T) {
	var (
		mu     sync.Mutex
		bodies [][]byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		var req struct {
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		answers := jev.Answers{}
		for name := range req.Questions {
			p := 0.1
			if name == qRelated || name == qChangesBA {
				p = 0.9
			}
			answers[name] = jev.Answer{Type: jev.TypeNoul, Noul: p}
		}
		_ = json.NewEncoder(w).Encode(jev.Response{Model: jev.DefaultModel, Answers: answers,
			Usage: jev.Usage{InputTokens: 100, OutputTokens: 7}})
	}))
	defer srv.Close()
	client, err := jev.New("apikey_test", jev.WithBaseURL(srv.URL), jev.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}

	cands := []*Candidate{
		{A: doc("a.md", "1"), B: doc("b.md", "2")},
		{A: doc("c.md", "3"), B: doc("d.md", "4")},
	}
	judgments, report, err := New(decide.NewJevJudge(client)).Judge(context.Background(), cands)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || report.Requests != 2 || report.InputTokens != 200 || report.Judge != "jev/"+jev.DefaultModel {
		t.Fatalf("%d requests, report %+v", len(bodies), report)
	}
	v := judgments[0].Verdict
	if v.PRelated != 0.9 || v.PChanges != [2]float64{0.1, 0.9} || v.PRecords != [2]float64{0.1, 0.1} {
		t.Errorf("verdict = %+v", v)
	}

	w := DefaultWording()
	want := jev.Questions{qRelated: jev.NoulWithCriteria(w.Related.Ask, w.Related.WhenTrue, w.Related.WhenFalse)}
	for name, q := range map[string]decide.Question{
		qChangesAB: w.Changes("A", "B"), qChangesBA: w.Changes("B", "A"),
		qReplacesAB: w.Replaces("A", "B"), qReplacesBA: w.Replaces("B", "A"),
		qRecordsAB: w.Records("A", "B"), qRecordsBA: w.Records("B", "A"),
	} {
		want[name] = jev.NoulWithCriteria(q.Ask, q.WhenTrue, q.WhenFalse)
	}
	wantJSON, _ := json.Marshal(want)
	for _, b := range bodies {
		var req struct {
			State     PairState       `json:"state"`
			Questions json.RawMessage `json:"questions"`
		}
		if err := json.Unmarshal(b, &req); err != nil {
			t.Fatal(err)
		}
		if string(req.Questions) != string(wantJSON) {
			t.Errorf("questions =\n%s\nwant\n%s", req.Questions, wantJSON)
		}
		if req.State.A.Path == "" || req.State.B.Path == "" {
			t.Errorf("state = %+v, want one pair's cards", req.State)
		}
	}
}
