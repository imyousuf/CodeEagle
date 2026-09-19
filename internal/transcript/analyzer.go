package transcript

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/decide"
	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// Analyzer enriches sessions using an LLM.
//
// Enrichment runs as two passes rather than one. The first works out who was
// speaking; the second reads the transcript with those names substituted in.
// The order matters: "Person 3 will update the schema" is not an assignable
// follow-up, while "Kevin will update the schema" is. Splitting the work also
// keeps each prompt narrow, which is what makes a small, inexpensive model
// reliable enough to run over hundreds of hours of recordings.
type Analyzer struct {
	client llm.Client
	opts   Options
	// judge adjudicates speaker identity when one is configured.
	//
	// Optional, and nil by default: without it identification runs exactly as
	// it always has, through the language model. Its value is the confidence
	// figure — a speaker enters the graph only when confidence clears
	// MinConfidence, and a self-reported number makes that threshold a
	// formality rather than a filter.
	judge decide.Judge
}

// WithJudge returns an analyzer that adjudicates speaker identity with a
// decision model. A nil judge leaves the analyzer unchanged.
func (a *Analyzer) WithJudge(j decide.Judge) *Analyzer {
	if j != nil {
		a.judge = j
	}
	return a
}

// JudgeName names the decision model in use, or "" when there is none.
func (a *Analyzer) JudgeName() string {
	if a.judge == nil {
		return ""
	}
	return a.judge.Name()
}

// Options configures enrichment.
type Options struct {
	// Owner is the person whose microphone made the recordings.
	Owner string
	// OwnerAliases lists other spellings of the owner's name.
	OwnerAliases []string
	// Roster lists people known to attend. Supplying it converts an
	// open-ended guess into a choice among known colleagues.
	Roster []string
	// ExcludeNames lists terms never to treat as people, such as product and
	// team names that read like names in conversation.
	ExcludeNames []string
	// KnownPeople supplies names discovered so far in a batch, so a person
	// recognized in one meeting is a known name for the rest of the run. It
	// may be nil, and is called once per meeting.
	KnownPeople func() []string
	// KnownTopics supplies the topic labels already in use, so meetings about
	// the same subject settle on one label instead of each inventing its own.
	// It may be nil, and is called once per meeting.
	KnownTopics func() []string
	// TopicTaxonomy renders the hierarchy topics are organized into, so a
	// meeting can place a new subject under an existing concept rather than
	// leaving it loose for a later rebuild to sort out. It may be nil.
	TopicTaxonomy func() string
	// MinConfidence is the bar for accepting an identification.
	MinConfidence float64
	// MaxTranscriptChars bounds how much transcript is sent in one request.
	MaxTranscriptChars int
	// BackgroundMinConfidence is the bar for treating a voice as something
	// other than a person. Zero uses DefaultBackgroundConfidence.
	BackgroundMinConfidence float64
	// BackgroundFilter screens out voices that are not people — a television,
	// a demonstrated video — when a judge is configured. On by default.
	BackgroundFilter *bool
}

const (
	// defaultMaxTranscriptChars is generous because the default model holds a
	// million tokens of context and whole-meeting coherence matters more than
	// a marginal saving. Every session in the reference corpus fits well
	// inside it.
	defaultMaxTranscriptChars = 600_000

	// defaultMinConfidence is the bar for automatic identification.
	defaultMinConfidence = 0.70
)

// NewAnalyzer creates an Analyzer.
func NewAnalyzer(client llm.Client, opts Options) *Analyzer {
	if opts.MaxTranscriptChars <= 0 {
		opts.MaxTranscriptChars = defaultMaxTranscriptChars
	}
	if opts.MinConfidence <= 0 {
		opts.MinConfidence = defaultMinConfidence
	}
	return &Analyzer{client: client, opts: opts}
}

