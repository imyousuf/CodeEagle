package transcript

import (
	"regexp"
	"sort"
	"strings"
)

// This file harvests the evidence that makes speaker identification possible.
//
// People say each other's names constantly — they introduce themselves, greet,
// thank, and hand off the floor — and each of those carries a *direction*.
// "Kevin, what do you think?" says the next person to speak is Kevin.
// "Thanks, Kevin" says the previous one was. "This is Saki" names the speaker
// themselves. Resolving that direction against the turn order converts loose
// name mentions into votes for specific diarization labels.
//
// We compute these votes deterministically rather than asking a model to do
// it, for three reasons: it is free, it is reproducible, and a small model
// asked to track twenty anonymous labels across an hour of talk loses the
// thread. The model's job is then the narrow one it is good at — judging which
// candidate names are real and resolving the cases evidence leaves ambiguous.

// HintKind classifies how a name was used, which determines who it refers to.
type HintKind string

const (
	// HintSelfIntro is a speaker naming themselves ("this is Saki").
	HintSelfIntro HintKind = "self_intro"
	// HintVocative is addressing someone directly, expecting them to answer.
	HintVocative HintKind = "vocative"
	// HintThanks is thanking someone, who has usually just finished speaking.
	HintThanks HintKind = "thanks"
	// HintGreeting is greeting someone by name.
	HintGreeting HintKind = "greeting"
	// HintHandoff is passing the floor to someone by name.
	HintHandoff HintKind = "handoff"
	// HintThirdPerson refers to someone without addressing them. It identifies
	// nobody, but it populates the roster of names in play.
	HintThirdPerson HintKind = "third_person"
)

// Direction says whose identity a hint reveals, relative to the turn it
// appears in.
type Direction string

const (
	// DirSelf points at the speaker of the turn.
	DirSelf Direction = "self"
	// DirNext points at the next person to speak.
	DirNext Direction = "next"
	// DirPrev points at the previous person to have spoken.
	DirPrev Direction = "prev"
	// DirNone points at nobody in particular.
	DirNone Direction = "none"
)

// hintWeight scores how much each kind of evidence is trusted. Self-
// introductions are near-certain; greetings are weak because people greet a
// room, not always the person who speaks next.
var hintWeight = map[HintKind]float64{
	HintSelfIntro:   4.0,
	HintHandoff:     2.5,
	HintVocative:    2.0,
	HintThanks:      1.5,
	HintGreeting:    0.8,
	HintThirdPerson: 0,
}

// Hint is one name occurrence with its inferred referent.
type Hint struct {
	Name string
	Kind HintKind
	Dir  Direction
	// TurnIndex is the turn the hint was spoken in.
	TurnIndex int
	// Speaker is the label of whoever uttered the hint.
	Speaker string
	// Target is the label the hint points at, once adjacency is resolved.
	// Empty when no turn satisfies the direction.
	Target string
	// Quote is the sentence the name appeared in, for auditing.
	Quote string
	// At is the offset in seconds where the hint was spoken.
	At float64
}

// namePart matches a capitalized token that could be part of a name.
const namePart = `([A-Z][a-z'\-]{1,19}(?:\s+[A-Z][a-z'\-]{1,19})?)`

