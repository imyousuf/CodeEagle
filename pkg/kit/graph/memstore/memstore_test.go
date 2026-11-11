package memstore_test

import (
	"testing"

	"github.com/imyousuf/CodeEagle/pkg/kit/graph"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph/graphtest"
	"github.com/imyousuf/CodeEagle/pkg/kit/graph/memstore"
)

func TestMemstoreConforms(t *testing.T) {
	graphtest.Run(t, func(t *testing.T) graph.Store {
		s := memstore.New()
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}
