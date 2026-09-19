package transcript

import (
	"context"
	"fmt"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/decide"
)

// Answers to the question of whether a voice was in the meeting at all.
const (
	optionParticipant = "participant"
	optionMixed       = "mixed"
	optionBackground  = "background_media"
	optionCannotTell  = "cannot_tell"
)

// DefaultBackgroundConfidence is the mass at which a voice is treated as
// something other than a person.
//
// Set from reading every flag the model produced over the whole corpus. Above
// it the judgments were sound; between 0.70 and 0.85 they were not — a real
// presenter running a webinar was flagged at 0.76, because a scripted
// presentation reads like broadcast, and a real participant whose transcription
// was garbled across two languages at 0.68. Both are people.
const DefaultBackgroundConfidence = 0.85

// screenBackground marks speakers that are not people.
//
// A television, a video being demonstrated on a shared screen, a stream left
// running: `IsSubstantive` admits all of them, because it asks how much a
// voice spoke and never what it said. Broadcast audio is loud, continuous and
// grammatical, so it clears that bar more easily than a quiet colleague does,
// and everything downstream then treats it as one — a Speaker node, an
// attendance edge, a candidate for a real person's name, and words that reach
// the content pass where a decision can be quoted from a cookery programme
// with the quote verifying.
//
// Measured over 614 recordings: eleven such labels out of 1,572. Small, but
// each one costs a human being asked to put a name to a racing commentator.
//
// Returns the labels judged to be background, and never asks about the host.
func (a *Analyzer) screenBackground(ctx context.Context, s *Session) (map[string]BackgroundMark, error) {
	if a.judge == nil || s.NamedSpeakers {
		// A platform-named transcript lists the people who were in the call;
		// there is nothing to screen.
		return nil, nil
	}

	hints := ExtractHints(s)
	candidates := a.backgroundCandidates(s, hints)
	if len(candidates) == 0 {
		return nil, nil
	}

	questions := make(map[string]decide.Question, len(candidates))
	labelFor := make(map[string]string, len(candidates))
	for i, c := range candidates {
		key := fmt.Sprintf("voice_%d", i)
		labelFor[key] = c.Stat.Label
		questions[key] = decide.Question{
			Ask: fmt.Sprintf(
				"Was the voice labelled %q a person taking part in this meeting, "+
					"or something else the microphone picked up?", c.Stat.Label),
			Options: map[string]string{
				optionParticipant: "A person taking part in the meeting",
				optionBackground: "Not a person in the meeting at all — a television, a video " +
					"being demonstrated, a podcast, an advertisement, a sports or news " +
					"broadcast, or a conversation elsewhere that the microphone caught",
				optionMixed: "Both: this label holds some meeting speech and some " +
					"background audio that the recorder filed together",
				optionCannotTell: "There is not enough here to tell",
			},
		}
	}

	verdicts, err := a.judge.Decide(ctx, a.backgroundState(s, candidates), questions)
	if err != nil {
		return nil, fmt.Errorf("screen background audio: %w", err)
	}

	marks := make(map[string]BackgroundMark)
	for key, label := range labelFor {
		verdict, ok := verdicts[key]
		if !ok || verdict.Choice != optionBackground {
			// "mixed" is deliberately treated as a participant: the label
			// holds real meeting speech, and dropping it would lose words
			// somebody actually said.
			continue
		}
		if verdict.Confidence < a.backgroundConfidence() {
			continue
		}
		marks[label] = BackgroundMark{
			Label:      label,
			Confidence: verdict.Confidence,
			Evidence:   excerptFor(candidates, label),
		}
	}
	return marks, nil
}

// BackgroundMark records a voice judged not to be a person.
type BackgroundMark struct {
	Label      string
	Confidence float64
	// Evidence is a little of what the voice said, so a human reviewing the
	// mark can see why without opening the transcript.
	Evidence string
}

// backgroundCandidate is one voice and what is known about it.
type backgroundCandidate struct {
	Stat SpeakerStat
	// Answered counts how many times this voice received a name-hint vote —
	// somebody addressed it and it replied.
	Answered int
	// Addressed counts hints this voice itself uttered that resolved to
	// another substantive speaker.
	Addressed int
	// Partners counts the distinct voices it traded turns with.
	Partners int
	Excerpt  []string
}

