package relations

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/imyousuf/CodeEagle/internal/search"
)

// Signal is a bit set of the signals that proposed a candidate.
type Signal uint8

// Signals that propose a (document, meeting) candidate.
const (
	// SignalSaid: a section and a segment summary are near in meaning.
	SignalSaid Signal = 1 << iota
	// SignalName: the document's title or a distinctive heading is named in
	// what the meeting said.
	SignalName
	// SignalIDs: the two share an identifier (issue key, URL, file name).
	SignalIDs
	// SignalTopic: the document and a segment were filed under one topic.
	SignalTopic
)

var signalNames = []struct {
	s    Signal
	name string
}{
	{SignalSaid, "said"}, {SignalName, "name"}, {SignalIDs, "ids"}, {SignalTopic, "topic"},
}

// Names lists the signals in s.
func (s Signal) Names() []string {
	var out []string
	for _, sn := range signalNames {
		if s&sn.s != 0 {
			out = append(out, sn.name)
		}
	}
	return out
}

// Vectors looks up a node's stored vector.
type Vectors interface {
	Vector(nodeID string) ([]float32, bool)
}

// Options bound a proposal run.
type Options struct {
	// K is how many nearest neighbours each section and segment proposes.
	K int
	// MaxPerSubject caps the meetings proposed for one document.
	MaxPerSubject int
	// MaxPerMeeting caps the documents proposed for one meeting.
	MaxPerMeeting int
	// MaxPairs caps the candidates proposed in one run.
	MaxPairs int
}

// DefaultOptions are the caps the spec sets.
func DefaultOptions() Options {
	return Options{K: 5, MaxPerSubject: 8, MaxPerMeeting: 8, MaxPairs: 200}
}

// Candidate is a proposed (document, meeting) pair and why it was proposed.
type Candidate struct {
	Subject   *Subject
	Meeting   *Meeting
	Signals   Signal
	Cosine    float64
	NameScore float64
	Names     []string
	IDs       []string
	TopicHits int
	// Sections and Segments are the matched parts, closest first, kept to a
	// few: they are what the judge is shown.
	Sections []*Section
	Segments []*Segment
	Score    float64
}

// Key identifies the pair.
func (c *Candidate) Key() string { return c.Subject.Path + "\x00" + c.Meeting.SessionID }

// SigDoc is the signature of the parts of the document the pair rests on. A
// change to any of them makes the pair worth judging again; a change elsewhere
// in the document does not.
func (c *Candidate) SigDoc() string {
	var parts []string
	for _, s := range c.Sections {
		parts = append(parts, s.HeadingPath+":"+s.Hash)
	}
	if len(parts) == 0 {
		parts = append(parts, c.Subject.Hash)
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:8])
}

// SigMeeting is the signature of the meeting's transcript. Re-enriching a
// meeting does not change it, so a re-enrichment does not set off re-judging.
func (c *Candidate) SigMeeting() string {
	if c.Meeting.ContentHash != "" {
		return c.Meeting.ContentHash
	}
	return c.Meeting.NodeID
}

// Proposal is the outcome of a proposal run.
type Proposal struct {
	// Candidates are those selected for judging, best first.
	Candidates []*Candidate
	// Proposed counts candidates before any were set aside.
	Proposed int
	// BySignal counts proposed candidates per signal.
	BySignal map[string]int
	// AlreadyJudged counts candidates set aside because the pair was judged
	// under the same signatures.
	AlreadyJudged int
	// CutByCaps counts candidates set aside by the per-subject, per-meeting
	// or per-run caps.
	CutByCaps int
}

// Proposer proposes candidates over a loaded corpus.
type Proposer struct {
	subjects []*Subject
	bySubj   map[string]*Subject
	meetings map[string]*Meeting
	vec      Vectors
	opts     Options

	once     sync.Once
	segVecs  []segVec
	secVecs  []secVec
	phrases  map[*Subject][]phrase
	fuzzy    *fuzzyIndex
	postings map[string][]*Segment
	segIDs   map[*Segment]map[string]bool
	subjIDs  map[*Subject]map[string]bool
}

type segVec struct {
	seg *Segment
	v   []float32
}

type secVec struct {
	subj *Subject
	sec  *Section
	v    []float32
}

// phrase is a title or heading worth looking for in what a meeting said.
type phrase struct {
	text     string
	tokens   []string
	weights  []float64
	required []bool
	section  *Section
}

