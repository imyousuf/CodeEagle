package chatlog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// readJSONL reads one message per line: {"role", "content"} as chat
// histories store them, or an assistant session's records, which wrap the
// message and carry a timestamp. Any line that is neither ends it: a JSONL
// file of something else is not a chat.
func readJSONL(content []byte) *Log {
	log := &Log{Format: FormatRoleJSONL}
	sc := bufio.NewScanner(bytes.NewReader(content))
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	other := 0
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec struct {
			Role      string          `json:"role"`
			Content   json.RawMessage `json:"content"`
			Type      string          `json:"type"`
			Timestamp string          `json:"timestamp"`
			Message   *struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil
		}
		switch {
		case rec.Role != "" && rec.Content != nil:
			if text := contentText(rec.Content); text != "" {
				log.Messages = append(log.Messages, Message{Speaker: rec.Role, Text: text})
			}
		case rec.Message != nil && rec.Message.Role != "":
			log.Format = FormatSession
			text := contentText(rec.Message.Content)
			if text == "" {
				continue // a tool call or its result, not something said
			}
			t, _ := time.Parse(time.RFC3339Nano, rec.Timestamp)
			log.Messages = append(log.Messages, Message{Time: t, Speaker: rec.Message.Role, Text: text})
		default:
			// Session files interleave records that are not messages
			// (summaries, snapshots); a file of nothing else is not a chat.
			other++
			if len(log.Messages) == 0 && other >= 50 {
				return nil
			}
		}
	}
	if other > 2*len(log.Messages) {
		return nil
	}
	return log
}

// contentText is the text of a message's content: a string, or the text
// parts of a list of parts.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if (p.Type == "text" || p.Type == "") && strings.TrimSpace(p.Text) != "" {
			texts = append(texts, strings.TrimSpace(p.Text))
		}
	}
	return strings.Join(texts, "\n")
}

// readJSON recognizes the exports that are one JSON document: a ChatGPT
// conversations.json, a Slack channel's day file, a Teams (Graph) message
// list.
func readJSON(content []byte) *Log {
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) == 0 {
		return nil
	}
	// Most JSON is not a chat; parsing all of it to find out is not free.
	head := trimmed[:min(len(trimmed), 64<<10)]
	if !bytes.Contains(head, []byte(`"mapping"`)) && !bytes.Contains(head, []byte(`"ts"`)) &&
		!bytes.Contains(head, []byte(`"createdDateTime"`)) {
		return nil
	}
	if trimmed[0] == '{' {
		var page struct {
			Value []teamsMessage `json:"value"`
		}
		if json.Unmarshal(trimmed, &page) == nil && len(page.Value) > 0 {
			return teamsLog(page.Value)
		}
		return nil
	}
	if trimmed[0] != '[' {
		return nil
	}
	var probe []map[string]json.RawMessage
	if json.Unmarshal(trimmed, &probe) != nil || len(probe) == 0 {
		return nil
	}
	first := probe[0]
	switch {
	case first["mapping"] != nil:
		var convs []chatgptConversation
		if json.Unmarshal(trimmed, &convs) != nil {
			return nil
		}
		return chatgptLog(convs)
	case first["ts"] != nil && (first["text"] != nil || first["type"] != nil):
		var msgs []slackMessage
		if json.Unmarshal(trimmed, &msgs) != nil {
			return nil
		}
		return slackLog(msgs)
	case first["createdDateTime"] != nil && first["body"] != nil:
		var msgs []teamsMessage
		if json.Unmarshal(trimmed, &msgs) != nil {
			return nil
		}
		return teamsLog(msgs)
	}
	return nil
}

type chatgptConversation struct {
	Title   string                 `json:"title"`
	Created float64                `json:"create_time"`
	Mapping map[string]chatgptNode `json:"mapping"`
}

