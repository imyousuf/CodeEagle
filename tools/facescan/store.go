package main

import (
	"encoding/json"
	"image"
	"os"
	"path/filepath"
)

// FaceRecord represents a single detected face in an image.
type FaceRecord struct {
	ImagePath  string          `json:"image_path"`
	FaceIdx    int             `json:"face_idx"`
	BBox       image.Rectangle `json:"bbox"`
	Confidence float32         `json:"confidence"`
	Embedding  []float32       `json:"embedding"`
	ClusterID  int             `json:"cluster_id"`
}

// FaceDB holds all face records and cluster labels.
type FaceDB struct {
	Faces  []FaceRecord   `json:"faces"`
	Labels map[int]string `json:"labels"` // clusterID → person name
}

// LoadDB loads the face database from a JSON file.
func LoadDB(path string) (*FaceDB, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var db FaceDB
	if err := json.Unmarshal(data, &db); err != nil {
		return nil, err
	}
	if db.Labels == nil {
		db.Labels = make(map[int]string)
	}
	return &db, nil
}

// Save writes the face database to a JSON file.
func (db *FaceDB) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(db, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