// Enrich runs both passes over a session.
func (a *Analyzer) Enrich(ctx context.Context, s *Session) (*Result, error) {
	res := &Result{Session: s}
	if s.IsEmpty() {
		return res, nil
	}

	identities, err := a.Identify(ctx, s, &res.Usage)
	if err != nil {
		return nil, fmt.Errorf("identify speakers: %w", err)
	}
	res.Identities = identities

	analysis, err := a.Analyze(ctx, res, &res.Usage)
	if err != nil {
		return nil, fmt.Errorf("analyze content: %w", err)
	}
	res.Analysis = analysis
	return res, nil
}

// --- Pass 1: identity ---

const identitySystemPrompt = `You work out who was speaking in a meeting transcript.

The recording software separated the voices it heard but does not know who
they belong to. It labelled them "Person 1", "Person 2", and so on. Your job
is to map each label to a real person's name.

Work only from what the transcript actually says:

- A name identifies a speaker only when the transcript shows it refers to that
  specific speaker: they introduce themselves ("this is Saki"), someone
  addresses them and they answer next, or someone thanks them for what they
  just finished saying.
- A name merely being spoken in the room identifies nobody. People discuss
  absent colleagues constantly.
- Product names, company names, team names, and place names are not people.
- Speech recognition mangles names. Prefer a spelling from the known-people
  list when it plainly refers to the same person.

Leaving a speaker unidentified is a correct and expected outcome. A wrong name
is far worse than no name, because it silently attributes one person's words
to another. When the evidence is thin, return an empty name.

Confidence:
  0.9-1.0  explicit self-introduction, or direct address answered immediately
  0.7-0.9  consistent indirect evidence across several exchanges
  0.4-0.7  suggestive but not conclusive
  0.0-0.4  guessing — return an empty name instead`

// Identify resolves diarization labels to people.
func (a *Analyzer) Identify(ctx context.Context, s *Session, usage *Usage) ([]SpeakerIdentity, error) {
	speakers := s.SubstantiveSpeakers()
	if len(speakers) == 0 {
		return nil, nil
	}

	// A conferencing platform writes the names of everyone in the call, so
	// there is nothing to infer. Spending a model call to rediscover what the
	// file states would cost money to produce a worse answer.
	if s.NamedSpeakers {
		return a.identitiesFromTranscript(speakers), nil
	}

	// Screened first, so a television is never offered a colleague's name and
	// its turns never feed the hint extractor.
	background, err := a.screenBackgroundIfEnabled(ctx, s)
	if err != nil {
		return nil, err
	}

	hints := ExtractHints(s)
	evidence := Evidence(s, hints)

	var identities []SpeakerIdentity
	var unresolved []SpeakerEvidence

	for _, ev := range evidence {
		if mark, ok := background[ev.Stat.Label]; ok {
			identities = append(identities, SpeakerIdentity{
				Label:      mark.Label,
				Confidence: mark.Confidence,
				Evidence:   mark.Evidence,
				Method:     MethodBackground,
			})
			continue
		}
		// The microphone owner is known structurally: mic audio is by
		// definition whoever made the recording. Never spend a model call, or
		// risk a model disagreement, on a fact the format guarantees.
		if ev.Stat.IsOwner {
			identities = append(identities, SpeakerIdentity{
				Label:      ev.Stat.Label,
				Name:       a.opts.Owner,
				Confidence: 1.0,
				Evidence:   "microphone audio is the recording owner",
				Method:     MethodOwnerAnchor,
			})
			continue
		}
		unresolved = append(unresolved, ev)
	}

	if len(unresolved) == 0 {
		return identities, nil
	}

	// Whether the recording told us which voice is the host. When it did,
	// every remaining label is by construction somebody else.
	hostAnchored := len(identities) > 0

	if a.judge != nil {
		judged, err := a.adjudicate(ctx, s, unresolved, hints, hostAnchored)
		if err != nil {
			return identities, err
		}
		return append(identities, judged...), nil
	}

	prompt := a.identityPrompt(s, unresolved, hints)
	var out struct {
		Speakers []SpeakerIdentity `json:"speakers"`
	}
	if err := a.chatJSON(ctx, identitySystemPrompt, prompt, identitySchema(), &out, usage); err != nil {
		return identities, err
	}

	known := make(map[string]bool, len(unresolved))
	for _, ev := range unresolved {
		known[ev.Stat.Label] = true
	}
	for _, id := range out.Speakers {
		// A model may echo back a label that was not asked about.
		if !known[id.Label] {
			continue
		}
		id.Name = a.sanitizeName(id.Name)
		if hostAnchored && a.isOwnerName(id.Name) {
			// The host is whoever's microphone made the recording, and that
			// label is already resolved. Another voice claiming the host's
			// name is a different person who happens to share it, or a
			// mishearing — either way, not the host. Accepting it would put a
			// stranger's words in the host's mouth and record them as having
			// attended twice.
			id.Name = ""
		}
		if id.Name == "" {
			id.Confidence = 0
		}
		id.Method = MethodLLM
		identities = append(identities, id)
	}
	return resolveCollisions(identities, evidence), nil
}

