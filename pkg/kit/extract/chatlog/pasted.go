package chatlog

import (
	"regexp"
	"strings"
	"time"
)

// Message lines a pasted chat is made of. Each names a time and a speaker;
// text that follows on later lines continues the message.
var (
	// [10:32] Kevin: text, [2026-06-01 10:32] Kevin: text, [10:32 AM] Kevin: text
	bracketed = regexp.MustCompile(`^\[(?:(20\d\d-\d\d-\d\d)[ T])?(\d{1,2}:\d\d(?::\d\d)?(?:\s?[AaPp][Mm])?)\]\s*([^:\[\]\n]{1,40}?):\s+(.*)$`)
	// 2026-06-01 10:32 Kevin: text
	stamped = regexp.MustCompile(`^(20\d\d-\d\d-\d\d)[ T](\d{1,2}:\d\d(?::\d\d)?)\s+([^:\n]{1,40}?):\s+(.*)$`)
	// Kevin Li  10:32 AM, or Kevin Li 6/1/2026, 10:32 AM, as copying from
	// Slack or Teams leaves a header line above the message.
	header = regexp.MustCompile(`^([A-Z][\p{L}.' -]{1,40}?),?\s+(?:(\d{1,2}/\d{1,2}/\d{2,4}),?\s+)?(\d{1,2}:\d\d\s?[AaPp][Mm])$`)
	// A line that is only a date separates days: 2026-06-01, or
	// Monday, June 1, 2026.
	dayLine = regexp.MustCompile(`^(?:(?:Mon|Tues|Wednes|Thurs|Fri|Satur|Sun)day,?\s+)?((?:January|February|March|April|May|June|July|August|September|October|November|December)\s+\d{1,2},\s+20\d\d|20\d\d-\d\d-\d\d)$`)
)

// minMessageShare is the share of a text's non-empty lines that must be
// message lines for it to be a chat. Continuation lines count against it,
// so an email that quotes "Kevin: sounds good" twice is not a chat.
const minMessageShare = 0.3

// readPasted reads a conversation pasted into text.
func readPasted(text string) *Log {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	log := &Log{Format: FormatPasted}
	var day time.Time
	nonEmpty, headers := 0, 0
	var cur *Message
	push := func() {
		if cur != nil && strings.TrimSpace(cur.Text) != "" {
			cur.Text = strings.TrimSpace(cur.Text)
			log.Messages = append(log.Messages, *cur)
		}
		cur = nil
	}
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		nonEmpty++
		if m := dayLine.FindStringSubmatch(line); m != nil {
			if t, ok := parseDay(m[1]); ok {
				day = t
				continue
			}
		}
		if m := bracketed.FindStringSubmatch(line); m != nil {
			push()
			headers++
			cur = &Message{Time: at(m[1], m[2], day), Speaker: strings.TrimSpace(m[3]), Text: m[4]}
			continue
		}
		if m := stamped.FindStringSubmatch(line); m != nil {
			push()
			headers++
			cur = &Message{Time: at(m[1], m[2], day), Speaker: strings.TrimSpace(m[3]), Text: m[4]}
			continue
		}
		if m := header.FindStringSubmatch(line); m != nil {
			push()
			headers++
			d := day
			if m[2] != "" {
				if t, err := time.Parse("1/2/2006", m[2]); err == nil {
					d = t
				} else if t, err := time.Parse("1/2/06", m[2]); err == nil {
					d = t
				}
			}
			cur = &Message{Time: at("", m[3], d), Speaker: strings.TrimSpace(m[1])}
			continue
		}
		if cur != nil {
			if cur.Text != "" {
				cur.Text += "\n"
			}
			cur.Text += line
		}
	}
	push()
	if nonEmpty == 0 || float64(headers)/float64(nonEmpty) < minMessageShare {
		return nil
	}
	// A conversation has at least two sides; an application's log, whose
	// "speakers" are levels and components, is not one.
	speakers := make(map[string]bool)
	for _, m := range log.Messages {
		if logLevels[strings.ToUpper(m.Speaker)] {
			return nil
		}
		speakers[m.Speaker] = true
	}
	if len(speakers) < 2 {
		return nil
	}
	return log
}

var logLevels = map[string]bool{
	"TRACE": true, "DEBUG": true, "INFO": true, "NOTICE": true, "WARN": true, "WARNING": true,
	"ERROR": true, "FATAL": true, "CRITICAL": true, "PANIC": true,
}

func parseDay(s string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02", "January 2, 2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// at combines a date (stated, or the day the log is on) with a clock time.
// Without a date the time of day alone is not a time, so it is dropped.
func at(date, clock string, day time.Time) time.Time {
	base := day
	if date != "" {
		if t, err := time.Parse("2006-01-02", date); err == nil {
			base = t
		}
	}
	if base.IsZero() {
		return time.Time{}
	}
	clock = strings.ToUpper(strings.ReplaceAll(clock, " ", ""))
	for _, layout := range []string{"15:04", "15:04:05", "3:04PM", "3:04:05PM"} {
		if t, err := time.Parse(layout, clock); err == nil {
			return time.Date(base.Year(), base.Month(), base.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
		}
	}
	return base
}
