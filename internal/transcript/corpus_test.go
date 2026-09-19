package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestCorpus runs hint extraction over a real transcript corpus and reports
// coverage. It is skipped unless CODEEAGLE_TRANSCRIPT_CORPUS points at a
// directory of session folders, so it never runs in CI but is available for
// checking identification quality against real recordings.
//
// Run with:
//
//	CODEEAGLE_TRANSCRIPT_CORPUS=~/.local/share/tomoe/sessions go test \
//	  ./internal/transcript/ -run TestCorpus -v
func TestCorpus(t *testing.T) {
	dir := os.Getenv("CODEEAGLE_TRANSCRIPT_CORPUS")
	if dir == "" {
		t.Skip("set CODEEAGLE_TRANSCRIPT_CORPUS to run")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read corpus dir: %v", err)
	}

	var (
		sessions, empty, withHints                  int
		speakersTotal, speakersNamed, speakersOwner int
		secsTotal, secsNamed, secsOwner             float64
		nameCounts                                  = map[string]int{}
		kindCounts                                  = map[HintKind]int{}
		unresolved                                  int
		samples                                     []string
	)

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), SessionFileName)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if !LooksLikeSession(data) {
			t.Errorf("%s: not recognized as a session", path)
			continue
		}
		s, err := Parse(data, path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		sessions++
		if s.IsEmpty() {
			empty++
			continue
		}

		hints := ExtractHints(s)
		if len(hints) > 0 {
			withHints++
		}
		for _, h := range hints {
			kindCounts[h.Kind]++
		}

		ev := Evidence(s, hints)
		for _, e := range ev {
			speakersTotal++
			secsTotal += e.Stat.SpeakingSeconds
			if e.Stat.IsOwner {
				speakersOwner++
				secsOwner += e.Stat.SpeakingSeconds
				continue
			}
			if best, ok := e.Best(); ok && e.Margin() >= 2.0 {
				speakersNamed++
				secsNamed += e.Stat.SpeakingSeconds
				nameCounts[NormalizeName(best.Name)]++
				if len(samples) < 25 {
					samples = append(samples, fmt.Sprintf(
						"%-10s → %-12s w=%.1f margin=%.1f  %q",
						e.Stat.Label, best.Name, best.Weight, e.Margin(), truncate(best.Quotes[0], 70)))
				}
			} else {
				unresolved++
			}
		}
	}

	fmt.Printf("\n══ CORPUS ══\n")
	fmt.Printf("sessions=%d empty=%d with-hints=%d (%.0f%% of non-empty)\n",
		sessions, empty, withHints, pct(withHints, sessions-empty))
	fmt.Printf("participants=%d  owner=%d  named=%d  unresolved=%d\n",
		speakersTotal, speakersOwner, speakersNamed, unresolved)
	fmt.Printf("non-owner participants identified: %.1f%% by count\n",
		pct(speakersNamed, speakersTotal-speakersOwner))
	fmt.Printf("speech attributed: %.1f%% of all speech (%.1f%% owner + %.1f%% named)\n",
		(secsOwner+secsNamed)/secsTotal*100, secsOwner/secsTotal*100, secsNamed/secsTotal*100)
	fmt.Printf("non-owner speech identified: %.1f%%\n",
		secsNamed/(secsTotal-secsOwner)*100)

	fmt.Printf("\n══ HINT KINDS ══\n")
	type kc struct {
		k HintKind
		n int
	}
	var kinds []kc
	for k, n := range kindCounts {
		kinds = append(kinds, kc{k, n})
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i].n > kinds[j].n })
	for _, k := range kinds {
		fmt.Printf("  %-16s %d\n", k.k, k.n)
	}

	fmt.Printf("\n══ TOP IDENTIFIED NAMES ══\n")
	type nc struct {
		n string
		c int
	}
	var names []nc
	for n, c := range nameCounts {
		names = append(names, nc{n, c})
	}
	sort.Slice(names, func(i, j int) bool { return names[i].c > names[j].c })
	for i, n := range names {
		if i >= 30 {
			break
		}
		fmt.Printf("  %-20s %d sessions\n", n.n, n.c)
	}

	fmt.Printf("\n══ SAMPLE ATTRIBUTIONS ══\n")
	for _, s := range samples {
		fmt.Printf("  %s\n", s)
	}
	fmt.Println()
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b) * 100
}