// resolveCollisions unresolves speakers that were given the same name while
// speaking at the same time as one another.
//
// One person's label sometimes changes partway through a recording, and both
// halves should resolve to them — but two labels that overlap in time are two
// people, and giving them one name puts one person's words in the other's
// mouth. The evidence for a name is weakest in exactly this case, so the
// project's rule applies: unresolved beats a guess.
//
// Spans that do not overlap are left alone. That is the drift case, and it is
// the one the merge is for.
func resolveCollisions(identities []SpeakerIdentity, evidence []SpeakerEvidence) []SpeakerIdentity {
	spans := make(map[string]SpeakerStat, len(evidence))
	for _, ev := range evidence {
		spans[ev.Stat.Label] = ev.Stat
	}

	// Group the resolved, non-host speakers by the name they were given.
	byName := make(map[string][]int)
	for i, id := range identities {
		if id.Name == "" || id.Method == MethodOwnerAnchor {
			continue
		}
		byName[NormalizeName(id.Name)] = append(byName[NormalizeName(id.Name)], i)
	}

	for _, idx := range byName {
		if len(idx) < 2 {
			continue
		}
		if spansAreDisjoint(idx, identities, spans) {
			continue
		}

		// Keep the best-supported one; the rest lose their name. A tie means
		// nothing distinguishes them, so none of them survives.
		best, tied := bestSupported(idx, identities)
		for _, i := range idx {
			if !tied && i == best {
				continue
			}
			identities[i].Evidence = fmt.Sprintf(
				"collided with another speaker also identified as %s", identities[i].Name)
			identities[i].Name = ""
			identities[i].Confidence = 0
		}
	}
	return identities
}

// spansAreDisjoint reports whether every pair of the given speakers stops
// before the next one starts.
func spansAreDisjoint(idx []int, identities []SpeakerIdentity, spans map[string]SpeakerStat) bool {
	for a := range idx {
		for b := a + 1; b < len(idx); b++ {
			x, okX := spans[identities[idx[a]].Label]
			y, okY := spans[identities[idx[b]].Label]
			if !okX || !okY {
				return false
			}
			if x.LastAt > y.FirstAt && y.LastAt > x.FirstAt {
				return false
			}
		}
	}
	return true
}

// bestSupported returns the index of the most confident identification, and
// whether the best score is shared.
func bestSupported(idx []int, identities []SpeakerIdentity) (best int, tied bool) {
	best = idx[0]
	for _, i := range idx[1:] {
		switch {
		case identities[i].Confidence > identities[best].Confidence:
			best, tied = i, false
		case identities[i].Confidence == identities[best].Confidence:
			tied = true
		}
	}
	return best, tied
}

// screenBackgroundIfEnabled screens for voices that are not people, unless
// the caller turned it off.
func (a *Analyzer) screenBackgroundIfEnabled(ctx context.Context, s *Session) (map[string]BackgroundMark, error) {
	if a.opts.BackgroundFilter != nil && !*a.opts.BackgroundFilter {
		return nil, nil
	}
	return a.screenBackground(ctx, s)
}

