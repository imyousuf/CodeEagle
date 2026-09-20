package transcript

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/config"
	_ "github.com/imyousuf/CodeEagle/internal/llm"
	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// TestEnrichSmoke runs a real enrichment against a live provider. It is
// skipped unless both a corpus and an API key are available.
//
//	BASETEN_API_KEY=$(keyring get baseten.co you@example.com) \
//	CODEEAGLE_TRANSCRIPT_CORPUS=~/.local/share/tomoe/sessions \
//	go test ./internal/transcript/ -run TestEnrichSmoke -v -timeout 10m
//
// Set CODEEAGLE_TRANSCRIPT_SESSION to pin a specific session id.
func TestEnrichSmoke(t *testing.T) {
	dir := os.Getenv("CODEEAGLE_TRANSCRIPT_CORPUS")
	// The credential may come straight from the environment, or from a command
	// that reads it out of the system keyring — the same resolution path the
	// transcripts config uses.
	key, err := config.ResolveSecret(config.SecretSource{
		EnvVar:  "BASETEN_API_KEY",
		Command: os.Getenv("BASETEN_API_KEY_COMMAND"),
	})
	if err != nil {
		t.Fatalf("resolve credential: %v", err)
	}
	if dir == "" {
		t.Skip("set CODEEAGLE_TRANSCRIPT_CORPUS to run")
	}
	// A locally served model needs no credential.
	if key == "" && os.Getenv("CODEEAGLE_TRANSCRIPT_PROVIDER") != "ollama" {
		t.Skip("set BASETEN_API_KEY or BASETEN_API_KEY_COMMAND to run against Baseten")
	}

	session := pickSession(t, dir)
	t.Logf("session %s (%s), %s, %d segments",
		session.ID, session.Title,
		FormatTimestamp(session.DurationSeconds()), len(session.Segments))

	provider := envOr("CODEEAGLE_TRANSCRIPT_PROVIDER", "baseten")
	client, err := llm.NewClient(llm.Config{
		Provider:        provider,
		Model:           os.Getenv("CODEEAGLE_TRANSCRIPT_MODEL"),
		APIKey:          key,
		ReasoningEffort: envOr("CODEEAGLE_TRANSCRIPT_EFFORT", "low"),
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer client.Close()

	// Both supported providers enforce a response schema; losing that would
	// silently move extraction back onto prose salvage.
	if !llm.SupportsStructured(client) {
		t.Errorf("%s client should support structured output", provider)
	}

	analyzer := NewAnalyzer(client, Options{
		Owner:        envOr("CODEEAGLE_TRANSCRIPT_OWNER", "Imran Yousuf"),
		ExcludeNames: []string{"Opal", "Claude", "Optimizely"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	start := time.Now()
	res, err := analyzer.Enrich(ctx, session)
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}
	elapsed := time.Since(start)

	fmt.Printf("\n══ IDENTITIES ══\n")
	for _, id := range res.Identities {
		fmt.Printf("  %-12s → %-18s conf=%.2f via=%-12s %q\n",
			id.Label, orDash(id.Name), id.Confidence, id.Method, truncate(id.Evidence, 80))
	}

	a := res.Analysis
	if a == nil {
		t.Fatal("no analysis returned")
	}

	fmt.Printf("\n══ MEETING ══\n  title:   %s\n  summary: %s\n", a.Title, a.Summary)

	fmt.Printf("\n══ TOPICS (%d) ══\n", len(a.Topics))
	for _, tp := range a.Topics {
		fmt.Printf("  [%s–%s] %s\n      %s\n      who: %v  keywords: %v\n",
			FormatTimestamp(tp.StartTime), FormatTimestamp(tp.EndTime),
			tp.Name, truncate(tp.Summary, 220), tp.Participants, tp.Keywords)
	}

	fmt.Printf("\n══ DECISIONS (%d) ══\n", len(a.Decisions))
	for _, d := range a.Decisions {
		fmt.Printf("  • %s\n      by: %v  topic: %s\n      why: %s\n      quote: %q\n",
			d.Text, d.DecidedBy, d.Topic, truncate(d.Rationale, 120), truncate(d.Quote, 110))
	}

	fmt.Printf("\n══ ACTION ITEMS (%d) ══\n", len(a.ActionItems))
	for _, ai := range a.ActionItems {
		fmt.Printf("  □ %s\n      assignee: %s  due: %s  topic: %s\n      quote: %q\n",
			ai.Text, orDash(ai.Assignee), orDash(ai.DueDate), ai.Topic, truncate(ai.Quote, 110))
	}

	fmt.Printf("\n══ MENTIONS ══\n  %v\n", a.Mentions)

	// DeepSeek V4.1 Flash list price, dollars per million tokens.
	const inRate, outRate = 0.30, 1.20
	cost := float64(res.Usage.InputTokens)/1e6*inRate + float64(res.Usage.OutputTokens)/1e6*outRate
	fmt.Printf("\n══ COST ══\n  %d requests, %d in / %d out tokens, $%.4f, %s\n\n",
		res.Usage.Requests, res.Usage.InputTokens, res.Usage.OutputTokens, cost, elapsed.Round(time.Millisecond))

	if res.Usage.Requests == 0 {
		t.Error("no requests were made")
	}
	// Quotes are the audit trail; a quote that is not in the transcript means
	// the model invented it.
	verifyQuotes(t, res)
}

// verifyQuotes checks that supporting quotes actually appear in the transcript.
func verifyQuotes(t *testing.T, res *Result) {
	t.Helper()
	haystack := res.Session.Transcript()
	checked, missing := 0, 0
	for _, d := range res.Analysis.Decisions {
		if d.Quote == "" {
			continue
		}
		checked++
		if !containsLoose(haystack, d.Quote) {
			missing++
			t.Logf("decision quote not found verbatim: %q", truncate(d.Quote, 90))
		}
	}
	for _, ai := range res.Analysis.ActionItems {
		if ai.Quote == "" {
			continue
		}
		checked++
		if !containsLoose(haystack, ai.Quote) {
			missing++
			t.Logf("action quote not found verbatim: %q", truncate(ai.Quote, 90))
		}
	}
	fmt.Printf("══ QUOTE CHECK ══\n  %d/%d quotes found verbatim in transcript\n", checked-missing, checked)
}

func pickSession(t *testing.T, dir string) *Session {
	t.Helper()

	if id := os.Getenv("CODEEAGLE_TRANSCRIPT_SESSION"); id != "" {
		s, err := Load(filepath.Join(dir, id, SessionFileName))
		if err != nil {
			t.Fatalf("load pinned session: %v", err)
		}
		return s
	}

	// Otherwise pick the session richest in name evidence, since that is where
	// identification quality is most visible.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var best *Session
	bestScore := -1
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		s, err := Load(filepath.Join(dir, e.Name(), SessionFileName))
		if err != nil || s.IsEmpty() {
			continue
		}
		// Keep the smoke test quick and cheap.
		if s.DurationSeconds() > 2400 || len(s.SubstantiveSpeakers()) < 3 {
			continue
		}
		score := 0
		for _, h := range ExtractHints(s) {
			if h.Target != "" {
				score++
			}
		}
		if score > bestScore {
			best, bestScore = s, score
		}
	}
	if best == nil {
		t.Skip("no suitable session found")
	}
	return best
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// containsLoose reports whether needle appears in haystack, ignoring
// whitespace differences introduced by turn merging.
func containsLoose(haystack, needle string) bool {
	return len(needle) > 0 && stringsContainsFold(collapseSpace(haystack), collapseSpace(needle))
}
