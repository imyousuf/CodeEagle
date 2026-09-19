package transcript

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"
)

// Throwaway investigation harness. Delete before finishing.
//
//	PROBE=1 go test -race ./internal/transcript/ -run TestProbeCharacterise -v

type pairStat struct {
	Session           string  `json:"session"`
	A                 string  `json:"a"`
	B                 string  `json:"b"`
	ASecs             float64 `json:"a_secs"`
	BSecs             float64 `json:"b_secs"`
	AUtt              int     `json:"a_utt"`
	BUtt              int     `json:"b_utt"`
	OverlapSec        float64 `json:"overlap_sec"`
	IntervalsDisjoint bool    `json:"intervals_disjoint"`
	AdjAB             int     `json:"adj_ab"`
	AdjBA             int     `json:"adj_ba"`
	AMeanUtt          float64 `json:"a_mean_utt"`
	BMeanUtt          float64 `json:"b_mean_utt"`
	AWordsPerUtt      float64 `json:"a_wpu"`
	BWordsPerUtt      float64 `json:"b_wpu"`
	SameSource        bool    `json:"same_source"`
	ANamed            string  `json:"a_named"`
	BNamed            string  `json:"b_named"`
}

type sessStat struct {
	ID                   string    `json:"id"`
	Path                 string    `json:"path"`
	DurationSec          float64   `json:"duration_sec"`
	SpeechSec            float64   `json:"speech_sec"`
	Segments             int       `json:"segments"`
	Labels               int       `json:"labels"`
	Substantive          int       `json:"substantive"`
	MicLabels            int       `json:"mic_labels"`
	MicSubstantiveLabels int       `json:"mic_substantive_labels"`
	Top8Share            float64   `json:"top8_share"`
	SubShare             float64   `json:"sub_share"`
	NamedSpeakers        bool      `json:"named_speakers"`
	WithVotes            int       `json:"with_votes"`
	DupNamePairs         int       `json:"dup_name_pairs"`
	SubSecs              []float64 `json:"sub_secs"`
}

func TestProbeCharacterise(t *testing.T) {
	if os.Getenv("PROBE") == "" {
		t.Skip("set PROBE=1")
	}
	dir := envOr("PROBE_SESSIONS", os.ExpandEnv("$HOME/.local/share/tomoe/sessions"))
	paths, err := DiscoverSessionsIn([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("discovered %d transcripts", len(paths))

	var sessions []sessStat
	var pairs []pairStat
	var loadErr, empty int

	for _, p := range paths {
		s, err := Load(p)
		if err != nil {
			loadErr++
			continue
		}
		if s.IsEmpty() {
			empty++
			continue
		}
		all := s.SpeakerStats()
		sub := s.SubstantiveSpeakers()

		var total, topShare float64
		for _, st := range all {
			total += st.SpeakingSeconds
		}
		for i, st := range all {
			if i < 8 {
				topShare += st.SpeakingSeconds
			}
		}
		var subSecsTotal float64
		subSecs := make([]float64, 0, len(sub))
		micLabels, micSub := 0, 0
		for _, st := range all {
			if st.Source == SourceMic || st.Label == OwnerLabel {
				micLabels++
			}
		}
		for _, st := range sub {
			subSecsTotal += st.SpeakingSeconds
			subSecs = append(subSecs, st.SpeakingSeconds)
			if st.Source == SourceMic || st.Label == OwnerLabel {
				micSub++
			}
		}

		hints := ExtractHints(s)
		ev := Evidence(s, hints)
		named := map[string]string{}
		withVotes := 0
		for _, e := range ev {
			if len(e.Votes) > 0 {
				withVotes++
				named[e.Stat.Label] = e.Votes[0].Name
			}
		}
		dupName := 0
		labels := make([]string, 0, len(named))
		for l := range named {
			labels = append(labels, l)
		}
		sort.Strings(labels)
		for i := 0; i < len(labels); i++ {
			for j := i + 1; j < len(labels); j++ {
				if SameName(named[labels[i]], named[labels[j]]) {
					dupName++
				}
			}
		}

		ss := sessStat{
			ID: s.ID, Path: p, DurationSec: s.DurationSeconds(), SpeechSec: total,
			Segments: len(s.Segments), Labels: len(all), Substantive: len(sub),
			MicLabels: micLabels, MicSubstantiveLabels: micSub,
			NamedSpeakers: s.NamedSpeakers, WithVotes: withVotes,
			DupNamePairs: dupName, SubSecs: subSecs,
		}
		if total > 0 {
			ss.Top8Share = topShare / total
			ss.SubShare = subSecsTotal / total
		}
		sessions = append(sessions, ss)

		// Pairwise structure among substantive labels.
		if len(sub) >= 2 && len(sub) <= 20 {
			byLabel := map[string][]Segment{}
			for _, seg := range s.Segments {
				byLabel[seg.Speaker] = append(byLabel[seg.Speaker], seg)
			}
			turns := s.Turns()
			adj := map[[2]string]int{}
			for i := 1; i < len(turns); i++ {
				adj[[2]string{turns[i-1].Speaker, turns[i].Speaker}]++
			}
			for i := 0; i < len(sub); i++ {
				for j := i + 1; j < len(sub); j++ {
					a, b := sub[i], sub[j]
					ov := overlapSeconds(byLabel[a.Label], byLabel[b.Label])
					disjoint := a.LastAt < b.FirstAt || b.LastAt < a.FirstAt
					ps := pairStat{
						Session: s.ID, A: a.Label, B: b.Label,
						ASecs: a.SpeakingSeconds, BSecs: b.SpeakingSeconds,
						AUtt: a.Utterances, BUtt: b.Utterances,
						OverlapSec: ov, IntervalsDisjoint: disjoint,
						AdjAB:        adj[[2]string{a.Label, b.Label}],
						AdjBA:        adj[[2]string{b.Label, a.Label}],
						AMeanUtt:     a.SpeakingSeconds / float64(max(a.Utterances, 1)),
						BMeanUtt:     b.SpeakingSeconds / float64(max(b.Utterances, 1)),
						AWordsPerUtt: float64(a.Words) / float64(max(a.Utterances, 1)),
						BWordsPerUtt: float64(b.Words) / float64(max(b.Utterances, 1)),
						SameSource:   a.Source == b.Source,
						ANamed:       named[a.Label], BNamed: named[b.Label],
					}
					pairs = append(pairs, ps)
				}
			}
		}
	}

	out := envOr("PROBE_OUT", "/home/imyousuf/.claude/jobs/b68b43a5/tmp")
	writeJSON(t, out+"/sessions.json", sessions)
	writeJSON(t, out+"/pairs.json", pairs)
	t.Logf("load errors: %d, empty: %d, usable sessions: %d, pairs: %d",
		loadErr, empty, len(sessions), len(pairs))
}

func overlapSeconds(a, b []Segment) float64 {
	// Both slices are in time order as recorded.
	var total float64
	j := 0
	for _, sa := range a {
		for j < len(b) && b[j].EndTime < sa.StartTime {
			j++
		}
		for k := j; k < len(b) && b[k].StartTime < sa.EndTime; k++ {
			lo := sa.StartTime
			if b[k].StartTime > lo {
				lo = b[k].StartTime
			}
			hi := sa.EndTime
			if b[k].EndTime < hi {
				hi = b[k].EndTime
			}
			if hi > lo {
				total += hi - lo
			}
		}
	}
	return total
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	fmt.Println("wrote", path)
}
