package transcript

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/decide"
	"github.com/imyousuf/CodeEagle/pkg/jev"
)

// unresolvedOption is the answer that means "the transcript does not say".
//
// A choice with no way out forces the model to spread its belief across
// answers it has already rejected, which inflates every other option and makes
// the confidence figure mean less. Here it is more than a modelling detail: an
// unidentified speaker is a correct outcome, and this is how one is expressed.
const unresolvedOption = "unresolved"

// judgeTranscriptChars bounds the transcript sent to a decision model.
//
// Far smaller than the language model's budget: a decision model holds about
// 32,000 tokens for the state and the longest question together, and a long
// meeting runs well past that. The limit is the service's, not ours, and
// exceeding it is refused outright rather than truncated — so the caller has
// to do the trimming.
const judgeTranscriptChars = 80_000

// judgeRetries is how many times the transcript is halved and the judgment
// retried after the service refuses it as too large.
//
// The character estimate is approximate — tokenization varies with the words —
// so rather than guess a safe size, start generous and halve until it fits. A
// meeting of any realistic length is inside the limit within two or three.
const judgeRetries = 3

// maxIdentityOptions caps how many names a speaker is chosen between.
//
// Every name is a plausible-looking answer, and a long list of them dilutes
// the distribution without adding information — the right answer is usually
// among the few the evidence already points at.
const maxIdentityOptions = 24

// adjudicate resolves speakers with a decision model rather than a language
// model.
//
// The work is split the way each tool is good at. Reading the transcript for
// direct address, self-introduction, thanks and hand-offs is done in code:
// deterministic, free, and already better at tracking twenty labels across an
// hour than a small model is. What is left is a judgment between a handful of
// named candidates, which is what a decision model does — and it returns a
// number that means something, which matters because a speaker only enters the
// graph if that number clears a threshold.
//
// Passing the candidate votes along with the transcript is worth doing rather
// than leaving the model to find the evidence again: on a sample meeting it
// moved a correct identification from 0.58 to 0.98.
func (a *Analyzer) adjudicate(
	ctx context.Context,
	s *Session,
	unresolved []SpeakerEvidence,
	hints []Hint,
	hostAnchored bool,
) ([]SpeakerIdentity, error) {
	options := a.identityOptions(unresolved, hints)
	if len(options) == 0 {
		// Nobody was named anywhere in the meeting, so there is nothing to
		// choose between and no call worth paying for.
		return unidentified(unresolved), nil
	}

	questions := make(map[string]decide.Question, len(unresolved))
	labelFor := make(map[string]string, len(unresolved))
	for i, ev := range unresolved {
		key := fmt.Sprintf("speaker_%d", i)
		labelFor[key] = ev.Stat.Label
		questions[key] = decide.Question{
			Ask: fmt.Sprintf(
				"Which person is the speaker labelled %q? Answer %q unless the "+
					"transcript itself establishes who they are.",
				ev.Stat.Label, unresolvedOption),
			Options: identityCriteria(ev.Stat.Label, options),
		}
	}

	verdicts, err := a.decideWithinLimit(ctx, s, unresolved, hints, questions)
	if err != nil {
		return nil, fmt.Errorf("adjudicate speakers: %w", err)
	}

	identities := make([]SpeakerIdentity, 0, len(unresolved))
	for key, label := range labelFor {
		verdict, ok := verdicts[key]
		if !ok || verdict.Choice == unresolvedOption {
			identities = append(identities, SpeakerIdentity{Label: label, Method: MethodJudge})
			continue
		}

		name := a.sanitizeName(verdict.Choice)
		if hostAnchored && a.isOwnerName(name) {
			// The host is anchored to their own label. Another voice sharing
			// that name is a different person, or a mishearing.
			name = ""
		}
		if name == "" {
			identities = append(identities, SpeakerIdentity{Label: label, Method: MethodJudge})
			continue
		}

		identities = append(identities, SpeakerIdentity{
			Label:      label,
			Name:       name,
			Confidence: verdict.Confidence,
			Evidence:   evidenceFor(unresolved, label, verdict.Choice),
			Method:     MethodJudge,
		})
	}

	// Ordered by label so a re-run writes the graph the same way.
	sort.Slice(identities, func(i, j int) bool { return identities[i].Label < identities[j].Label })
	return resolveCollisions(identities, unresolved), nil
}

