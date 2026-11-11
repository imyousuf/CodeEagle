// Package chatlog recognizes chat logs among documents (exports from chat
// tools, an assistant's history, a conversation pasted into a text file) and
// splits them into one section per day, so a conversation can be read,
// quoted and dated like any other document.
package chatlog

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Message is one message of a chat.
type Message struct {
	// Time is when it was sent; zero when the log does not say.
	Time    time.Time
	Speaker string
	Text    string
	// Thread names the conversation it belongs to, where a log holds several.
	Thread string
}

// Log is a chat log.
type Log struct {
	// Format names the layout it was read from.
	Format   string
	Messages []Message
}

// Section is one part of a chat log: a day, or a day of one thread.
type Section struct {
	Heading string
	Text    string
}

// Formats.
const (
	FormatRoleJSONL = "role-jsonl"
	FormatSession   = "assistant-session"
	FormatChatGPT   = "chatgpt"
	FormatSlack     = "slack"
	FormatTeams     = "teams"
	FormatPasted    = "pasted"
)

// Bounds.
const (
	maxSections    = 200
	maxSectionText = 6000
	maxMessageText = 2000
	// minMessages is the fewest messages a log must hold to be taken as a
	// chat: a document with two lines that look like messages is a
	// document.
	minMessages = 4
)

// Detect reads content as a chat log if it is one. JSON layouts are
// recognized by their shape, text by the share of its lines that are
// messages. path supplies a date for logs that carry none in their
// messages (chat_history_20250427164006.jsonl).
func Detect(path string, content []byte) (*Log, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	var log *Log
	switch ext {
	case ".jsonl":
		log = readJSONL(content)
	case ".json":
		log = readJSON(content)
	default:
		log = readPasted(string(content))
	}
	if log == nil || len(log.Messages) < minMessages {
		return nil, false
	}
	if day := dateFromName(path); !day.IsZero() {
		for i := range log.Messages {
			if log.Messages[i].Time.IsZero() {
				log.Messages[i].Time = day
			}
		}
	}
	return log, true
}

// DetectText reads text extracted from a document (a PDF or word processor
// file) as a pasted chat.
func DetectText(path, text string) (*Log, bool) {
	log := readPasted(text)
	if log == nil || len(log.Messages) < minMessages {
		return nil, false
	}
	if day := dateFromName(path); !day.IsZero() {
		for i := range log.Messages {
			if log.Messages[i].Time.IsZero() {
				log.Messages[i].Time = day
			}
		}
	}
	return log, true
}

var nameDate = regexp.MustCompile(`(20\d\d)-?(\d\d)-?(\d\d)`)

// dateFromName is the date a file name carries, or zero.
func dateFromName(path string) time.Time {
	m := nameDate.FindStringSubmatch(filepath.Base(path))
	if m == nil {
		return time.Time{}
	}
	t, err := time.Parse("20060102", m[1]+m[2]+m[3])
	if err != nil {
		return time.Time{}
	}
	return t
}

// Sections splits a log into a section per day, and per thread within a
// day where the log holds several; a day too long for one section
// continues in the next. Messages read "15:04 Speaker: text". A log with
// no dates at all is split into parts.
func (l *Log) Sections() []Section {
	type key struct{ day, thread string }
	var order []key
	groups := make(map[key][]Message)
	threads := make(map[string]bool)
	for _, m := range l.Messages {
		threads[m.Thread] = true
	}
	for _, m := range l.Messages {
		k := key{thread: m.Thread}
		if !m.Time.IsZero() {
			k.day = m.Time.Format("2006-01-02")
		}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], m)
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].day != order[j].day {
			return order[i].day < order[j].day
		}
		return false
	})

	var out []Section
	part := 0
	for _, k := range order {
		heading := k.day
		if heading == "" {
			part++
			heading = fmt.Sprintf("Part %d", part)
		}
		if len(threads) > 1 && k.thread != "" {
			heading += " > " + clip(k.thread, 80)
		}
		var b strings.Builder
		cont := 1
		flush := func() {
			if b.Len() == 0 {
				return
			}
			h := heading
			if cont > 1 {
				h = fmt.Sprintf("%s (%d)", heading, cont)
			}
			out = append(out, Section{Heading: h, Text: strings.TrimSpace(b.String())})
			b.Reset()
			cont++
		}
		for _, m := range groups[k] {
			line := renderMessage(m)
			if b.Len() > 0 && b.Len()+len(line) > maxSectionText {
				flush()
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
		flush()
		if len(out) >= maxSections {
			return out[:maxSections]
		}
	}
	return out
}

func renderMessage(m Message) string {
	text := strings.TrimSpace(m.Text)
	text = clip(text, maxMessageText)
	speaker := m.Speaker
	if speaker == "" {
		speaker = "unknown"
	}
	if m.Time.IsZero() || (m.Time.Hour() == 0 && m.Time.Minute() == 0 && m.Time.Second() == 0) {
		return speaker + ": " + text
	}
	return m.Time.Format("15:04") + " " + speaker + ": " + text
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}
