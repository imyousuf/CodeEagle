// Package transcript loads meeting transcripts, resolves who was speaking,
// and extracts topics, decisions, and follow-ups for the knowledge graph.
//
// Transcripts arrive as diarized recordings: the recorder knows that speech
// came from N distinct voices but not who those voices belong to. It labels
// them "Person 1", "Person 2", ... and those labels are meaningful only
// within a single recording — "Person 1" in Monday's meeting and "Person 1"
// in Tuesday's are unrelated. Turning those labels into durable identities is
// what makes the rest of the graph useful, and is the bulk of this package.
package transcript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	// SourceMic is audio captured from the local microphone. It is always the
	// person who made the recording.
	SourceMic = "mic"
	// SourceMonitor is audio captured from the system output — everyone else.
	SourceMonitor = "monitor"

	// OwnerLabel is the speaker label the recorder assigns to microphone audio.
	OwnerLabel = "You"

	// SessionFileName is the transcript filename written per recording session.
	SessionFileName = "session.json"
)

// Segment is one continuous utterance by one speaker.
type Segment struct {
	ID        string  `json:"id"`
	Speaker   string  `json:"speaker"`
	Text      string  `json:"text"`
	StartTime float64 `json:"start_time"`
	EndTime   float64 `json:"end_time"`
	Source    string  `json:"source"`
	Language  string  `json:"language,omitempty"`
}

// Duration returns how long the utterance lasted, in seconds.
func (s Segment) Duration() float64 {
	d := s.EndTime - s.StartTime
	if d < 0 || math.IsNaN(d) {
		return 0
	}
	return d
}

