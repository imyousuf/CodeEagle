//go:build !faces

package cli

import (
	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/queue"
)

// registerFaceHandlers is a no-op when the "faces" build tag is not active.
func registerFaceHandlers(
	_ *queue.WorkerPool,
	_ *config.Config,
	_ graph.Store,
	_ func(format string, args ...any),
) func() {
	return func() {}
}
