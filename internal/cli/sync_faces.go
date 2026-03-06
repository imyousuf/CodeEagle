//go:build faces

package cli

import (
	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/queue"
)

// registerFaceHandlers opens the face detector and face store, registers
// face-detect and face-cluster handlers on the worker pool, and returns a
// cleanup function. Compiled only with the "faces" build tag.
func registerFaceHandlers(
	pool *queue.WorkerPool,
	cfg *config.Config,
	graphStore graph.Store,
	warnFn func(format string, args ...any),
) func() {
	// TODO: Phase 3 will implement face handler registration:
	// 1. Open face detector (ONNX models)
	// 2. Create KNN classifier
	// 3. Register FaceDetectHandler
	// 4. Register FaceClusterHandler
	// 5. Return cleanup function that closes detector + face store
	_ = pool
	_ = cfg
	_ = graphStore
	warnFn("[faces] Face handlers not yet implemented")
	return func() {}
}
