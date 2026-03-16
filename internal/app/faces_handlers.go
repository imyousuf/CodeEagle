//go:build app && faces

package app

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/image/draw"

	"github.com/imyousuf/CodeEagle/internal/faces"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
	genericparser "github.com/imyousuf/CodeEagle/internal/parser/generic"
)

// PersonInfo is the frontend-facing person data.
type PersonInfo struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Relationships []string `json:"relationships"`
	FaceCount     int      `json:"face_count"`
	CreatedAt     string   `json:"created_at"`
}

// ClusterInfo is the frontend-facing cluster data.
type ClusterInfo struct {
	ClusterID  int      `json:"cluster_id"`
	FaceCount  int      `json:"face_count"`
	ImagePaths []string `json:"image_paths"`
	Label      string   `json:"label"`
}

// FaceStats holds face pipeline statistics.
type FaceStats struct {
	TotalPersons    int    `json:"total_persons"`
	TotalFaces      int    `json:"total_faces"`
	DetectedFaces   int    `json:"detected_faces"`
	ImagesWithFaces int    `json:"images_with_faces"`
	TotalImages     int    `json:"total_images"`
	ScannedCount    int    `json:"scanned_count"`
	OldestDate      string `json:"oldest_date"`
	NewestDate      string `json:"newest_date"`
}

// FaceReviewItem represents a face pending manual review.
type FaceReviewItem struct {
	ImagePath  string  `json:"image_path"`
	FaceIndex  int     `json:"face_index"`
	ClusterID  int     `json:"cluster_id"`
	Confidence float64 `json:"confidence"`
}

// GetPersons returns all known persons with face counts.
func (a *App) GetPersons() ([]PersonInfo, error) {
	gr, close, err := a.openGraph()
	if err != nil {
		return nil, err
	}
	defer close()

	persons, err := gr.store.ListPersons()
	if err != nil {
		return nil, fmt.Errorf("list persons: %w", err)
	}

	var result []PersonInfo
	for _, p := range persons {
		faces, _ := gr.store.FacesForPerson(p.ID)
		result = append(result, PersonInfo{
			ID:            p.ID,
			Name:          p.Name,
			Relationships: p.Relationships,
			FaceCount:     len(faces),
			CreatedAt:     p.CreatedAt.Format(time.RFC3339),
		})
	}
	return result, nil
}

// CreatePerson creates a new person with optional relationships.
func (a *App) CreatePerson(name string, relationships []string) (*PersonInfo, error) {
	gr, close, err := a.openGraph()
	if err != nil {
		return nil, err
	}
	defer close()

	p := &embedded.Person{Name: name, Relationships: relationships}
	if err := gr.store.CreatePerson(p); err != nil {
		return nil, fmt.Errorf("create person: %w", err)
	}

	return &PersonInfo{
		ID:            p.ID,
		Name:          p.Name,
		Relationships: p.Relationships,
		CreatedAt:     p.CreatedAt.Format(time.RFC3339),
	}, nil
}

// UpdatePerson updates a person's name and relationships.
func (a *App) UpdatePerson(id, name string, relationships []string) error {
	gr, close, err := a.openGraph()
	if err != nil {
		return err
	}
	defer close()

	p, err := gr.store.GetPerson(id)
	if err != nil || p == nil {
		return fmt.Errorf("person %s not found", id)
	}

	p.Name = name
	p.Relationships = relationships
	return gr.store.UpdatePerson(p)
}

// DeletePerson removes a person and all associated face data.
func (a *App) DeletePerson(id string) error {
	gr, close, err := a.openGraph()
	if err != nil {
		return err
	}
	defer close()
	return gr.store.DeletePerson(id)
}

