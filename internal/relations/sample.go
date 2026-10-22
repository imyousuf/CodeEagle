package relations

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// SamplePair is one candidate exported for a person to label. Label is left
// empty: "related" or "unrelated", filled in by hand.
type SamplePair struct {
	PairID         string   `json:"pair_id"`
	Stratum        string   `json:"stratum"`
	SubjectPath    string   `json:"subject_path"`
	SubjectTitle   string   `json:"subject_title"`
	Section        string   `json:"section,omitempty"`
	SectionExcerpt string   `json:"section_excerpt,omitempty"`
	SessionID      string   `json:"session_id"`
	MeetingTitle   string   `json:"meeting_title"`
	MeetingDate    string   `json:"meeting_date"`
	Segment        string   `json:"segment,omitempty"`
	SegmentSummary string   `json:"segment_summary,omitempty"`
	Signals        []string `json:"signals"`
	Cosine         float64  `json:"cosine"`
	NameScore      float64  `json:"name_score,omitempty"`
	IDs            []string `json:"ids,omitempty"`
	Label          string   `json:"label"`
}

const sampleReadme = `Label each line of pairs.jsonl by setting "label" to "related" or
"unrelated", from the excerpts and, where in doubt, the document and the
meeting themselves.

related:   the meeting discusses the work, design or decision the document
           describes, or changes or questions something in it.
unrelated: they share vocabulary or a broad area only.

Label before looking at any score the system gives, and leave a pair empty
rather than guess. The labels calibrate the gate that decides which relations
are kept, so a careless "related" lets wrong relations through.
`

// WriteSample writes up to n candidates, spread across strata of signal and
// similarity, to dir/pairs.jsonl with labelling instructions beside it.
// Strata matter because most candidates come from one signal at middling
// similarity; a plain random sample would say little about the rest.
func WriteSample(dir string, cands []*Candidate, n int) (int, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	strata := make(map[string][]*Candidate)
	for _, c := range cands {
		key := stratum(c)
		strata[key] = append(strata[key], c)
	}
	keys := make([]string, 0, len(strata))
	for k := range strata {
		keys = append(keys, k)
		sort.SliceStable(strata[k], func(i, j int) bool { return strata[k][i].Key() < strata[k][j].Key() })
	}
	sort.Strings(keys)

	// Round robin across strata, so a small stratum is represented in full
	// and a large one does not crowd it out.
	var picked []*Candidate
	for i := 0; len(picked) < n; i++ {
		added := false
		for _, k := range keys {
			if i < len(strata[k]) && len(picked) < n {
				picked = append(picked, strata[k][i])
				added = true
			}
		}
		if !added {
			break
		}
	}

	f, err := os.Create(filepath.Join(dir, "pairs.jsonl"))
	if err != nil {
		return 0, err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for i, c := range picked {
		p := SamplePair{
			PairID:       fmt.Sprintf("p%04d", i+1),
			Stratum:      stratum(c),
			SubjectPath:  c.Subject.Path,
			SubjectTitle: c.Subject.Title,
			SessionID:    c.Meeting.SessionID,
			MeetingTitle: c.Meeting.Title,
			MeetingDate:  c.Meeting.Date.Format("2006-01-02"),
			Signals:      c.Signals.Names(),
			Cosine:       c.Cosine,
			NameScore:    c.NameScore,
			IDs:          c.IDs,
		}
		if len(c.Sections) > 0 {
			p.Section = c.Sections[0].HeadingPath
			p.SectionExcerpt = excerpt(c.Sections[0].Text, 400)
		}
		if len(c.Segments) > 0 {
			p.Segment = c.Segments[0].Name
			p.SegmentSummary = excerpt(c.Segments[0].Summary, 400)
		}
		if err := enc.Encode(p); err != nil {
			return i, err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte(sampleReadme), 0o644); err != nil {
		return len(picked), err
	}
	return len(picked), nil
}

// stratum groups candidates by which signals proposed them and, for those
// proposed by meaning, how near they were.
func stratum(c *Candidate) string {
	band := ""
	if c.Signals&SignalSaid != 0 {
		switch {
		case c.Cosine >= 0.7:
			band = "@0.7+"
		case c.Cosine >= 0.6:
			band = "@0.6-0.7"
		case c.Cosine >= 0.5:
			band = "@0.5-0.6"
		default:
			band = "@<0.5"
		}
	}
	return strings.Join(c.Signals.Names(), "+") + band
}

// excerpt clips text to about n bytes on a word boundary.
func excerpt(text string, n int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= n {
		return text
	}
	cut := strings.LastIndex(text[:n], " ")
	if cut < n/2 {
		cut = n
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
	}
	return text[:cut] + "…"
}
