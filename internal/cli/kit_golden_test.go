package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/fs"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"github.com/dgraph-io/badger/v4"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/imyousuf/CodeEagle/internal/embedding"
)

// TestKitGolden pins what the CLI does end to end over a small fixture: the
// graph a sync writes, the vector index it builds, and what rag, find, brief,
// show and status print from them. It reaches the code only through command
// constructors and reads the databases from disk, so it holds while the
// packages under them move; only the embedding registry's import may change.
//
// Regenerate the golden files after a deliberate change with:
//
//	UPDATE_GOLDEN=1 go test -race -count=1 -run KitGolden ./internal/cli/
func TestKitGolden(t *testing.T) {
	root := kitGoldenEnv(t)
	docsServer := kitGoldenDocsServer(t)

	// The federated project is synced first, so the main project's text rag
	// has another index to search beside its own.
	fedDir := kitGoldenProject(t, root, "kitfed", filepath.Join("testdata", "kit_golden", "federated"), docsServer.URL, nil)
	kitGoldenUseConfig(t, fedDir)
	fedOut, fedErr := runKitCmd(t, newSyncCmd())
	checkKitGolden(t, root, "sync_federated.txt", kitSyncLines(fedOut, fedErr))

	configDir := kitGoldenProject(t, root, "kitgolden", filepath.Join("testdata", "kit_golden", "fixture"), docsServer.URL, []string{fedDir})
	kitGoldenUseConfig(t, configDir)

	syncOut, syncErr := runKitCmd(t, newSyncCmd())
	checkKitGolden(t, root, "sync.txt", kitSyncLines(syncOut, syncErr))

	graphDump := dumpKitGraph(t, filepath.Join(configDir, "graph.db"))
	checkKitGolden(t, root, "graph.jsonl", graphDump)
	checkKitGolden(t, root, "vector.jsonl", dumpKitVectors(t, filepath.Join(configDir, "vec.db")))

	// A second sync over unchanged files changes nothing.
	again, againErr := runKitCmd(t, newSyncCmd())
	checkKitGolden(t, root, "sync_again.txt", kitSyncLines(again, againErr))
	if got := dumpKitGraph(t, filepath.Join(configDir, "graph.db")); got != graphDump {
		t.Errorf("a second sync over unchanged files changed the graph:\n%s", kitDiff(graphDump, got))
	}

	for i, query := range []string{"retry backoff", "evict cache entries", "key value store Get"} {
		out, _ := runKitCmd(t, newRagCmd(), "--json", query)
		checkKitGolden(t, root, fmt.Sprintf("rag_json_%d.json", i+1), roundKitScores(out))
	}
	ragEdges, _ := runKitCmd(t, newRagCmd(), "--json", "--edges", "--limit", "5", "store eviction")
	checkKitGolden(t, root, "rag_json_edges.json", roundKitScores(ragEdges))

	// Text mode is the only one that searches the federated index too.
	ragText, _ := runKitCmd(t, newRagCmd(), "retry backoff")
	if !strings.Contains(ragText, "also in kitfed-project") {
		t.Errorf("text rag did not search the federated index:\n%s", ragText)
	}
	checkKitGolden(t, root, "rag_text.txt", ragText)
	ragFiltered, _ := runKitCmd(t, newRagCmd(), "--no-federate", "--type", "Function,Method", "--edges", "store")
	checkKitGolden(t, root, "rag_text_filtered.txt", ragFiltered)

	find, _ := runKitCmd(t, newFindCmd(), "retry", "backoff")
	checkKitGolden(t, root, "find.txt", find)
	findCache, _ := runKitCmd(t, newFindCmd(), "cache", "eviction")
	checkKitGolden(t, root, "find_cache.txt", findCache)

	// No relation exists without a decision model, so a document is briefed
	// as its outline.
	docID := kitFirstField(t, find, "docs/design.md")
	adrID := kitFirstField(t, findCache, "docs/adr-001-cache.md")
	brief, _ := runKitCmd(t, newBriefCmd(), docID)
	checkKitGolden(t, root, "brief_id.txt", brief)
	briefMulti, _ := runKitCmd(t, newBriefCmd(), docID+","+adrID)
	checkKitGolden(t, root, "brief_multi.txt", briefMulti)
	briefPath, _ := runKitCmd(t, newBriefCmd(), "kitgolden/docs/adr-001-cache.md")
	checkKitGolden(t, root, "brief_path.txt", briefPath)
	briefWords, _ := runKitCmd(t, newBriefCmd(), "retry", "design")
	checkKitGolden(t, root, "brief_words.txt", briefWords)

	sectionID := kitFirstField(t, brief, "Backoff")
	showSection, _ := runKitCmd(t, newShowCmd(), sectionID)
	checkKitGolden(t, root, "show_section.txt", showSection)
	showDoc, _ := runKitCmd(t, newShowCmd(), adrID)
	checkKitGolden(t, root, "show_doc.txt", showDoc)
	showNode, _ := runKitCmd(t, newShowCmd(), kitRagID(t, ragEdges, "Store"))
	checkKitGolden(t, root, "show_node.txt", showNode)

	status, _ := runKitCmd(t, newStatusCmd())
	checkKitGolden(t, root, "status.txt", kitStatusLines(status))

	// The docs provider is only ever probed, and the 404 leaves sync without
	// one, so nothing is sent to a model.
	probes := docsServer.paths()
	for _, p := range probes {
		if p != "/api/tags" {
			t.Errorf("docs server was asked for %s; want only the provider probe", p)
		}
	}
	if len(probes) == 0 {
		t.Error("the docs provider was never probed")
	}
}