// GetFaceStats returns aggregate face pipeline statistics.
func (a *App) GetFaceStats() (*FaceStats, error) {
	gr, close, err := a.openGraph()
	if err != nil {
		return nil, err
	}
	defer close()

	persons, _ := gr.store.ListPersons()
	assignedFaces := 0
	for _, p := range persons {
		pf, _ := gr.store.FacesForPerson(p.ID)
		assignedFaces += len(pf)
	}

	imageCount := gr.store.ImageCount()
	unscanned, _ := gr.store.UnscannedImages()
	oldest, newest, _ := gr.store.DateRange()

	// Count detected faces from the face store.
	var detectedFaces, imagesWithFaces int
	fs, fsErr := a.getFaceStore()
	if fsErr == nil {
		allFaces, err := fs.AllFaces()
		if err == nil {
			detectedFaces = len(allFaces)
			imgSet := make(map[string]bool)
			for _, f := range allFaces {
				imgSet[f.ImagePath] = true
			}
			imagesWithFaces = len(imgSet)
		}
	}

	stats := &FaceStats{
		TotalPersons:    len(persons),
		TotalFaces:      assignedFaces,
		DetectedFaces:   detectedFaces,
		ImagesWithFaces: imagesWithFaces,
		TotalImages:     imageCount,
		ScannedCount:    imageCount - len(unscanned),
	}
	if !oldest.IsZero() {
		stats.OldestDate = oldest.Format("2006-01-02")
	}
	if !newest.IsZero() {
		stats.NewestDate = newest.Format("2006-01-02")
	}
	return stats, nil
}

// ResumeSync unblocks a face checkpoint pause.
func (a *App) ResumeSync() {
	a.emit("sync:resume-requested", "")
}

// AssignFaceToPerson manually assigns a face to a person.
func (a *App) AssignFaceToPerson(personID, imagePath string, faceIndex int, confidence float64) error {
	gr, close, err := a.openGraph()
	if err != nil {
		return err
	}
	defer close()

	return gr.store.AssignFaceToPerson(&embedded.FaceAssignment{
		PersonID:   personID,
		ImagePath:  imagePath,
		FaceIndex:  faceIndex,
		Confidence: confidence,
	})
}

// ---------------------------------------------------------------------------
// Cluster types
// ---------------------------------------------------------------------------

// ClusterFace identifies a single face within an image.
type ClusterFace struct {
	ImagePath string `json:"image_path"`
	FaceIndex int    `json:"face_index"`
}

// ClusterDetail is the frontend-facing cluster data with face list.
type ClusterDetail struct {
	ClusterID int           `json:"cluster_id"`
	Label     string        `json:"label"`
	FaceCount int           `json:"face_count"`
	Faces     []ClusterFace `json:"faces"`
	PersonID  string        `json:"person_id"`
}

// MergeSuggestion represents a pair of clusters that may belong to the same person.
type MergeSuggestion struct {
	ClusterA   int     `json:"cluster_a"`
	ClusterB   int     `json:"cluster_b"`
	LabelA     string  `json:"label_a"`
	LabelB     string  `json:"label_b"`
	Similarity float64 `json:"similarity"`
	FaceCountA int     `json:"face_count_a"`
	FaceCountB int     `json:"face_count_b"`
}

// ---------------------------------------------------------------------------
// Face store singleton
// ---------------------------------------------------------------------------

var (
	faceStoreOnce sync.Once
	faceStoreInst *faces.Store
	faceStoreErr  error
)

// getFaceStore returns a shared face store instance. The store is opened once
// on first call and reused for the lifetime of the app. BadgerDB is safe for
// concurrent read/write access from multiple goroutines within a single
// process, so there is no need for per-request open/close.
func (a *App) getFaceStore() (*faces.Store, error) {
	faceStoreOnce.Do(func() {
		p := filepath.Join(a.cfg.ConfigDir, "faces.db")
		faceStoreInst, faceStoreErr = faces.OpenStore(p)
		if faceStoreErr == nil {
			a.shutdownHooks = append(a.shutdownHooks, func() {
				faceStoreInst.Close()
			})
		}
	})
	return faceStoreInst, faceStoreErr
}

// ---------------------------------------------------------------------------
// Image serving handlers
// ---------------------------------------------------------------------------

// GetFaceThumbnail returns a base64-encoded JPEG thumbnail of a detected face.
func (a *App) GetFaceThumbnail(imagePath string, faceIndex, size int) (string, error) {
	if size <= 0 {
		size = 64
	}

	fs, err := a.getFaceStore()
	if err != nil {
		return "", fmt.Errorf("open face store: %w", err)
	}

	rec, err := fs.GetFace(imagePath, faceIndex)
	if err != nil {
		return "", fmt.Errorf("get face record: %w", err)
	}

	absPath := faces.ResolveFilePath(imagePath, a.repoPaths)
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("read image: %w", err)
	}

	mimeType := detectImageMIME(imagePath)
	src, err := decodeImageData(data, mimeType)
	if err != nil {
		return "", fmt.Errorf("decode image: %w", err)
	}

	// Crop face with 20% padding.
	crop := padRect(rec.BBox, src.Bounds(), 0.2)
	cropped := src.(interface {
		SubImage(r image.Rectangle) image.Image
	}).SubImage(crop)

	// Resize to target size.
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.BiLinear.Scale(dst, dst.Bounds(), cropped, cropped.Bounds(), draw.Over, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
		return "", fmt.Errorf("encode jpeg: %w", err)
	}

	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// GetImagePreview returns a base64-encoded JPEG preview of an image.
