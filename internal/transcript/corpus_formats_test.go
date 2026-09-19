package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestForeignFormatCorpus parses real exports from conferencing tools, which
// is the only way to know the parsers match what those tools actually write
// rather than what their documentation says. Skipped unless
// CODEEAGLE_FOREIGN_DIR points at a directory of them.
//
//	CODEEAGLE_FOREIGN_DIR=~/Downloads go test ./internal/transcript/ \
//	  -run TestForeignFormatCorpus -v
func TestForeignFormatCorpus(t *testing.T) {
	dir := os.Getenv("CODEEAGLE_FOREIGN_DIR")
	if dir == "" {
		t.Skip("set CODEEAGLE_FOREIGN_DIR to run")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	var parsed, skipped, failed int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if !MayBeTranscript(path) {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		f := FormatFor(path, data)
		if f == nil {
			skipped++
			continue
		}

		s, err := ParseAny(path, data)
		if err != nil {
			failed++
			fmt.Printf("  FAIL  %-46s %v\n", truncate(e.Name(), 46), err)
			continue
		}
		parsed++

		speakers := s.SubstantiveSpeakers()
		names := make([]string, 0, len(speakers))
		for _, st := range speakers {
			names = append(names, fmt.Sprintf("%s (%s)", st.Label, FormatTimestamp(st.SpeakingSeconds)))
		}
		fmt.Printf("  OK    %-46s %-11s %4d segs  %s  named=%v\n",
			truncate(e.Name(), 46), f.Name(), len(s.Segments),
			FormatTimestamp(s.DurationSeconds()), s.NamedSpeakers)
		fmt.Printf("        title   %q\n        started %s\n        speakers %v\n",
			s.Title, s.StartedAt().Format("2006-01-02 15:04"), names)

		if len(s.Segments) == 0 {
			t.Errorf("%s: parsed with no segments", e.Name())
		}
		if s.NamedSpeakers && len(speakers) == 0 {
			t.Errorf("%s: named speakers but none substantive", e.Name())
		}
	}

	fmt.Printf("\n  parsed=%d failed=%d unrecognized=%d\n", parsed, failed, skipped)
	if parsed == 0 {
		t.Skip("no recognizable transcripts in the directory")
	}
	if failed > 0 {
		t.Errorf("%d transcripts failed to parse", failed)
	}
}
