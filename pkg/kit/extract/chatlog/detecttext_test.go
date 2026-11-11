package chatlog_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/pkg/kit/extract/chatlog"
)

// These tests pin DetectText exactly, from outside the package: the format,
// every message (time, speaker, text, thread) and every section, so they
// survive the package moving with only the import path changed.

// msg renders a message for comparison; a zero time renders as "-".
func msg(m chatlog.Message) string {
	at := "-"
	if !m.Time.IsZero() {
		at = m.Time.Format(time.RFC3339)
	}
	return fmt.Sprintf("%s|%s|%q|%s", at, m.Speaker, m.Text, m.Thread)
}

func TestDetectTextPinsMessagesAndSections(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		text     string
		messages []string
		sections []chatlog.Section
	}{
		{
			name: "bracketed lines with day separators and continuations",
			path: "notes/oauth-thread.pdf",
			text: "Monday, June 1, 2026\n" +
				"[10:32 AM] Kevin: token exchange fails\n" +
				"with invalid_client\n" +
				"[10:33 AM] Imran: which auth method?\n" +
				"[1:05 PM] Kevin: basic\n" +
				"June 2, 2026\n" +
				"[09:00] Imran: fixed: it registered post\n",
			messages: []string{
				`2026-06-01T10:32:00Z|Kevin|"token exchange fails\nwith invalid_client"|`,
				`2026-06-01T10:33:00Z|Imran|"which auth method?"|`,
				`2026-06-01T13:05:00Z|Kevin|"basic"|`,
				// The speaker ends at the first colon.
				`2026-06-02T09:00:00Z|Imran|"fixed: it registered post"|`,
			},
			sections: []chatlog.Section{
				{Heading: "2026-06-01", Text: "10:32 Kevin: token exchange fails\nwith invalid_client\n10:33 Imran: which auth method?\n13:05 Kevin: basic"},
				{Heading: "2026-06-02", Text: "09:00 Imran: fixed: it registered post"},
			},
		},
		{
			name: "dates inside the brackets, with seconds",
			path: "notes/deploy.docx",
			text: "[2026-06-01 10:32:15] Kevin: deploy started\n" +
				"[2026-06-01T10:40] Mona: canary green\n" +
				"[2026-06-02 08:00] Kevin: promoting\n" +
				"[2026-06-02 08:05] Mona: done\n",
			messages: []string{
				`2026-06-01T10:32:15Z|Kevin|"deploy started"|`,
				`2026-06-01T10:40:00Z|Mona|"canary green"|`,
				`2026-06-02T08:00:00Z|Kevin|"promoting"|`,
				`2026-06-02T08:05:00Z|Mona|"done"|`,
			},
			sections: []chatlog.Section{
				{Heading: "2026-06-01", Text: "10:32 Kevin: deploy started\n10:40 Mona: canary green"},
				{Heading: "2026-06-02", Text: "08:00 Kevin: promoting\n08:05 Mona: done"},
			},
		},
		{
			name: "stamped lines",
			path: "notes/deploy.odt",
			text: "2026-06-01 10:32 Kevin Li: deploy started\n" +
				"2026-06-01 10:40 Mona: canary green\n" +
				"2026-06-01T11:00:30 Kevin Li: promoting\n" +
				"2026-06-01 11:05 Mona: done\n",
			messages: []string{
				`2026-06-01T10:32:00Z|Kevin Li|"deploy started"|`,
				`2026-06-01T10:40:00Z|Mona|"canary green"|`,
				`2026-06-01T11:00:30Z|Kevin Li|"promoting"|`,
				`2026-06-01T11:05:00Z|Mona|"done"|`,
			},
			sections: []chatlog.Section{
				{Heading: "2026-06-01", Text: "10:32 Kevin Li: deploy started\n10:40 Mona: canary green\n11:00 Kevin Li: promoting\n11:05 Mona: done"},
			},
		},
		{
			name: "header lines copied from Teams, four- and two-digit years",
			path: "notes/thread.pdf",
			text: "Kevin Li 6/1/2026, 10:32 AM\n" +
				"token exchange fails\n" +
				"Imran Yousuf 6/1/2026, 10:33 AM\n" +
				"which auth method?\n" +
				"Kevin Li 6/2/26, 9:05 AM\n" +
				"basic\n" +
				"Imran Yousuf 6/2/26, 4:15 PM\n" +
				"it registered post\n",
			messages: []string{
				`2026-06-01T10:32:00Z|Kevin Li|"token exchange fails"|`,
				`2026-06-01T10:33:00Z|Imran Yousuf|"which auth method?"|`,
				`2026-06-02T09:05:00Z|Kevin Li|"basic"|`,
				`2026-06-02T16:15:00Z|Imran Yousuf|"it registered post"|`,
			},
			sections: []chatlog.Section{
				{Heading: "2026-06-01", Text: "10:32 Kevin Li: token exchange fails\n10:33 Imran Yousuf: which auth method?"},
				{Heading: "2026-06-02", Text: "09:05 Kevin Li: basic\n16:15 Imran Yousuf: it registered post"},
			},
		},
		{
			// A clock time with no date is dropped, so a date taken from the
			// file name is midnight and the rendered lines carry no time.
			name: "no dates in the text: the file name dates it",
			path: "Downloads/standup-2026-06-03.pdf",
			text: "[10:32] Kevin: morning\n[10:33] Mona: hi\n[10:34] Kevin: blockers?\n[10:35] Mona: none\n",
			messages: []string{
				`2026-06-03T00:00:00Z|Kevin|"morning"|`,
				`2026-06-03T00:00:00Z|Mona|"hi"|`,
				`2026-06-03T00:00:00Z|Kevin|"blockers?"|`,
				`2026-06-03T00:00:00Z|Mona|"none"|`,
			},
			sections: []chatlog.Section{
				{Heading: "2026-06-03", Text: "Kevin: morning\nMona: hi\nKevin: blockers?\nMona: none"},
			},
		},
		{
			name: "compact file-name date",
			path: "exports/chat_20260604.txt",
			text: "[10:32] Kevin: one\n[10:33] Mona: two\n[10:34] Kevin: three\n[10:35] Mona: four\n",
			messages: []string{
				`2026-06-04T00:00:00Z|Kevin|"one"|`,
				`2026-06-04T00:00:00Z|Mona|"two"|`,
				`2026-06-04T00:00:00Z|Kevin|"three"|`,
				`2026-06-04T00:00:00Z|Mona|"four"|`,
			},
			sections: []chatlog.Section{
				{Heading: "2026-06-04", Text: "Kevin: one\nMona: two\nKevin: three\nMona: four"},
			},
		},
		{
			// The file name only fills messages with no date of their own,
			// and days sort before they are sectioned.
			name: "file-name date fills only undated messages",
			path: "notes/2026-01-01-chat.txt",
			text: "[09:00] Kevin: before any date\n[09:01] Mona: same\n2026-06-01\n[10:00] Kevin: after the date\n[10:01] Mona: also after\n",
			messages: []string{
				`2026-01-01T00:00:00Z|Kevin|"before any date"|`,
				`2026-01-01T00:00:00Z|Mona|"same"|`,
				`2026-06-01T10:00:00Z|Kevin|"after the date"|`,
				`2026-06-01T10:01:00Z|Mona|"also after"|`,
			},
			sections: []chatlog.Section{
				{Heading: "2026-01-01", Text: "Kevin: before any date\nMona: same"},
				{Heading: "2026-06-01", Text: "10:00 Kevin: after the date\n10:01 Mona: also after"},
			},
		},
		{
			name: "an impossible file-name date is ignored",
			path: "notes/report_20261399.pdf",
			text: "[10:32] Kevin: one\n[10:33] Mona: two\n[10:34] Kevin: three\n[10:35] Mona: four\n",
			messages: []string{
				`-|Kevin|"one"|`,
				`-|Mona|"two"|`,
				`-|Kevin|"three"|`,
				`-|Mona|"four"|`,
			},
			sections: []chatlog.Section{
				{Heading: "Part 1", Text: "Kevin: one\nMona: two\nKevin: three\nMona: four"},
			},
		},
		{
			name: "CRLF line endings",
			path: "notes/thread.txt",
			text: "[10:00] Kevin: one\r\n[10:01] Mona: two\r\nand more\r\n[10:02] Kevin: three\r\n[10:03] Mona: four\r\n",
			messages: []string{
				`-|Kevin|"one"|`,
				`-|Mona|"two\nand more"|`,
				`-|Kevin|"three"|`,
				`-|Mona|"four"|`,
			},
			sections: []chatlog.Section{
				{Heading: "Part 1", Text: "Kevin: one\nMona: two\nand more\nKevin: three\nMona: four"},
			},
		},
		{
			// Text before the first message line belongs to no message, and
			// a header with nothing under it is not a message.
			name: "leading prose and an empty header are dropped",
			path: "notes/thread.txt",
			text: "Copied from the channel\n" +
				"Kevin Li  10:31 AM\n" +
				"Kevin Li  10:32 AM\n" +
				"token exchange fails\n" +
				"Imran Yousuf  10:33 AM\n" +
				"which auth method?\n" +
				"Kevin Li  10:35 AM\n" +
				"basic\n" +
				"Imran Yousuf  10:36 AM\n" +
				"it registered post\n",
			messages: []string{
				`-|Kevin Li|"token exchange fails"|`,
				`-|Imran Yousuf|"which auth method?"|`,
				`-|Kevin Li|"basic"|`,
				`-|Imran Yousuf|"it registered post"|`,
			},
			sections: []chatlog.Section{
				{Heading: "Part 1", Text: "Kevin Li: token exchange fails\nImran Yousuf: which auth method?\nKevin Li: basic\nImran Yousuf: it registered post"},
			},
		},
		{
			// Four message lines in thirteen non-empty lines is just over
			// the share a chat needs.
			name: "message share at the threshold",
			path: "notes/thread.txt",
			text: "[10:00] Kevin: one\nc1\nc2\nc3\n[10:01] Mona: two\nc4\nc5\nc6\n[10:02] Kevin: three\nc7\nc8\nc9\n[10:03] Mona: four\n",
			messages: []string{
				`-|Kevin|"one\nc1\nc2\nc3"|`,
				`-|Mona|"two\nc4\nc5\nc6"|`,
				`-|Kevin|"three\nc7\nc8\nc9"|`,
				`-|Mona|"four"|`,
			},
			sections: []chatlog.Section{
				{Heading: "Part 1", Text: "Kevin: one\nc1\nc2\nc3\nMona: two\nc4\nc5\nc6\nKevin: three\nc7\nc8\nc9\nMona: four"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, ok := chatlog.DetectText(tt.path, tt.text)
			if !ok {
				t.Fatal("not detected")
			}
			if log.Format != chatlog.FormatPasted {
				t.Errorf("format = %q, want %q", log.Format, chatlog.FormatPasted)
			}
			var got []string
			for _, m := range log.Messages {
				got = append(got, msg(m))
			}
			if strings.Join(got, "\n") != strings.Join(tt.messages, "\n") {
				t.Errorf("messages =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.messages, "\n"))
			}
			secs := log.Sections()
			if len(secs) != len(tt.sections) {
				t.Fatalf("sections = %q, want %q", secs, tt.sections)
			}
			for i := range secs {
				if secs[i] != tt.sections[i] {
					t.Errorf("section %d = %q, want %q", i, secs[i], tt.sections[i])
				}
			}
		})
	}
}

// TestFormatNames pins the format names, which are stored on document nodes
// as their chat format and so must not change.
func TestFormatNames(t *testing.T) {
	got := []string{chatlog.FormatRoleJSONL, chatlog.FormatSession, chatlog.FormatChatGPT,
		chatlog.FormatSlack, chatlog.FormatTeams, chatlog.FormatPasted}
	want := []string{"role-jsonl", "assistant-session", "chatgpt", "slack", "teams", "pasted"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("formats = %q, want %q", got, want)
	}
}

func TestDetectTextRejects(t *testing.T) {
	for name, tc := range map[string]struct{ path, text string }{
		"empty":       {"notes/a.pdf", ""},
		"blank lines": {"notes/a.pdf", "\n \n\t\n"},
		"plain prose": {"notes/plan.pdf", "The plan is to time out prefill at 30 seconds.\nThen alert on queue age."},
		// Four messages are the fewest that make a chat.
		"three messages": {"notes/a.pdf", "[10:32] Kevin: hi\n[10:33] Mona: hello\n[10:34] Kevin: bye"},
		// Headers with nothing under them are counted towards the share
		// but are not messages, which can leave too few.
		"headers without bodies": {"notes/a.txt", "Kevin Li  10:32 AM\nImran Yousuf  10:33 AM\nKevin Li  10:35 AM\nbasic\nImran Yousuf  10:36 AM\ndone"},
		// Four message lines in fourteen non-empty lines is under the share.
		"message share below the threshold": {"notes/a.txt",
			"[10:00] Kevin: one\nc1\nc2\nc3\n[10:01] Mona: two\nc4\nc5\nc6\n[10:02] Kevin: three\nc7\nc8\nc9\nc10\n[10:03] Mona: four\n"},
		"one voice": {"notes/diary.pdf", "[09:00] Me: up\n[10:00] Me: coffee\n[11:00] Me: code\n[12:00] Me: lunch\n[13:00] Me: code"},
		"application log": {"notes/run.pdf", "2026-06-01 10:32:00 INFO: started\n2026-06-01 10:32:01 INFO: listening\n" +
			"2026-06-01 10:33:00 WARN: slow request\n2026-06-01 10:34:00 ERROR: timeout\n2026-06-01 10:35:00 INFO: recovered"},
		// One speaker named like a log level rejects the whole text,
		// whatever its case.
		"one log-level speaker among people": {"notes/a.pdf", "[10:00] Kevin: one\n[10:01] Mona: two\n[10:02] Warn: three\n[10:03] Mona: four"},
		// DetectText reads only pasted text: a JSON export is not one,
		// whatever the file is called.
		"role jsonl": {"Downloads/chat_history_20250427164006.jsonl",
			`{"role":"user","content":"How should the prefill queue time out?"}` + "\n" +
				`{"role":"assistant","content":"Per request, at 30 seconds."}` + "\n" +
				`{"role":"user","content":"And alerting?"}` + "\n" +
				`{"role":"assistant","content":"Alert on queue age above a minute."}`},
		"slack day file": {"slack/eng/2026-06-01.json",
			`[{"type":"message","user":"U1","text":"a","ts":"1780308000.000100"},{"type":"message","user":"U2","text":"b","ts":"1780308060.000200"},` +
				`{"type":"message","user":"U1","text":"c","ts":"1780308120.000300"},{"type":"message","user":"U2","text":"d","ts":"1780308180.000400"}]`},
	} {
		if log, ok := chatlog.DetectText(tc.path, tc.text); ok || log != nil {
			t.Errorf("%s: DetectText = %+v, %v; want nil, false", name, log, ok)
		}
	}
}

// TestDetectTextMatchesDetectOnText pins that a pasted chat in a text file
// reads the same whether it arrives as bytes through Detect or as extracted
// text through DetectText.
func TestDetectTextMatchesDetectOnText(t *testing.T) {
	const path = "notes/standup-2026-06-03.txt"
	text := "Monday, June 1, 2026\n[10:32 AM] Kevin: token exchange fails\nwith invalid_client\n" +
		"[10:33 AM] Imran: which auth method?\n[10:34] Kevin: basic\n[10:35] Imran: post\n"
	fromText, ok := chatlog.DetectText(path, text)
	if !ok {
		t.Fatal("DetectText did not detect")
	}
	fromBytes, ok := chatlog.Detect(path, []byte(text))
	if !ok {
		t.Fatal("Detect did not detect")
	}
	if fromText.Format != fromBytes.Format || len(fromText.Messages) != len(fromBytes.Messages) {
		t.Fatalf("DetectText = %+v, Detect = %+v", fromText, fromBytes)
	}
	for i := range fromText.Messages {
		if msg(fromText.Messages[i]) != msg(fromBytes.Messages[i]) {
			t.Errorf("message %d: DetectText %s, Detect %s", i, msg(fromText.Messages[i]), msg(fromBytes.Messages[i]))
		}
	}
}