// Patterns are applied per sentence. The case-insensitive flag is scoped to
// the cue words only — the captured name must be capitalized in the source,
// which is the single cheapest filter available.
var hintPatterns = []struct {
	kind Direction
	hint HintKind
	re   *regexp.Regexp
}{
	// "my name is Saki", "this is Saki", "I'm Saki here"
	{DirSelf, HintSelfIntro, regexp.MustCompile(`(?i:\bmy name(?:'s| is)\s+)` + namePart)},
	{DirSelf, HintSelfIntro, regexp.MustCompile(`(?i:\bthis is\s+)` + namePart + `(?i:\s+(?:from|here|speaking)\b|\.|$)`)},
	{DirSelf, HintSelfIntro, regexp.MustCompile(`(?i:\bi'?m\s+)` + namePart + `(?i:\s+(?:from|here|speaking)\b)`)},

	// "I'll hand it over to John", "over to John", "take it away, John"
	{DirNext, HintHandoff, regexp.MustCompile(`(?i:\b(?:hand(?:ing)?\s+(?:it\s+)?over to|pass(?:ing)?\s+(?:it\s+)?(?:over\s+)?to|turn(?:ing)?\s+it over to|over to|take it away,?)\s+)` + namePart)},

	// "Kevin, what do you think?" — a name at the head of a question.
	{DirNext, HintVocative, regexp.MustCompile(`^` + namePart + `,\s+(?i:what|can|could|do|did|would|will|are|is|were|was|how|any|your|you|should|have|has|want|thoughts)\b`)},
	// "...what do you think, Kevin?" — a name at the tail of a question.
	{DirNext, HintVocative, regexp.MustCompile(`(?i:\b(?:what do you think|any thoughts|does that make sense|right|correct|agree)[,?]?\s+)` + namePart + `\s*\?`)},

	// "Thanks, Kevin" — whoever just spoke.
	{DirPrev, HintThanks, regexp.MustCompile(`(?i:\b(?:thanks|thank you|thank you so much|appreciate it),?\s+)` + namePart)},

	// "Hi Kevin", "welcome Kevin"
	{DirNext, HintGreeting, regexp.MustCompile(`(?i:\b(?:hi|hey|hello|welcome|good morning|good afternoon)[,!]?\s+)` + namePart)},

	// "Kevin's point", "as Kevin mentioned" — names someone without addressing them.
	{DirNone, HintThirdPerson, regexp.MustCompile(namePart + `'s\s+(?i:point|team|comment|question|idea|change|PR|proposal|concern|side|work|suggestion)\b`)},
	{DirNone, HintThirdPerson, regexp.MustCompile(`(?i:\bas\s+)` + namePart + `(?i:\s+(?:said|mentioned|noted|pointed out|suggested|put it))`)},
	{DirNone, HintThirdPerson, regexp.MustCompile(`(?i:\b(?:ask|asked|tell|told|with|and)\s+)` + namePart + `(?i:\s+(?:about|to|if|whether|that))`)},
}

// sentenceSplit breaks a turn into sentences so that anchored patterns (a name
// at the head of a question) can match mid-turn.
var sentenceSplit = regexp.MustCompile(`(?:[.!?]+\s+|\n+)`)

// ExtractHints finds every name occurrence in the session and resolves which
// speaker label each one refers to.
func ExtractHints(s *Session) []Hint {
	turns := s.Turns()
	var hints []Hint

	for i, turn := range turns {
		for _, sentence := range sentenceSplit.Split(turn.Text, -1) {
			sentence = strings.TrimSpace(sentence)
			if sentence == "" {
				continue
			}
			for _, p := range hintPatterns {
				for _, m := range p.re.FindAllStringSubmatch(sentence, -1) {
					name := CleanName(m[1])
					if name == "" {
						continue
					}
					hints = append(hints, Hint{
						Name:      name,
						Kind:      p.hint,
						Dir:       p.kind,
						TurnIndex: i,
						Speaker:   turn.Speaker,
						Quote:     truncate(sentence, 200),
						At:        turn.StartTime,
					})
				}
			}
		}
	}

	resolveTargets(hints, turns)
	return hints
}

// adjacencyWindow bounds how far ahead or behind a directional hint may look
// for its referent. Beyond a couple of turns the inference stops holding:
// someone addressed may be interrupted, or simply not answer.
const adjacencyWindow = 3

// adjacencyMaxGap is the longest silence, in seconds, across which a
// directional hint is still believed.
const adjacencyMaxGap = 45.0

// resolveTargets fills in each hint's Target by walking the turn order in the
// hint's direction until it finds a different speaker.
func resolveTargets(hints []Hint, turns []Turn) {
	for i := range hints {
		h := &hints[i]
		switch h.Dir {
		case DirSelf:
			h.Target = h.Speaker
		case DirNext:
			h.Target = findAdjacentSpeaker(turns, h.TurnIndex, +1)
		case DirPrev:
			h.Target = findAdjacentSpeaker(turns, h.TurnIndex, -1)
		}
	}
}

// findAdjacentSpeaker returns the label of the nearest speaker in the given
// direction who differs from the speaker at idx.
func findAdjacentSpeaker(turns []Turn, idx, step int) string {
	if idx < 0 || idx >= len(turns) {
		return ""
	}
	origin := turns[idx]
	for n, j := 0, idx+step; n < adjacencyWindow && j >= 0 && j < len(turns); n, j = n+1, j+step {
		cand := turns[j]
		if cand.Speaker == origin.Speaker {
			continue
		}
		gap := cand.StartTime - origin.EndTime
		if step < 0 {
			gap = origin.StartTime - cand.EndTime
		}
		if gap > adjacencyMaxGap {
			return ""
		}
		return cand.Speaker
	}
	return ""
}

