package chatlog

import (
	"strings"
	"testing"
)

func headings(l *Log) []string {
	var out []string
	for _, s := range l.Sections() {
		out = append(out, s.Heading)
	}
	return out
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		content  string
		format   string
		headings []string
		contains string
	}{
		{
			name: "role jsonl dated by its name",
			path: "Downloads/chat_history_20250427164006.jsonl",
			content: `{"role":"user","content":"How should the prefill queue time out?"}
{"role":"assistant","content":"Per request, at 30 seconds."}
{"role":"user","content":[{"type":"text","text":"And alerting?"}]}
{"role":"assistant","content":"Alert on queue age above a minute."}`,
			format: FormatRoleJSONL, headings: []string{"2025-04-27"},
			contains: "user: And alerting?",
		},
		{
			name: "assistant session skips tool records",
			path: "sessions/abc.jsonl",
			content: `{"type":"summary","summary":"x"}
{"type":"user","timestamp":"2026-06-01T10:00:00Z","message":{"role":"user","content":"Rename the flag"}}
{"type":"assistant","timestamp":"2026-06-01T10:00:05Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit"}]}}
{"type":"assistant","timestamp":"2026-06-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Renamed."}]}}
{"type":"user","timestamp":"2026-06-02T09:00:00Z","message":{"role":"user","content":"Ship it"}}
{"type":"assistant","timestamp":"2026-06-02T09:00:30Z","message":{"role":"assistant","content":"Shipped."}}`,
			format: FormatSession, headings: []string{"2026-06-01", "2026-06-02"},
			contains: "10:01 assistant: Renamed.",
		},
		{
			name: "chatgpt export, one thread per conversation",
			path: "export/conversations.json",
			content: `[{"title":"Queue timeouts","create_time":1748772000,"mapping":{
 "b":{"message":{"author":{"role":"assistant"},"create_time":1748772060,"content":{"parts":["Thirty seconds."]}}},
 "a":{"message":{"author":{"role":"user"},"create_time":1748772000,"content":{"parts":["What timeout?"]}}},
 "s":{"message":{"author":{"role":"system"},"content":{"parts":["You are helpful"]}}}}},
 {"title":"Alerting","create_time":1748858400,"mapping":{
 "c":{"message":{"author":{"role":"user"},"create_time":1748858400,"content":{"parts":["Alert on what?"]}}},
 "d":{"message":{"author":{"role":"assistant"},"create_time":1748858460,"content":{"parts":["Queue age."]}}}}}]`,
			format: FormatChatGPT, headings: []string{"2025-06-01 > Queue timeouts", "2025-06-02 > Alerting"},
			contains: "user: What timeout?\n",
		},
		{
			name: "slack day file",
			path: "slack/eng/2026-06-01.json",
			content: `[{"type":"message","user":"U1","user_profile":{"real_name":"Kevin Li"},"text":"Prefill stalled again","ts":"1780308000.000100"},
{"type":"message","subtype":"channel_join","user":"U2","text":"joined","ts":"1780308001.000100"},
{"type":"message","user":"U2","user_name":"mona","text":"Looking","ts":"1780308060.000200"},
{"type":"message","user":"U1","user_profile":{"real_name":"Kevin Li"},"text":"Timeout it at 30s","ts":"1780308120.000300"},
{"type":"message","user":"U2","user_name":"mona","text":"Agreed","ts":"1780308180.000400"}]`,
			format: FormatSlack, headings: []string{"2026-06-01"},
			contains: "Kevin Li: Prefill stalled again",
		},
		{
			name: "teams graph page, newest first, html bodies",
			path: "teams/thread.json",
			content: `{"value":[
{"createdDateTime":"2026-06-01T16:45:00Z","messageType":"message","from":{"user":{"displayName":"Nikita"}},"body":{"contentType":"html","content":"<p>Fixed &amp; deployed</p>"}},
{"createdDateTime":"2026-06-01T16:44:00Z","messageType":"message","from":{"user":{"displayName":"Imran"}},"body":{"contentType":"text","content":"Try client_secret_post"}},
{"createdDateTime":"2026-06-01T16:43:00Z","messageType":"systemEventMessage","body":{"contentType":"html","content":"<p>added</p>"}},
{"createdDateTime":"2026-06-01T16:42:30Z","messageType":"message","from":{"user":{"displayName":"Nikita"}},"body":{"contentType":"text","content":"Which method?"}},
{"createdDateTime":"2026-06-01T16:42:00Z","messageType":"message","from":{"user":{"displayName":"Nikita"}},"body":{"contentType":"text","content":"Token exchange fails"}}]}`,
			format: FormatTeams, headings: []string{"2026-06-01"},
			contains: "16:45 Nikita: Fixed & deployed",
		},
		{
			name: "pasted bracketed lines with a day separator",
			path: "notes/oauth-thread.txt",
			content: `Monday, June 1, 2026
[10:32 AM] Kevin: token exchange fails
with invalid_client
[10:33 AM] Imran: which auth method?
[10:35 AM] Kevin: basic
[10:36 AM] Imran: it registered post
2026-06-02
[09:00] Kevin: fixed`,
			format: FormatPasted, headings: []string{"2026-06-01", "2026-06-02"},
			contains: "10:32 Kevin: token exchange fails\nwith invalid_client",
		},
		{
			name: "copied from slack: header lines above messages",
			path: "notes/thread.txt",
			content: `Kevin Li  10:32 AM
token exchange fails
Imran Yousuf  10:33 AM
which auth method?
Kevin Li  10:35 AM
basic
Imran Yousuf  10:36 AM
it registered post`,
			format: FormatPasted, headings: []string{"Part 1"},
			contains: "Kevin Li: token exchange fails",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, ok := Detect(tt.path, []byte(tt.content))
			if !ok {
				t.Fatal("not detected")
			}
			if log.Format != tt.format {
				t.Errorf("format = %s, want %s", log.Format, tt.format)
			}
			if got := headings(log); strings.Join(got, "|") != strings.Join(tt.headings, "|") {
				t.Errorf("headings = %q, want %q", got, tt.headings)
			}
			all := ""
			for _, s := range log.Sections() {
				all += s.Text + "\n"
			}
			if !strings.Contains(all, tt.contains) {
				t.Errorf("sections do not contain %q:\n%s", tt.contains, all)
			}
		})
	}
}