type chatgptNode struct {
	Message *struct {
		Author struct {
			Role string `json:"role"`
		} `json:"author"`
		Created *float64 `json:"create_time"`
		Content struct {
			Parts []json.RawMessage `json:"parts"`
		} `json:"content"`
	} `json:"message"`
}

func chatgptLog(convs []chatgptConversation) *Log {
	log := &Log{Format: FormatChatGPT}
	for _, c := range convs {
		var msgs []Message
		for _, n := range c.Mapping {
			m := n.Message
			if m == nil || m.Author.Role == "system" || m.Author.Role == "tool" {
				continue
			}
			var texts []string
			for _, p := range m.Content.Parts {
				var s string
				if json.Unmarshal(p, &s) == nil && strings.TrimSpace(s) != "" {
					texts = append(texts, strings.TrimSpace(s))
				}
			}
			if len(texts) == 0 {
				continue
			}
			at := c.Created
			if m.Created != nil {
				at = *m.Created
			}
			msgs = append(msgs, Message{Time: epoch(at), Speaker: m.Author.Role, Text: strings.Join(texts, "\n"), Thread: c.Title})
		}
		// The mapping is a tree keyed by ID; time is the order it was said.
		sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].Time.Before(msgs[j].Time) })
		log.Messages = append(log.Messages, msgs...)
	}
	return log
}

type slackMessage struct {
	Type     string `json:"type"`
	User     string `json:"user"`
	UserName string `json:"user_name"`
	Profile  *struct {
		RealName    string `json:"real_name"`
		DisplayName string `json:"display_name"`
	} `json:"user_profile"`
	Text     string `json:"text"`
	TS       string `json:"ts"`
	ThreadTS string `json:"thread_ts"`
	Subtype  string `json:"subtype"`
}

func slackLog(msgs []slackMessage) *Log {
	log := &Log{Format: FormatSlack}
	for _, m := range msgs {
		if m.Type != "" && m.Type != "message" || strings.HasSuffix(m.Subtype, "_join") || strings.TrimSpace(m.Text) == "" {
			continue
		}
		who := m.UserName
		if m.Profile != nil {
			who = firstNonEmpty(m.Profile.RealName, m.Profile.DisplayName, who)
		}
		who = firstNonEmpty(who, m.User)
		ts, _ := strconv.ParseFloat(m.TS, 64)
		log.Messages = append(log.Messages, Message{Time: epoch(ts), Speaker: who, Text: m.Text})
	}
	return log
}

type teamsMessage struct {
	Created string `json:"createdDateTime"`
	Type    string `json:"messageType"`
	From    *struct {
		User *struct {
			DisplayName string `json:"displayName"`
		} `json:"user"`
	} `json:"from"`
	Body struct {
		ContentType string `json:"contentType"`
		Content     string `json:"content"`
	} `json:"body"`
	Subject string `json:"subject"`
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

func teamsLog(msgs []teamsMessage) *Log {
	log := &Log{Format: FormatTeams}
	for _, m := range msgs {
		if m.Type != "" && m.Type != "message" {
			continue
		}
		text := m.Body.Content
		if strings.EqualFold(m.Body.ContentType, "html") {
			text = html.UnescapeString(tagRe.ReplaceAllString(text, " "))
		}
		text = strings.Join(strings.Fields(text), " ")
		if text == "" {
			continue
		}
		// A message has a sender; other Graph lists with a body and a
		// creation time (calendar events, posts by an app) do not.
		if m.From == nil || m.From.User == nil {
			continue
		}
		who := m.From.User.DisplayName
		t, _ := time.Parse(time.RFC3339Nano, m.Created)
		log.Messages = append(log.Messages, Message{Time: t, Speaker: who, Text: text})
	}
	// Graph lists newest first.
	sort.SliceStable(log.Messages, func(i, j int) bool { return log.Messages[i].Time.Before(log.Messages[j].Time) })
	return log
}

func epoch(sec float64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	whole := int64(sec)
	return time.Unix(whole, int64((sec-float64(whole))*1e9)).UTC()
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