// kitGoldenEnv points HOME at a temporary directory and clears every variable
// that could pick a provider, a credential or a repository, so nothing on the
// machine running the test reaches it. It returns the root all of the test's
// files live under.
func kitGoldenEnv(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cleared := []string{"CODEEAGLE_", "GOOGLE_", "CLOUDSDK_", "GIT_", "OLLAMA_", "ANTHROPIC_", "BASETEN_", "JEV_", "TYPESAFE_", "OPENAI_"}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		for _, prefix := range cleared {
			if strings.HasPrefix(k, prefix) {
				t.Setenv(k, "")
				os.Unsetenv(k)
				break
			}
		}
	}
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	// A temporary directory inside a checkout would otherwise be indexed
	// as that checkout's branch.
	t.Setenv("GIT_CEILING_DIRECTORIES", root)

	embedding.RegisterProvider(kitGoldenProvider, func(cfg embedding.Config) (embedding.Provider, error) {
		model := cfg.Model
		if model == "" {
			model = "bow-64"
		}
		return bagOfWordsEmbedder{model: model}, nil
	})
	return root
}

// kitDocsServer stands in for a docs provider that is not there: it answers
// every request with 404 and remembers what was asked.
type kitDocsServer struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
}

func (s *kitDocsServer) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

func kitGoldenDocsServer(t *testing.T) *kitDocsServer {
	t.Helper()
	s := &kitDocsServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.seen = append(s.seen, r.URL.Path)
		s.mu.Unlock()
		http.NotFound(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

// kitGoldenTime is every fixture file's modification time, from which each
// node's UpdatedAt and, in UTC, the date nodes are made.
var kitGoldenTime = time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC)

// kitGoldenProject copies a fixture to root/name, writes a configuration for
// it under root/name-project/.CodeEagle, and returns that directory. The
// repository is not a git checkout, so it is indexed on branch "default"
// under paths prefixed with its name.
func kitGoldenProject(t *testing.T, root, name, fixture, docsURL string, federate []string) string {
	t.Helper()
	repo := filepath.Join(root, name)
	copyKitFixture(t, fixture, repo)

	configDir := filepath.Join(root, name+"-project", ".CodeEagle")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "project:\n  name: %s\n", name)
	fmt.Fprintf(&b, "repositories:\n  - path: %s\n    type: single\n", repo)
	if len(federate) > 0 {
		b.WriteString("federate:\n")
		for _, dir := range federate {
			fmt.Fprintf(&b, "  - %s\n", dir)
		}
	}
	b.WriteString("agents:\n  auto_summarize: false\n  auto_link: false\n")
	fmt.Fprintf(&b, "  embedding_provider: %s\n  embedding_model: bow-64\n", kitGoldenProvider)
	fmt.Fprintf(&b, "docs:\n  base_url: %s\n", docsURL)
	// Fixed, or the queue sizes itself by the machine's processors and
	// says so in the sync's output.
	b.WriteString("queue:\n  max_workers: 2\n")
	b.WriteString("transcripts:\n  enabled: false\n")
	b.WriteString("relations:\n  enabled: false\n")
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return configDir
}

// copyKitFixture copies a fixture tree and stamps every file and directory
// with kitGoldenTime, which is what a node's UpdatedAt and the date nodes
// are made from.
func copyKitFixture(t *testing.T, from, to string) {
	t.Helper()
	var copied []string
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		copied = append(copied, dst)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy fixture %s: %v", from, err)
	}
	for _, path := range copied {
		if err := os.Chtimes(path, kitGoldenTime, kitGoldenTime); err != nil {
			t.Fatal(err)
		}
	}
}