// decideWithinLimit asks the judge, trimming the transcript and asking again
// if the service says the request was too large.
//
// A meeting that cannot be judged at all leaves every one of its speakers
// unidentified and the whole recording unenriched, which is a poor trade
// against sending a shorter transcript. The SDK distinguishes this refusal
// from every other precisely so a caller can respond to it.
func (a *Analyzer) decideWithinLimit(
	ctx context.Context,
	s *Session,
	unresolved []SpeakerEvidence,
	hints []Hint,
	questions map[string]decide.Question,
) (map[string]decide.Verdict, error) {
	budget := judgeTranscriptChars

	for attempt := 0; ; attempt++ {
		state := a.identityState(s, unresolved, hints)
		state["transcript"] = trimTranscript(s.Transcript(), budget)

		verdicts, err := a.judge.Decide(ctx, state, questions)
		if err == nil {
			return verdicts, nil
		}
		if !errors.Is(err, jev.ErrTooLarge) || attempt >= judgeRetries {
			return nil, err
		}
		budget /= 2
	}
}

// trimTranscript shortens a transcript to fit, keeping the beginning and the
// end.
//
// Who is who is established early — people greet each other and introduce
// themselves — and again late, when they say goodbye by name. The middle is
// where the work is discussed, which matters for the content pass and far
// less for working out which voice belongs to whom.
func trimTranscript(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	head := limit * 2 / 3
	tail := limit - head
	return text[:head] + "\n\n[...]\n\n" + text[len(text)-tail:]
}

// pointsAt says why a quote implicates the speaker it was counted for.
//
// A quote alone is not evidence of anything: "Thank you, Shaw" names the
// previous speaker, "Shaw, what do you think?" the next one, and the same five
// words serve both. The matcher resolved that direction against the turn order
// when it counted the vote, and dropping the reasoning on the way to the judge
// leaves it to re-derive adjacency from a flattened transcript — which is the
// one thing it reliably cannot do.
func pointsAt(kinds []HintKind) string {
	seen := make(map[HintKind]bool, len(kinds))
	var reasons []string
	for _, k := range kinds {
		if seen[k] {
			continue
		}
		seen[k] = true
		if why := hintImplies[k]; why != "" {
			reasons = append(reasons, why)
		}
	}
	if len(reasons) == 0 {
		return "named nearby in the transcript"
	}
	return strings.Join(reasons, ", and ")
}

// hintImplies explains, per hint kind, whose identity the quote reveals.
var hintImplies = map[HintKind]string{
	HintSelfIntro: "this speaker introduced themselves by this name",
	HintVocative:  "addressed by this name, and answered in the turn that follows",
	HintThanks:    "thanked by this name, having just finished speaking",
	HintGreeting:  "greeted by this name as they joined",
	HintHandoff:   "handed the floor under this name, and spoke next",
}

// identityOptions gathers the names a speaker might be.
//
// Candidates the hint extractor found for any speaker come first, because
// direct evidence beats a name that merely came up; the rest of the meeting's
// roster follows, since someone is often addressed only once and by somebody
// else.
func (a *Analyzer) identityOptions(unresolved []SpeakerEvidence, hints []Hint) []string {
	var ordered []string
	seen := make(map[string]bool)
	add := func(name string) {
		clean := a.sanitizeName(name)
		if clean == "" || a.isOwnerName(clean) {
			// The host is resolved structurally and must not be on offer.
			return
		}
		key := NormalizeName(clean)
		if seen[key] {
			return
		}
		seen[key] = true
		ordered = append(ordered, clean)
	}

	for _, ev := range unresolved {
		for _, vote := range ev.Votes {
			add(vote.Name)
		}
	}
	for _, name := range Roster(hints) {
		add(name)
	}
	// Colleagues from earlier meetings come last, and only fill what is left
	// of the cap. Someone is often present without ever being named aloud —
	// no hint fires, no mention appears — and without them on the list the
	// transcript can make it perfectly clear who is speaking while the answer
	// remains unavailable. They rank behind the evidence because an option
	// with nothing behind it should never displace one that has support.
	for _, name := range a.knownPeople() {
		add(name)
	}

	if len(ordered) > maxIdentityOptions {
		ordered = ordered[:maxIdentityOptions]
	}
	return ordered
}