// backgroundCandidates selects the voices worth asking about, vetoing the ones
// the transcript already proves are people.
//
// The veto matters more than the saving. A voice that was addressed and
// answered, or that traded turns with several others, is taking part in a
// conversation — a television does neither. Both of the model's known mistakes
// fail this veto: the webinar presenter thanked somebody by name, and the
// garbled participant spoke 179 times throughout.
func (a *Analyzer) backgroundCandidates(s *Session, hints []Hint) []backgroundCandidate {
	stats := s.SubstantiveSpeakers()
	evidence := Evidence(s, hints)

	answered := make(map[string]int)
	addressed := make(map[string]int)
	for _, ev := range evidence {
		answered[ev.Stat.Label] = len(ev.Votes)
	}
	for _, h := range hints {
		if h.Speaker != "" {
			addressed[h.Speaker]++
		}
	}
	partners := turnPartners(s)

	var out []backgroundCandidate
	for _, st := range stats {
		if st.IsOwner {
			// The microphone is the host by construction. A television in the
			// host's own room is folded into that one label and no
			// label-level screening can see it.
			continue
		}
		if answered[st.Label] > 0 || addressed[st.Label] > 0 || partners[st.Label] >= 2 {
			continue
		}
		out = append(out, backgroundCandidate{
			Stat:      st,
			Answered:  answered[st.Label],
			Addressed: addressed[st.Label],
			Partners:  partners[st.Label],
			Excerpt:   utterancesOf(s, st.Label, 8),
		})
	}
	return out
}

// turnPartners counts, for each label, how many distinct other labels spoke
// immediately after it or immediately before it.
func turnPartners(s *Session) map[string]int {
	turns := s.Turns()
	partners := make(map[string]map[string]bool)
	note := func(a, b string) {
		if a == "" || b == "" || a == b {
			return
		}
		if partners[a] == nil {
			partners[a] = make(map[string]bool)
		}
		partners[a][b] = true
	}
	for i := 1; i < len(turns); i++ {
		note(turns[i-1].Speaker, turns[i].Speaker)
		note(turns[i].Speaker, turns[i-1].Speaker)
	}

	out := make(map[string]int, len(partners))
	for label, set := range partners {
		out[label] = len(set)
	}
	return out
}

// utterancesOf returns up to n of a label's utterances, spread across the
// recording rather than taken from the start.
func utterancesOf(s *Session, label string, n int) []string {
	var all []string
	for _, seg := range s.Segments {
		if seg.Speaker != label {
			continue
		}
		if text := strings.TrimSpace(seg.Text); text != "" {
			all = append(all, truncate(text, 220))
		}
	}
	if len(all) <= n {
		return all
	}
	out := make([]string, 0, n)
	step := float64(len(all)-1) / float64(n-1)
	for i := range n {
		out = append(out, all[int(float64(i)*step)])
	}
	return out
}

// backgroundState describes each voice, with the numbers computed here rather
// than left to the model.
func (a *Analyzer) backgroundState(s *Session, candidates []backgroundCandidate) map[string]any {
	voices := make(map[string]any, len(candidates))
	for _, c := range candidates {
		span := c.Stat.LastAt - c.Stat.FirstAt
		coverage := 0.0
		if span > 0 {
			coverage = c.Stat.SpeakingSeconds / span
		}
		voices[c.Stat.Label] = map[string]any{
			"speaking_seconds":            fmt.Sprintf("%.0f", c.Stat.SpeakingSeconds),
			"utterances":                  c.Stat.Utterances,
			"first_spoke_at":              FormatTimestamp(c.Stat.FirstAt),
			"last_spoke_at":               FormatTimestamp(c.Stat.LastAt),
			"share_of_its_own_span":       fmt.Sprintf("%.2f", coverage),
			"nobody_replied_to_it":        c.Answered == 0,
			"it_addressed_nobody":         c.Addressed == 0,
			"voices_it_traded_turns_with": c.Partners,
			"said":                        c.Excerpt,
		}
	}

	return map[string]any{
		"situation": "A meeting was recorded. The recorder separated the voices it " +
			"heard, but some of what it heard may not be people in the meeting at " +
			"all — a television, a video being demonstrated on a shared screen, a " +
			"stream left running nearby.",
		"meeting":          s.Title,
		"duration_minutes": fmt.Sprintf("%.0f", s.DurationSeconds()/60),
		"voices":           voices,
		"note": "Every voice here already spoke enough to look like a participant by " +
			"length alone, and none of them was addressed by name or answered. " +
			"Judge by what was said.",
	}
}

// excerptFor returns a little of what a label said, for the mark's evidence.
func excerptFor(candidates []backgroundCandidate, label string) string {
	for _, c := range candidates {
		if c.Stat.Label != label || len(c.Excerpt) == 0 {
			continue
		}
		return truncate(c.Excerpt[0], 160)
	}
	return ""
}

// backgroundConfidence is the configured bar, or the default.
func (a *Analyzer) backgroundConfidence() float64 {
	if a.opts.BackgroundMinConfidence > 0 {
		return a.opts.BackgroundMinConfidence
	}
	return DefaultBackgroundConfidence
}