// kitGoldenUseConfig makes the commands read the configuration in configDir,
// as --config would.
func kitGoldenUseConfig(t *testing.T, configDir string) {
	t.Helper()
	viper.Set("config_file", filepath.Join(configDir, "config.yaml"))
	t.Cleanup(func() { viper.Set("config_file", "") })
}

// runKitCmd executes a freshly constructed command and returns what it
// printed to stdout and stderr.
func runKitCmd(t *testing.T, cmd *cobra.Command, args ...string) (stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("%s %s: %v\nstderr:\n%s", cmd.Name(), strings.Join(args, " "), err, errOut.String())
	}
	return out.String(), errOut.String()
}

// kitSyncLines joins what a sync printed to stdout and stderr, dropping the
// queue's progress lines, which are paced by a clock.
func kitSyncLines(stdout, stderr string) string {
	var keep []string
	for _, line := range strings.Split(strings.TrimRight(stdout, "\n"), "\n") {
		if strings.HasPrefix(line, "[queue] Progress:") {
			continue
		}
		keep = append(keep, line)
	}
	return strings.Join(keep, "\n") + "\n--- stderr ---\n" + stderr
}

// kitStatusLines drops the lines of status that report the index file's
// size, which varies with the vector graph's random levels, and when it was
// written.
func kitStatusLines(out string) string {
	var keep []string
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Index file:") || strings.HasPrefix(trimmed, "Last updated:") {
			continue
		}
		keep = append(keep, line)
	}
	return strings.Join(keep, "\n")
}

// kitScore matches a score in rag --json.
var kitScore = regexp.MustCompile(`"score": (-?[0-9.eE+-]+)`)

// roundKitScores rounds the scores rag --json prints to six places, so that
// the golden files pin the ranking and not the last bits of a float sum.
func roundKitScores(out string) string {
	return kitScore.ReplaceAllStringFunc(out, func(m string) string {
		f, err := strconv.ParseFloat(kitScore.FindStringSubmatch(m)[1], 64)
		if err != nil {
			return m
		}
		return fmt.Sprintf(`"score": %.6f`, f)
	})
}

// kitFirstField returns the first field of the first line of out that
// contains substr: the ID find and brief start their lines with.
func kitFirstField(t *testing.T, out, substr string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, substr) {
			if fields := strings.Fields(line); len(fields) > 0 {
				return strings.Trim(fields[0], "[]()")
			}
		}
	}
	t.Fatalf("no line containing %q in:\n%s", substr, out)
	return ""
}

// kitRagID returns the ID rag --json gave the node named name.
func kitRagID(t *testing.T, out, name string) string {
	t.Helper()
	var results []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("parse rag --json: %v\n%s", err, out)
	}
	for _, r := range results {
		if r.Name == name {
			return r.ID
		}
	}
	t.Fatalf("rag --json has no node named %q:\n%s", name, out)
	return ""
}