// identityCriteria turns the candidate names into the options one speaker is
// chosen between, always including a way to decline.
func identityCriteria(label string, names []string) map[string]string {
	options := make(map[string]string, len(names)+1)
	for _, name := range names {
		options[name] = fmt.Sprintf("The transcript establishes that %s is %s", label, name)
	}
	options[unresolvedOption] = fmt.Sprintf(
		"The transcript does not establish who %s is", label)
	return options
}

// identityState is everything the judgment is made against.
//
// Structured rather than prose: the fields are read as separate facts, and the
// transcript stays in one of them so that what people said cannot be mistaken
// for instructions about how to answer.
func (a *Analyzer) identityState(s *Session, unresolved []SpeakerEvidence, hints []Hint) map[string]any {
	state := map[string]any{
		"meeting":    s.Title,
		"date":       s.StartedAt().Format("2006-01-02 15:04"),
		"transcript": a.budgetedTranscript(s.Transcript()),
	}
	if a.opts.Owner != "" {
		state["host"] = fmt.Sprintf(
			"The speaker labelled %q is %s, who made the recording. No other speaker is %s.",
			OwnerLabel, a.opts.Owner, a.opts.Owner)
	}
	if people := a.knownPeople(); len(people) > 0 {
		state["known_colleagues"] = strings.Join(people, ", ")
	}
	if names := Roster(hints); len(names) > 0 {
		state["names_mentioned"] = strings.Join(names, ", ")
	}

	votes := make(map[string]string, len(unresolved))
	for _, ev := range unresolved {
		if len(ev.Votes) == 0 {
			continue
		}
		var b strings.Builder
		for i, v := range ev.Votes {
			if i > 0 {
				b.WriteString("; ")
			}
			fmt.Fprintf(&b, "%s (strength %.1f) — %s: %s",
				v.Name, v.Weight, pointsAt(v.Kinds),
				strings.Join(quoteList(v.Quotes), " | "))
		}
		votes[ev.Stat.Label] = b.String()
	}
	if len(votes) > 0 {
		state["candidate_evidence"] = votes
		state["candidate_evidence_note"] = "Produced by a pattern matcher reading " +
			"direct address, self-introduction, thanks and hand-offs, and resolved " +
			"against the order the turns actually occurred in. Where a quote and " +
			"the direction given for it fit that order, this is reliable — more so " +
			"than re-reading a long transcript to work out who answered whom. It " +
			"misses people who are never addressed by name, and it errs in four " +
			"ways worth rejecting: a product, model or company name read as a " +
			"person; a voice from a recording, video or television playing in the " +
			"room; a name said in negation or doubt (\"I'm not sure if this is X\"); " +
			"and a common word the transcriber capitalised. Judge the candidate on " +
			"whether it names a participant in this meeting, not on whether the " +
			"matcher could in principle be wrong."
	}
	return state
}

// evidenceFor returns the quote that best supports naming a speaker, so the
// graph records why rather than only what.
func evidenceFor(unresolved []SpeakerEvidence, label, name string) string {
	for _, ev := range unresolved {
		if ev.Stat.Label != label {
			continue
		}
		for _, vote := range ev.Votes {
			if !SameName(vote.Name, name) || len(vote.Quotes) == 0 {
				continue
			}
			return vote.Quotes[0]
		}
	}
	return "judged from the transcript"
}

// unidentified marks every speaker as unresolved, for when there was nothing
// to choose between.
func unidentified(unresolved []SpeakerEvidence) []SpeakerIdentity {
	out := make([]SpeakerIdentity, 0, len(unresolved))
	for _, ev := range unresolved {
		out = append(out, SpeakerIdentity{Label: ev.Stat.Label, Method: MethodJudge})
	}
	return out
}
