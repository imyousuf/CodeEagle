package transcript

import (
	"context"
	"errors"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/decide"
)

// stubJudge answers with canned verdicts and records what it was asked.
type stubJudge struct {
	// verdictFor maps a speaker label to the verdict returned for it.
	verdictFor map[string]decide.Verdict
	err        error

	calls     int
	lastState any
	// asked records the options offered for each label.
	asked map[string]map[string]string
}

func (j *stubJudge) Name() string { return "stub" }

func (j *stubJudge) Decide(
	_ context.Context, state any, questions map[string]decide.Question,
) (map[string]decide.Verdict, error) {
	j.calls++
	j.lastState = state
	if j.err != nil {
		return nil, j.err
	}
	j.asked = make(map[string]map[string]string, len(questions))

	out := make(map[string]decide.Verdict, len(questions))
	for key, q := range questions {
		// Recover the label from the question text, which names it.
		label := labelFromAsk(q.Ask)
		j.asked[label] = q.Options
		if v, ok := j.verdictFor[label]; ok {
			out[key] = v
			continue
		}
		out[key] = decide.Verdict{Choice: unresolvedOption, Confidence: 0.99}
	}
	return out, nil
}

// labelFromAsk pulls the quoted label out of the question.
func labelFromAsk(ask string) string {
	first := -1
	for i, r := range ask {
		if r != '"' {
			continue
		}
		if first < 0 {
			first = i
			continue
		}
		return ask[first+1 : i]
	}
	return ""
}

func judgedSession() *Session {
	return build(
		[3]string{"You", SourceMic, "Morning. Shall we start with the retention job?"},
		[3]string{"Person 1", SourceMonitor, "I looked at it yesterday, the query does a full scan."},
		[3]string{"You", SourceMic, "Right. Kevin, did the composite index get dropped?"},
		[3]string{"Person 1", SourceMonitor, "It did. I can put a migration up today."},
		[3]string{"Person 2", SourceMonitor, "Before we move on, I want to flag the alerting gap."},
		[3]string{"You", SourceMic, "Go ahead, Mona."},
		[3]string{"Person 2", SourceMonitor, "We only hear about it when a customer tells us."},
	)
}

// TestAdjudicateResolvesSpeakers covers the whole path: the host stays
// anchored, a named speaker is accepted with the judge's confidence, and the
// supporting quote is carried onto the identification.
func TestAdjudicateResolvesSpeakers(t *testing.T) {
	judge := &stubJudge{verdictFor: map[string]decide.Verdict{
		"Person 1": {Choice: "Kevin", Confidence: 0.98},
		"Person 2": {Choice: "Mona", Confidence: 0.93},
	}}

	a := NewAnalyzer(nil, Options{Owner: "Imran Yousuf", MinConfidence: 0.7}).WithJudge(judge)
	identities, err := a.Identify(context.Background(), judgedSession(), &Usage{})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}

	byLabel := make(map[string]SpeakerIdentity, len(identities))
	for _, id := range identities {
		byLabel[id.Label] = id
	}

	if host := byLabel["You"]; host.Name != "Imran Yousuf" || host.Method != MethodOwnerAnchor {
		t.Errorf("host = %q via %q, want the owner anchor", host.Name, host.Method)
	}
	if got := byLabel["Person 1"]; got.Name != "Kevin" || got.Confidence != 0.98 {
		t.Errorf("Person 1 = %q at %.2f, want Kevin at 0.98", got.Name, got.Confidence)
	}
	if got := byLabel["Person 1"]; got.Method != MethodJudge {
		t.Errorf("Person 1 resolved via %q, want %q", got.Method, MethodJudge)
	}
	if got := byLabel["Person 1"]; got.Evidence == "" {
		t.Error("Person 1 carries no evidence quote")
	}
	if got := byLabel["Person 2"]; got.Name != "Mona" {
		t.Errorf("Person 2 = %q, want Mona", got.Name)
	}

	if judge.calls != 1 {
		t.Errorf("made %d judgments, want 1: every speaker fits in one request", judge.calls)
	}
}