// dumpKitGraph reads every node and edge of every scope in the graph
// database, straight from its keys, one JSON object per line in key order.
// UpdatedAt and the read-time graph_source property are left out.
func dumpKitGraph(t *testing.T, dbPath string) string {
	t.Helper()
	type record struct {
		scope, kind, id string
		value           map[string]any
	}
	var records []record
	counts := map[string][2]int{}
	readKitBadger(t, dbPath, func(key string, val []byte) {
		var kind, rest string
		switch {
		case strings.HasPrefix(key, "n:"):
			kind, rest = "node", key[len("n:"):]
		case strings.HasPrefix(key, "e:"):
			kind, rest = "edge", key[len("e:"):]
		default:
			return
		}
		scope, id, ok := strings.Cut(rest, ":")
		if !ok {
			t.Fatalf("graph key %q has no scope", key)
		}
		var v map[string]any
		if err := json.Unmarshal(val, &v); err != nil {
			t.Fatalf("graph key %q: %v", key, err)
		}
		delete(v, "updated_at")
		if props, ok := v["properties"].(map[string]any); ok {
			delete(props, "graph_source")
		}
		records = append(records, record{scope: scope, kind: kind, id: id, value: v})
		c := counts[scope]
		if kind == "node" {
			c[0]++
		} else {
			c[1]++
		}
		counts[scope] = c
	})
	sort.Slice(records, func(i, j int) bool {
		a, b := records[i], records[j]
		if a.scope != b.scope {
			return a.scope < b.scope
		}
		if a.kind != b.kind {
			return a.kind > b.kind // nodes before edges
		}
		return a.id < b.id
	})
	scopes := make([]string, 0, len(counts))
	for s := range counts {
		scopes = append(scopes, s)
	}
	sort.Strings(scopes)
	var b strings.Builder
	for _, s := range scopes {
		fmt.Fprintf(&b, "# scope %s: %d nodes, %d edges\n", s, counts[s][0], counts[s][1])
	}
	for _, r := range records {
		line, err := json.Marshal(map[string]any{"scope": r.scope, r.kind: r.value})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// dumpKitVectors reads the vector index's metadata, chunks and text hashes
// from its database, leaving out the metadata's timestamps.
func dumpKitVectors(t *testing.T, dbPath string) string {
	t.Helper()
	var b strings.Builder
	readKitBadger(t, dbPath, func(key string, val []byte) {
		var v any = string(val)
		if strings.HasPrefix(key, "meta:") || strings.HasPrefix(key, "chunk:") {
			var m map[string]any
			if err := json.Unmarshal(val, &m); err != nil {
				t.Fatalf("vector key %q: %v", key, err)
			}
			delete(m, "created_at")
			delete(m, "updated_at")
			v = m
		}
		line, err := json.Marshal(map[string]any{"key": key, "value": v})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	})
	return b.String()
}

// readKitBadger calls fn for every key in a database, in key order.
func readKitBadger(t *testing.T, dbPath string, fn func(key string, val []byte)) {
	t.Helper()
	db, err := badger.Open(badger.DefaultOptions(dbPath).WithReadOnly(true).WithLogger(nil))
	if err != nil {
		t.Fatalf("open %s: %v", dbPath, err)
	}
	defer db.Close()
	err = db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			val, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
			fn(string(item.KeyCopy(nil)), val)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read %s: %v", dbPath, err)
	}
}

// checkKitGolden compares got, with the test's temporary root replaced by
// $ROOT, against testdata/kit_golden/golden/name, or rewrites the file when
// UPDATE_GOLDEN is set.
func checkKitGolden(t *testing.T, root, name, got string) {
	t.Helper()
	got = strings.ReplaceAll(got, root, "$ROOT")
	if resolved, err := filepath.EvalSymlinks(root); err == nil && resolved != root {
		got = strings.ReplaceAll(got, resolved, "$ROOT")
	}
	path := filepath.Join("testdata", "kit_golden", "golden", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden file: %v (regenerate with UPDATE_GOLDEN=1)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from its golden file (regenerate with UPDATE_GOLDEN=1 if the change is intended):\n%s",
			name, kitDiff(string(want), got))
	}
}

// kitDiff shows where two texts first differ, with a little context.
func kitDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	i := 0
	for i < len(w) && i < len(g) && w[i] == g[i] {
		i++
	}
	var b strings.Builder
	fmt.Fprintf(&b, "first difference at line %d (want %d lines, got %d)\n", i+1, len(w), len(g))
	for j := max(0, i-2); j < min(len(w), i+3); j++ {
		fmt.Fprintf(&b, "want %4d: %s\n", j+1, w[j])
	}
	for j := max(0, i-2); j < min(len(g), i+3); j++ {
		fmt.Fprintf(&b, "got  %4d: %s\n", j+1, g[j])
	}
	return b.String()
}

// kitGoldenProvider names the embedding provider the test registers.
const kitGoldenProvider = "kit-golden-bow"

// bagOfWordsEmbedder embeds text as normalised counts of its words hashed
// into a few dimensions: texts sharing words land near each other, with no
// network and the same vectors on every run.
type bagOfWordsEmbedder struct{ model string }

const bagOfWordsDims = 64

func (bagOfWordsEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = bagOfWords(text)
	}
	return out, nil
}

func (bagOfWordsEmbedder) EmbedQuery(_ context.Context, text string) ([]float32, error) {
	return bagOfWords(text), nil
}

func (bagOfWordsEmbedder) Dimensions() int     { return bagOfWordsDims }
func (bagOfWordsEmbedder) Name() string        { return kitGoldenProvider }
func (e bagOfWordsEmbedder) ModelName() string { return e.model }

func bagOfWords(text string) []float32 {
	v := make([]float32, bagOfWordsDims)
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, w := range words {
		h := fnv.New32a()
		h.Write([]byte(w))
		v[h.Sum32()%bagOfWordsDims]++
	}
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	if n == 0 {
		// No words still has to be a unit vector, or its cosine is NaN.
		v[0] = 1
		return v
	}
	norm := float32(math.Sqrt(n))
	for i := range v {
		v[i] /= norm
	}
	return v
}