// NewProposer prepares a proposer. vec may be nil, in which case the said
// signal does not run.
func NewProposer(subjects []*Subject, meetings map[string]*Meeting, vec Vectors, opts Options) *Proposer {
	if opts.K <= 0 {
		opts.K = DefaultOptions().K
	}
	by := make(map[string]*Subject, len(subjects))
	for _, s := range subjects {
		by[s.Path] = s
	}
	return &Proposer{subjects: subjects, bySubj: by, meetings: meetings, vec: vec, opts: opts}
}

// ForSubjects proposes meetings for the given documents, as after they change.
func (p *Proposer) ForSubjects(paths ...string) []*Candidate {
	p.prepare()
	var subjects []*Subject
	for _, path := range paths {
		if s, ok := p.bySubj[path]; ok {
			subjects = append(subjects, s)
		}
	}
	agg := newAggregate()
	p.saidFromSections(subjects, agg)
	for _, s := range subjects {
		p.nameMatches(s, nil, agg)
	}
	p.idMatches(subjects, nil, agg)
	p.topicMatches(subjects, nil, agg)
	return agg.list()
}

// ForMeetings proposes documents for the given meetings, as after they are
// indexed.
func (p *Proposer) ForMeetings(sessionIDs ...string) []*Candidate {
	p.prepare()
	var meetings []*Meeting
	for _, id := range sessionIDs {
		if m, ok := p.meetings[id]; ok {
			meetings = append(meetings, m)
		}
	}
	agg := newAggregate()
	p.saidFromSegments(meetings, agg)
	for _, s := range p.subjects {
		p.nameMatches(s, meetings, agg)
	}
	p.idMatches(p.subjects, meetings, agg)
	p.topicMatches(p.subjects, meetings, agg)
	return agg.list()
}

// All proposes for every document, as a full or first pass.
func (p *Proposer) All() []*Candidate {
	paths := make([]string, len(p.subjects))
	for i, s := range p.subjects {
		paths[i] = s.Path
	}
	return p.ForSubjects(paths...)
}

// Select scores candidates, sets aside pairs already judged, and applies the
// caps, best first.
func (p *Proposer) Select(cands []*Candidate, judged func(*Candidate) bool) *Proposal {
	prop := &Proposal{Proposed: len(cands), BySignal: make(map[string]int)}
	for _, c := range cands {
		c.Score = score(c)
		for _, n := range c.Signals.Names() {
			prop.BySignal[n]++
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].Score != cands[j].Score {
			return cands[i].Score > cands[j].Score
		}
		return cands[i].Key() < cands[j].Key()
	})
	perSubject := make(map[string]int)
	perMeeting := make(map[string]int)
	for _, c := range cands {
		if judged != nil && judged(c) {
			prop.AlreadyJudged++
			continue
		}
		if (p.opts.MaxPerSubject > 0 && perSubject[c.Subject.Path] >= p.opts.MaxPerSubject) ||
			(p.opts.MaxPerMeeting > 0 && perMeeting[c.Meeting.SessionID] >= p.opts.MaxPerMeeting) ||
			(p.opts.MaxPairs > 0 && len(prop.Candidates) >= p.opts.MaxPairs) {
			prop.CutByCaps++
			continue
		}
		perSubject[c.Subject.Path]++
		perMeeting[c.Meeting.SessionID]++
		prop.Candidates = append(prop.Candidates, c)
	}
	return prop
}

// score ranks candidates. Nearness in meaning leads; a named title, a shared
// identifier or topic, and the document changing near the meeting add to it.
// A candidate no vector proposed starts from a floor, so a title said aloud
// still competes.
func score(c *Candidate) float64 {
	s := 0.3
	if c.Signals&SignalSaid != 0 {
		s = c.Cosine
	}
	s += 0.25 * c.NameScore
	s += 0.15 * math.Min(float64(len(c.IDs)), 2)
	if c.TopicHits > 0 {
		s += 0.1
	}
	if timeNear(c.Subject.UpdatedAt, c.Meeting.Date) {
		s += 0.05
	}
	return s
}

// --- preparation ---

func (p *Proposer) prepare() {
	p.once.Do(func() {
		p.loadVectors()
		p.buildPhrases()
		p.buildTargets()
	})
}

func (p *Proposer) loadVectors() {
	if p.vec == nil {
		return
	}
	for _, s := range p.subjects {
		for _, sec := range s.Sections {
			if v, ok := p.vec.Vector(sec.NodeID); ok {
				p.secVecs = append(p.secVecs, secVec{subj: s, sec: sec, v: normalize(v)})
			}
		}
	}
	for _, m := range p.meetings {
		for _, seg := range m.Segments {
			if v, ok := p.vec.Vector(seg.NodeID); ok {
				p.segVecs = append(p.segVecs, segVec{seg: seg, v: normalize(v)})
			}
		}
	}
}

