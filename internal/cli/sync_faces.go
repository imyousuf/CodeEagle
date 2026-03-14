//go:build faces

package cli

import (
	"path/filepath"

	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/faces"
	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
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
	modelDir := cfg.Docs.Faces.ModelDir
	if modelDir == "" {
		modelDir = filepath.Join(cfg.ConfigDir, "models")
	}
	if err := faces.EnsureModels(modelDir, warnFn); err != nil {
		warnFn("[faces] Failed to ensure models: %v", err)
		return func() {}
	}

	threshold := float32(cfg.Docs.Faces.ConfidenceThreshold)
	if threshold <= 0 {
		threshold = 0.5
	}
	minFace := cfg.Docs.Faces.MinFaceSize
	if minFace <= 0 {
		minFace = 40
	}
	maxRes := cfg.Docs.MaxImageRes
	if maxRes <= 0 {
		maxRes = 1024
	}

	detector, err := faces.NewDetector(modelDir, threshold, minFace, maxRes)
	if err != nil {
		warnFn("[faces] Failed to open detector: %v", err)
		return func() {}
	}

	faceStorePath := filepath.Join(cfg.ConfigDir, "faces.db")
	faceStore, err := faces.OpenStore(faceStorePath)
	if err != nil {
		detector.Close()
		warnFn("[faces] Failed to open face store: %v", err)
		return func() {}
	}

	// The graph store must be a *embedded.BranchStore to satisfy PersonStore.
	branchStore, ok := graphStore.(*embedded.BranchStore)
	if !ok {
		detector.Close()
		faceStore.Close()
		warnFn("[faces] Graph store is not a BranchStore; face handlers disabled")
		return func() {}
	}

	classifyK := cfg.Docs.Faces.ClassifyK
	if classifyK <= 0 {
		classifyK = 7
	}
	autoAccept := cfg.Docs.Faces.AutoAcceptThreshold
	if autoAccept <= 0 {
		autoAccept = 0.55
	}
	decayWarning := cfg.Docs.Faces.ConfidenceDecayWarning
	classifier := faces.NewKNNClassifier(classifyK, autoAccept, decayWarning)

	simThreshold := float32(cfg.Docs.Faces.SimilarityThreshold)
	if simThreshold <= 0 {
		simThreshold = 0.55
	}
	minCluster := cfg.Docs.Faces.CheckpointClusters
	if minCluster <= 0 {
		minCluster = 2
	}

	queue.RegisterFaceHandlers(
		pool, detector, faceStore, branchStore, classifier,
		autoAccept, simThreshold, minCluster, repoPaths(cfg),
	)

	warnFn("[faces] Face handlers registered (K=%d, auto-accept=%.2f)", classifyK, autoAccept)

	return func() {
		detector.Close()
		faceStore.Close()
	}
}