// TestAdjudicateOffersAWayOut covers the option that lets a speaker stay
// unidentified, and the host never being on offer.
func TestAdjudicateOffersAWayOut(t *testing.T) {
	judge := &stubJudge{verdictFor: map[string]decide.Verdict{}}

	a := NewAnalyzer(nil, Options{Owner: "Imran Yousuf", MinConfidence: 0.7}).WithJudge(judge)
	identities, err := a.Identify(context.Background(), judgedSession(), &Usage{})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}

	options, ok := judge.asked["Person 1"]
	if !ok {
		t.Fatal("Person 1 was never asked about")
	}
	if _, ok := options[unresolvedOption]; !ok {
		t.Errorf("options %v offer no way to decline", options)
	}
	for name := range options {
		if name != unresolvedOption && a.isOwnerName(name) {
			t.Errorf("the host %q was offered as a candidate; the host is already anchored", name)
		}
	}

	for _, id := range identities {
		if id.Label == "Person 1" && id.Name != "" {
			t.Errorf("Person 1 = %q, want unidentified when the judge declines", id.Name)
		}
	}
}

// TestAdjudicateRejectsTheHostsName covers a second voice claiming to be the
// host. The microphone already settled who that is.
func TestAdjudicateRejectsTheHostsName(t *testing.T) {
	judge := &stubJudge{verdictFor: map[string]decide.Verdict{
		"Person 1": {Choice: "Imran", Confidence: 0.96},
	}}

	a := NewAnalyzer(nil, Options{Owner: "Imran Yousuf", MinConfidence: 0.7}).WithJudge(judge)
	identities, err := a.Identify(context.Background(), judgedSession(), &Usage{})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	for _, id := range identities {
		if id.Label == "Person 1" && id.Name != "" {
			t.Errorf("Person 1 = %q; the host is anchored to another label", id.Name)
		}
	}
}

// TestAdjudicateSkipsTheCallWhenNobodyIsNamed covers not paying for a question
// that has no candidate answers.
func TestAdjudicateSkipsTheCallWhenNobodyIsNamed(t *testing.T) {
	judge := &stubJudge{}
	s := build(
		[3]string{"You", SourceMic, "Shall we begin the review of the deployment?"},
		[3]string{"Person 1", SourceMonitor, "Mm-hmm, sounds good to me, let us proceed."},
		[3]string{"Person 1", SourceMonitor, "Yes, agreed, that all seems reasonable enough."},
	)

	a := NewAnalyzer(nil, Options{Owner: "Imran Yousuf", MinConfidence: 0.7}).WithJudge(judge)
	if _, err := a.Identify(context.Background(), s, &Usage{}); err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if judge.calls != 0 {
		t.Errorf("consulted the judge %d times with no candidate names to choose between", judge.calls)
	}
}

// TestAdjudicateReportsFailure covers a judgment that could not be made. A
// speaker silently left unidentified would look like a considered answer.
func TestAdjudicateReportsFailure(t *testing.T) {
	judge := &stubJudge{err: errors.New("service unavailable")}

	a := NewAnalyzer(nil, Options{Owner: "Imran Yousuf", MinConfidence: 0.7}).WithJudge(judge)
	if _, err := a.Identify(context.Background(), judgedSession(), &Usage{}); err == nil {
		t.Fatal("Identify succeeded despite the judge failing")
	}
}

// TestAdjudicateStateCarriesTheEvidence covers passing the deterministic hint
// votes along with the transcript. Measured against the live service, that
// moved a correct identification from 0.58 to 0.98, so it is not incidental.
func TestAdjudicateStateCarriesTheEvidence(t *testing.T) {
	judge := &stubJudge{verdictFor: map[string]decide.Verdict{}}

	a := NewAnalyzer(nil, Options{Owner: "Imran Yousuf", MinConfidence: 0.7}).WithJudge(judge)
	if _, err := a.Identify(context.Background(), judgedSession(), &Usage{}); err != nil {
		t.Fatalf("Identify: %v", err)
	}

	state, ok := judge.lastState.(map[string]any)
	if !ok {
		t.Fatalf("state was %T, want a map of named facts", judge.lastState)
	}
	for _, key := range []string{"transcript", "host", "candidate_evidence"} {
		if _, ok := state[key]; !ok {
			t.Errorf("state carries no %q", key)
		}
	}
	votes, ok := state["candidate_evidence"].(map[string]string)
	if !ok || votes["Person 1"] == "" {
		t.Errorf("no candidate evidence for Person 1: %v", state["candidate_evidence"])
	}
}