// Session is a single recorded meeting.
type Session struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Platform  string    `json:"platform,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	EndedAt   time.Time `json:"ended_at"`
	Duration  float64   `json:"duration"`
	Sources   []string  `json:"sources,omitempty"`
	AudioPath string    `json:"audio_path,omitempty"`
	Language  string    `json:"language,omitempty"`
	Segments  []Segment `json:"segments"`

	// Path is where this session was loaded from. Not part of the wire format.
	Path string `json:"-"`
}

// Load reads a session transcript from disk.
func Load(path string) (*Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read session: %w", err)
	}
	return Parse(data, path)
}

// Parse decodes a session transcript. The path is recorded on the result for
// provenance and may be empty.
func Parse(data []byte, path string) (*Session, error) {
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse session %s: %w", path, err)
	}
	if s.ID == "" {
		return nil, fmt.Errorf("parse session %s: missing id", path)
	}
	s.Path = path
	return &s, nil
}

// probeScanLimit bounds the cheap pre-filter applied before a full JSON parse,
// so an unrelated multi-megabyte JSON file is rejected on a substring scan
// rather than by decoding it.
const probeScanLimit = 8192

// requiredKeys are the fields every transcript carries. Requiring all of them
// keeps false positives near zero.
var requiredKeys = []string{"id", "created_at", "duration", "segments"}

// LooksLikeSession reports whether data is a meeting transcript.
//
// Transcripts are plain .json files, so the file extension proves nothing —
// content sniffing is what separates them from every other JSON file in a
// repository. This mirrors how the YAML parser distinguishes its dialects.
//
// Key *presence* is what is checked, not key contents: a recording that
// captured no speech writes "segments": null, and it is still a transcript.
func LooksLikeSession(data []byte) bool {
	head := data
	if len(head) > probeScanLimit {
		head = head[:probeScanLimit]
	}
	if !bytes.Contains(head, []byte(`"segments"`)) || !bytes.Contains(head, []byte(`"created_at"`)) {
		return false
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return false
	}
	for _, k := range requiredKeys {
		if _, ok := fields[k]; !ok {
			return false
		}
	}
	// Guard against an unrelated document that happens to share these key
	// names: the id must be a non-empty string and duration a number.
	var id string
	if json.Unmarshal(fields["id"], &id) != nil || id == "" {
		return false
	}
	var duration float64
	return json.Unmarshal(fields["duration"], &duration) == nil
}

// IsEmpty reports whether the session captured no speech.
func (s *Session) IsEmpty() bool {
	for _, seg := range s.Segments {
		if strings.TrimSpace(seg.Text) != "" {
			return false
		}
	}
	return true
}

// HasGenericTitle reports whether the recorder fell back to a placeholder
// title. Such titles ("Unknown Meeting 2026-08-20 11:33") carry no meaning, so
// callers should prefer an LLM-generated title instead.
func (s *Session) HasGenericTitle() bool {
	t := strings.TrimSpace(s.Title)
	if t == "" {
		return true
	}
	for _, prefix := range []string{"Unknown Meeting", "Zoom Meeting", "Meeting ", "Session ", "Untitled"} {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	return false
}

// WordCount returns the total number of words spoken in the session.
func (s *Session) WordCount() int {
	n := 0
	for _, seg := range s.Segments {
		n += len(strings.Fields(seg.Text))
	}
	return n
}

// SpeakerStat summarizes one speaker's participation in a session.
type SpeakerStat struct {
	// Label is the raw diarization label ("You", "Person 3").
	Label string
	// Source is the audio source the label was heard on.
	Source string
	// IsOwner reports whether this label is the person who made the recording.
	IsOwner bool
	// Utterances is how many segments the speaker contributed.
	Utterances int
	// SpeakingSeconds is the total time the speaker held the floor.
	SpeakingSeconds float64
	// Words is the total word count across the speaker's segments.
	Words int
	// FirstAt and LastAt bound the speaker's participation, in seconds.
	FirstAt float64
	LastAt  float64
}

// IsOwnerLabel reports whether a speaker label denotes the recording's owner.
//
// The microphone source is the stronger signal: the recorder may in principle
// label owner audio differently, but mic audio is by construction the person
// holding the device.
func IsOwnerLabel(label, source string) bool {
	return source == SourceMic || label == OwnerLabel
}

// SpeakerStats summarizes every speaker in the session, ordered by speaking
// time descending so the dominant voices come first.
func (s *Session) SpeakerStats() []SpeakerStat {
	byLabel := make(map[string]*SpeakerStat)
	for _, seg := range s.Segments {
		if seg.Speaker == "" {
			continue
		}
		st, ok := byLabel[seg.Speaker]
		if !ok {
			st = &SpeakerStat{
				Label:   seg.Speaker,
				Source:  seg.Source,
				FirstAt: seg.StartTime,
				LastAt:  seg.EndTime,
			}
			byLabel[seg.Speaker] = st
		}
		st.Utterances++
		st.SpeakingSeconds += seg.Duration()
		st.Words += len(strings.Fields(seg.Text))
		if seg.StartTime < st.FirstAt {
			st.FirstAt = seg.StartTime
		}
		if seg.EndTime > st.LastAt {
			st.LastAt = seg.EndTime
		}
		if IsOwnerLabel(seg.Speaker, seg.Source) {
			st.IsOwner = true
		}
	}

	stats := make([]SpeakerStat, 0, len(byLabel))
	for _, st := range byLabel {
		stats = append(stats, *st)
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].SpeakingSeconds != stats[j].SpeakingSeconds {
			return stats[i].SpeakingSeconds > stats[j].SpeakingSeconds
		}
		return stats[i].Label < stats[j].Label
	})
	return stats
}

// Diarization splits audio by voice, but it splits imperfectly: a cough, a
// half-second "mm-hmm", or a moment of crosstalk each tend to be assigned a
// brand-new speaker label. In this corpus 85% of labels carry under five
// seconds of speech, while the eight busiest labels in a meeting account for
// over 99% of what was said.
//
// Treating every label as a person would therefore flood the graph with
// thousands of phantom participants. These thresholds separate the people who
// were actually in the meeting from the debris.
const (
	// MinSpeakingSeconds is the floor for a label to count as a participant.
	MinSpeakingSeconds = 15.0
	// MinUtterances is an alternative floor: someone who interjects briefly but
	// repeatedly was present, even if their total time is short.
	MinUtterances = 5
)

// IsSubstantive reports whether a speaker held the floor enough to be treated
// as a participant rather than as diarization noise.
func (st SpeakerStat) IsSubstantive() bool {
	if st.IsOwner {
		return true
	}
	return st.SpeakingSeconds >= MinSpeakingSeconds || st.Utterances >= MinUtterances
}

// SubstantiveSpeakers returns only the speakers who were really participating,
// ordered by speaking time descending.
func (s *Session) SubstantiveSpeakers() []SpeakerStat {
	all := s.SpeakerStats()
	out := make([]SpeakerStat, 0, len(all))
	for _, st := range all {
		if st.IsSubstantive() {
			out = append(out, st)
		}
	}
	return out
}

// SpeechSeconds returns the total time anyone was speaking.
func (s *Session) SpeechSeconds() float64 {
	var total float64
	for _, seg := range s.Segments {
		total += seg.Duration()
	}
	return total
}

// StartedAt returns the session start time, falling back to the file's
// recorded creation time.
func (s *Session) StartedAt() time.Time {
	if !s.CreatedAt.IsZero() {
		return s.CreatedAt
	}
	return s.EndedAt
}

// DurationSeconds returns the session length, deriving it from timestamps when
// the recorder did not supply one.
func (s *Session) DurationSeconds() float64 {
	if s.Duration > 0 {
		return s.Duration
	}
	if !s.EndedAt.IsZero() && !s.CreatedAt.IsZero() {
		if d := s.EndedAt.Sub(s.CreatedAt).Seconds(); d > 0 {
			return d
		}
	}
	var max float64
	for _, seg := range s.Segments {
		if seg.EndTime > max {
			max = seg.EndTime
		}
	}
	return max
}

// FormatTimestamp renders an offset in seconds as [hh:]mm:ss.
func FormatTimestamp(seconds float64) string {
	if seconds < 0 || math.IsNaN(seconds) {
		seconds = 0
	}
	total := int(seconds)
	h, m, sec := total/3600, (total%3600)/60, total%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%02d:%02d", m, sec)
}

// Turn is a run of consecutive segments from a single speaker, merged into one
// conversational turn.
type Turn struct {
	Speaker   string
	Text      string
	StartTime float64
	EndTime   float64
	Source    string
	// FirstSegment is the index into Session.Segments where this turn begins.
	FirstSegment int
}

// Turns merges consecutive same-speaker segments into conversational turns.
//
// Diarizers emit many short fragments per speaker; collapsing them into turns
// both shortens prompts and makes adjacency ("who answered whom") meaningful,
// which is what speaker identification depends on.
func (s *Session) Turns() []Turn {
	var turns []Turn
	for i, seg := range s.Segments {
		text := strings.TrimSpace(seg.Text)
		if text == "" {
			continue
		}
		if n := len(turns); n > 0 && turns[n-1].Speaker == seg.Speaker {
			turns[n-1].Text += " " + text
			turns[n-1].EndTime = seg.EndTime
			continue
		}
		turns = append(turns, Turn{
			Speaker:      seg.Speaker,
			Text:         text,
			StartTime:    seg.StartTime,
			EndTime:      seg.EndTime,
			Source:       seg.Source,
			FirstSegment: i,
		})
	}
	return turns
}

// Transcript renders the session as timestamped, speaker-attributed turns.
func (s *Session) Transcript() string {
	var b strings.Builder
	for _, t := range s.Turns() {
		fmt.Fprintf(&b, "[%s] %s: %s\n", FormatTimestamp(t.StartTime), t.Speaker, t.Text)
	}
	return b.String()
}
