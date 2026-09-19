package transcript

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/imyousuf/CodeEagle/internal/parser/generic"
)

// Teams exports a meeting recording's transcript as a Word document. It opens
// with three lines of metadata and then one paragraph per utterance:
//
//	Opti Models Standup-20260710_140714UTC-Meeting Recording
//	July 10, 2026, 2:07PM
//	29m 7s
//	Carlos Pereira   0:16EE.
//	Benjamin Schaefer   0:18Hello?What is this?Oh boy, this chat is brutal.
//
// The name and timestamp are separated by a run of spaces, and the text begins
// immediately after the timestamp with no separator at all. Each sentence is a
// separate run in the document, so extracting the text concatenates them
// without spaces; that is repaired when the utterance is built.
//
// Speakers are named, which is the whole point of preferring this file over a
// diarized recording of the same meeting.

func init() {
	RegisterFormat(&teamsDocxFormat{})
}

type teamsDocxFormat struct{}

func (f *teamsDocxFormat) Name() string { return "teams-docx" }

func (f *teamsDocxFormat) Matches(path string) bool { return hasExt(path, ".docx") }

func (f *teamsDocxFormat) Detect(data []byte) bool {
	// A Word document is a zip, so its content has to be extracted before it
	// can be recognized. Only the opening lines are needed.
	text, err := generic.ExtractDocument("transcript.docx", data)
	if err != nil {
		return false
	}
	head := text
	if len(head) > 4096 {
		head = head[:4096]
	}
	return teamsHeader.MatchString(head) && teamsUtterance.MatchString(head)
}

func (f *teamsDocxFormat) Parse(path string, data []byte) (*Session, error) {
	text, err := generic.ExtractDocument(path, data)
	if err != nil {
		return nil, fmt.Errorf("extract document: %w", err)
	}
	return parseTeamsTranscript(path, text)
}

var (
	// The title line ends with "-Meeting Recording" and usually carries a
	// UTC stamp, which is more reliable than the file's modification time.
	teamsHeader = regexp.MustCompile(`(?m)^.*(?:-\d{8}_\d{6}UTC|Meeting Recording).*$`)
	// "Carlos Pereira   0:16EE." — name, gap, clock, then text with no space.
	// Multi-line: detection matches this against a block of text, while parsing
	// matches it a line at a time.
	teamsUtterance = regexp.MustCompile(`(?m)^(\S.*?)\s{2,}(\d{1,2}:\d{2}(?::\d{2})?)(.*)$`)
	// "29m 7s", "1h 4m 53s"
	teamsDuration = regexp.MustCompile(`^(?:(\d+)h\s*)?(?:(\d+)m\s*)?(?:(\d+)s)?$`)
)

// parseTeamsTranscript reads the extracted text of a Teams transcript.
func parseTeamsTranscript(path, text string) (*Session, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	s := &Session{
		Platform:      "Teams",
		NamedSpeakers: true,
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		m := teamsUtterance.FindStringSubmatch(line)
		if m == nil {
			// Anything before the first utterance is the header block.
			if len(s.Segments) == 0 {
				readTeamsHeader(s, line)
			}
			continue
		}

		speaker := strings.TrimSpace(m[1])
		at, ok := parseClock(m[2])
		if !ok {
			continue
		}
		body := cleanRunTogether(strings.TrimSpace(m[3]))
		if body == "" {
			continue
		}

		// A Teams transcript gives the moment an utterance began and nothing
		// more, so each one runs until the next begins; the last is closed
		// with the meeting's stated length.
		if n := len(s.Segments); n > 0 && s.Segments[n-1].EndTime < at {
			s.Segments[n-1].EndTime = at
		}
		s.Segments = append(s.Segments, Segment{
			ID:        fmt.Sprintf("utt-%d", len(s.Segments)+1),
			Speaker:   speaker,
			Text:      body,
			StartTime: at,
			EndTime:   at,
			Source:    SourceMonitor,
		})
	}

	if len(s.Segments) == 0 {
		return nil, fmt.Errorf("no utterances found")
	}

	last := &s.Segments[len(s.Segments)-1]
	if s.Duration > last.StartTime {
		last.EndTime = s.Duration
	} else {
		// Without a stated length, give the closing utterance a nominal span
		// rather than a zero one, which would read as silence.
		last.EndTime = last.StartTime + 5
		s.Duration = last.EndTime
	}
	if !s.CreatedAt.IsZero() {
		s.EndedAt = s.CreatedAt.Add(time.Duration(s.Duration) * time.Second)
	}
	if s.Title == "" {
		s.Title = titleFromPath(path)
	}
	return s, nil
}

// readTeamsHeader picks the title, start time and length out of the lines
// preceding the first utterance.
func readTeamsHeader(s *Session, line string) {
	if s.Title == "" && strings.Contains(line, "Meeting Recording") {
		s.Title = cleanTeamsTitle(line)
		if t, ok := teamsStampFromTitle(line); ok {
			s.CreatedAt = t
		}
		return
	}
	if s.Duration == 0 {
		if d, ok := parseTeamsDuration(line); ok {
			s.Duration = d
			return
		}
	}
	if s.CreatedAt.IsZero() {
		if t, ok := parseTeamsDate(line); ok {
			s.CreatedAt = t
		}
	}
}

// cleanTeamsTitle strips the stamp and suffix Teams appends to the name.
func cleanTeamsTitle(line string) string {
	if i := strings.Index(line, "-Meeting Recording"); i > 0 {
		line = line[:i]
	}
	// The stamp is sometimes written with a UTC suffix and sometimes without.
	line = teamsTitleStamp.ReplaceAllString(line, "")
	return strings.TrimSpace(strings.Trim(line, "-"))
}

// teamsTitleStamp matches the timestamp Teams appends to a recording's name.
var teamsTitleStamp = regexp.MustCompile(`-\d{8}_\d{6}(UTC)?$`)

// teamsStampFromTitle reads the UTC stamp Teams embeds in the title.
func teamsStampFromTitle(line string) (time.Time, bool) {
	m := findSubmatch(`(\d{8}_\d{6})UTC`, line)
	if m == "" {
		return time.Time{}, false
	}
	t, err := time.Parse("20060102_150405", m)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// parseTeamsDate reads the local date line, "July 10, 2026, 2:07PM".
func parseTeamsDate(line string) (time.Time, bool) {
	for _, layout := range []string{
		"January 2, 2006, 3:04PM",
		"January 2, 2006, 3:04 PM",
		"2 January 2006, 3:04PM",
		"January 2, 2006",
	} {
		if t, err := time.Parse(layout, strings.TrimSpace(line)); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// parseTeamsDuration reads the length line, "29m 7s".
func parseTeamsDuration(line string) (float64, bool) {
	m := teamsDuration.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil || (m[1] == "" && m[2] == "" && m[3] == "") {
		return 0, false
	}
	var total float64
	for i, unit := range []float64{3600, 60, 1} {
		if m[i+1] == "" {
			continue
		}
		var v float64
		if _, err := fmt.Sscanf(m[i+1], "%g", &v); err != nil {
			return 0, false
		}
		total += v * unit
	}
	return total, total > 0
}
