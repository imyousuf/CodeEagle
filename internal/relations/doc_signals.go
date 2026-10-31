package relations

import (
	"math"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/imyousuf/CodeEagle/internal/search"
)

// Signals that propose a pair of documents.
const (
	DocSignalRef   = "ref"   // one names the other
	DocSignalTitle = "title" // one's distinctive title words appear, in order, in the other
	DocSignalName  = "name"  // their file names are versions of one name
	DocSignalIDs   = "ids"   // they share an identifier few documents use
	DocSignalNear  = "near"  // their sections are near in meaning
)

// DocCandidate is a pair of documents proposed for judging, A the lower path.
type DocCandidate struct {
	A, B    *Subject
	Signals []string
	// Facts are what the judge is told about the pair, literally: the
	// sentence in which one names the other, a version-style name.
	Facts []string
	// Sections are the parts of each document the pair rests on, nearest
	// first, kept to a few.
	SectionsA, SectionsB []*Section
	Cosine               float64
	Score                float64
	Status               string
}

// Key identifies the pair.
func (c *DocCandidate) Key() string { return c.A.Path + "\x00" + c.B.Path }

// DocOptions bound document-pair proposal.
type DocOptions struct {
	// MaxPerDocument caps the pairs proposed for one document.
	MaxPerDocument int
	// MaxPairs caps the pairs one run judges.
	MaxPairs int
	// MinCosine is how near two sections must be to count, and K how many
	// nearest sections of other documents each section considers.
	MinCosine float64
	K         int
}

// DefaultDocOptions keep one run's document judging small: one request per
// pair, at most sixty pairs.
func DefaultDocOptions() DocOptions {
	return DocOptions{MaxPerDocument: 12, MaxPairs: 60, MinCosine: 0.75, K: 3}
}

// DocProposer proposes document pairs over a loaded corpus.
type DocProposer struct {
	subjects map[string]*Subject
	rels     map[string]*Relation
	vec      Vectors
	opts     DocOptions
}

// NewDocProposer builds a proposer. rels supplies each document's resolved
// references; vec may be nil, leaving out nearness in meaning.
func NewDocProposer(subjects []*Subject, rels map[string]*Relation, vec Vectors, opts DocOptions) *DocProposer {
	by := make(map[string]*Subject, len(subjects))
	for _, s := range subjects {
		by[s.Path] = s
	}
	return &DocProposer{subjects: by, rels: rels, vec: vec, opts: opts}
}

// ForDocuments proposes pairs involving any of the given documents.
func (p *DocProposer) ForDocuments(paths ...string) []*DocCandidate {
	want := make(map[string]bool, len(paths))
	for _, x := range paths {
		want[x] = true
	}
	pairs := make(map[string]*DocCandidate)
	pair := func(x, y *Subject) *DocCandidate {
		if x == nil || y == nil || x.Path == y.Path {
			return nil
		}
		if x.Path > y.Path {
			x, y = y, x
		}
		key := x.Path + "\x00" + y.Path
		c := pairs[key]
		if c == nil {
			c = &DocCandidate{A: x, B: y}
			pairs[key] = c
		}
		return c
	}
	addSignal := func(c *DocCandidate, sig string) {
		if c != nil && !contains(c.Signals, sig) {
			c.Signals = append(c.Signals, sig)
		}
	}

	// References, both ways: a document naming another is the surest sign
	// they belong together, and the sentence that names it is what the
	// judge most needs to read.
	for src, rel := range p.rels {
		for _, ref := range rel.Refs {
			if !want[src] && !want[ref.Target] {
				continue
			}
			c := pair(p.subjects[src], p.subjects[ref.Target])
			if c == nil {
				continue
			}
			addSignal(c, DocSignalRef)
			c.Facts = append(c.Facts, refFact(src, ref))
			p.addSection(c, src, ref.Section)
		}
	}

	p.titleMentions(want, pair, addSignal)
	p.nameVersions(want, pair, addSignal)
	p.sharedIdentifiers(want, pair, addSignal)
	p.nearSections(want, pair, addSignal)

	out := make([]*DocCandidate, 0, len(pairs))
	for _, c := range pairs {
		c.Score = docScore(c)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Key() < out[j].Key()
	})
	return out
}

func refFact(src string, ref Reference) string {
	if ref.Context != "" {
		return src + " names " + ref.Target + ": \"" + clip(ref.Context, maxRefContext) + "\""
	}
	return src + " names " + ref.Target
}

