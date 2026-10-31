package relations

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DocPairSample is one pair of documents written out for a person to label.
type DocPairSample struct {
	ID       string   `json:"pair_id"`
	Stratum  string   `json:"stratum"`
	A        string   `json:"a"`
	B        string   `json:"b"`
	ATitle   string   `json:"a_title"`
	BTitle   string   `json:"b_title"`
	AOpening string   `json:"a_opening,omitempty"`
	BOpening string   `json:"b_opening,omitempty"`
	ASection []string `json:"a_sections,omitempty"`
	BSection []string `json:"b_sections,omitempty"`
	Signals  []string `json:"signals"`
	Facts    []string `json:"facts,omitempty"`
	Cosine   float64  `json:"cosine,omitempty"`

	// Labels, filled in by the labeller: Related yes or no; each kind none,
	// a>b or b>a.
	Related  string `json:"related"`
	Changes  string `json:"changes"`
	Replaces string `json:"replaces"`
	Records  string `json:"records"`
}

const docLabelReadme = `Label each line of pairs.jsonl. Read both documents where the excerpts are
not enough (paths are as indexed, under the project's root).

related:  "yes" if they are about the same piece of work, so a reader of one
          would want to know about the other; "no" if they share vocabulary or
          a broad area only.
changes:  "a>b" if A changes part of what B specifies (revises, renames,
          re-homes or overrides some of it while the rest may still hold),
          "b>a" for the reverse, "none" otherwise.
replaces: "a>b" if A replaces B as a whole (B is no longer the account to
          follow), "b>a" for the reverse, "none" otherwise. Replacing implies
          changing; mark changes too.
records:  "a>b" if A is an account of something that happened (an incident,
          investigation, review, meeting, conversation, decision) involving
          what B describes, "b>a" for the reverse, "none" otherwise.

Label before looking at any score the system gives, and leave a field empty
rather than guess. These labels set the gates that decide which links are
shown; a careless "replaces" would hide a document someone needs.
`

