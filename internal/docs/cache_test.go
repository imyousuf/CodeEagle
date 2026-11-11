package docs_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/docs"
)

func openCache(t *testing.T, dir string) *docs.Cache {
	t.Helper()
	c, err := docs.OpenCache(dir)
	if err != nil {
		t.Fatalf("OpenCache: %v", err)
	}
	return c
}

// check calls Check and fails the test on an error, which Check never returns
// today: every miss is (nil, nil).
func check(t *testing.T, c *docs.Cache, path, hash string) *docs.ExtractionResult {
	t.Helper()
	got, err := c.Check(path, hash)
	if err != nil {
		t.Fatalf("Check(%q, %q): %v", path, hash, err)
	}
	return got
}

func listSkipped(t *testing.T, c *docs.Cache) []string {
	t.Helper()
	got, err := c.ListSkipped()
	if err != nil {
		t.Fatalf("ListSkipped: %v", err)
	}
	slices.Sort(got)
	return got
}

// TestCacheRoundTrip pins the cache's model: a path records the hash it was
// last seen with, results and skip markers are keyed by hash and so shared by
// every path with that content, and a hit needs both to line up.
func TestCacheRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs.db")
	c := openCache(t, dir)

	if got := check(t, c, "a.md", "h1"); got != nil {
		t.Errorf("empty cache Check = %+v, want nil", got)
	}
	if got := listSkipped(t, c); got != nil {
		t.Errorf("empty cache ListSkipped = %q, want nil", got)
	}

	r1 := &docs.ExtractionResult{Summary: "one", Topics: []string{"t1"}, Entities: []string{"E1"}}
	if err := c.Store("a.md", "h1", r1); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if got := check(t, c, "a.md", "h1"); !reflect.DeepEqual(got, r1) {
		t.Errorf("Check(a.md, h1) = %+v, want %+v", got, r1)
	}
	if got := check(t, c, "a.md", "h2"); got != nil {
		t.Errorf("Check with a changed hash = %+v, want nil", got)
	}
	// The result for h1 exists, but b.md has never been recorded.
	if got := check(t, c, "b.md", "h1"); got != nil {
		t.Errorf("Check for an unrecorded path = %+v, want nil", got)
	}

	// Results are keyed by hash: storing the same content under another path
	// replaces the result every path with that hash sees.
	r2 := &docs.ExtractionResult{Summary: "two", Topics: []string{"t2"}}
	if err := c.Store("b.md", "h1", r2); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if got := check(t, c, "a.md", "h1"); !reflect.DeepEqual(got, r2) {
		t.Errorf("Check(a.md, h1) after storing b.md = %+v, want %+v", got, r2)
	}

	// A skip records the path's hash and marks the hash, with no result.
	if err := c.MarkSkipped("c.md", "h3"); err != nil {
		t.Fatalf("MarkSkipped: %v", err)
	}
	if !c.IsSkipped("h3") {
		t.Error("IsSkipped(h3) = false after MarkSkipped")
	}
	if c.IsSkipped("h1") {
		t.Error("IsSkipped(h1) = true for a stored result")
	}
	if got := check(t, c, "c.md", "h3"); got != nil {
		t.Errorf("Check on a skipped hash = %+v, want nil", got)
	}
	// Every path whose current hash is skipped is listed.
	if err := c.MarkSkipped("d.md", "h3"); err != nil {
		t.Fatalf("MarkSkipped: %v", err)
	}
	if got, want := listSkipped(t, c), []string{"c.md", "d.md"}; !slices.Equal(got, want) {
		t.Errorf("ListSkipped = %q, want %q", got, want)
	}

	// A path that moved to new content leaves the list; the hash it left
	// stays marked.
	if err := c.Store("d.md", "h4", r1); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if got, want := listSkipped(t, c), []string{"c.md"}; !slices.Equal(got, want) {
		t.Errorf("ListSkipped after d.md changed = %q, want %q", got, want)
	}
	if !c.IsSkipped("h3") {
		t.Error("IsSkipped(h3) = false; moving one path off it should not clear the hash")
	}

	// Storing a result for a skipped hash clears the skip.
	if err := c.Store("c.md", "h3", r1); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if c.IsSkipped("h3") {
		t.Error("IsSkipped(h3) = true after a result was stored for it")
	}
	if got := listSkipped(t, c); got != nil {
		t.Errorf("ListSkipped after the skip was cleared = %q, want nil", got)
	}

	// Everything survives a close and reopen.
	if err := c.MarkSkipped("e.md", "h5"); err != nil {
		t.Fatalf("MarkSkipped: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	c = openCache(t, dir)
	defer c.Close()
	if got := check(t, c, "a.md", "h1"); !reflect.DeepEqual(got, r2) {
		t.Errorf("after reopen Check(a.md, h1) = %+v, want %+v", got, r2)
	}
	if got := check(t, c, "c.md", "h3"); !reflect.DeepEqual(got, r1) {
		t.Errorf("after reopen Check(c.md, h3) = %+v, want %+v", got, r1)
	}
	if !c.IsSkipped("h5") {
		t.Error("after reopen IsSkipped(h5) = false")
	}
	if got, want := listSkipped(t, c), []string{"e.md"}; !slices.Equal(got, want) {
		t.Errorf("after reopen ListSkipped = %q, want %q", got, want)
	}
}

// TestCacheStoreEmptyResult pins that an empty result is a hit, not a miss:
// what is cached is whatever was stored.
func TestCacheStoreEmptyResult(t *testing.T) {
	c := openCache(t, t.TempDir())
	defer c.Close()
	if err := c.Store("a.md", "h", &docs.ExtractionResult{}); err != nil {
		t.Fatalf("Store: %v", err)
	}
	got := check(t, c, "a.md", "h")
	if got == nil || !reflect.DeepEqual(*got, docs.ExtractionResult{}) {
		t.Errorf("Check = %+v, want an empty result", got)
	}
}

// TestCacheMigratePaths pins re-keying: only paths the remap claims move,
// results and skip markers follow because they are keyed by hash, and the
// count is the number moved.
func TestCacheMigratePaths(t *testing.T) {
	c := openCache(t, t.TempDir())
	defer c.Close()

	r := &docs.ExtractionResult{Summary: "kept"}
	for _, p := range []string{"/old/a.md", "/old/b.md", "/keep/c.md"} {
		if err := c.Store(p, "hash-"+filepath.Base(p), r); err != nil {
			t.Fatalf("Store: %v", err)
		}
	}
	if err := c.MarkSkipped("/old/s.pdf", "hash-s"); err != nil {
		t.Fatalf("MarkSkipped: %v", err)
	}

	var seen []string
	n, err := c.MigratePaths(func(old string) (string, bool) {
		seen = append(seen, old)
		if rest, ok := strings.CutPrefix(old, "/old/"); ok {
			return "repo/" + rest, true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("MigratePaths: %v", err)
	}
	if n != 3 {
		t.Errorf("migrated %d, want 3", n)
	}
	slices.Sort(seen)
	if want := []string{"/keep/c.md", "/old/a.md", "/old/b.md", "/old/s.pdf"}; !slices.Equal(seen, want) {
		t.Errorf("remap saw %q, want every recorded path %q", seen, want)
	}

	if got := check(t, c, "/old/a.md", "hash-a.md"); got != nil {
		t.Errorf("old path still hits: %+v", got)
	}
	if got := check(t, c, "repo/a.md", "hash-a.md"); !reflect.DeepEqual(got, r) {
		t.Errorf("Check(repo/a.md) = %+v, want %+v", got, r)
	}
	if got := check(t, c, "/keep/c.md", "hash-c.md"); !reflect.DeepEqual(got, r) {
		t.Errorf("unclaimed path Check = %+v, want %+v", got, r)
	}
	if got, want := listSkipped(t, c), []string{"repo/s.pdf"}; !slices.Equal(got, want) {
		t.Errorf("ListSkipped = %q, want %q", got, want)
	}

	// Nothing claimed, nothing moved.
	n, err = c.MigratePaths(func(string) (string, bool) { return "x", false })
	if err != nil || n != 0 {
		t.Errorf("MigratePaths with no claims = (%d, %v), want (0, nil)", n, err)
	}
}

// TestCacheMigratePathsEdgeCases pins two remaps the doc comment does not
// anticipate. Both are today's behaviour, not a statement that they are right.
func TestCacheMigratePathsEdgeCases(t *testing.T) {
	r := &docs.ExtractionResult{Summary: "kept"}

	t.Run("empty new path is migrated", func(t *testing.T) {
		// The comment says only a non-empty new path is re-keyed, but only
		// the bool is consulted, so the entry moves to the empty path.
		c := openCache(t, t.TempDir())
		defer c.Close()
		if err := c.Store("a.md", "h", r); err != nil {
			t.Fatalf("Store: %v", err)
		}
		n, err := c.MigratePaths(func(string) (string, bool) { return "", true })
		if err != nil || n != 1 {
			t.Fatalf("MigratePaths = (%d, %v), want (1, nil)", n, err)
		}
		if got := check(t, c, "a.md", "h"); got != nil {
			t.Errorf("old path still hits: %+v", got)
		}
		if got := check(t, c, "", "h"); !reflect.DeepEqual(got, r) {
			t.Errorf("Check(\"\", h) = %+v, want %+v", got, r)
		}
	})

	t.Run("identity remap drops the path", func(t *testing.T) {
		// Setting the new key and deleting the old one in one transaction,
		// when they are the same key, leaves it deleted.
		c := openCache(t, t.TempDir())
		defer c.Close()
		if err := c.Store("a.md", "h", r); err != nil {
			t.Fatalf("Store: %v", err)
		}
		n, err := c.MigratePaths(func(old string) (string, bool) { return old, true })
		if err != nil || n != 1 {
			t.Fatalf("MigratePaths = (%d, %v), want (1, nil)", n, err)
		}
		if got := check(t, c, "a.md", "h"); got != nil {
			t.Errorf("Check(a.md, h) = %+v, want nil: the path record is gone", got)
		}
	})
}

// TestOpenCacheError pins that a path that cannot hold a database is an
// error, wrapped with what was being opened.
func TestOpenCacheError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := docs.OpenCache(file); err == nil || !strings.HasPrefix(err.Error(), "open docs cache: ") {
		t.Errorf("OpenCache on a file = %v, want an open docs cache error", err)
	}
}
