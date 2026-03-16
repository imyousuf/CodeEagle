//go:build app && faces

package app

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/imyousuf/CodeEagle/internal/faces"
	"github.com/imyousuf/CodeEagle/internal/graph/embedded"
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
	faceStorePath := filepath.Join(a.cfg.ConfigDir, "faces.db")
	fs, fsErr := faces.OpenStore(faceStorePath)
	if fsErr == nil {
		defer fs.Close()
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
