package vectorstore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/vectorstore"
)

// The on-disk format of an index, as written today:
//
//	vec.db (Badger)
//	  chunk:<nodeID>:<n>  json.Marshal(ChunkEntry), HTML-escaped as encoding/json does
//	  text:<nodeID>       FNV-64a of the node's embedded text, lowercase hex, unpadded, not JSON
//	  meta:<branch>       json.Marshal(VectorIndexMeta) (the key has no "vec:" prefix)
//	vec.idx               coder/hnsw v0.6.1 Export, keys "<nodeID>:<n>", cosine distance
//
// An index written by one release is opened by the next, so any change to
// these is a migration, not a refactor.

// vecDBGolden holds every key and value of the golden vec.db, one per line as
// "<key>\t<value>", with the meta timestamps masked. Lines starting with "#"
// are comments. Regenerate it, after a deliberate format change only, with:
//
//	UPDATE_GOLDEN=1 go test -race -run TestVecDBFormatIsPinned ./internal/vectorstore/
const vecDBGolden = "testdata/vecdb_format.golden"

// goldenNodes is the fixed corpus the golden index is built from.
func goldenNodes() []*graph.Node {
	var big strings.Builder
	for i := 0; big.Len() < 15000; i++ {
		fmt.Fprintf(&big, "Section %02d. The retention job keeps %d days of history and compacts the rest; it runs at %02d:00 UTC.\n\n",
			i, 30+i, i%24)
	}
	return []*graph.Node{
		// Long enough for eleven or more chunks, so "big:10" sorts before "big:2".
		{ID: "big", Type: graph.NodeDocument, Name: "retention.md", FilePath: "docs/retention.md", DocComment: big.String()},
		// Characters encoding/json escapes, and characters it does not.
		{
			ID: "esc", Type: graph.NodeFunction, Name: "Render", Package: "tmpl", FilePath: "tmpl/render.go",
			QualifiedName: "tmpl.Render[T]",
			Properties:    map[string]string{graph.PropArchRole: "renderer & <helper>"},
			DocComment:    "Render escapes <b>bold</b> & \"quotes\" — café, 日本語, ✓.",
			Signature:     "func Render[T any](w io.Writer, v T) error",
		},
		// A topic embeds from its label alone.
		{ID: "topic", Type: graph.NodeTopic, Name: "retry policy", Properties: map[string]string{graph.PropAliases: "backoff,jitter"}},
		// Embeddable type, nothing to embed: counted in node_count, never stored.
		{ID: "empty", Type: graph.NodeFunction, Name: "Empty"},
		// Not an embeddable type: neither counted nor stored.
		{ID: "file", Type: graph.NodeFile, Name: "main.go", DocComment: "a file with a comment"},
	}
}

var metaTimes = regexp.MustCompile(`"(created_at|updated_at)":"[^"]*"`)

// buildGolden indexes the golden corpus with a rebuild and a save, the way a
// first sync does, and closes the store.
func buildGolden(t *testing.T) (storePaths, *memGraph, *fakeEmbedder) {
	t.Helper()
	p := newPaths(t)
	g := newMemGraph(goldenNodes()...)
	emb := newFakeEmbedder()
	vs := openStore(t, g, emb, p)
	if err := vs.Rebuild(context.Background()); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	mustSaveClose(t, vs)
	return p, g, emb
}

