// Package llmjson gets JSON out of a language model reliably: a schema the
// provider enforces where it can, and a reply salvaged where it cannot.
package llmjson

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/imyousuf/CodeEagle/pkg/llm"
)

// Object builds a strict JSON Schema object. Strict mode requires every
// property to be listed as required and forbids extras, so the helper makes
// that the default rather than something to remember.
func Object(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = make([]string, 0, len(props))
		for k := range props {
			required = append(required, k)
		}
		sort.Strings(required)
	}
	return map[string]any{
		"type":                 "object",
		"properties":           props,
		"required":             required,
		"additionalProperties": false,
	}
}

// ChatJSON sends one user message and decodes the reply into out, returning
// what the request cost.
//
// Providers that can enforce a schema do so. Those that cannot (a local
// Ollama model, for instance) are asked for JSON and their reply is salvaged,
// since small models habitually wrap JSON in prose or a code fence.
func ChatJSON(ctx context.Context, client llm.Client, system, user string, schema *llm.JSONSchema, out any) (llm.TokenUsage, error) {
	messages := []llm.Message{{Role: llm.RoleUser, Content: user}}

	var resp *llm.Response
	var err error
	if sc, ok := client.(llm.StructuredClient); ok {
		resp, err = sc.ChatJSON(ctx, system, messages, schema)
	} else {
		resp, err = client.Chat(ctx, system+"\n\nRespond with JSON only. No prose, no code fences.", messages)
	}
	if err != nil {
		return llm.TokenUsage{}, err
	}

	body := ExtractJSON(resp.Content)
	if body == "" {
		return resp.Usage, fmt.Errorf("no JSON in response (%d bytes)", len(resp.Content))
	}
	if err := json.Unmarshal([]byte(body), out); err != nil {
		return resp.Usage, fmt.Errorf("decode response: %w", err)
	}
	return resp.Usage, nil
}

// ExtractJSON pulls the JSON document out of a reply that may be wrapped in
// a code fence or surrounded by commentary.
func ExtractJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if fenced := insideFence(s); fenced != "" {
		s = fenced
	}
	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return ""
	}
	opener := rune(s[start])
	closer := '}'
	if opener == '[' {
		closer = ']'
	}
	// Scan for the matching close, ignoring braces inside string literals.
	depth := 0
	inString := false
	escaped := false
	for i, r := range s[start:] {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && inString:
			escaped = true
		case r == '"':
			inString = !inString
		case inString:
			// Braces inside a string are not structural.
		case r == opener:
			depth++
		case r == closer:
			depth--
			if depth == 0 {
				return s[start : start+i+len(string(r))]
			}
		}
	}
	return s[start:]
}

// insideFence returns the contents of the first fenced code block, if any.
func insideFence(s string) string {
	const fence = "```"
	start := strings.Index(s, fence)
	if start < 0 {
		return ""
	}
	rest := s[start+len(fence):]
	// Drop an optional language tag on the opening fence.
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 && !strings.Contains(rest[:nl], fence) {
		rest = rest[nl+1:]
	}
	if end := strings.Index(rest, fence); end >= 0 {
		return rest[:end]
	}
	return rest
}
