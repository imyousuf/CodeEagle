package transcript

import (
	"fmt"
	"sort"
	"strings"
)

// This file defines what enrichment produces. The shapes here are what the
// graph is built from, and they are also the JSON contract handed to the
// model, so every field is something a reader of the transcript could point
// at rather than something a model would have to invent.

// SpeakerIdentity is the resolution of one diarization label to a person.
type SpeakerIdentity struct {
	// Label is the diarization label being resolved.
	Label string `json:"label"`
	// Name is the person's name, or "" when the transcript does not say.
	Name string `json:"name"`
	// Confidence is how strongly the transcript supports the name, 0..1.
	Confidence float64 `json:"confidence"`
	// Evidence is the verbatim quote that justifies the identification.
	Evidence string `json:"evidence"`
	// Method records how the identification was reached.
	Method string `json:"-"`
}

// Resolution methods, recorded on the graph edge so a reader can tell a
// certainty from a guess.
const (
	// MethodOwnerAnchor means the speaker was the microphone owner. This is
	// structural rather than inferred, and is never wrong.
	MethodOwnerAnchor = "owner_anchor"
	// MethodEvidence means deterministic hint voting was decisive on its own.
	MethodEvidence = "evidence"
	// MethodLLM means a model adjudicated the evidence.
	MethodLLM = "llm"
	// MethodManual means a human assigned the identity.
	MethodManual = "manual"
)

// Topic is a subject the meeting covered, with the span of time spent on it.
type Topic struct {
	Name string `json:"name"`
	// Summary describes the meeting from this topic's point of view: what was
	// said about this subject specifically, not the meeting as a whole.
	Summary      string   `json:"summary"`
	StartTime    float64  `json:"start_time"`
	EndTime      float64  `json:"end_time"`
	Participants []string `json:"participants"`
	Keywords     []string `json:"keywords"`
}

// Decision is a choice the meeting settled on.
type Decision struct {
	Text      string   `json:"text"`
	Rationale string   `json:"rationale"`
	DecidedBy []string `json:"decided_by"`
	Topic     string   `json:"topic"`
	// Quote anchors the decision to the transcript so it can be verified.
	Quote string `json:"quote"`
}

// ActionItem is a follow-up someone committed to.
type ActionItem struct {
	Text     string `json:"text"`
	Assignee string `json:"assignee"`
	// DueDate is ISO-8601, or "" when no date was mentioned.
	DueDate string `json:"due_date"`
	Topic   string `json:"topic"`
	Quote   string `json:"quote"`
}

// Analysis is everything enrichment derives from one meeting.
type Analysis struct {
	// Title is a descriptive name for the meeting. Recorders usually supply a
	// placeholder ("Unknown Meeting 2026-08-20 11:33"), so this is what makes
	// a meeting findable.
	Title string `json:"title"`
	// Summary is an overview of the whole meeting.
	Summary string `json:"summary"`
	// Topics are the subjects covered, each summarized in its own right.
	Topics []Topic `json:"topics"`
	// Decisions are the choices reached.
	Decisions []Decision `json:"decisions"`
	// ActionItems are the follow-ups arising from the meeting.
	ActionItems []ActionItem `json:"action_items"`
	// Mentions are systems, services, and repositories referred to. These are
	// what link a meeting to the code in the graph.
	Mentions []string `json:"mentions"`
}

// Result is the complete enrichment of one session.
type Result struct {
	Session    *Session
	Identities []SpeakerIdentity
	Analysis   *Analysis
	// Usage accumulates token cost across both enrichment passes.
	Usage Usage
}

// Usage tracks token consumption, so a batch run can report what it cost.
type Usage struct {
	InputTokens  int
	OutputTokens int
	Requests     int
}

// Add accumulates another request's usage.
func (u *Usage) Add(in, out int) {
	u.InputTokens += in
	u.OutputTokens += out
	u.Requests++
}

// IdentityFor returns the resolved identity for a label.
func (r *Result) IdentityFor(label string) (SpeakerIdentity, bool) {
	for _, id := range r.Identities {
		if id.Label == label {
			return id, true
		}
	}
	return SpeakerIdentity{}, false
}

// NameMap maps diarization labels to resolved names, including only
// identities that met the confidence bar.
func (r *Result) NameMap(minConfidence float64) map[string]string {
	out := make(map[string]string)
	for _, id := range r.Identities {
		if id.Name != "" && id.Confidence >= minConfidence {
			out[id.Label] = id.Name
		}
	}
	return out
}

// Participants returns the distinct identified people, most talkative first.
func (r *Result) Participants(minConfidence float64) []string {
	names := r.NameMap(minConfidence)
	seconds := make(map[string]float64)
	for _, st := range r.Session.SubstantiveSpeakers() {
		if n, ok := names[st.Label]; ok {
			seconds[n] += st.SpeakingSeconds
		}
	}
	out := make([]string, 0, len(seconds))
	for n := range seconds {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		if seconds[out[i]] != seconds[out[j]] {
			return seconds[out[i]] > seconds[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// NamedTranscript renders the transcript with resolved names in place of
// diarization labels.
//
// This is what makes the second enrichment pass work. "Person 3 will update
// the schema" is not an assignable action item; "Kevin will update the schema"
// is. Unresolved labels are left as they are rather than guessed at.
func (r *Result) NamedTranscript(minConfidence float64) string {
	names := r.NameMap(minConfidence)
	var b strings.Builder
	for _, t := range r.Session.Turns() {
		speaker := t.Speaker
		if n, ok := names[speaker]; ok {
			speaker = n
		}
		fmt.Fprintf(&b, "[%s] %s: %s\n", FormatTimestamp(t.StartTime), speaker, t.Text)
	}
	return b.String()
}

// collapseSpace normalizes runs of whitespace to single spaces, so a quote can
// be matched against a transcript whose line breaks differ.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// stringsContainsFold reports case-insensitive substring containment.
func stringsContainsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// QuoteInTranscript reports whether a supporting quote genuinely appears in
// the session, ignoring whitespace and case. A quote that fails this check was
// invented, and anything resting on it should be treated as unverified.
func QuoteInTranscript(s *Session, quote string) bool {
	if strings.TrimSpace(quote) == "" {
		return false
	}
	return stringsContainsFold(collapseSpace(s.Transcript()), collapseSpace(quote))
}