// TestVecDBFormatIsPinned fixes every key and value an index writes to its
// database.
func TestVecDBFormatIsPinned(t *testing.T) {
	p, g, _ := buildGolden(t)
	dump := rawDump(t, p.db)

	var got bytes.Buffer
	for _, kv := range dump {
		v := kv.value
		if strings.HasPrefix(kv.key, "meta:") {
			v = metaTimes.ReplaceAllString(v, `"$1":"<masked>"`)
		}
		fmt.Fprintf(&got, "%s\t%s\n", kv.key, v)
	}
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		header := "# vec.db written by Rebuild then Save over goldenNodes(); see format_golden_test.go.\n" +
			"# Regenerate: UPDATE_GOLDEN=1 go test -race -run TestVecDBFormatIsPinned ./internal/vectorstore/\n"
		if err := os.MkdirAll(filepath.Dir(vecDBGolden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(vecDBGolden, []byte(header+got.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", vecDBGolden)
	}
	raw, err := os.ReadFile(vecDBGolden)
	if err != nil {
		t.Fatalf("read golden (set UPDATE_GOLDEN=1 to create it): %v", err)
	}
	var want strings.Builder
	for _, line := range strings.SplitAfter(string(raw), "\n") {
		if !strings.HasPrefix(line, "#") {
			want.WriteString(line)
		}
	}
	if got.String() != want.String() {
		gotLines, wantLines := strings.Split(got.String(), "\n"), strings.Split(want.String(), "\n")
		for i := range max(len(gotLines), len(wantLines)) {
			var gl, wl string
			if i < len(gotLines) {
				gl = gotLines[i]
			}
			if i < len(wantLines) {
				wl = wantLines[i]
			}
			if gl != wl {
				t.Fatalf("vec.db differs from %s at entry %d:\n got  %q\n want %q", vecDBGolden, i+1, gl, wl)
			}
		}
	}

	// What the golden file holds, said as rules, so a failure says which one
	// broke.
	var keys []string
	for _, kv := range dump {
		keys = append(keys, kv.key)
	}
	bigChunks := 0
	for _, k := range keys {
		if strings.HasPrefix(k, "chunk:big:") {
			bigChunks++
		}
	}
	if bigChunks < 11 {
		t.Fatalf("big document has %d chunks, want 11 or more", bigChunks)
	}
	if i, j := slices.Index(keys, "chunk:big:10"), slices.Index(keys, "chunk:big:2"); i < 0 || j < 0 || i > j {
		t.Errorf("chunk:big:10 at %d, chunk:big:2 at %d; want byte order, 10 before 2", i, j)
	}
	for _, k := range keys {
		switch {
		case strings.HasPrefix(k, "chunk:"), strings.HasPrefix(k, "text:"), k == "meta:"+testBranch:
		default:
			t.Errorf("unexpected key %q", k)
		}
		if strings.HasPrefix(k, "chunk:empty:") || strings.HasPrefix(k, "chunk:file:") || k == "text:empty" || k == "text:file" {
			t.Errorf("key %q stored for a node with nothing to embed", k)
		}
	}

	for _, kv := range dump {
		switch {
		case strings.HasPrefix(kv.key, "chunk:"):
			rest := strings.TrimPrefix(kv.key, "chunk:")
			cut := strings.LastIndex(rest, ":")
			id := rest[:cut]
			n, err := strconv.Atoi(rest[cut+1:])
			if err != nil {
				t.Fatalf("chunk key %q has no index", kv.key)
			}
			var entry vectorstore.ChunkEntry
			if err := json.Unmarshal([]byte(kv.value), &entry); err != nil {
				t.Fatalf("chunk %q is not JSON: %v", kv.key, err)
			}
			if entry.NodeID != id || entry.ChunkIndex != n {
				t.Errorf("chunk %q holds node %q index %d", kv.key, entry.NodeID, entry.ChunkIndex)
			}
			canonical, _ := json.Marshal(vectorstore.ChunkEntry{NodeID: id, ChunkIndex: n, ChunkText: entry.ChunkText})
			if kv.value != string(canonical) {
				t.Errorf("chunk %q = %s, want encoding/json's own encoding %s", kv.key, kv.value, canonical)
			}
		case strings.HasPrefix(kv.key, "text:"):
			node, err := g.GetNode(context.Background(), strings.TrimPrefix(kv.key, "text:"))
			if err != nil {
				t.Fatalf("text hash for unknown node %q", kv.key)
			}
			if want := fnvHex(vectorstore.EmbeddableText(node)); kv.value != want {
				t.Errorf("%s = %q, want the unpadded hex FNV-64a %q", kv.key, kv.value, want)
			}
		}
	}

	byKey := rawMap(t, p.db)
	esc := byKey["chunk:esc:0"]
	for _, frag := range []string{`\u003cb\u003ebold\u003c/b\u003e`, `\u0026`, `\"quotes\"`, "café, 日本語, ✓", `\n`} {
		if !strings.Contains(esc, frag) {
			t.Errorf("chunk:esc:0 = %s, want it to contain %s", esc, frag)
		}
	}
	escNode, _ := g.GetNode(context.Background(), "esc")
	var escEntry vectorstore.ChunkEntry
	_ = json.Unmarshal([]byte(esc), &escEntry)
	if escEntry.ChunkText != vectorstore.EmbeddableText(escNode) {
		t.Errorf("a one-chunk node stores %q, want its whole embeddable text", escEntry.ChunkText)
	}

	var meta map[string]any
	if err := json.Unmarshal([]byte(byKey["meta:"+testBranch]), &meta); err != nil {
		t.Fatalf("meta is not JSON: %v", err)
	}
	wantMeta := map[string]any{
		"provider": "fake", "model": "fake-embed-v1", "dimensions": 8.0, "chunk_size": 1500.0, "overlap": 200.0,
		// Four nodes of embeddable types, including the one with nothing to
		// embed. Version 2: the rebuild marker creates the record at version
		// 1, and the rebuild then counts itself.
		"node_count": 4.0, "version": 2.0, "text_version": float64(vectorstore.EmbeddableTextVersion),
	}
	for k, want := range wantMeta {
		if meta[k] != want {
			t.Errorf("meta %s = %v, want %v", k, meta[k], want)
		}
	}
	if _, ok := meta["rebuilding"]; ok {
		t.Error("a finished rebuild left the rebuilding marker in meta")
	}
	for _, k := range []string{"created_at", "updated_at"} {
		s, _ := meta[k].(string)
		if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
			t.Errorf("meta %s = %q, want an RFC 3339 time", k, s)
		}
	}
	if len(meta) != len(wantMeta)+2 {
		t.Errorf("meta has fields %v, want only %d", meta, len(wantMeta)+2)
	}
}

// TestVecIdxFormatIsPinned reads the index file as coder/hnsw v0.6.1 lays it
// out and checks the parts that do not depend on the clock-seeded level
// assignment: the parameters, and the base layer's keys and vectors.
func TestVecIdxFormatIsPinned(t *testing.T) {
	p, _, emb := buildGolden(t)
	h, layers := readHNSW(t, p.idx)
	want := hnswHeader{version: 1, m: 16, ml: 0.25, efSearch: 40, distance: "cosine"}
	if h != want {
		t.Errorf("vec.idx header = %+v, want %+v", h, want)
	}
	if len(layers) == 0 {
		t.Fatal("vec.idx has no layers")
	}

	chunks := make(map[string]string)
	for k, v := range rawMap(t, p.db) {
		if key, ok := strings.CutPrefix(k, "chunk:"); ok {
			var entry vectorstore.ChunkEntry
			if err := json.Unmarshal([]byte(v), &entry); err != nil {
				t.Fatal(err)
			}
			chunks[key] = entry.ChunkText
		}
	}
	if len(layers[0]) != len(chunks) {
		t.Errorf("base layer holds %d nodes, vec.db %d chunks", len(layers[0]), len(chunks))
	}
	for key, text := range chunks {
		n, ok := layers[0][key]
		if !ok {
			t.Errorf("base layer lacks %s", key)
			continue
		}
		if !slices.Equal(n.vec, emb.vec(text)) {
			t.Errorf("vector of %s = %v, want the embedding of its chunk text", key, n.vec)
		}
	}
	assertCleanLayers(t, layers)
}

// TestGoldenIndexReopens loads the golden index in a new store and checks
// that every chunk is searchable under the key it was stored with.
func TestGoldenIndexReopens(t *testing.T) {
	p, g, emb := buildGolden(t)
	chunks := make(map[string]string)
	for k, v := range rawMap(t, p.db) {
		if key, ok := strings.CutPrefix(k, "chunk:"); ok {
			var entry vectorstore.ChunkEntry
			_ = json.Unmarshal([]byte(v), &entry)
			chunks[key] = entry.ChunkText
		}
	}

	vs := reopen(t, g, emb, p)
	ctx := context.Background()
	if vs.Len() != len(chunks) {
		t.Errorf("Len = %d, want %d chunks", vs.Len(), len(chunks))
	}
	if vs.NeedsReindex() {
		t.Errorf("NeedsReindex after reopening with the same embedder: %s", vs.ReindexReason())
	}
	if m := vs.Meta(); m == nil || m.Provider != "fake" || m.Model != "fake-embed-v1" || m.Dimensions != 8 ||
		m.NodeCount != 4 || m.Version != 2 || m.TextVersion != vectorstore.EmbeddableTextVersion || m.Rebuilding {
		t.Errorf("Meta after reopening = %+v", m)
	}

	for _, id := range []string{"big", "esc", "topic"} {
		got, ok := vs.Vector(id)
		if !ok {
			t.Errorf("Vector(%s) missing after reopening", id)
			continue
		}
		if want := emb.vec(chunks[id+":0"]); !slices.Equal(got, want) {
			t.Errorf("Vector(%s) = %v, want the embedding of its first chunk", id, got)
		}
	}
	for _, id := range []string{"empty", "file", "nobody"} {
		if _, ok := vs.Vector(id); ok {
			t.Errorf("Vector(%s) present for a node never embedded", id)
		}
	}

	// Every chunk is found first by its own text, with its index and text.
	for key, text := range chunks {
		results, err := vs.Search(ctx, text, 3)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(results) == 0 {
			t.Fatalf("Search for chunk %s found nothing", key)
		}
		top := results[0]
		if gotKey := fmt.Sprintf("%s:%d", top.Node.ID, top.ChunkIndex); gotKey != key {
			t.Errorf("Search for chunk %s ranked %s first", key, gotKey)
		}
		if top.ChunkText != text {
			t.Errorf("Search for chunk %s returned chunk text %q", key, top.ChunkText)
		}
		if top.Score < 0.9999 {
			t.Errorf("Search for chunk %s scored %v, want 1", key, top.Score)
		}
	}
}
