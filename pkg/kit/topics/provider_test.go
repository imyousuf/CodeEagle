package topics_test

import (
	"strings"
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/kit/topics"
)

func TestUsable(t *testing.T) {
	long := "A summary that is long enough."
	for _, tt := range []struct {
		name string
		r    *topics.ExtractionResult
		want bool
	}{
		{"nil", nil, false},
		{"three topics", &topics.ExtractionResult{Summary: long, Topics: []string{"a", "b", "c"}}, true},
		{"two topics", &topics.ExtractionResult{Summary: long, Topics: []string{"a", "b"}}, false},
		{"ellipsis", &topics.ExtractionResult{Summary: "A summary that trails off...", Topics: []string{"a", "b", "c"}}, false},
		{"twenty characters", &topics.ExtractionResult{Summary: strings.Repeat("x", 20), Topics: []string{"a", "b", "c"}}, false},
		{"twenty-one characters", &topics.ExtractionResult{Summary: strings.Repeat("x", 21), Topics: []string{"a", "b", "c"}}, true},
	} {
		if got := topics.Usable(tt.r); got != tt.want {
			t.Errorf("%s: Usable = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// mapCache is a caller's own cache, keyed by content hash as the contract
// asks.
type mapCache struct {
	hashOf  map[string]string
	results map[string]*topics.ExtractionResult
	skipped map[string]bool
}

func (c *mapCache) Check(path, hash string) (*topics.ExtractionResult, error) {
	if c.hashOf[path] != hash {
		return nil, nil
	}
	return c.results[hash], nil
}

func (c *mapCache) Store(path, hash string, r *topics.ExtractionResult) error {
	c.hashOf[path], c.results[hash] = hash, r
	delete(c.skipped, hash)
	return nil
}

func (c *mapCache) MarkSkipped(path, hash string) error {
	c.hashOf[path], c.skipped[hash] = hash, true
	return nil
}

func (c *mapCache) IsSkipped(hash string) bool { return c.skipped[hash] }

func TestCacheIsImplementableElsewhere(t *testing.T) {
	var c topics.Cache = &mapCache{hashOf: map[string]string{}, results: map[string]*topics.ExtractionResult{}, skipped: map[string]bool{}}
	_ = c.MarkSkipped("a.md", "h1")
	if !c.IsSkipped("h1") {
		t.Error("skip not recorded")
	}
	r := &topics.ExtractionResult{Summary: "s"}
	_ = c.Store("b.md", "h1", r)
	if got, _ := c.Check("b.md", "h1"); got != r || c.IsSkipped("h1") {
		t.Error("store did not replace the skip")
	}
	if got, _ := c.Check("b.md", "h2"); got != nil {
		t.Error("a changed hash hit the cache")
	}
}