// buildPhrases picks each document's title and distinctive headings. A word
// in the titles and headings of many documents ("overview", "goals") is
// generic and cannot carry a match on its own.
func (p *Proposer) buildPhrases() {
	type raw struct {
		text string
		sec  *Section
	}
	rawBy := make(map[*Subject][]raw)
	df := make(map[string]int)
	for _, s := range p.subjects {
		seen := make(map[string]bool)
		add := func(text string, sec *Section) {
			rawBy[s] = append(rawBy[s], raw{text, sec})
			for _, t := range search.QueryTerms(text) {
				if !seen[t] {
					seen[t] = true
					df[t]++
				}
			}
		}
		add(s.Title, nil)
		for _, sec := range s.Sections {
			if sec.Heading != s.Title {
				add(sec.Heading, sec)
			}
		}
	}
	n := float64(len(p.subjects))
	genericAt := math.Max(2, 0.1*n)
	p.phrases = make(map[*Subject][]phrase)
	for s, raws := range rawBy {
		for _, r := range raws {
			terms := search.QueryTerms(r.text)
			ph := phrase{text: r.text, section: r.sec}
			anyRequired := false
			for _, t := range terms {
				ph.tokens = append(ph.tokens, t)
				ph.weights = append(ph.weights, math.Log((n+1)/float64(df[t]+1))+1)
				req := float64(df[t]) <= genericAt
				ph.required = append(ph.required, req)
				anyRequired = anyRequired || req
			}
			// One word names something only if it is long and rare.
			if !anyRequired || (len(terms) == 1 && (len(terms[0]) < 6 || !ph.required[0])) {
				continue
			}
			p.phrases[s] = append(p.phrases[s], ph)
		}
	}
}

// buildTargets indexes the words of everything meetings said that names could
// be matched against: segment names, summaries and keywords, and the meeting's
// title and mentions.
func (p *Proposer) buildTargets() {
	p.fuzzy = newFuzzyIndex()
	p.postings = make(map[string][]*Segment)
	p.segIDs = make(map[*Segment]map[string]bool)
	p.subjIDs = make(map[*Subject]map[string]bool)
	for _, m := range p.meetings {
		meetingText := m.Title + "\n" + m.Mentions
		for _, seg := range m.Segments {
			text := seg.Name + "\n" + seg.Summary + "\n" + seg.Keywords + "\n" + meetingText
			seen := make(map[string]bool)
			for _, t := range search.Tokenize(text) {
				if !seen[t] {
					seen[t] = true
					p.fuzzy.add(t)
					p.postings[t] = append(p.postings[t], seg)
				}
			}
			p.segIDs[seg] = identifiers(text)
		}
	}
	for _, s := range p.subjects {
		var b strings.Builder
		b.WriteString(s.Title)
		for _, sec := range s.Sections {
			b.WriteString("\n")
			b.WriteString(sec.Text)
		}
		p.subjIDs[s] = identifiers(b.String())
	}
}

// --- signals ---

// saidFromSections proposes, for each section, the segments nearest in
// meaning.
func (p *Proposer) saidFromSections(subjects []*Subject, agg *aggregate) {
	if len(p.segVecs) == 0 {
		return
	}
	want := make(map[*Subject]bool, len(subjects))
	for _, s := range subjects {
		want[s] = true
	}
	var secs []secVec
	for _, sv := range p.secVecs {
		if want[sv.subj] {
			secs = append(secs, sv)
		}
	}
	type hit struct {
		sv  secVec
		seg *Segment
		cos float64
	}
	results := make([][]hit, len(secs))
	parallel(len(secs), func(i int) {
		top := newTopK(p.opts.K)
		for j, gv := range p.segVecs {
			top.offer(j, dot(secs[i].v, gv.v))
		}
		for _, e := range top.items() {
			results[i] = append(results[i], hit{secs[i], p.segVecs[e.idx].seg, e.score})
		}
	})
	for _, hits := range results {
		for _, h := range hits {
			agg.said(h.sv.subj, h.seg.meeting, h.sv.sec, h.seg, h.cos)
		}
	}
}