func (a *App) GetImagePreview(imagePath string, maxRes int) (string, error) {
	if maxRes <= 0 {
		maxRes = 800
	}

	absPath := faces.ResolveFilePath(imagePath, a.repoPaths)
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("read image: %w", err)
	}

	mimeType := detectImageMIME(imagePath)
	imgData, _, _, err := genericparser.DownscaleImage(data, mimeType, maxRes)
	if err != nil {
		return "", fmt.Errorf("downscale: %w", err)
	}

	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(imgData), nil
}

// padRect expands a rectangle by a fraction while staying within bounds.
func padRect(r image.Rectangle, bounds image.Rectangle, frac float64) image.Rectangle {
	w := r.Dx()
	h := r.Dy()
	padW := int(float64(w) * frac)
	padH := int(float64(h) * frac)

	minX := r.Min.X - padW
	minY := r.Min.Y - padH
	maxX := r.Max.X + padW
	maxY := r.Max.Y + padH

	if minX < bounds.Min.X {
		minX = bounds.Min.X
	}
	if minY < bounds.Min.Y {
		minY = bounds.Min.Y
	}
	if maxX > bounds.Max.X {
		maxX = bounds.Max.X
	}
	if maxY > bounds.Max.Y {
		maxY = bounds.Max.Y
	}
	return image.Rect(minX, minY, maxX, maxY)
}

// detectImageMIME returns the MIME type for an image file.
func detectImageMIME(filePath string) string {
	ext := filepath.Ext(filePath)
	if ext == "" {
		return "image/jpeg"
	}
	mt := mime.TypeByExtension(ext)
	if mt == "" {
		switch strings.ToLower(ext) {
		case ".webp":
			return "image/webp"
		case ".bmp":
			return "image/bmp"
		case ".tiff", ".tif":
			return "image/tiff"
		default:
			return "image/jpeg"
		}
	}
	return mt
}