// isOwnerName reports whether a name refers to the recording's host.
//
// Checked against the configured aliases too, since the point is to catch a
// name arriving by a route other than the microphone anchor, and transcribers
// spell the same person several ways.
func (a *Analyzer) isOwnerName(name string) bool {
	if name == "" || a.opts.Owner == "" {
		return false
	}
	// A surname settles it. Two people who share a given name and differ in
	// surname are two people, whatever spellings the host goes by — and the
	// aliases are bare given names, which would otherwise match every
	// colleague who happens to share one.
	ownerSurname := Surname(a.opts.Owner)
	if s := Surname(name); s != "" && ownerSurname != "" && s != ownerSurname {
		return false
	}
	if SameName(a.opts.Owner, name) {
		return true
	}
	// Compare given names as well, so an alias recorded as a bare first name
	// still recognizes the host in a transcript that supplies a surname —
	// "Emran Yousuf" when the host is Imran Yousuf known also as Emran.
	given := GivenName(name)
	if SameName(GivenName(a.opts.Owner), given) {
		return true
	}
	for _, alias := range a.opts.OwnerAliases {
		if SameName(alias, given) {
			return true
		}
	}
	return false
}

// identitiesFromTranscript takes the speakers at their word, for a format that
// records who was talking.
//
// The owner is recognized by name rather than by audio source: there is no
// microphone channel in an exported transcript, so the only thing marking the
// recording's owner is that one of the participants is them.
func (a *Analyzer) identitiesFromTranscript(speakers []SpeakerStat) []SpeakerIdentity {
	out := make([]SpeakerIdentity, 0, len(speakers))
	for _, st := range speakers {
		name := a.sanitizeName(st.Label)
		if name == "" {
			// An unattributed voice in an otherwise named transcript stays
			// unidentified rather than being guessed at.
			out = append(out, SpeakerIdentity{Label: st.Label, Method: MethodTranscript})
			continue
		}
		out = append(out, SpeakerIdentity{
			Label:      st.Label,
			Name:       name,
			Confidence: 1.0,
			Evidence:   "the transcript records this speaker's name",
			Method:     MethodTranscript,
		})
	}
	return out
}

// sanitizeName rejects names the model should not have produced: ordinary
// words, honorific fragments, and terms the user has excluded.
func (a *Analyzer) sanitizeName(name string) string {
	cleaned := CleanName(name)
	if cleaned == "" {
		return ""
	}
	for _, ex := range a.opts.ExcludeNames {
		if SameName(ex, cleaned) {
			return ""
		}
	}
	// Prefer the roster's spelling when this is plainly the same person, so
	// one colleague does not accumulate a spelling per meeting.
	//
	// Only when one colleague matches, though. A bare given name matches
	// every colleague who shares it, and taking the first would settle an
	// ambiguity by list order — the same coin toss recorded as a fact that
	// PersonRegistry refuses to make. Left alone, it reaches that refusal
	// and the speaker stays unidentified.
	var matched []string
	for _, known := range a.knownPeople() {
		if SameName(known, cleaned) {
			matched = append(matched, known)
		}
	}
	if len(matched) == 1 {
		return matched[0]
	}
	return cleaned
}

// knownPeople is the owner, the configured roster, and anyone already
// identified during this run, de-duplicated.
func (a *Analyzer) knownPeople() []string {
	var out []string
	seen := make(map[string]bool)
	add := func(name string) {
		key := NormalizeName(name)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, name)
	}

	if a.opts.Owner != "" {
		add(a.opts.Owner)
	}
	for _, n := range a.opts.Roster {
		add(n)
	}
	if a.opts.KnownPeople != nil {
		for _, n := range a.opts.KnownPeople() {
			add(n)
		}
	}

	// A very long list stops being a hint and starts being noise, so the
	// configured roster and owner are kept and the discovered tail is capped.
	const maxKnown = 60
	if len(out) > maxKnown {
		out = out[:maxKnown]
	}
	return out
}

// maxKnownTopics bounds the vocabulary offered back. Past a point the list
// stops being a hint and becomes noise that crowds out the transcript.
const maxKnownTopics = 80

