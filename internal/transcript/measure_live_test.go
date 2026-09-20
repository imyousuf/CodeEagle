package transcript

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/decide"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

// TestMeasureJevAgainstCorpus compares what a decision model concludes about
// real recordings with what the language model already wrote into the graph.
//
// The point of calibrated confidence is that the threshold means something, so
// the useful measurements are not "did the two agree" alone but where they
// disagreed and how sure each was. The microphone anchor gives free ground
// truth for one speaker in every meeting, which is a check on whether the
// pipeline as a whole is behaving.
//
// Gated, because it calls a paid service over real meeting content:
//
//	JEV_MEASURE=1 JEV_MEASURE_DB=~/.local/share/codeeagle-meetings/graph.db \
//	JEV_MEASURE_SESSIONS=~/.local/share/tomoe/sessions \
//	go test -race ./internal/transcript/ -run TestMeasureJevAgainstCorpus -v
func TestMeasureJevAgainstCorpus(t *testing.T) {
	if os.Getenv("JEV_MEASURE") == "" {
		t.Skip("set JEV_MEASURE=1 to measure against the real corpus")
	}

	sessionsDir := envOr("JEV_MEASURE_SESSIONS", os.ExpandEnv("$HOME/.local/share/tomoe/sessions"))
	dbPath := envOr("JEV_MEASURE_DB", os.ExpandEnv("$HOME/.local/share/codeeagle-meetings/graph.db"))
	sampleSize, _ := strconv.Atoi(envOr("JEV_MEASURE_N", "12"))
	owner := envOr("JEV_MEASURE_OWNER", "Imran Yousuf")

	judge, err := decide.NewJevJudge(jevKey(t), "")
	if err != nil {
		t.Fatalf("judge: %v", err)
	}

	store, err := embedded.NewReadOnlyBranchStore(dbPath, "measure",
		[]string{"measure", embedded.MeetingScope})
	if err != nil {
		t.Skipf("no meeting graph to compare against: %v", err)
	}
	defer store.Close()

	paths, err := DiscoverSessionsIn([]string{sessionsDir})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(paths) == 0 {
		t.Skip("no recordings found")
	}
	// Most recent first: the newest meetings are the ones whose people the
	// registry knows least about, so they are the harder sample.
	for i, j := 0, len(paths)-1; i < j; i, j = i+1, j-1 {
		paths[i], paths[j] = paths[j], paths[i]
	}
	if len(paths) > sampleSize {
		paths = paths[:sampleSize]
	}

	ctx := context.Background()

	// The people already in the graph, as the real pipeline supplies them.
	registry, err := LoadPersonRegistry(ctx, store)
	if err != nil {
		t.Fatalf("load people: %v", err)
	}
	knownPeople := registry.Names

	var agreed, disagreed, jevOnly, llmOnly, bothSilent int
	var totalConfidence float64
	var confident int
	var anchorOK, anchorTotal int
	start := time.Now()

	for _, path := range paths {
		s, err := Load(path)
		if err != nil || s.IsEmpty() || s.NamedSpeakers {
			continue
		}
		existing := indexedNames(ctx, t, store, s.ID)
		if len(existing) == 0 {
			continue // not indexed yet; nothing to compare against
		}

		a := NewAnalyzer(nil, Options{
			Owner:         owner,
			MinConfidence: 0.70,
			// Mirrors the real pipeline, which feeds people identified in
			// earlier meetings forward into later ones.
			KnownPeople: knownPeople,
		}).WithJudge(judge)
		identities, err := a.Identify(ctx, s, &Usage{})
		if err != nil {
			t.Logf("%s: %v", s.ID, err)
			continue
		}

		for _, id := range identities {
			if id.Method == MethodOwnerAnchor {
				anchorTotal++
				if SameName(id.Name, owner) {
					anchorOK++
				}
				continue
			}

			was := existing[id.Label]
			now := id.Name
			if now != "" {
				totalConfidence += id.Confidence
				confident++
			}

			switch {
			case now == "" && was == "":
				bothSilent++
			case now == "" && was != "":
				llmOnly++
				t.Logf("  %s/%s: language model said %q, decision model declined",
					short(s.ID), id.Label, was)
			case now != "" && was == "":
				jevOnly++
				t.Logf("  %s/%s: decision model said %q at %.2f, language model declined",
					short(s.ID), id.Label, now, id.Confidence)
			case SameName(now, was):
				agreed++
			default:
				disagreed++
				t.Logf("  %s/%s: decision model %q at %.2f vs language model %q",
					short(s.ID), id.Label, now, id.Confidence, was)
			}
		}
	}

	usage := judge.Usage()
	elapsed := time.Since(start)

	t.Log("")
	t.Logf("meetings sampled:     %d", len(paths))
	t.Logf("agreed:               %d", agreed)
	t.Logf("disagreed:            %d", disagreed)
	t.Logf("decision model only:  %d", jevOnly)
	t.Logf("language model only:  %d", llmOnly)
	t.Logf("both left unresolved: %d", bothSilent)
	if confident > 0 {
		t.Logf("mean confidence when named: %.2f", totalConfidence/float64(confident))
	}
	t.Logf("microphone anchor correct: %d/%d", anchorOK, anchorTotal)
	t.Logf("cost: %d requests, %d input tokens (~$%.4f), %s",
		usage.Requests, usage.InputTokens,
		float64(usage.InputTokens)/1e6*0.042, elapsed.Round(time.Millisecond))

	if anchorTotal > 0 && anchorOK != anchorTotal {
		t.Errorf("the microphone anchor is structural and must never be wrong: %d/%d",
			anchorOK, anchorTotal)
	}
}

// indexedNames reads what is already recorded for a meeting's speakers.
func indexedNames(ctx context.Context, t *testing.T, store graph.Store, sessionID string) map[string]string {
	t.Helper()
	speakers, err := store.QueryNodes(ctx, graph.NodeFilter{Type: graph.NodeSpeaker})
	if err != nil {
		return nil
	}

	out := make(map[string]string)
	for _, sp := range speakers {
		if sp.Properties[graph.PropMeetingID] != sessionID {
			continue
		}
		out[sp.Name] = ""
		people, err := store.GetNeighbors(ctx, sp.ID, graph.EdgeIdentifiedAs, graph.Outgoing)
		if err == nil && len(people) > 0 {
			out[sp.Name] = people[0].Name
		}
	}
	return out
}

func jevKey(t *testing.T) string {
	t.Helper()
	if key := os.Getenv("JEV_API_KEY"); key != "" {
		return key
	}
	account := os.Getenv("JEV_KEYRING_ACCOUNT")
	if account == "" {
		t.Skip("set JEV_API_KEY, or JEV_KEYRING_ACCOUNT to read it from the keyring")
	}
	out, err := exec.Command("keyring", "get", "typesafe.ai", account).Output()
	if err != nil {
		t.Skipf("keyring lookup for %q failed: %v", account, err)
	}
	return strings.TrimSpace(string(out))
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