func TestDetectLeavesOtherDocumentsAlone(t *testing.T) {
	for name, tc := range map[string]struct{ path, content string }{
		"email quoting a chat": {"notes/reply.txt", `Scott,

Thanks for looping us in. Kevin: sounds good was the reply.
[10:32] Kevin: we can ship
Two readings of the question follow, and each matters.
The first is the protocol reading.
The second is the property reading.
Suggested next step: a call this week.
Regards`},
		"config json": {"config/app.json", `[{"name":"a","ts":"x","value":1}]`},
		"graph calendar events": {"exports/events.json", `{"value":[` +
			`{"createdDateTime":"2026-06-01T10:00:00Z","subject":"Sync","body":{"contentType":"html","content":"<p>agenda one</p>"},"organizer":{"emailAddress":{"name":"Kevin"}}},` +
			`{"createdDateTime":"2026-06-02T10:00:00Z","subject":"Sync","body":{"contentType":"html","content":"<p>agenda two</p>"},"organizer":{"emailAddress":{"name":"Kevin"}}},` +
			`{"createdDateTime":"2026-06-03T10:00:00Z","subject":"Sync","body":{"contentType":"html","content":"<p>agenda three</p>"},"organizer":{"emailAddress":{"name":"Kevin"}}},` +
			`{"createdDateTime":"2026-06-04T10:00:00Z","subject":"Sync","body":{"contentType":"html","content":"<p>agenda four</p>"},"organizer":{"emailAddress":{"name":"Kevin"}}}]}`},
		"data jsonl":  {"data/rows.jsonl", `{"id":1,"name":"a"}` + "\n" + `{"id":2,"name":"b"}`},
		"too short":   {"notes/two.txt", "[10:32] Kevin: hi\n[10:33] Mona: hello"},
		"plain prose": {"notes/plan.txt", "The plan is to time out prefill at 30 seconds.\nThen alert on queue age."},
		"application log": {"notes/run.txt", "2026-06-01 10:32:00 INFO: started\n2026-06-01 10:32:01 INFO: listening\n" +
			"2026-06-01 10:33:00 WARN: slow request\n2026-06-01 10:34:00 ERROR: timeout\n2026-06-01 10:35:00 INFO: recovered"},
		"one voice": {"notes/diary.txt", "[09:00] Me: up\n[10:00] Me: coffee\n[11:00] Me: code\n[12:00] Me: lunch\n[13:00] Me: code"},
	} {
		if log, ok := Detect(tc.path, []byte(tc.content)); ok {
			t.Errorf("%s: detected as %s with %d messages", name, log.Format, len(log.Messages))
		}
	}
}

func TestSectionsSplitALongDay(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 40; i++ {
		b.WriteString(`{"role":"user","content":"` + strings.Repeat("word ", 60) + `"}` + "\n")
	}
	log, ok := Detect("chat_history_20250427.jsonl", []byte(b.String()))
	if !ok {
		t.Fatal("not detected")
	}
	secs := log.Sections()
	if len(secs) < 2 || secs[1].Heading != "2025-04-27 (2)" {
		t.Fatalf("headings = %q", headings(log))
	}
	for _, s := range secs {
		if len(s.Text) > maxSectionText {
			t.Errorf("section %s is %d long", s.Heading, len(s.Text))
		}
	}
}