// knownTopics returns the labels already in use, most-used first.
func (a *Analyzer) knownTopics() []string {
	if a.opts.KnownTopics == nil {
		return nil
	}
	topics := a.opts.KnownTopics()
	if len(topics) > maxKnownTopics {
		topics = topics[:maxKnownTopics]
	}
	return topics
}

// identityPrompt assembles the speaker table, the deterministic evidence, and
// the transcript into one request.
func (a *Analyzer) identityPrompt(s *Session, unresolved []SpeakerEvidence, hints []Hint) string {
	var b strings.Builder

	fmt.Fprintf(&b, "MEETING: %s\n", s.Title)
	fmt.Fprintf(&b, "DATE: %s\n", s.StartedAt().Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "LENGTH: %s\n\n", FormatTimestamp(s.DurationSeconds()))

	if people := a.knownPeople(); len(people) > 0 {
		fmt.Fprintf(&b, "KNOWN PEOPLE (prefer these spellings):\n  %s\n\n", strings.Join(people, ", "))
	}
	if a.opts.Owner != "" {
		fmt.Fprintf(&b, "The speaker labelled %q is %s, who made the recording. Do not reassign that label.\n\n",
			OwnerLabel, a.opts.Owner)
	}
	if len(a.opts.ExcludeNames) > 0 {
		fmt.Fprintf(&b, "NOT PEOPLE (products, teams): %s\n\n", strings.Join(a.opts.ExcludeNames, ", "))
	}

	b.WriteString("SPEAKERS TO IDENTIFY:\n")
	for _, ev := range unresolved {
		fmt.Fprintf(&b, "  %s — spoke %s across %d turns (%d words), from %s to %s\n",
			ev.Stat.Label,
			FormatTimestamp(ev.Stat.SpeakingSeconds),
			ev.Stat.Utterances,
			ev.Stat.Words,
			FormatTimestamp(ev.Stat.FirstAt),
			FormatTimestamp(ev.Stat.LastAt))
		for _, v := range ev.Votes {
			fmt.Fprintf(&b, "      candidate %q (evidence strength %.1f) from: %s\n",
				v.Name, v.Weight, strings.Join(quoteList(v.Quotes), " | "))
		}
	}
	b.WriteString("\n")

	if names := Roster(hints); len(names) > 0 {
		fmt.Fprintf(&b, "NAMES MENTIONED ANYWHERE IN THIS MEETING (some are people not present):\n  %s\n\n",
			strings.Join(names, ", "))
	}

	b.WriteString("The candidate lists above come from a pattern matcher that reads direct\n")
	b.WriteString("address, self-introduction, thanks, and hand-offs. It is a starting point,\n")
	b.WriteString("not an answer: it makes mistakes, and it misses people who are never\n")
	b.WriteString("addressed by name. Check every candidate against the transcript, and return\n")
	b.WriteString("an empty name where the transcript does not settle it.\n\n")

	b.WriteString("TRANSCRIPT:\n")
	b.WriteString(a.budgetedTranscript(s.Transcript()))
	return b.String()
}

func quoteList(quotes []string) []string {
	out := make([]string, 0, len(quotes))
	for _, q := range quotes {
		out = append(out, fmt.Sprintf("%q", q))
	}
	return out
}

// identitySchema constrains the identity response.
func identitySchema() *llm.JSONSchema {
	return &llm.JSONSchema{
		Name:   "speaker_identities",
		Strict: true,
		Schema: object(map[string]any{
			"speakers": map[string]any{
				"type": "array",
				"items": object(map[string]any{
					"label": map[string]any{
						"type":        "string",
						"description": "the diarization label being resolved, exactly as given",
					},
					"name": map[string]any{
						"type":        "string",
						"description": "the person's name, or an empty string if the transcript does not identify them",
					},
					"confidence": map[string]any{
						"type":        "number",
						"description": "0 to 1, how strongly the transcript supports this name",
					},
					"evidence": map[string]any{
						"type":        "string",
						"description": "the verbatim quote that justifies the name, or why it is unknown",
					},
				}, "label", "name", "confidence", "evidence"),
			},
		}, "speakers"),
	}
}

