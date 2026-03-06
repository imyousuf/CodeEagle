//go:build faces

package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/imyousuf/CodeEagle/internal/faces"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
)

// PersonStore is a minimal interface for person domain operations needed by
// face handlers. Implemented by *embedded.BranchStore.
type PersonStore interface {
	AllExemplars() ([]*embedded.Exemplar, error)
	AssignFaceToPerson(fa *embedded.FaceAssignment) error
	AddExemplar(e *embedded.Exemplar) error
	MarkImageScanned(imagePath string) error
	IsImageScanned(imagePath string) bool
	IndexImage(meta *embedded.ImageMetadata) error
}

// FaceDetectHandler detects faces in images, runs KNN classification, and
// assigns high-confidence matches.
type FaceDetectHandler struct {
	detector    *faces.Detector
	faceStore   *faces.Store
	personStore PersonStore
	classifier  *faces.KNNClassifier
	autoAccept  float64
}

// NewFaceDetectHandler creates a face detection handler.
func NewFaceDetectHandler(
	detector *faces.Detector,
	faceStore *faces.Store,
	personStore PersonStore,
	classifier *faces.KNNClassifier,
	autoAcceptThreshold float64,
) *FaceDetectHandler {
	return &FaceDetectHandler{
		detector:    detector,
		faceStore:   faceStore,
		personStore: personStore,
		classifier:  classifier,
		autoAccept:  autoAcceptThreshold,
	}
}

// Handle processes a face-detect job.
func (h *FaceDetectHandler) Handle(_ context.Context, job *Job) (json.RawMessage, error) {
	if len(job.FilePaths) == 0 {
		return nil, fmt.Errorf("face-detect job has no file paths")
	}
	imagePath := job.FilePaths[0]

	// Skip if already scanned.
	if h.personStore.IsImageScanned(imagePath) {
		return marshalFaceResult("skipped", imagePath, 0), nil
	}

	// Extract image metadata.
	dateResult := faces.ExtractImageDate(imagePath)
	meta := &embedded.ImageMetadata{
		ImagePath:   imagePath,
		DateTaken:   dateResult.DateTaken,
		DateSource:  string(dateResult.DateSource),
		FolderName:  dateResult.FolderName,
		CameraModel: dateResult.CameraModel,
		GPSLat:      dateResult.GPSLat,
		GPSLon:      dateResult.GPSLon,
	}

	// Detect faces.
	result, err := h.detector.Detect(imagePath)
	if err != nil {
		return nil, fmt.Errorf("detect faces in %s: %w", imagePath, err)
	}

	meta.FaceCount = len(result.Faces)

	// Store each detected face.
	for i := range result.Faces {
		if err := h.faceStore.StoreFace(&result.Faces[i]); err != nil {
			return nil, fmt.Errorf("store face %d in %s: %w", i, imagePath, err)
		}
	}

	// KNN classification if exemplars exist.
	if h.classifier != nil {
		exemplars, _ := h.personStore.AllExemplars()
		if len(exemplars) > 0 {
			h.classifyFaces(imagePath, result.Faces, exemplars)
		}
	}

	// Index image metadata and mark as scanned.
	if err := h.personStore.IndexImage(meta); err != nil {
		return nil, fmt.Errorf("index image %s: %w", imagePath, err)
	}
	if err := h.personStore.MarkImageScanned(imagePath); err != nil {
		return nil, fmt.Errorf("mark scanned %s: %w", imagePath, err)
	}

	return marshalFaceResult("detected", imagePath, len(result.Faces)), nil
}

func (h *FaceDetectHandler) classifyFaces(imagePath string, detectedFaces []faces.FaceRecord, exemplars []*embedded.Exemplar) {
	// Convert exemplars to classifier format.
	var exData []faces.ExemplarData
	for _, e := range exemplars {
		exData = append(exData, faces.ExemplarData{
			PersonID:  e.PersonID,
			Embedding: e.Embedding,
			ImagePath: e.ImagePath,
			DateTaken: e.DateTaken,
		})
	}

	for i, face := range detectedFaces {
		cr := h.classifier.Classify(face.Embedding, imagePath, exData)
		if cr == nil || cr.Confidence < h.autoAccept {
			continue
		}

		// Auto-assign high-confidence match.
		_ = h.personStore.AssignFaceToPerson(&embedded.FaceAssignment{
			PersonID:   cr.PersonID,
			ImagePath:  imagePath,
			FaceIndex:  i,
			Confidence: cr.Confidence,
		})
	}
}