// NameVote is an accumulated claim that one speaker label belongs to one name.
type NameVote struct {
	Name   string
	Weight float64
	// Kinds records which kinds of evidence contributed, strongest first.
	Kinds []HintKind
	// Quotes are the supporting utterances, most useful first.
	Quotes []string
}

// SpeakerEvidence gathers everything known about one diarization label.
type SpeakerEvidence struct {
	Stat SpeakerStat
	// Votes are candidate names ordered by descending weight.
	Votes []NameVote
}

// Best returns the highest-weighted candidate name, and whether one exists.
func (e SpeakerEvidence) Best() (NameVote, bool) {
	if len(e.Votes) == 0 {
		return NameVote{}, false
	}
	return e.Votes[0], true
}

// Margin reports how far ahead the leading candidate is over the runner-up.
// A large margin means the evidence is not merely present but decisive.
func (e SpeakerEvidence) Margin() float64 {
	switch len(e.Votes) {
	case 0:
		return 0
	case 1:
		return e.Votes[0].Weight
	default:
		return e.Votes[0].Weight - e.Votes[1].Weight
	}
}

// Evidence aggregates hints into per-speaker name votes, merging candidate
// names that differ only by transcription.
func Evidence(s *Session, hints []Hint) []SpeakerEvidence {
	byLabel := make(map[string]*SpeakerEvidence)
	for _, st := range s.SubstantiveSpeakers() {
		byLabel[st.Label] = &SpeakerEvidence{Stat: st}
	}

	for _, h := range hints {
		w := hintWeight[h.Kind]
		if w == 0 || h.Target == "" {
			continue
		}
		ev, ok := byLabel[h.Target]
		if !ok {
			continue
		}
		// A speaker naming themselves in the third person is a misparse.
		if h.Kind == HintThirdPerson {
			continue
		}
		addVote(ev, h.Name, w, h.Kind, h.Quote)
	}

	out := make([]SpeakerEvidence, 0, len(byLabel))
	for _, ev := range byLabel {
		sort.SliceStable(ev.Votes, func(i, j int) bool {
			return ev.Votes[i].Weight > ev.Votes[j].Weight
		})
		out = append(out, *ev)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Stat.SpeakingSeconds > out[j].Stat.SpeakingSeconds
	})
	return out
}

// addVote records a weighted vote, folding it into an existing candidate when
// the names match as transcription variants.
func addVote(ev *SpeakerEvidence, name string, w float64, kind HintKind, quote string) {
	for i := range ev.Votes {
		if SameName(ev.Votes[i].Name, name) {
			ev.Votes[i].Weight += w
			ev.Votes[i].Kinds = append(ev.Votes[i].Kinds, kind)
			if len(ev.Votes[i].Quotes) < 4 {
				ev.Votes[i].Quotes = append(ev.Votes[i].Quotes, quote)
			}
			return
		}
	}
	ev.Votes = append(ev.Votes, NameVote{
		Name:   name,
		Weight: w,
		Kinds:  []HintKind{kind},
		Quotes: []string{quote},
	})
}

// Roster returns every plausible name mentioned anywhere in the session,
// ordered by how often it came up. These are the people in play — useful both
// as a closed set of options for the model and for resolving who an action
// item was assigned to.
func Roster(hints []Hint) []string {
	counts := make(map[string]int)
	canonical := make(map[string]string)
	for _, h := range hints {
		key := ""
		for existing := range canonical {
			if SameName(existing, h.Name) {
				key = existing
				break
			}
		}
		if key == "" {
			key = NormalizeName(h.Name)
			canonical[key] = h.Name
		}
		counts[key]++
	}

	names := make([]string, 0, len(counts))
	for k := range counts {
		names = append(names, canonical[k])
	}
	sort.Slice(names, func(i, j int) bool {
		ci, cj := counts[NormalizeName(names[i])], counts[NormalizeName(names[j])]
		if ci != cj {
			return ci > cj
		}
		return names[i] < names[j]
	})
	return names
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