// --- Pass 2: content ---

const analysisSystemPrompt = `You analyse a meeting transcript and extract what it would be
useful to remember later.

The transcript is machine-transcribed, so it contains mishearings, false
starts, and filler. Read through that to what people meant.

Ground everything in the transcript:

- Summarize each topic from that topic's point of view — what was said about
  that subject specifically, not a restatement of the meeting as a whole.
  Someone looking up "authentication" should learn what this meeting concluded
  about authentication.
- A topic's NAME is the subject, not a description of this meeting. Write the
  shortest label another meeting about the same thing would also choose: two to
  four words, a noun phrase, no colons or lists. "MCP authentication", not
  "Clarifying Opal MCP/OAuth security answers and token revocation". Everything
  specific to this meeting belongs in the topic's summary, which is what that
  field is for.
- Reuse a label from the known topics list whenever it fits, even if you would
  have phrased it differently. Shared labels are what make it possible to ask
  which meetings covered a subject; a new label for an existing subject makes
  that subject invisible.
- Give each topic a parent: the broader concept it is a facet of, taken from
  the taxonomy shown to you. "OAuth token lifetimes" sits under "OAuth", which
  sits under "Authentication". If nothing in the taxonomy fits, name the parent
  you would add — one concept, two to four words. Leave it empty only when the
  topic genuinely has no broader subject.
- A decision is a choice the group actually settled on, not a suggestion
  someone floated. If it was left open, it is not a decision.
- An action item is something a specific person committed to doing. Assign it
  only to someone who appears in the transcript by name. If nobody clearly
  owns it, leave the assignee empty rather than guessing.
- Quote verbatim. Quotes are what let a reader verify a claim, so they must
  appear in the transcript word for word.
- List systems, services, repositories, and tools that were discussed, using
  the names the speakers used. These connect the meeting to a codebase.

Write a title that says what the meeting was about. Recording software names
meetings things like "Unknown Meeting 2026-08-20 11:33"; yours should be
useful in a list of hundreds.

If the recording is too short or fragmentary to analyse, return empty arrays
rather than inventing content.`

// Analyze extracts topics, decisions, and follow-ups from a session whose
// speakers have already been resolved.
func (a *Analyzer) Analyze(ctx context.Context, res *Result, usage *Usage) (*Analysis, error) {
	s := res.Session
	var b strings.Builder

	fmt.Fprintf(&b, "MEETING DATE: %s\n", s.StartedAt().Format("2006-01-02 15:04 (Monday)"))
	fmt.Fprintf(&b, "LENGTH: %s\n", FormatTimestamp(s.DurationSeconds()))
	if s.Platform != "" && s.Platform != "Unknown" && s.Platform != "None" {
		fmt.Fprintf(&b, "PLATFORM: %s\n", s.Platform)
	}
	if people := res.Participants(a.opts.MinConfidence); len(people) > 0 {
		fmt.Fprintf(&b, "IDENTIFIED PARTICIPANTS: %s\n", strings.Join(people, ", "))
	}
	if topics := a.knownTopics(); len(topics) > 0 {
		fmt.Fprintf(&b, "\nKNOWN TOPICS (reuse these labels where they fit):\n  %s\n",
			strings.Join(topics, "\n  "))
	}
	if a.opts.TopicTaxonomy != nil {
		if tree := strings.TrimSpace(a.opts.TopicTaxonomy()); tree != "" {
			b.WriteString("\nTOPIC TAXONOMY (place each topic under one of these concepts):\n")
			for _, line := range strings.Split(tree, "\n") {
				fmt.Fprintf(&b, "  %s\n", line)
			}
		}
	}
	b.WriteString("\nTimestamps below are mm:ss (or h:mm:ss) from the start of the meeting.\n")
	b.WriteString("Use them for topic start and end times, expressed in seconds.\n\n")
	b.WriteString("TRANSCRIPT:\n")
	b.WriteString(a.budgetedTranscript(res.NamedTranscript(a.opts.MinConfidence)))

	var analysis Analysis
	if err := a.chatJSON(ctx, analysisSystemPrompt, b.String(), analysisSchema(), &analysis, usage); err != nil {
		return nil, err
	}

	if strings.TrimSpace(analysis.Title) == "" {
		analysis.Title = s.Title
	}
	analysis.clampTo(s.DurationSeconds())
	return &analysis, nil
}