// saidFromSegments proposes, for each segment of the given meetings, the
// sections nearest in meaning.
func (p *Proposer) saidFromSegments(meetings []*Meeting, agg *aggregate) {
	if len(p.secVecs) == 0 {
		return
	}
	want := make(map[*Meeting]bool, len(meetings))
	for _, m := range meetings {
		want[m] = true
	}
	var segs []segVec
	for _, gv := range p.segVecs {
		if want[gv.seg.meeting] {
			segs = append(segs, gv)
		}
	}
	for _, gv := range segs {
		top := newTopK(p.opts.K)
		for j, sv := range p.secVecs {
			top.offer(j, dot(gv.v, sv.v))
		}
		for _, e := range top.items() {
			sv := p.secVecs[e.idx]
			agg.said(sv.subj, gv.seg.meeting, sv.sec, gv.seg, e.score)
		}
	}
}

// nameMatches proposes the meetings whose segments name one of the subject's
// phrases: every distinctive word present, allowing one edit in long words.
// meetings limits the search when given.
func (p *Proposer) nameMatches(s *Subject, meetings []*Meeting, agg *aggregate) {
	var only map[*Meeting]bool
	if meetings != nil {
		only = make(map[*Meeting]bool, len(meetings))
		for _, m := range meetings {
			only[m] = true
		}
	}
	for _, ph := range p.phrases[s] {
		// Segments containing each required word (or a near spelling of it).
		var candidates map[*Segment]bool
		for i, tok := range ph.tokens {
			if !ph.required[i] {
				continue
			}
			hits := make(map[*Segment]bool)
			for _, w := range p.fuzzy.near(tok) {
				for _, seg := range p.postings[w] {
					hits[seg] = true
				}
			}
			if candidates == nil {
				candidates = hits
			} else {
				for seg := range candidates {
					if !hits[seg] {
						delete(candidates, seg)
					}
				}
			}
			if len(candidates) == 0 {
				break
			}
		}
		for seg := range candidates {
			if only != nil && !only[seg.meeting] {
				continue
			}
			agg.name(s, seg.meeting, ph, seg, p.phraseScore(ph, seg))
		}
	}
}

// phraseScore is the share of a phrase's weight found in the segment.
func (p *Proposer) phraseScore(ph phrase, seg *Segment) float64 {
	var got, total float64
	for i, tok := range ph.tokens {
		total += ph.weights[i]
		for _, w := range p.fuzzy.near(tok) {
			if containsSeg(p.postings[w], seg) {
				got += ph.weights[i]
				break
			}
		}
	}
	if total == 0 {
		return 0
	}
	return got / total
}

func containsSeg(list []*Segment, seg *Segment) bool {
	for _, s := range list {
		if s == seg {
			return true
		}
	}
	return false
}

// idMatches proposes pairs that share an identifier. One shared by more than
// a few documents ("readme.md") identifies nothing.
func (p *Proposer) idMatches(subjects []*Subject, meetings []*Meeting, agg *aggregate) {
	docFreq := make(map[string]int)
	for _, ids := range p.subjIDs {
		for id := range ids {
			docFreq[id]++
		}
	}
	var only map[*Meeting]bool
	if meetings != nil {
		only = make(map[*Meeting]bool, len(meetings))
		for _, m := range meetings {
			only[m] = true
		}
	}
	for _, s := range subjects {
		ids := p.subjIDs[s]
		if len(ids) == 0 {
			continue
		}
		for seg, segIDs := range p.segIDs {
			if only != nil && !only[seg.meeting] {
				continue
			}
			for id := range segIDs {
				if ids[id] && docFreq[id] <= 3 {
					agg.id(s, seg.meeting, seg, id)
				}
			}
		}
	}
}

// topicMatches proposes pairs whose document and segment share a topic.
func (p *Proposer) topicMatches(subjects []*Subject, meetings []*Meeting, agg *aggregate) {
	var only map[*Meeting]bool
	if meetings != nil {
		only = make(map[*Meeting]bool, len(meetings))
		for _, m := range meetings {
			only[m] = true
		}
	}
	for _, s := range subjects {
		if len(s.Topics) == 0 {
			continue
		}
		topics := make(map[string]bool, len(s.Topics))
		for _, t := range s.Topics {
			topics[t] = true
		}
		for _, m := range p.meetings {
			if only != nil && !only[m] {
				continue
			}
			for _, seg := range m.Segments {
				for _, t := range seg.Topics {
					if topics[t] {
						agg.topic(s, m, seg)
					}
				}
			}
		}
	}
}

// --- aggregation ---

type aggregate struct {
	byKey map[string]*Candidate
	order []*Candidate
}