// decodeImageData decodes image data from bytes using the MIME type.
func decodeImageData(data []byte, mimeType string) (image.Image, error) {
	// Use genericparser.DownscaleImage logic path: decode based on mime.
	// But we need the raw image, not re-encoded. Use image.Decode with registered decoders.
	_ = mimeType // all standard formats are registered via imports
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

// ---------------------------------------------------------------------------
// Cluster data handlers
// ---------------------------------------------------------------------------

// GetClusters returns all face clusters with their faces.
func (a *App) GetClusters() ([]ClusterDetail, error) {
	fs, err := a.getFaceStore()
	if err != nil {
		return nil, fmt.Errorf("open face store: %w", err)
	}

	allFaces, err := fs.AllFaces()
	if err != nil {
		return nil, fmt.Errorf("list faces: %w", err)
	}

	labels, _ := fs.AllLabels()

	// Group by cluster ID, excluding noise.
	clusterMap := make(map[int][]ClusterFace)
	for _, f := range allFaces {
		if f.ClusterID < 0 {
			continue
		}
		clusterMap[f.ClusterID] = append(clusterMap[f.ClusterID], ClusterFace{
			ImagePath: f.ImagePath,
			FaceIndex: f.FaceIdx,
		})
	}

	var result []ClusterDetail
	for cid, cfaces := range clusterMap {
		detail := ClusterDetail{
			ClusterID: cid,
			Label:     labels[cid],
			FaceCount: len(cfaces),
		}
		// Limit faces to first 20 for preview.
		if len(cfaces) > 20 {
			detail.Faces = cfaces[:20]
		} else {
			detail.Faces = cfaces
		}
		result = append(result, detail)
	}

	// Sort by face count descending.
	sort.Slice(result, func(i, j int) bool {
		return result[i].FaceCount > result[j].FaceCount
	})

	return result, nil
}

// GetNoiseFaces returns all faces not assigned to any cluster.
func (a *App) GetNoiseFaces() ([]ClusterFace, error) {
	fs, err := a.getFaceStore()
	if err != nil {
		return nil, fmt.Errorf("open face store: %w", err)
	}

	allFaces, err := fs.AllFaces()
	if err != nil {
		return nil, fmt.Errorf("list faces: %w", err)
	}

	var noise []ClusterFace
	for _, f := range allFaces {
		if f.ClusterID < 0 {
			noise = append(noise, ClusterFace{
				ImagePath: f.ImagePath,
				FaceIndex: f.FaceIdx,
			})
		}
	}
	return noise, nil
}

// RemoveFaceFromCluster sets a face's cluster to -1 (noise).
func (a *App) RemoveFaceFromCluster(imagePath string, faceIndex int) error {
	fs, err := a.getFaceStore()
	if err != nil {
		return fmt.Errorf("open face store: %w", err)
	}
	return fs.UpdateCluster(imagePath, faceIndex, -1)
}

// MergeClusters merges source clusters into a target cluster.
func (a *App) MergeClusters(targetID int, sourceIDs []int) (int, error) {
	fs, err := a.getFaceStore()
	if err != nil {
		return 0, fmt.Errorf("open face store: %w", err)
	}
	return faces.Merge(fs, targetID, sourceIDs)
}

// SplitCluster re-clusters a single cluster at a tighter threshold.
func (a *App) SplitCluster(clusterID int, simThreshold float64) (map[int]int, error) {
	fs, err := a.getFaceStore()
	if err != nil {
		return nil, fmt.Errorf("open face store: %w", err)
	}
	return faces.Split(fs, clusterID, float32(simThreshold))
}

// SetClusterLabel assigns a label to a cluster.
func (a *App) SetClusterLabel(clusterID int, label string) error {
	fs, err := a.getFaceStore()
	if err != nil {
		return fmt.Errorf("open face store: %w", err)
	}
	return fs.SetLabel(clusterID, label)
}

// AssignClusterToPerson assigns all faces in a cluster to a person.
func (a *App) AssignClusterToPerson(clusterID int, personID string) error {
	fs, err := a.getFaceStore()
	if err != nil {
		return fmt.Errorf("open face store: %w", err)
	}

	gr, closeFn, err := a.openGraph()
	if err != nil {
		return err
	}
	defer closeFn()

	// Get person to set label.
	person, err := gr.store.GetPerson(personID)
	if err != nil || person == nil {
		return fmt.Errorf("person %s not found", personID)
	}

	allFaces, err := fs.AllFaces()
	if err != nil {
		return fmt.Errorf("list faces: %w", err)
	}

	var addedExemplar bool
	for _, f := range allFaces {
		if f.ClusterID != clusterID {
			continue
		}
		if err := gr.store.AssignFaceToPerson(&embedded.FaceAssignment{
			PersonID:   personID,
			ImagePath:  f.ImagePath,
			FaceIndex:  f.FaceIdx,
			Confidence: 1.0,
		}); err != nil {
			continue
		}
		// Add the first face as an exemplar for future KNN classification.
		if !addedExemplar && len(f.Embedding) > 0 {
			_ = gr.store.AddExemplar(&embedded.Exemplar{
				PersonID:  personID,
				Hash:      fmt.Sprintf("cluster:%d", clusterID),
				Embedding: f.Embedding,
				ImagePath: f.ImagePath,
			})
			addedExemplar = true
		}
	}

	// Set cluster label to person name.
	_ = fs.SetLabel(clusterID, person.Name)
	return nil
}

// ---------------------------------------------------------------------------
// Clustering
// ---------------------------------------------------------------------------

// clusteringRunning prevents concurrent clustering runs.
var clusteringRunning atomic.Bool

// RunClustering runs agglomerative face clustering asynchronously.
func (a *App) RunClustering(simThreshold float64) error {
	if simThreshold <= 0 {
		simThreshold = 0.30
	}
	if !clusteringRunning.CompareAndSwap(false, true) {
		return fmt.Errorf("clustering already in progress")
	}

	go func() {
		defer clusteringRunning.Store(false)
		a.emit("faces:clustering-started", "")

		fs, err := a.getFaceStore()
		if err != nil {
			a.emit("faces:clustering-error", err.Error())
			return
		}

		allFaces, err := fs.AllFaces()
		if err != nil {
			a.emit("faces:clustering-error", err.Error())
			return
		}

		if len(allFaces) == 0 {
			a.emit("faces:clustering-complete", map[string]int{
				"clusters": 0, "faces": 0, "noise": 0,
			})
			return
		}

		// Build embedding + image path slices.
		embeddings := make([][]float32, len(allFaces))
		imagePaths := make([]string, len(allFaces))
		for i, f := range allFaces {
			embeddings[i] = f.Embedding
			imagePaths[i] = f.ImagePath
		}

		// Run agglomerative clustering.
		labels := faces.AgglomerativeClustering(
			embeddings, imagePaths, float32(simThreshold), 2,
		)

		// Absorb noise at 75% of threshold.
		labels = faces.AbsorbNoise(
			embeddings, imagePaths, labels, float32(simThreshold*0.75),
		)

		// Update cluster IDs in face store.
		clusterSet := make(map[int]bool)
		noiseCount := 0
		for i, lbl := range labels {
			_ = fs.UpdateCluster(allFaces[i].ImagePath, allFaces[i].FaceIdx, lbl)
			if lbl < 0 {
				noiseCount++
			} else {
				clusterSet[lbl] = true
			}
		}

		a.emit("faces:clustering-complete", map[string]int{
			"clusters": len(clusterSet),
			"faces":    len(allFaces),
			"noise":    noiseCount,
		})
	}()

	return nil
}

// IsClusteringRunning returns whether clustering is currently in progress.
func (a *App) IsClusteringRunning() bool {
	return clusteringRunning.Load()
}

// ---------------------------------------------------------------------------
// Merge suggestions
// ---------------------------------------------------------------------------

// GetSuggestedMerges returns pairs of clusters with high centroid similarity.
func (a *App) GetSuggestedMerges() ([]MergeSuggestion, error) {
	fs, err := a.getFaceStore()
	if err != nil {
		return nil, fmt.Errorf("open face store: %w", err)
	}

	allFaces, err := fs.AllFaces()
	if err != nil {
		return nil, fmt.Errorf("list faces: %w", err)
	}

	labels, _ := fs.AllLabels()

	// Group embeddings by cluster.
	type clusterData struct {
		embeddings [][]float32
		imagePaths []string
	}
	clusters := make(map[int]*clusterData)
	for _, f := range allFaces {
		if f.ClusterID < 0 {
			continue
		}
		cd, ok := clusters[f.ClusterID]
		if !ok {
			cd = &clusterData{}
			clusters[f.ClusterID] = cd
		}
		cd.embeddings = append(cd.embeddings, f.Embedding)
		cd.imagePaths = append(cd.imagePaths, f.ImagePath)
	}

	// Compute centroids.
	type centroid struct {
		id        int
		embedding []float32
		paths     []string
		count     int
	}
	var centroids []centroid
	for cid, cd := range clusters {
		if len(cd.embeddings) == 0 {
			continue
		}
		dim := len(cd.embeddings[0])
		avg := make([]float32, dim)
		for _, emb := range cd.embeddings {
			for j, v := range emb {
				avg[j] += v
			}
		}
		n := float32(len(cd.embeddings))
		for j := range avg {
			avg[j] /= n
		}
		centroids = append(centroids, centroid{
			id:        cid,
			embedding: avg,
			paths:     cd.imagePaths,
			count:     len(cd.embeddings),
		})
	}

	// Pairwise similarity.
	var suggestions []MergeSuggestion
	for i := 0; i < len(centroids); i++ {
		for j := i + 1; j < len(centroids); j++ {
			sim := float64(faces.CosineSimilarity(centroids[i].embedding, centroids[j].embedding))
			if sim < 0.25 {
				continue
			}
			suggestions = append(suggestions, MergeSuggestion{
				ClusterA:   centroids[i].id,
				ClusterB:   centroids[j].id,
				LabelA:     labels[centroids[i].id],
				LabelB:     labels[centroids[j].id],
				Similarity: sim,
				FaceCountA: centroids[i].count,
				FaceCountB: centroids[j].count,
			})
		}
	}

	sort.Slice(suggestions, func(i, j int) bool {
		return suggestions[i].Similarity > suggestions[j].Similarity
	})

	// Limit to top 50 suggestions.
	if len(suggestions) > 50 {
		suggestions = suggestions[:50]
	}

	return suggestions, nil
}