func (p *DocProposer) addSection(c *DocCandidate, docPath, heading string) {
	s := p.subjects[docPath]
	if s == nil {
		return
	}
	for _, sec := range s.Sections {
		if sec.HeadingPath == heading {
			if docPath == c.A.Path {
				c.SectionsA = appendDocSection(c.SectionsA, sec)
			} else {
				c.SectionsB = appendDocSection(c.SectionsB, sec)
			}
			return
		}
	}
}

func appendDocSection(list []*Section, sec *Section) []*Section {
	for _, s := range list {
		if s == sec {
			return list
		}
	}
	if len(list) >= 3 {
		return list
	}
	return append(list, sec)
}

// titleMentions proposes a pair when one document's distinctive title words
// appear, in order, in the other's text. A title word is distinctive when
// few documents' text contains it.
func (p *DocProposer) titleMentions(want map[string]bool, pair func(x, y *Subject) *DocCandidate, addSignal func(*DocCandidate, string)) {
	words := make(map[string][]string, len(p.subjects)) // doc -> its text's words in order
	df := make(map[string]int)
	for path, s := range p.subjects {
		var seq []string
		seen := make(map[string]bool)
		for _, sec := range s.Sections {
			for _, w := range search.QueryTerms(sec.Text) {
				seq = append(seq, w)
				if !seen[w] {
					seen[w] = true
					df[w]++
				}
			}
		}
		words[path] = seq
	}
	rare := max(3, len(p.subjects)/50)
	postings := make(map[string][]string)
	for path, seq := range words {
		seen := make(map[string]bool)
		for _, w := range seq {
			if !seen[w] && df[w] <= rare {
				seen[w] = true
				postings[w] = append(postings[w], path)
			}
		}
	}
	for path, s := range p.subjects {
		var title []string
		for _, w := range search.QueryTerms(s.Title) {
			if df[w] <= rare && len(w) > 3 {
				title = append(title, w)
			}
		}
		if len(title) < 2 {
			continue
		}
		for _, other := range postings[title[0]] {
			if other == path || (!want[path] && !want[other]) {
				continue
			}
			if wordsInOrder(title, words[other]) {
				c := pair(s, p.subjects[other])
				addSignal(c, DocSignalTitle)
				if c != nil {
					c.Facts = append(c.Facts, other+" uses the words of the title of "+path+": \""+s.Title+"\"")
				}
			}
		}
	}
}

// versionSuffix is what marks one copy of a name as a version of another,
// at the end of a name only: ".v1-with-schema", " (with AO)", "_1", "-final".
// Numbers inside a name (opal-7194-plan) are part of it.
var versionSuffix = regexp.MustCompile(`(?i)(\.v\d+.*|[._ -]v\d+$|\s*\(.*?\)$|[._ -]\d{1,2}$|[._ -](old|new|redesign|draft|final|copy)$)`)

// stem is a file name without directory, extension or version suffix.
func stem(p string) string {
	base := strings.TrimSuffix(path.Base(p), path.Ext(p))
	for {
		next := strings.TrimSpace(versionSuffix.ReplaceAllString(base, ""))
		if next == base {
			break
		}
		base = next
	}
	return strings.ToLower(strings.Trim(base, "._ -"))
}

// nameVersions proposes documents whose file names are versions of one name:
// a plan and its "v1-with-schema".
func (p *DocProposer) nameVersions(want map[string]bool, pair func(x, y *Subject) *DocCandidate, addSignal func(*DocCandidate, string)) {
	byStem := make(map[string][]string)
	for path := range p.subjects {
		if st := stem(path); len(st) >= 4 {
			byStem[st] = append(byStem[st], path)
		}
	}
	for st, paths := range byStem {
		if len(paths) < 2 || len(paths) > 5 {
			continue
		}
		for i := range paths {
			for j := i + 1; j < len(paths); j++ {
				if !want[paths[i]] && !want[paths[j]] {
					continue
				}
				c := pair(p.subjects[paths[i]], p.subjects[paths[j]])
				addSignal(c, DocSignalName)
				if c != nil {
					c.Facts = append(c.Facts, "their file names are versions of one name, \""+st+"\"")
				}
			}
		}
	}
}

// sharedIdentifiers proposes documents sharing an identifier (a ticket key,
// a PR number, a file name) that at most three documents use.
func (p *DocProposer) sharedIdentifiers(want map[string]bool, pair func(x, y *Subject) *DocCandidate, addSignal func(*DocCandidate, string)) {
	byID := make(map[string][]string)
	for path, s := range p.subjects {
		ids := make(map[string]bool)
		for _, sec := range s.Sections {
			for id := range identifiers(sec.Text) {
				ids[id] = true
			}
		}
		for id := range ids {
			byID[id] = append(byID[id], path)
		}
	}
	for id, paths := range byID {
		if len(paths) < 2 || len(paths) > 3 {
			continue
		}
		for i := range paths {
			for j := i + 1; j < len(paths); j++ {
				if !want[paths[i]] && !want[paths[j]] {
					continue
				}
				c := pair(p.subjects[paths[i]], p.subjects[paths[j]])
				addSignal(c, DocSignalIDs)
				if c != nil && len(c.Facts) < 8 {
					c.Facts = append(c.Facts, "both mention "+id)
				}
			}
		}
	}
}