func newAggregate() *aggregate { return &aggregate{byKey: make(map[string]*Candidate)} }

func (a *aggregate) get(s *Subject, m *Meeting) *Candidate {
	key := s.Path + "\x00" + m.SessionID
	if c, ok := a.byKey[key]; ok {
		return c
	}
	c := &Candidate{Subject: s, Meeting: m}
	a.byKey[key] = c
	a.order = append(a.order, c)
	return c
}

func (a *aggregate) said(s *Subject, m *Meeting, sec *Section, seg *Segment, cos float64) {
	c := a.get(s, m)
	c.Signals |= SignalSaid
	if cos > c.Cosine {
		c.Cosine = cos
		// The closest pair leads what the judge is shown.
		c.Sections = prependSection(c.Sections, sec)
		c.Segments = prependSegment(c.Segments, seg)
		return
	}
	c.Sections = appendSection(c.Sections, sec)
	c.Segments = appendSegment(c.Segments, seg)
}

func (a *aggregate) name(s *Subject, m *Meeting, ph phrase, seg *Segment, sc float64) {
	c := a.get(s, m)
	c.Signals |= SignalName
	if sc > c.NameScore {
		c.NameScore = sc
	}
	if !contains(c.Names, ph.text) {
		c.Names = append(c.Names, ph.text)
	}
	if ph.section != nil {
		c.Sections = appendSection(c.Sections, ph.section)
	}
	c.Segments = appendSegment(c.Segments, seg)
}

func (a *aggregate) id(s *Subject, m *Meeting, seg *Segment, id string) {
	c := a.get(s, m)
	c.Signals |= SignalIDs
	if !contains(c.IDs, id) {
		c.IDs = append(c.IDs, id)
	}
	c.Segments = appendSegment(c.Segments, seg)
}

func (a *aggregate) topic(s *Subject, m *Meeting, seg *Segment) {
	c := a.get(s, m)
	c.Signals |= SignalTopic
	c.TopicHits++
	c.Segments = appendSegment(c.Segments, seg)
}

func (a *aggregate) list() []*Candidate { return a.order }

// maxParts bounds the sections and segments kept per candidate.
const maxParts = 3

func prependSection(list []*Section, s *Section) []*Section {
	out := []*Section{s}
	for _, x := range list {
		if x != s && len(out) < maxParts {
			out = append(out, x)
		}
	}
	return out
}

func appendSection(list []*Section, s *Section) []*Section {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	if len(list) >= maxParts {
		return list
	}
	return append(list, s)
}

func prependSegment(list []*Segment, s *Segment) []*Segment {
	out := []*Segment{s}
	for _, x := range list {
		if x != s && len(out) < maxParts {
			out = append(out, x)
		}
	}
	return out
}

func appendSegment(list []*Segment, s *Segment) []*Segment {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	if len(list) >= maxParts {
		return list
	}
	return append(list, s)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// --- vector helpers ---

func normalize(v []float32) []float32 {
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	out := make([]float32, len(v))
	if n == 0 {
		return out
	}
	inv := float32(1 / math.Sqrt(n))
	for i, x := range v {
		out[i] = x * inv
	}
	return out
}

func dot(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return float64(s)
}

type scored struct {
	idx   int
	score float64
}

// topK keeps the k highest scores seen.
type topK struct {
	k    int
	best []scored
}

func newTopK(k int) *topK { return &topK{k: k} }

func (t *topK) offer(idx int, score float64) {
	if len(t.best) < t.k {
		t.best = append(t.best, scored{idx, score})
		return
	}
	low := 0
	for i := 1; i < len(t.best); i++ {
		if t.best[i].score < t.best[low].score {
			low = i
		}
	}
	if score > t.best[low].score {
		t.best[low] = scored{idx, score}
	}
}

func (t *topK) items() []scored {
	sort.Slice(t.best, func(i, j int) bool { return t.best[i].score > t.best[j].score })
	return t.best
}

// parallel runs fn for 0..n-1 across the CPUs.
func parallel(n int, fn func(int)) {
	workers := runtime.NumCPU()
	if workers > n {
		workers = n
	}
	if workers <= 1 {
		for i := 0; i < n; i++ {
			fn(i)
		}
		return
	}
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				fn(i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		next <- i
	}
	close(next)
	wg.Wait()
}

// timeNear reports whether two times fall within a week of each other.
func timeNear(a, b time.Time) bool {
	if a.IsZero() || b.IsZero() {
		return false
	}
	d := a.Sub(b)
	return math.Abs(d.Hours()) <= 7*24
}
