//go:build faces

package faces

import (
	"encoding/json"
	"fmt"
	"image"
	"strconv"
	"strings"

	"github.com/dgraph-io/badger/v4"
)

const (
	prefixFaceEmb     = "face:emb:"     // face:emb:<imagePath>:<faceIdx> → embedding JSON
	prefixFaceBox     = "face:box:"     // face:box:<imagePath>:<faceIdx> → FaceRecord JSON
	prefixFaceCluster = "face:cluster:" // face:cluster:<imagePath>:<faceIdx> → cluster ID string
	prefixLabel       = "face:label:"   // face:label:<clusterID> → person name
	prefixScanned     = "face:scanned:" // face:scanned:<imagePath> → "1"
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

// Store provides persistent storage for face detection data using BadgerDB.
type Store struct {
	db *badger.DB
}

// OpenStore opens (or creates) the face store at the given directory path.
func OpenStore(dbPath string) (*Store, error) {
	opts := badger.DefaultOptions(dbPath)
	opts.Logger = nil
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("open face store: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying BadgerDB.
func (s *Store) Close() error {
	return s.db.Close()
}

func faceKey(imagePath string, faceIdx int) string {
	return fmt.Sprintf("%s:%d", imagePath, faceIdx)
}

// StoreFace saves a face record to the store.
func (s *Store) StoreFace(rec *FaceRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal face record: %w", err)
	}

	key := faceKey(rec.ImagePath, rec.FaceIdx)
	return s.db.Update(func(txn *badger.Txn) error {
		if err := txn.Set([]byte(prefixFaceBox+key), data); err != nil {
			return err
		}
		embData, err := json.Marshal(rec.Embedding)
		if err != nil {
			return err
		}
		if err := txn.Set([]byte(prefixFaceEmb+key), embData); err != nil {
			return err
		}
		return txn.Set([]byte(prefixFaceCluster+key), []byte(strconv.Itoa(rec.ClusterID)))
	})
}

// UpdateCluster updates the cluster ID for a face.
func (s *Store) UpdateCluster(imagePath string, faceIdx, clusterID int) error {
	key := faceKey(imagePath, faceIdx)
	return s.db.Update(func(txn *badger.Txn) error {
		// Update cluster in the face record.
		item, err := txn.Get([]byte(prefixFaceBox + key))
		if err != nil {
			return err
		}
		var rec FaceRecord
		if err := item.Value(func(val []byte) error {
			return json.Unmarshal(val, &rec)
		}); err != nil {
			return err
		}
		rec.ClusterID = clusterID
		data, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		if err := txn.Set([]byte(prefixFaceBox+key), data); err != nil {
			return err
		}
		return txn.Set([]byte(prefixFaceCluster+key), []byte(strconv.Itoa(clusterID)))
	})
}

// AllFaces returns all face records from the store.
func (s *Store) AllFaces() ([]FaceRecord, error) {
	var faces []FaceRecord
	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		prefix := []byte(prefixFaceBox)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			if err := item.Value(func(val []byte) error {
				var rec FaceRecord
				if err := json.Unmarshal(val, &rec); err != nil {
					return err
				}
				faces = append(faces, rec)
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return faces, err
}

// SetLabel associates a person name with a cluster ID.
func (s *Store) SetLabel(clusterID int, name string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(prefixLabel+strconv.Itoa(clusterID)), []byte(name))
	})
}

// GetLabel returns the person name for a cluster ID, or "" if not labeled.
func (s *Store) GetLabel(clusterID int) string {
	var name string
	_ = s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(prefixLabel + strconv.Itoa(clusterID)))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			name = string(val)
			return nil
		})
	})
	return name
}

// DeleteLabel removes the label for a cluster ID.
func (s *Store) DeleteLabel(clusterID int) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete([]byte(prefixLabel + strconv.Itoa(clusterID)))
	})
}

// AllLabels returns all cluster ID → name mappings.
func (s *Store) AllLabels() (map[int]string, error) {
	labels := make(map[int]string)
	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		prefix := []byte(prefixLabel)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			key := strings.TrimPrefix(string(item.Key()), prefixLabel)
			id, err := strconv.Atoi(key)
			if err != nil {
				continue
			}
			if err := item.Value(func(val []byte) error {
				labels[id] = string(val)
				return nil
			}); err != nil {
				continue
			}
		}
		return nil
	})
	return labels, err
}

// MarkScanned records that an image has been processed.
func (s *Store) MarkScanned(imagePath string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(prefixScanned+imagePath), []byte("1"))
	})
}

// IsScanned returns true if the image was previously processed.
func (s *Store) IsScanned(imagePath string) bool {
	err := s.db.View(func(txn *badger.Txn) error {
		_, err := txn.Get([]byte(prefixScanned + imagePath))
		return err
	})
	return err == nil
}

// DeleteFacesForImage removes all face records for a given image path.
func (s *Store) DeleteFacesForImage(imagePath string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		// Scan for all face keys with this image path prefix.
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		it := txn.NewIterator(opts)
		defer it.Close()

		var keysToDelete [][]byte
		for _, pfx := range []string{prefixFaceBox, prefixFaceEmb, prefixFaceCluster} {
			prefix := []byte(pfx + imagePath + ":")
			for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
				keysToDelete = append(keysToDelete, append([]byte{}, it.Item().Key()...))
			}
		}

		for _, key := range keysToDelete {
			if err := txn.Delete(key); err != nil {
				return err
			}
		}

		// Also remove the scanned marker.
		_ = txn.Delete([]byte(prefixScanned + imagePath))
		return nil
	})
}
