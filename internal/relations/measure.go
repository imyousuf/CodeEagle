package relations

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// LabelledPair is a sample pair with a person's label.
type LabelledPair struct {
	SamplePair
	Related bool
}

// ReadLabels reads a labelled sample, keeping lines labelled "related" or
// "unrelated" and skipping the rest.
func ReadLabels(path string) ([]LabelledPair, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []LabelledPair
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var p SamplePair
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		switch strings.ToLower(strings.TrimSpace(p.Label)) {
		case "related":
			out = append(out, LabelledPair{SamplePair: p, Related: true})
		case "unrelated":
			out = append(out, LabelledPair{SamplePair: p})
		}
	}
	return out, sc.Err()
}

// Rebuild turns labelled pairs back into candidates over a loaded corpus, so
// they can be judged exactly as a run would judge them. Pairs whose document
// or meeting is no longer in the corpus are returned as missing.
func Rebuild(pairs []LabelledPair, subjects []*Subject, meetings map[string]*Meeting) (cands []*Candidate, labels []bool, missing int) {
	by := make(map[string]*Subject, len(subjects))
	for _, s := range subjects {
		by[s.Path] = s
	}
	for _, p := range pairs {
		s, m := by[p.SubjectPath], meetings[p.SessionID]
		if s == nil || m == nil {
			missing++
			continue
		}
		c := &Candidate{Subject: s, Meeting: m, Cosine: p.Cosine, NameScore: p.NameScore, IDs: p.IDs}
		for _, name := range p.Signals {
			for _, sn := range signalNames {
				if sn.name == name {
					c.Signals |= sn.s
				}
			}
		}
		for _, sec := range s.Sections {
			if sec.HeadingPath == p.Section {
				c.Sections = append(c.Sections, sec)
				break
			}
		}
		for _, seg := range m.Segments {
			if seg.Name == p.Segment {
				c.Segments = append(c.Segments, seg)
				break
			}
		}
		if len(c.Segments) == 0 && len(m.Segments) > 0 {
			c.Segments = append(c.Segments, m.Segments[0])
		}
		cands = append(cands, c)
		labels = append(labels, p.Related)
	}
	return cands, labels, missing
}

// Calibration summarises how well probabilities separate labelled pairs.
type Calibration struct {
	Pairs, Related int
	AUC            float64
	Buckets        []GateStats
	// Gate is the lowest gate whose precision meets the target, with at
	// least a few pairs above it; zero if none does.
	Gate float64
}

// GateStats is what a gate would admit.
type GateStats struct {
	Gate      float64
	Admitted  int
	Precision float64
	Recall    float64
}

// Calibrate computes the AUC and precision and recall at gates 0.1 to 0.9.
func Calibrate(probs []float64, related []bool, targetPrecision float64) Calibration {
	cal := Calibration{Pairs: len(probs)}
	for _, r := range related {
		if r {
			cal.Related++
		}
	}
	cal.AUC = auc(probs, related)
	for g := 1; g <= 9; g++ {
		gate := float64(g) / 10
		st := GateStats{Gate: gate}
		tp := 0
		for i, p := range probs {
			if p >= gate {
				st.Admitted++
				if related[i] {
					tp++
				}
			}
		}
		if st.Admitted > 0 {
			st.Precision = float64(tp) / float64(st.Admitted)
		}
		if cal.Related > 0 {
			st.Recall = float64(tp) / float64(cal.Related)
		}
		cal.Buckets = append(cal.Buckets, st)
		if cal.Gate == 0 && st.Admitted >= 5 && st.Precision >= targetPrecision {
			cal.Gate = gate
		}
	}
	return cal
}

// auc is the probability that a random related pair scores above a random
// unrelated one, ties counting half.
func auc(probs []float64, related []bool) float64 {
	type item struct {
		p float64
		r bool
	}
	items := make([]item, len(probs))
	for i := range probs {
		items[i] = item{probs[i], related[i]}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].p < items[j].p })
	var pos, neg float64
	var rankSum float64
	for i := 0; i < len(items); {
		j := i
		for j < len(items) && items[j].p == items[i].p {
			j++
		}
		mid := float64(i+j+1) / 2 // average 1-based rank of the tie group
		for k := i; k < j; k++ {
			if items[k].r {
				rankSum += mid
				pos++
			} else {
				neg++
			}
		}
		i = j
	}
	if pos == 0 || neg == 0 {
		return 0
	}
	return (rankSum - pos*(pos+1)/2) / (pos * neg)
}
