package embedded

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/google/uuid"
)

// Person key prefix constants (not branch-aware — person data is global).
const (
	prefixPerson       = "person:"
	prefixExemplar     = "person:exemplar:"
	prefixFace         = "person:face:"
	prefixPersonIdx    = "person:idx:name:"
	prefixImgMeta      = "img:meta:"
	prefixImgDateIdx   = "img:idx:date:"
	prefixImgEventIdx  = "img:idx:event:"
	prefixImgScanned   = "img:scanned:"
)

// Relationship types for persons.
var KnownRelationships = []string{
	"family", "friend", "colleague", "acquaintance", "other",
}

// Person represents a recognized individual.
type Person struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Relationships []string  `json:"relationships,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Exemplar is a reference face embedding for a person.
type Exemplar struct {
	PersonID  string    `json:"person_id"`
	Hash      string    `json:"hash"` // content hash of source image
	Embedding []float32 `json:"embedding"`
	ImagePath string    `json:"image_path"`
	AutoAdded bool      `json:"auto_added"`
	DateTaken time.Time `json:"date_taken"`
	CreatedAt time.Time `json:"created_at"`
}

// FaceAssignment links a detected face in an image to a person.
type FaceAssignment struct {
	PersonID   string    `json:"person_id"`
	ImagePath  string    `json:"image_path"`
	FaceIndex  int       `json:"face_index"`
	Confidence float64   `json:"confidence"`
	AssignedAt time.Time `json:"assigned_at"`
}

// ImageMetadata holds extracted metadata for an image file.
type ImageMetadata struct {
	ImagePath   string    `json:"image_path"`
	DateTaken   time.Time `json:"date_taken"`
	DateSource  string    `json:"date_source"`
	FolderName  string    `json:"folder_name"`
	CameraModel string   `json:"camera_model,omitempty"`
	GPSLat      float64   `json:"gps_lat,omitempty"`
	GPSLon      float64   `json:"gps_lon,omitempty"`
	EventName   string    `json:"event_name,omitempty"`
	FaceCount   int       `json:"face_count"`
}

// --- Person CRUD ---

// CreatePerson adds a new person to the store.
func (s *BranchStore) CreatePerson(person *Person) error {
	if person.ID == "" {
		person.ID = uuid.New().String()
	}
	now := time.Now()
	person.CreatedAt = now
	person.UpdatedAt = now

	data, err := json.Marshal(person)
	if err != nil {
		return fmt.Errorf("marshal person: %w", err)
	}

	return s.db.Update(func(txn *badger.Txn) error {
		key := []byte(prefixPerson + person.ID)

		// Check duplicate name.
		nameKey := []byte(prefixPersonIdx + strings.ToLower(person.Name))
		if _, err := txn.Get(nameKey); err == nil {
			return fmt.Errorf("person with name %q already exists", person.Name)
		}

		if err := txn.Set(key, data); err != nil {
			return err
		}
		return txn.Set(nameKey, []byte(person.ID))
	})
}

// GetPerson retrieves a person by ID.
func (s *BranchStore) GetPerson(id string) (*Person, error) {
	var person Person
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(prefixPerson + id))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &person)
		})
	})
	if err == badger.ErrKeyNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &person, nil
}

// GetPersonByName finds a person by their name (case-insensitive).
func (s *BranchStore) GetPersonByName(name string) (*Person, error) {
	var personID string
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(prefixPersonIdx + strings.ToLower(name)))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			personID = string(val)
			return nil
		})
	})
	if err == badger.ErrKeyNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.GetPerson(personID)
}

// ListPersons returns all persons.
func (s *BranchStore) ListPersons() ([]*Person, error) {
	var persons []*Person
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(prefixPerson)
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek([]byte(prefixPerson)); it.Valid(); it.Next() {
			key := string(it.Item().Key())
			// Only match direct person keys, not exemplar/face/idx keys.
			rest := strings.TrimPrefix(key, prefixPerson)
			if strings.Contains(rest, ":") {
				continue
			}
			var p Person
			err := it.Item().Value(func(val []byte) error {
				return json.Unmarshal(val, &p)
			})
			if err != nil {
				continue
			}
			persons = append(persons, &p)
		}
		return nil
	})
	return persons, err
}

// UpdatePerson updates a person's name and relationships.
func (s *BranchStore) UpdatePerson(person *Person) error {
	person.UpdatedAt = time.Now()
	data, err := json.Marshal(person)
	if err != nil {
		return fmt.Errorf("marshal person: %w", err)
	}

	return s.db.Update(func(txn *badger.Txn) error {
		// Load existing to check name change.
		item, err := txn.Get([]byte(prefixPerson + person.ID))
		if err != nil {
			return fmt.Errorf("person %s not found: %w", person.ID, err)
		}
		var existing Person
		if err := item.Value(func(val []byte) error { return json.Unmarshal(val, &existing) }); err != nil {
			return err
		}

		// Update name index if changed.
		if !strings.EqualFold(existing.Name, person.Name) {
			_ = txn.Delete([]byte(prefixPersonIdx + strings.ToLower(existing.Name)))
			if err := txn.Set([]byte(prefixPersonIdx+strings.ToLower(person.Name)), []byte(person.ID)); err != nil {
				return err
			}
		}

		return txn.Set([]byte(prefixPerson+person.ID), data)
	})
}

// DeletePerson removes a person and cascades to exemplars and face assignments.
func (s *BranchStore) DeletePerson(id string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		// Load person for name index cleanup.
		item, err := txn.Get([]byte(prefixPerson + id))
		if err != nil {
			return err
		}
		var p Person
		if err := item.Value(func(val []byte) error { return json.Unmarshal(val, &p) }); err != nil {
			return err
		}

		// Delete name index.
		_ = txn.Delete([]byte(prefixPersonIdx + strings.ToLower(p.Name)))

		// Delete person record.
		if err := txn.Delete([]byte(prefixPerson + id)); err != nil {
			return err
		}

		// Delete exemplars.
		exemplarPrefix := []byte(prefixExemplar + id + ":")
		if err := deleteByPrefix(txn, exemplarPrefix); err != nil {
			return err
		}

		// Delete face assignments.
		facePrefix := []byte(prefixFace + id + ":")
		return deleteByPrefix(txn, facePrefix)
	})
}

// --- Exemplars ---

// AddExemplar adds a face exemplar for a person.
func (s *BranchStore) AddExemplar(e *Exemplar) error {
	e.CreatedAt = time.Now()
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal exemplar: %w", err)
	}
	key := []byte(prefixExemplar + e.PersonID + ":" + e.Hash)
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, data)
	})
}

// ExemplarsForPerson returns all exemplars for a person.
func (s *BranchStore) ExemplarsForPerson(personID string) ([]*Exemplar, error) {
	var exemplars []*Exemplar
	prefix := []byte(prefixExemplar + personID + ":")
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(prefix); it.Valid(); it.Next() {
			var e Exemplar
			if err := it.Item().Value(func(val []byte) error { return json.Unmarshal(val, &e) }); err != nil {
				continue
			}
			exemplars = append(exemplars, &e)
		}
		return nil
	})
	return exemplars, err
}

// AllExemplars returns all exemplars across all persons.
func (s *BranchStore) AllExemplars() ([]*Exemplar, error) {
	var exemplars []*Exemplar
	prefix := []byte(prefixExemplar)
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(prefix); it.Valid(); it.Next() {
			var e Exemplar
			if err := it.Item().Value(func(val []byte) error { return json.Unmarshal(val, &e) }); err != nil {
				continue
			}
			exemplars = append(exemplars, &e)
		}
		return nil
	})
	return exemplars, err
}

// PurgeAutoExemplars removes all auto-added exemplars for a person.
func (s *BranchStore) PurgeAutoExemplars(personID string) error {
	return s.purgeExemplars(personID, func(e *Exemplar) bool {
		return e.AutoAdded
	})
}

// PurgeAutoExemplarsAfter removes auto-added exemplars for a person added after the given time.
func (s *BranchStore) PurgeAutoExemplarsAfter(personID string, after time.Time) error {
	return s.purgeExemplars(personID, func(e *Exemplar) bool {
		return e.AutoAdded && e.CreatedAt.After(after)
	})
}

// ResetToSeedExemplars removes all auto-added exemplars for all persons.
func (s *BranchStore) ResetToSeedExemplars() error {
	persons, err := s.ListPersons()
	if err != nil {
		return err
	}
	for _, p := range persons {
		if err := s.PurgeAutoExemplars(p.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *BranchStore) purgeExemplars(personID string, shouldDelete func(*Exemplar) bool) error {
	prefix := []byte(prefixExemplar + personID + ":")
	var keysToDelete [][]byte

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(prefix); it.Valid(); it.Next() {
			var e Exemplar
			if err := it.Item().Value(func(val []byte) error { return json.Unmarshal(val, &e) }); err != nil {
				continue
			}
			if shouldDelete(&e) {
				keysToDelete = append(keysToDelete, it.Item().KeyCopy(nil))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	if len(keysToDelete) == 0 {
		return nil
	}

	return s.db.Update(func(txn *badger.Txn) error {
		for _, key := range keysToDelete {
			if err := txn.Delete(key); err != nil {
				return err
			}
		}
		return nil
	})
}

// --- Face Assignments ---

// AssignFaceToPerson creates a face-to-person assignment.
func (s *BranchStore) AssignFaceToPerson(fa *FaceAssignment) error {
	fa.AssignedAt = time.Now()
	data, err := json.Marshal(fa)
	if err != nil {
		return fmt.Errorf("marshal face assignment: %w", err)
	}
	key := fmt.Sprintf("%s%s:%s:%d", prefixFace, fa.PersonID, fa.ImagePath, fa.FaceIndex)
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(key), data)
	})
}

// FacesForPerson returns all face assignments for a person.
func (s *BranchStore) FacesForPerson(personID string) ([]*FaceAssignment, error) {
	var faces []*FaceAssignment
	prefix := []byte(prefixFace + personID + ":")
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(prefix); it.Valid(); it.Next() {
			var fa FaceAssignment
			if err := it.Item().Value(func(val []byte) error { return json.Unmarshal(val, &fa) }); err != nil {
				continue
			}
			faces = append(faces, &fa)
		}
		return nil
	})
	return faces, err
}

// PersonForFace finds which person a face in an image is assigned to.
func (s *BranchStore) PersonForFace(imagePath string, faceIndex int) (*FaceAssignment, error) {
	var result *FaceAssignment
	err := s.db.View(func(txn *badger.Txn) error {
		prefix := []byte(prefixFace)
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(prefix); it.Valid(); it.Next() {
			var fa FaceAssignment
			if err := it.Item().Value(func(val []byte) error { return json.Unmarshal(val, &fa) }); err != nil {
				continue
			}
			if fa.ImagePath == imagePath && fa.FaceIndex == faceIndex {
				result = &fa
				return nil
			}
		}
		return nil
	})
	return result, err
}

// --- Image Metadata ---

// IndexImage stores image metadata and creates date/event indexes.
func (s *BranchStore) IndexImage(meta *ImageMetadata) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal image metadata: %w", err)
	}

	return s.db.Update(func(txn *badger.Txn) error {
		if err := txn.Set([]byte(prefixImgMeta+meta.ImagePath), data); err != nil {
			return err
		}
		// Date index.
		if !meta.DateTaken.IsZero() {
			dateKey := fmt.Sprintf("%s%s:%s", prefixImgDateIdx, meta.DateTaken.Format("20060102"), meta.ImagePath)
			if err := txn.Set([]byte(dateKey), nil); err != nil {
				return err
			}
		}
		// Event index.
		if meta.EventName != "" {
			eventKey := fmt.Sprintf("%s%s:%s", prefixImgEventIdx, meta.EventName, meta.ImagePath)
			if err := txn.Set([]byte(eventKey), nil); err != nil {
				return err
			}
		}
		return nil
	})
}

// GetImageMetadata retrieves metadata for an image.
func (s *BranchStore) GetImageMetadata(imagePath string) (*ImageMetadata, error) {
	var meta ImageMetadata
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(prefixImgMeta + imagePath))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &meta)
		})
	})
	if err == badger.ErrKeyNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &meta, nil
}

// UpdateImageLocation updates GPS coordinates and event name for an image.
func (s *BranchStore) UpdateImageLocation(imagePath string, lat, lon float64, eventName string) error {
	meta, err := s.GetImageMetadata(imagePath)
	if err != nil || meta == nil {
		return fmt.Errorf("image %s not found", imagePath)
	}
	meta.GPSLat = lat
	meta.GPSLon = lon

	return s.db.Update(func(txn *badger.Txn) error {
		// Update event index if changed.
		if meta.EventName != eventName {
			if meta.EventName != "" {
				_ = txn.Delete(fmt.Appendf(nil, "%s%s:%s", prefixImgEventIdx, meta.EventName, imagePath))
			}
			if eventName != "" {
				if err := txn.Set(fmt.Appendf(nil, "%s%s:%s", prefixImgEventIdx, eventName, imagePath), nil); err != nil {
					return err
				}
			}
			meta.EventName = eventName
		}

		data, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		return txn.Set([]byte(prefixImgMeta+imagePath), data)
	})
}

// ImagesInDateRange returns image paths between start and end dates.
func (s *BranchStore) ImagesInDateRange(start, end time.Time) ([]string, error) {
	var paths []string
	startKey := []byte(prefixImgDateIdx + start.Format("20060102"))
	endKey := []byte(prefixImgDateIdx + end.Format("20060102") + "~") // ~ sorts after any path

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = []byte(prefixImgDateIdx)
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(startKey); it.Valid(); it.Next() {
			key := it.Item().Key()
			if string(key) >= string(endKey) {
				break
			}
			// Extract path from key: img:idx:date:<YYYYMMDD>:<path>
			k := string(key)
			parts := strings.SplitN(strings.TrimPrefix(k, prefixImgDateIdx), ":", 2)
			if len(parts) == 2 {
				paths = append(paths, parts[1])
			}
		}
		return nil
	})
	return paths, err
}

// ImagesForEvent returns image paths for a given event name.
func (s *BranchStore) ImagesForEvent(eventName string) ([]string, error) {
	var paths []string
	prefix := []byte(prefixImgEventIdx + eventName + ":")
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(prefix); it.Valid(); it.Next() {
			k := string(it.Item().Key())
			path := strings.TrimPrefix(k, string(prefix))
			if path != "" {
				paths = append(paths, path)
			}
		}
		return nil
	})
	return paths, err
}

// MarkImageScanned records that an image has been scanned for faces.
func (s *BranchStore) MarkImageScanned(imagePath string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(prefixImgScanned+imagePath), []byte(time.Now().Format(time.RFC3339)))
	})
}

// IsImageScanned returns true if the image has been scanned for faces.
func (s *BranchStore) IsImageScanned(imagePath string) bool {
	err := s.db.View(func(txn *badger.Txn) error {
		_, err := txn.Get([]byte(prefixImgScanned + imagePath))
		return err
	})
	return err == nil
}

// UnscannedImages returns image paths that haven't been scanned for faces.
func (s *BranchStore) UnscannedImages() ([]string, error) {
	var paths []string
	prefix := []byte(prefixImgMeta)
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(prefix); it.Valid(); it.Next() {
			imgPath := strings.TrimPrefix(string(it.Item().Key()), prefixImgMeta)
			// Check if scanned.
			if _, err := txn.Get([]byte(prefixImgScanned + imgPath)); err == badger.ErrKeyNotFound {
				paths = append(paths, imgPath)
			}
		}
		return nil
	})
	return paths, err
}

// ImageCount returns the total number of indexed images.
func (s *BranchStore) ImageCount() int {
	count := 0
	prefix := []byte(prefixImgMeta)
	_ = s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(prefix); it.Valid(); it.Next() {
			count++
		}
		return nil
	})
	return count
}

// DateRange returns the oldest and newest image dates.
func (s *BranchStore) DateRange() (oldest, newest time.Time, err error) {
	prefix := []byte(prefixImgDateIdx)
	err = s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		// First entry is oldest.
		it.Seek(prefix)
		if !it.Valid() {
			return nil
		}
		k := strings.TrimPrefix(string(it.Item().Key()), prefixImgDateIdx)
		parts := strings.SplitN(k, ":", 2)
		if len(parts) >= 1 {
			oldest, _ = time.Parse("20060102", parts[0])
		}

		// Scan to last entry for newest.
		for it.Next(); it.Valid(); it.Next() {
			// Keep going to find the last one.
		}
		// After loop, rewind needs re-scan. Use reverse iterator instead.
		return nil
	})
	if err != nil {
		return
	}

	// Separate reverse scan for newest.
	err = s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = prefix
		opts.Reverse = true
		it := txn.NewIterator(opts)
		defer it.Close()

		// Seek to end of prefix range.
		it.Seek(append(prefix, 0xFF))
		if !it.Valid() {
			return nil
		}
		k := strings.TrimPrefix(string(it.Item().Key()), prefixImgDateIdx)
		parts := strings.SplitN(k, ":", 2)
		if len(parts) >= 1 {
			newest, _ = time.Parse("20060102", parts[0])
		}
		return nil
	})
	return
}

// deleteByPrefix deletes all keys with the given prefix within a transaction.
func deleteByPrefix(txn *badger.Txn, prefix []byte) error {
	opts := badger.DefaultIteratorOptions
	opts.PrefetchValues = false
	opts.Prefix = prefix
	it := txn.NewIterator(opts)
	defer it.Close()

	var keys [][]byte
	for it.Seek(prefix); it.Valid(); it.Next() {
		keys = append(keys, it.Item().KeyCopy(nil))
	}

	for _, key := range keys {
		if err := txn.Delete(key); err != nil {
			return err
		}
	}
	return nil
}