// WriteDocSample writes up to n candidates stratified by their strongest
// signal, so references, title mentions, version names, identifiers and
// pairs only near in meaning are all represented.
func WriteDocSample(dir string, cands []*DocCandidate, n int) (int, error) {
	strata := make(map[string][]*DocCandidate)
	for _, c := range cands {
		strata[docStratum(c)] = append(strata[docStratum(c)], c)
	}
	names := make([]string, 0, len(strata))
	for s := range strata {
		names = append(names, s)
	}
	sort.Strings(names)
	rng := rand.New(rand.NewSource(19))
	per := max(1, n/max(1, len(names)))
	var picked []*DocCandidate
	for _, s := range names {
		list := strata[s]
		rng.Shuffle(len(list), func(i, j int) { list[i], list[j] = list[j], list[i] })
		picked = append(picked, list[:min(per, len(list))]...)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	f, err := os.Create(filepath.Join(dir, "pairs.jsonl"))
	if err != nil {
		return 0, err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for i, c := range picked {
		s := DocPairSample{
			ID: fmt.Sprintf("d%04d", i+1), Stratum: docStratum(c), A: c.A.Path, B: c.B.Path,
			ATitle: c.A.Title, BTitle: c.B.Title, AOpening: excerpt(c.A.Preamble, 400), BOpening: excerpt(c.B.Preamble, 400),
			Signals: c.Signals, Facts: c.Facts, Cosine: c.Cosine,
		}
		for _, sec := range c.SectionsA {
			s.ASection = append(s.ASection, sec.HeadingPath+": "+excerpt(sec.Text, 500))
		}
		for _, sec := range c.SectionsB {
			s.BSection = append(s.BSection, sec.HeadingPath+": "+excerpt(sec.Text, 500))
		}
		b, _ := json.Marshal(s)
		if _, err := w.Write(append(b, '\n')); err != nil {
			return 0, err
		}
	}
	if err := w.Flush(); err != nil {
		return 0, err
	}
	return len(picked), os.WriteFile(filepath.Join(dir, "README.txt"), []byte(docLabelReadme), 0o644)
}

func docStratum(c *DocCandidate) string {
	for _, s := range []string{DocSignalRef, DocSignalName, DocSignalTitle, DocSignalIDs} {
		if contains(c.Signals, s) {
			return s
		}
	}
	if c.Cosine >= 0.85 {
		return "near-high"
	}
	return "near-mid"
}

// ReadDocLabels reads labelled pairs, keeping those whose related label is
// filled in.
func ReadDocLabels(path string) ([]DocPairSample, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []DocPairSample
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for sc.Scan() {
		var s DocPairSample
		if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if s.Related = strings.ToLower(strings.TrimSpace(s.Related)); s.Related == "yes" || s.Related == "no" {
			out = append(out, s)
		}
	}
	return out, sc.Err()
}

// RebuildDocs turns labelled pairs back into candidates over the corpus as
// it is now, with the sections the sample showed.
func RebuildDocs(labels []DocPairSample, subjects []*Subject) ([]*DocCandidate, []DocPairSample) {
	by := make(map[string]*Subject, len(subjects))
	for _, s := range subjects {
		by[s.Path] = s
	}
	findSecs := func(s *Subject, shown []string) []*Section {
		var out []*Section
		for _, line := range shown {
			heading, _, _ := strings.Cut(line, ": ")
			for _, sec := range s.Sections {
				if sec.HeadingPath == heading {
					out = appendDocSection(out, sec)
				}
			}
		}
		return out
	}
	var cands []*DocCandidate
	var kept []DocPairSample
	for _, l := range labels {
		a, b := by[l.A], by[l.B]
		if a == nil || b == nil {
			continue
		}
		cands = append(cands, &DocCandidate{A: a, B: b, Signals: l.Signals, Facts: l.Facts, Cosine: l.Cosine,
			SectionsA: findSecs(a, l.ASection), SectionsB: findSecs(b, l.BSection)})
		kept = append(kept, l)
	}
	return cands, kept
}

// DocCalibration is how well the decision model's answers match labels.
type DocCalibration struct {
	Pairs, Related int
	AUC            float64
	// RelatedAt is precision and recall of "related" at each gate tried.
	RelatedAt []GateStats
	// Kinds is, per kind, precision and recall of the decided link
	// (direction included) against the labels, at the gates used.
	Kinds map[string][2]float64
}

// CalibrateDocs compares verdicts with labels. verdicts[i] answers labels[i].
func CalibrateDocs(verdicts []DocVerdict, labels []DocPairSample, a, b []*Subject, g DocGates) DocCalibration {
	cal := DocCalibration{Pairs: len(labels), Kinds: make(map[string][2]float64)}
	probs := make([]float64, len(labels))
	rel := make([]bool, len(labels))
	for i, l := range labels {
		probs[i] = verdicts[i].PRelated
		rel[i] = l.Related == "yes"
		if rel[i] {
			cal.Related++
		}
	}
	c := Calibrate(probs, rel, 1.0)
	cal.AUC, cal.RelatedAt = c.AUC, c.Buckets
	for _, kind := range []string{DocChanges, DocReplaces, DocRecords} {
		tp, fp, fn := 0, 0, 0
		for i, l := range labels {
			v := verdicts[i]
			v.Decide(a[i], b[i], g)
			got := "none"
			for _, link := range v.Links {
				if link.Kind == kind {
					got = "a>b"
					if link.From == l.B {
						got = "b>a"
					}
				}
			}
			want := map[string]string{DocChanges: l.Changes, DocReplaces: l.Replaces, DocRecords: l.Records}[kind]
			want = strings.ToLower(strings.TrimSpace(want))
			if want == "" {
				want = "none"
			}
			switch {
			case got != "none" && got == want:
				tp++
			case got != "none":
				fp++
			case want != "none":
				fn++
			}
		}
		prec, recall := 1.0, 1.0
		if tp+fp > 0 {
			prec = float64(tp) / float64(tp+fp)
		}
		if tp+fn > 0 {
			recall = float64(tp) / float64(tp+fn)
		}
		cal.Kinds[kind] = [2]float64{prec, recall}
	}
	return cal
}
