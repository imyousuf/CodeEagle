package extract_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/kit/extract"
	"github.com/imyousuf/CodeEagle/pkg/kit/extract/chatlog"
)

type ctxKey struct{}

func TestWithPDFReaderTakesOverPDFs(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxKey{}, "caller")
	pdf := pdfOf(helloPage)

	calls := 0
	var sawCtx context.Context
	var sawContent []byte
	fake := func(ctx context.Context, content []byte) (string, error) {
		calls++
		sawCtx, sawContent = ctx, content
		return "--- Page 1 ---\nfrom the fake", nil
	}

	// The extension is matched without regard to case, as for every format.
	got, err := extract.Document(ctx, "papers/Paper.PDF", pdf, extract.WithPDFReader(fake))
	if err != nil || got != "--- Page 1 ---\nfrom the fake" {
		t.Fatalf("Document with a PDF reader = %q, %v; want the reader's text", got, err)
	}
	if calls != 1 {
		t.Fatalf("reader called %d times, want 1", calls)
	}
	if sawCtx.Value(ctxKey{}) != "caller" {
		t.Error("the reader was not given the caller's context")
	}
	if !bytes.Equal(sawContent, pdf) {
		t.Error("the reader was not given the file's content")
	}

	// Its error is the caller's, unwrapped.
	errBroken := errors.New("broken")
	failing := func(context.Context, []byte) (string, error) { return "", errBroken }
	if _, err := extract.Document(ctx, "a.pdf", pdf, extract.WithPDFReader(failing)); !errors.Is(err, errBroken) {
		t.Errorf("Document with a failing reader: error %v, want %v", err, errBroken)
	}

	// Nothing but a PDF reaches it.
	calls = 0
	odt := zipOf(t, zipEntry{"content.xml", odfContent(`<office:text><text:p>still read</text:p></office:text>`)})
	if got, err := extract.Document(ctx, "docs/notes.odt", odt, extract.WithPDFReader(fake)); err != nil || got != "still read" {
		t.Errorf("an ODT with a PDF reader = %q, %v; want %q", got, err, "still read")
	}
	if _, err := extract.Document(ctx, "notes.txt", []byte("hello"), extract.WithPDFReader(fake)); err == nil {
		t.Error("plain text with a PDF reader was not an error")
	}
	if calls != 0 {
		t.Errorf("reader called %d times for files that are not PDFs", calls)
	}

	// Without one, or with nil, the pure Go reader reads it.
	for name, opts := range map[string][]extract.Option{
		"no option": nil,
		"nil":       {extract.WithPDFReader(nil)},
		"nil last":  {extract.WithPDFReader(fake), extract.WithPDFReader(nil)},
	} {
		got, err := extract.Document(ctx, "a.pdf", pdf, opts...)
		if err != nil || got != "--- Page 1 ---\nHello World" {
			t.Errorf("%s: Document = %q, %v; want the pure Go reader's text", name, got, err)
		}
	}
}

func TestDetectChat(t *testing.T) {
	jsonl := `{"role":"user","content":"How should the prefill queue time out?"}
{"role":"assistant","content":"Per request, at 30 seconds."}
{"role":"user","content":"And alerting?"}
{"role":"assistant","content":"Alert on queue age above a minute."}`
	// A pasted chat as a PDF reader leaves it: a ligature, and a word broken
	// across a line end.
	pasted := "[2026-06-01 10:32] Kevin: the ﬁrst deploy failed\n" +
		"[2026-06-01 10:33] Mona: which imple-\nmentation?\n" +
		"[2026-06-01 10:34] Kevin: the canary\n" +
		"[2026-06-01 10:35] Mona: rolling back\n"
	unread := func(t *testing.T) func() (string, error) {
		return func() (string, error) {
			t.Helper()
			t.Error("the document's text was asked for")
			return "", nil
		}
	}

	t.Run("a text file is read from its content", func(t *testing.T) {
		path := "Downloads/chat_history_20250427164006.JSONL"
		log, ok := extract.DetectChat(path, extract.Classify(path, nil), []byte(jsonl), unread(t))
		if !ok || log.Format != chatlog.FormatRoleJSONL || len(log.Messages) != 4 {
			t.Fatalf("DetectChat = %+v, %v; want a role-jsonl log of 4 messages", log, ok)
		}
		var headings []string
		for _, s := range log.Sections() {
			headings = append(headings, s.Heading)
		}
		if strings.Join(headings, "|") != "2025-04-27" {
			t.Errorf("sections = %q, want the day the file name carries", headings)
		}
	})

	t.Run("a document is read once, from its normalized text", func(t *testing.T) {
		calls := 0
		read := func() (string, error) { calls++; return pasted, nil }
		log, ok := extract.DetectChat("notes/deploy.pdf", extract.FileClassDocument, nil, read)
		if !ok || log.Format != chatlog.FormatPasted || len(log.Messages) != 4 {
			t.Fatalf("DetectChat = %+v, %v; want a pasted log of 4 messages", log, ok)
		}
		if calls != 1 {
			t.Errorf("the document's text was asked for %d times, want 1", calls)
		}
		if got := log.Messages[0].Text; got != "the first deploy failed" {
			t.Errorf("first message = %q; the ligature was not undone", got)
		}
		if got := log.Messages[1].Text; got != "which implementation?" {
			t.Errorf("second message = %q; the broken word was not joined", got)
		}
	})

	t.Run("a document that cannot be read is not a chat", func(t *testing.T) {
		read := func() (string, error) { return pasted, errors.New("unreadable") }
		if log, ok := extract.DetectChat("notes/deploy.docx", extract.FileClassDocument, nil, read); ok || log != nil {
			t.Errorf("DetectChat = %+v, %v; want nil, false", log, ok)
		}
	})

	t.Run("formats no chat is looked for in are never read", func(t *testing.T) {
		for _, tc := range []struct {
			path  string
			class extract.FileClass
		}{
			{"slides/deck.pptx", extract.FileClassDocument},
			{"data/report.xlsx", extract.FileClassDocument},
			{"notes/plan.md", extract.FileClassText},
			{"guide.rst", extract.FileClassText},
			{"data/rows.csv", extract.FileClassText},
		} {
			if log, ok := extract.DetectChat(tc.path, tc.class, []byte(pasted), unread(t)); ok || log != nil {
				t.Errorf("DetectChat(%q) = %+v, %v; want nil, false", tc.path, log, ok)
			}
		}
	})

	t.Run("data is not a chat", func(t *testing.T) {
		path := "config/app.json"
		if log, ok := extract.DetectChat(path, extract.Classify(path, nil), []byte(`{"name":"app","ts":"x"}`), unread(t)); ok || log != nil {
			t.Errorf("DetectChat = %+v, %v; want nil, false", log, ok)
		}
		path = "notes/plan.txt"
		if log, ok := extract.DetectChat(path, extract.Classify(path, nil), []byte("Plan\n\nShip on Friday."), unread(t)); ok || log != nil {
			t.Errorf("DetectChat = %+v, %v; want nil, false", log, ok)
		}
	})
}