// clampTo keeps model-supplied time ranges inside the real recording.
func (a *Analysis) clampTo(duration float64) {
	for i := range a.Topics {
		t := &a.Topics[i]
		if t.StartTime < 0 {
			t.StartTime = 0
		}
		if duration > 0 && t.EndTime > duration {
			t.EndTime = duration
		}
		if t.EndTime < t.StartTime {
			t.EndTime = t.StartTime
		}
	}
	sort.SliceStable(a.Topics, func(i, j int) bool {
		return a.Topics[i].StartTime < a.Topics[j].StartTime
	})
}

// analysisSchema constrains the content response.
func analysisSchema() *llm.JSONSchema {
	strs := func(desc string) map[string]any {
		return map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": desc,
		}
	}
	return &llm.JSONSchema{
		Name:   "meeting_analysis",
		Strict: true,
		Schema: object(map[string]any{
			"title": map[string]any{
				"type":        "string",
				"description": "a descriptive title for this meeting",
			},
			"summary": map[string]any{
				"type":        "string",
				"description": "an overview of the meeting in a few sentences",
			},
			"topics": map[string]any{
				"type": "array",
				"items": object(map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "the subject, as a reusable two-to-four word noun phrase; reuse a known topic label where one fits",
					},
					"parent": map[string]any{
						"type":        "string",
						"description": "the broader concept this is a facet of, from the taxonomy shown, or a new one; empty if it has no broader subject",
					},
					"summary": map[string]any{"type": "string", "description": "what this meeting established about this topic specifically"},
					"start_time": map[string]any{
						"type":        "number",
						"description": "seconds from the start of the meeting where this topic begins",
					},
					"end_time":     map[string]any{"type": "number"},
					"participants": strs("names of people who spoke on this topic"),
					"keywords":     strs("short search terms for this topic"),
				}, "name", "parent", "summary", "start_time", "end_time", "participants", "keywords"),
			},
			"decisions": map[string]any{
				"type": "array",
				"items": object(map[string]any{
					"text":       map[string]any{"type": "string", "description": "the decision reached"},
					"rationale":  map[string]any{"type": "string", "description": "why, if stated"},
					"decided_by": strs("people who made the call"),
					"topic":      map[string]any{"type": "string"},
					"quote":      map[string]any{"type": "string", "description": "verbatim supporting quote"},
				}, "text", "rationale", "decided_by", "topic", "quote"),
			},
			"action_items": map[string]any{
				"type": "array",
				"items": object(map[string]any{
					"text":     map[string]any{"type": "string", "description": "what needs doing"},
					"assignee": map[string]any{"type": "string", "description": "who committed to it, or empty"},
					"due_date": map[string]any{"type": "string", "description": "ISO-8601 date, or empty if none was given"},
					"topic":    map[string]any{"type": "string"},
					"quote":    map[string]any{"type": "string", "description": "verbatim supporting quote"},
				}, "text", "assignee", "due_date", "topic", "quote"),
			},
			"mentions": strs("systems, services, repositories, and tools discussed"),
		}, "title", "summary", "topics", "decisions", "action_items", "mentions"),
	}
}

// --- shared plumbing ---

// object builds a strict JSON Schema object. Strict mode requires every
// property to be listed as required and forbids extras, so the helper makes
// that the default rather than something to remember.
func object(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = make([]string, 0, len(props))
		for k := range props {
			required = append(required, k)
		}
		sort.Strings(required)
	}
	return map[string]any{
		"type":                 "object",
		"properties":           props,
		"required":             required,
		"additionalProperties": false,
	}
}