// FaceClusterHandler runs agglomerative clustering on all detected faces.
type FaceClusterHandler struct {
	faceStore    *faces.Store
	simThreshold float32
	minCluster   int
}

// NewFaceClusterHandler creates a face clustering handler.
func NewFaceClusterHandler(faceStore *faces.Store, simThreshold float32, minCluster int) *FaceClusterHandler {
	return &FaceClusterHandler{
		faceStore:    faceStore,
		simThreshold: simThreshold,
		minCluster:   minCluster,
	}
}

// Handle processes a face-cluster job.
func (h *FaceClusterHandler) Handle(_ context.Context, _ *Job) (json.RawMessage, error) {
	allFaces, err := h.faceStore.AllFaces()
	if err != nil {
		return nil, fmt.Errorf("load faces for clustering: %w", err)
	}

	if len(allFaces) == 0 {
		return json.Marshal(map[string]any{"status": "no_faces", "clusters": 0})
	}

	embeddings := make([][]float32, len(allFaces))
	imagePaths := make([]string, len(allFaces))
	for i, f := range allFaces {
		embeddings[i] = f.Embedding
		imagePaths[i] = f.ImagePath
	}

	labels := faces.AgglomerativeClustering(embeddings, imagePaths, h.simThreshold, h.minCluster)
	labels = faces.AbsorbNoise(embeddings, imagePaths, labels, h.simThreshold-0.05)

	for i, label := range labels {
		if err := h.faceStore.UpdateCluster(allFaces[i].ImagePath, allFaces[i].FaceIdx, label); err != nil {
			return nil, fmt.Errorf("update cluster for face %d: %w", i, err)
		}
	}

	// Count unique clusters.
	clusterSet := make(map[int]bool)
	for _, l := range labels {
		if l > 0 {
			clusterSet[l] = true
		}
	}

	return json.Marshal(map[string]any{
		"status":   "clustered",
		"faces":    len(allFaces),
		"clusters": len(clusterSet),
	})
}

// RegisterFaceHandlers registers face-detect and face-cluster handlers on a WorkerPool.
func RegisterFaceHandlers(
	pool *WorkerPool,
	detector *faces.Detector,
	faceStore *faces.Store,
	personStore PersonStore,
	classifier *faces.KNNClassifier,
	autoAcceptThreshold float64,
	clusterSimThreshold float32,
	minClusterSize int,
) {
	pool.Register(JobFaceDetect, NewFaceDetectHandler(
		detector, faceStore, personStore, classifier, autoAcceptThreshold,
	))
	pool.Register(JobFaceCluster, NewFaceClusterHandler(
		faceStore, clusterSimThreshold, minClusterSize,
	))
}

func marshalFaceResult(status, imagePath string, faceCount int) json.RawMessage {
	data, _ := json.Marshal(map[string]any{
		"status":     status,
		"image_path": imagePath,
		"face_count": faceCount,
	})
	return data
}

// init sets the checkpoint function for the worker pool.
func init() {
	CheckFaceCheckpointFn = checkFaceCheckpoint
}

// checkFaceCheckpoint is called after each face-detect job.
// It loads cluster info and triggers a checkpoint if enough new clusters are found.
func checkFaceCheckpoint(wp *WorkerPool) {
	if wp.IsPaused() {
		return
	}

	// Load checkpoint state.
	cp, err := wp.queue.LoadCheckpoint()
	if err != nil || cp == nil {
		cp = &CheckpointState{}
	}

	// Count completed face-detect jobs since last review.
	stats, err := wp.queue.Stats()
	if err != nil {
		return
	}

	faceStats, ok := stats.ByType[JobFaceDetect]
	if !ok {
		return
	}

	processed := faceStats.Done
	if processed <= cp.FacesAtLastReview {
		return
	}

	// Build cluster info from the completed count — simplified check.
	newClusters := processed - cp.FacesAtLastReview

	// Default threshold of 10 new face images before checkpoint.
	threshold := 10
	if newClusters >= threshold {
		payload := BuildCheckpointPayload(nil, processed, stats.Pending+stats.Running+stats.Done)

		// Save checkpoint state.
		_ = wp.queue.SaveCheckpoint(&CheckpointState{
			LastReviewAt:         time.Now(),
			FacesAtLastReview:    processed,
			ClustersAtLastReview: newClusters,
			ImagesProcessed:      processed,
			TotalImages:          stats.Pending + stats.Running + stats.Done,
			Paused:               true,
		})

		wp.PauseForCheckpoint(payload)
	}
}
