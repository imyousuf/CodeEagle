package cli

import (
	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/queue"
)

// registerFaceHandlers is a no-op.
//
// Face detection is not part of this branch: the enrichment queue keeps its
// document and image handlers, and face jobs are never enqueued because
// facesAvailable stays false. The seam is left in place so that the face
// pipeline can be registered here again without reshaping the sync path.
func registerFaceHandlers(
	_ *queue.WorkerPool,
	_ *config.Config,
	_ graph.Store,
	_ func(format string, args ...any),
) func() {
	return func() {}
}
