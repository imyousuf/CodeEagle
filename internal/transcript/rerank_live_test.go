package transcript

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/pkg/jev"
)

// TestMeasureRerankAgainstCorpus shows, side by side, the order word
// matching produces and the order a decision model produces for the same
// candidates, so a person can judge whether the model earns its call.
//
// Gated, because it calls a paid service over real meeting content:
//
//	JEV_RERANK=1 JEV_KEYRING_ACCOUNT=you@example.com \
//	JEV_MEASURE_DB=~/.CodeEagle/graph.db \
//	go test -race ./internal/transcript/ -run TestMeasureRerankAgainstCorpus -v
func TestMeasureRerankAgainstCorpus(t *testing.T) {
	if os.Getenv("JEV_RERANK") == "" {
		t.Skip("set JEV_RERANK=1 to measure against the real corpus")
	}
	dbPath := envOr("JEV_MEASURE_DB", os.ExpandEnv("$HOME/.CodeEagle/graph.db"))
	client, err := jev.New(jevKey(t))
	if err != nil {
		t.Fatal(err)
	}
	store, err := embedded.NewReadOnlyBranchStore(dbPath, "measure", []string{"measure", embedded.MeetingScope})
	if err != nil {
		t.Skipf("no meeting graph: %v", err)
	}
	defer store.Close()

	queries := strings.Split(envOr("JEV_RERANK_QUERIES",
		"AGI|Okta CIAM|brand portal cost|Azure Foundry 429|VT pricing"), "|")
	ctx := context.Background()
	rr := NewReranker(client)

	for _, q := range queries {
		found, err := FindMeetings(ctx, store, Query{Text: q, Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		before := make([]*Hit, len(found.Hits))
		copy(before, found.Hits)

		rep, err := rr.Rerank(ctx, q, found.Hits)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		t.Logf("=== %q: %d hits, %d judged, %d input tokens, %s, %s",
			q, found.Total, rep.Judged, rep.InputTokens, rep.Latency.Round(time.Millisecond), rep.Model)
		t.Logf("  word-match order:")
		for i, h := range before {
			if i >= 8 {
				break
			}
			t.Logf("    %d. %s %s  %s", i+1, h.Meeting.UpdatedAt.Format("2006-01-02"), ShortID(h.Meeting), clipRunes(h.Meeting.Name, 70))
		}
		t.Logf("  decision-model order:")
		for i, h := range found.Hits {
			if i >= 8 {
				break
			}
			t.Logf("    %d. p=%.2f %s %s  %s", i+1, h.Relevance, h.Meeting.UpdatedAt.Format("2006-01-02"), ShortID(h.Meeting), clipRunes(h.Meeting.Name, 70))
			for _, p := range passagesOf(h) {
				t.Logf("         %s", clipRunes(p, 160))
			}
		}
	}
}

// Keep the graph import used even when the corpus is absent.
var _ = graph.NodeMeeting