// chatJSON sends a request and decodes the reply into out.
//
// Providers that can enforce a schema do so. Those that cannot — a local
// Ollama model, for instance — are asked for JSON and their reply is salvaged,
// since small models habitually wrap JSON in prose or a code fence.
func (a *Analyzer) chatJSON(ctx context.Context, system, user string, schema *llm.JSONSchema, out any, usage *Usage) error {
	messages := []llm.Message{{Role: llm.RoleUser, Content: user}}

	var resp *llm.Response
	var err error
	if sc, ok := a.client.(llm.StructuredClient); ok {
		resp, err = sc.ChatJSON(ctx, system, messages, schema)
	} else {
		resp, err = a.client.Chat(ctx, system+"\n\nRespond with JSON only. No prose, no code fences.", messages)
	}
	if err != nil {
		return err
	}
	if usage != nil {
		usage.Add(resp.Usage.InputTokens, resp.Usage.OutputTokens)
	}

	body := extractJSON(resp.Content)
	if body == "" {
		return fmt.Errorf("no JSON in response (%d bytes)", len(resp.Content))
	}
	if err := json.Unmarshal([]byte(body), out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// extractJSON pulls the JSON document out of a reply that may be wrapped in a
// code fence or surrounded by commentary.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if fenced := insideFence(s); fenced != "" {
		s = fenced
	}
	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return ""
	}
	opener := rune(s[start])
	closer := '}'
	if opener == '[' {
		closer = ']'
	}
	// Scan for the matching close, ignoring braces inside string literals.
	depth := 0
	inString := false
	escaped := false
	for i, r := range s[start:] {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && inString:
			escaped = true
		case r == '"':
			inString = !inString
		case inString:
			// Braces inside a string are not structural.
		case r == opener:
			depth++
		case r == closer:
			depth--
			if depth == 0 {
				return s[start : start+i+len(string(r))]
			}
		}
	}
	return s[start:]
}

// insideFence returns the contents of the first fenced code block, if any.
func insideFence(s string) string {
	const fence = "```"
	start := strings.Index(s, fence)
	if start < 0 {
		return ""
	}
	rest := s[start+len(fence):]
	// Drop an optional language tag on the opening fence.
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 && !strings.Contains(rest[:nl], fence) {
		rest = rest[nl+1:]
	}
	if end := strings.Index(rest, fence); end >= 0 {
		return rest[:end]
	}
	return rest
}

// budgetedTranscript keeps a transcript within the request budget.
//
// When a recording is too long, the middle is thinned rather than the tail
// dropped: meetings routinely settle their decisions and hand out follow-ups
// in the closing minutes, so truncating the end discards the most valuable
// part.
func (a *Analyzer) budgetedTranscript(text string) string {
	limit := a.opts.MaxTranscriptChars
	if limit <= 0 || len(text) <= limit {
		return text
	}

	lines := strings.Split(text, "\n")
	headBudget := limit * 40 / 100
	tailBudget := limit * 40 / 100

	var head []string
	used := 0
	for _, ln := range lines {
		if used+len(ln) > headBudget {
			break
		}
		head = append(head, ln)
		used += len(ln) + 1
	}

	var tail []string
	used = 0
	for i := len(lines) - 1; i >= len(head); i-- {
		if used+len(lines[i]) > tailBudget {
			break
		}
		tail = append(tail, lines[i])
		used += len(lines[i]) + 1
	}
	for i, j := 0, len(tail)-1; i < j; i, j = i+1, j-1 {
		tail[i], tail[j] = tail[j], tail[i]
	}

	omitted := len(lines) - len(head) - len(tail)
	var b strings.Builder
	b.WriteString(strings.Join(head, "\n"))
	fmt.Fprintf(&b, "\n\n[... %d turns from the middle of the meeting omitted for length ...]\n\n", omitted)
	b.WriteString(strings.Join(tail, "\n"))
	return b.String()
}