// nearSections proposes documents whose sections are near in meaning: for
// each section of a wanted document, the K nearest sections of other
// documents, kept when at least MinCosine.
func (p *DocProposer) nearSections(want map[string]bool, pair func(x, y *Subject) *DocCandidate, addSignal func(*DocCandidate, string)) {
	if p.vec == nil {
		return
	}
	type secVec struct {
		doc string
		sec *Section
		v   []float32
	}
	var all []secVec
	for path, s := range p.subjects {
		for _, sec := range s.Sections {
			if v, ok := p.vec.Vector(sec.NodeID); ok {
				all = append(all, secVec{path, sec, normalize(v)})
			}
		}
	}
	var mine []int
	for i, sv := range all {
		if want[sv.doc] {
			mine = append(mine, i)
		}
	}
	type hit struct {
		i, j int
		cos  float64
	}
	results := make([][]hit, len(mine))
	parallel(len(mine), func(k int) {
		i := mine[k]
		best := make([]hit, 0, p.opts.K)
		for j, other := range all {
			if other.doc == all[i].doc {
				continue
			}
			cos := dot(all[i].v, other.v)
			if cos < p.opts.MinCosine {
				continue
			}
			if len(best) < p.opts.K {
				best = append(best, hit{i, j, cos})
				sort.Slice(best, func(a, b int) bool { return best[a].cos > best[b].cos })
			} else if cos > best[len(best)-1].cos {
				best[len(best)-1] = hit{i, j, cos}
				sort.Slice(best, func(a, b int) bool { return best[a].cos > best[b].cos })
			}
		}
		results[k] = best
	})
	for _, hs := range results {
		for _, h := range hs {
			a, b := all[h.i], all[h.j]
			c := pair(p.subjects[a.doc], p.subjects[b.doc])
			if c == nil {
				continue
			}
			addSignal(c, DocSignalNear)
			c.Cosine = math.Max(c.Cosine, h.cos)
			p.addSection(c, a.doc, a.sec.HeadingPath)
			p.addSection(c, b.doc, b.sec.HeadingPath)
		}
	}
}

// docScore ranks candidates: a reference first, then a version-style name, a
// title mention, nearness in meaning, a shared identifier.
func docScore(c *DocCandidate) float64 {
	s := c.Cosine
	for _, sig := range c.Signals {
		switch sig {
		case DocSignalRef:
			s += 1.0
		case DocSignalName:
			s += 0.8
		case DocSignalTitle:
			s += 0.6
		case DocSignalIDs:
			s += 0.3
		}
	}
	return s
}

// SelectDocs sets aside pairs already judged and applies the caps, best
// first. Like meeting pairs, only the per-run cap defers; the per-document
// cap keeps a document's best few.
func (p *DocProposer) SelectDocs(cands []*DocCandidate, judged func(*DocCandidate) bool) (selected, deferred []*DocCandidate) {
	perDoc := make(map[string]int)
	for _, c := range cands {
		if judged != nil && judged(c) {
			c.Status = StatusJudged
			continue
		}
		if p.opts.MaxPerDocument > 0 && (perDoc[c.A.Path] >= p.opts.MaxPerDocument || perDoc[c.B.Path] >= p.opts.MaxPerDocument) {
			c.Status = StatusCut
			continue
		}
		perDoc[c.A.Path]++
		perDoc[c.B.Path]++
		if p.opts.MaxPairs > 0 && len(selected) >= p.opts.MaxPairs {
			c.Status = StatusDeferred
			deferred = append(deferred, c)
			continue
		}
		c.Status = StatusSelected
		selected = append(selected, c)
	}
	return selected, deferred
}

// wordsInOrder reports whether every word of want appears in seq in order,
// each within a few words of the one before, so a title's words scattered
// across a document do not count as naming it.
func wordsInOrder(want, seq []string) bool {
	const gap = 6
	for start, w := range seq {
		if w != want[0] {
			continue
		}
		at, ok := start, true
		for _, next := range want[1:] {
			found := false
			for k := at + 1; k < len(seq) && k <= at+gap; k++ {
				if seq[k] == next {
					at, found = k, true
					break
				}
			}
			if !found {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}
