package jev

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// liveClient builds a client against the real service, or skips.
//
// Gated on JEV_LIVE_TEST rather than on the key being present: the key is
// meant to sit in the environment for ordinary use, and `make test` must not
// start calling a paid service because someone exported it.
// keyringService is the service name the vendor's key is filed under.
const keyringService = "typesafe.ai"

func liveClient(t *testing.T) *Client {
	t.Helper()
	if os.Getenv("JEV_LIVE_TEST") == "" {
		t.Skip("set JEV_LIVE_TEST=1 to exercise the real service")
	}

	key := os.Getenv("JEV_API_KEY")
	if key == "" {
		// A developer who keeps the key in a keyring rather than the
		// environment names their account here. There is no default: whose
		// account it would be is not something this repository should know.
		account := os.Getenv("JEV_KEYRING_ACCOUNT")
		if account == "" {
			t.Skip("set JEV_API_KEY, or JEV_KEYRING_ACCOUNT to read it from the keyring")
		}
		out, err := exec.Command("keyring", "get", keyringService, account).Output()
		if err != nil {
			t.Skipf("keyring lookup for %q failed: %v", account, err)
		}
		key = strings.TrimSpace(string(out))
	}

	c, err := New(key)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// TestLiveAllPrimitives checks the three primitives against the real service,
// which is the only way to know the wire format has not moved.
func TestLiveAllPrimitives(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := c.Ask(ctx, map[string]string{
		"command":           "git push --force origin main",
		"working_directory": "/app/service-payments",
	}, Questions{
		"destructive": NoulWithCriteria(
			"Does this command irreversibly overwrite remote history or delete resources?",
			"Rewrites history, drops data, or bypasses a safety mechanism",
			"Performs safe local work or an additive operation"),
		"routing": Choice("Which safety queue should process this command?", map[string]string{
			"auto_execute": "Read-only inspection or a non-destructive operation",
			"peer_review":  "Alters a shared environment or force-updates remote history",
			"hard_block":   "Leaks credentials or destroys production state",
			"unrecognized": "Ambiguous or obfuscated",
		}),
		"blast_radius": Score("Score the blast radius of this command.", []string{
			"Strictly read-only and local",
			"Alters local repository state or unmerged history",
			"Mutates canonical shared history or production state",
		}),
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	if resp.Model == "" {
		t.Error("response named no model")
	}
	if resp.Usage.InputTokens == 0 {
		t.Error("response reported no input tokens")
	}

	destructive, err := resp.Answers.Noul("destructive")
	if err != nil {
		t.Fatalf("Noul: %v", err)
	}
	if destructive < 0.5 {
		t.Errorf("a force push scored %.2f for destructive; want above 0.5", destructive)
	}

	routing, confidence, err := resp.Answers.Choice("routing")
	if err != nil {
		t.Fatalf("Choice: %v", err)
	}
	if routing == "auto_execute" {
		t.Errorf("a force push routed to %q at confidence %.2f", routing, confidence)
	}

	score, _, err := resp.Answers.Score("blast_radius")
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if score < 1.0 {
		t.Errorf("a force push scored %.2f for blast radius; want at least the middle level", score)
	}

	t.Logf("destructive=%.2f routing=%s(%.2f) blast=%.2f tokens=%d/%d model=%s",
		destructive, routing, confidence, score,
		resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Model)
}

// TestLiveRejectsOversizedState checks that the size ceiling still arrives as
// a distinguishable error, since callers trim their own input in response.
func TestLiveRejectsOversizedState(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	line := "Person 1 (00:12): We should revisit the retention policy before the release.\n"
	huge := strings.Repeat(line, 2400) // comfortably past the ceiling

	_, err := c.Ask(ctx, map[string]string{"transcript": huge}, Questions{
		"q": Noul("Does this discuss retention?"),
	})
	if err == nil {
		t.Fatal("the service accepted a state past its documented ceiling")
	}
	var apiErr *APIError
	if !asAPIError(err, &apiErr) || apiErr.Kind != "max_tokens_exceeded" {
		t.Fatalf("error = %v; want max_tokens_exceeded", err)
	}
	if apiErr.Retryable() {
		t.Error("an oversized request was reported as retryable")
	}
}

// asAPIError is errors.As, kept local so the test reads plainly.
func asAPIError(err error, target **APIError) bool {
	for err != nil {
		if e, ok := err.(*APIError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
