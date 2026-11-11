package embedded_test

import (
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph/graphtest"
)

// TestStoreConformance runs the graph.Store contract against a single-scope
// Badger store, the shape every caller that does not deal in branches sees.
func TestStoreConformance(t *testing.T) {
	graphtest.Run(t, func(t *testing.T) graph.Store {
		s, err := embedded.NewStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	})
}
