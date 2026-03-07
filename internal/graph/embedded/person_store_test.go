package embedded

import (
	"testing"
	"time"
)

func TestCreateGetPerson(t *testing.T) {
	s := newTestStore(t)
	p := &Person{Name: "Alice", Relationships: []string{"child"}}
	if err := s.CreatePerson(p); err != nil {
		t.Fatalf("CreatePerson: %v", err)
	}
	if p.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if p.CreatedAt.IsZero() {
		t.Fatal("expected non-zero CreatedAt")
	}

	got, err := s.GetPerson(p.ID)
	if err != nil {
		t.Fatalf("GetPerson: %v", err)
	}
	if got.Name != "Alice" {
		t.Errorf("Name = %q, want Alice", got.Name)
	}
	if len(got.Relationships) != 1 || got.Relationships[0] != "child" {
		t.Errorf("Relationships = %v", got.Relationships)
	}
}

func TestGetPersonNotFound(t *testing.T) {
	s := newTestStore(t)
	p, err := s.GetPerson("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != nil {
		t.Error("expected nil for missing person")
	}
}

func TestGetPersonByName(t *testing.T) {
	s := newTestStore(t)
	p := &Person{Name: "Bob"}
	if err := s.CreatePerson(p); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetPersonByName("bob") // case-insensitive
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != p.ID {
		t.Errorf("GetPersonByName: got %v, want %v", got, p.ID)
	}
}

func TestCreatePersonDuplicateName(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreatePerson(&Person{Name: "Alice"}); err != nil {
		t.Fatal(err)
	}
	err := s.CreatePerson(&Person{Name: "alice"})
	if err == nil {
		t.Error("expected error for duplicate name")
	}
}

func TestListPersons(t *testing.T) {
	s := newTestStore(t)
	for _, name := range []string{"Alice", "Bob", "Charlie"} {
		if err := s.CreatePerson(&Person{Name: name}); err != nil {
			t.Fatal(err)
		}
	}

	persons, err := s.ListPersons()
	if err != nil {
		t.Fatal(err)
	}
	if len(persons) != 3 {
		t.Errorf("ListPersons: got %d, want 3", len(persons))
	}
}

func TestUpdatePerson(t *testing.T) {
	s := newTestStore(t)
	p := &Person{Name: "Alice"}
	if err := s.CreatePerson(p); err != nil {
		t.Fatal(err)
	}

	p.Name = "Alicia"
	p.Relationships = []string{"friend"}
	if err := s.UpdatePerson(p); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetPerson(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Alicia" {
		t.Errorf("Name = %q, want Alicia", got.Name)
	}

	// Old name should no longer resolve.
	old, err := s.GetPersonByName("Alice")
	if err != nil {
		t.Fatal(err)
	}
	if old != nil {
		t.Error("old name should not resolve after rename")
	}

	// New name should resolve.
	byNew, err := s.GetPersonByName("Alicia")
	if err != nil {
		t.Fatal(err)
	}
	if byNew == nil || byNew.ID != p.ID {
		t.Error("new name should resolve")
	}
}

func TestDeletePersonCascade(t *testing.T) {
	s := newTestStore(t)
	p := &Person{Name: "Alice"}
	if err := s.CreatePerson(p); err != nil {
		t.Fatal(err)
	}

	// Add exemplar.
	if err := s.AddExemplar(&Exemplar{PersonID: p.ID, Hash: "abc", Embedding: []float32{1, 2, 3}}); err != nil {
		t.Fatal(err)
	}
	// Add face assignment.
	if err := s.AssignFaceToPerson(&FaceAssignment{PersonID: p.ID, ImagePath: "/img.jpg", FaceIndex: 0, Confidence: 0.9}); err != nil {
		t.Fatal(err)
	}

	if err := s.DeletePerson(p.ID); err != nil {
		t.Fatal(err)
	}

	// Person gone.
	got, _ := s.GetPerson(p.ID)
	if got != nil {
		t.Error("person should be deleted")
	}

	// Exemplars gone.
	exs, _ := s.ExemplarsForPerson(p.ID)
	if len(exs) != 0 {
		t.Errorf("exemplars should be deleted, got %d", len(exs))
	}

	// Face assignments gone.
	faces, _ := s.FacesForPerson(p.ID)
	if len(faces) != 0 {
		t.Errorf("face assignments should be deleted, got %d", len(faces))
	}

	// Name index gone.
	byName, _ := s.GetPersonByName("Alice")
	if byName != nil {
		t.Error("name index should be deleted")
	}
}

func TestAddExemplar(t *testing.T) {
	s := newTestStore(t)
	p := &Person{Name: "Alice"}
	if err := s.CreatePerson(p); err != nil {
		t.Fatal(err)
	}

	e := &Exemplar{PersonID: p.ID, Hash: "hash1", Embedding: []float32{0.1, 0.2}, ImagePath: "/photo.jpg"}
	if err := s.AddExemplar(e); err != nil {
		t.Fatal(err)
	}

	exs, err := s.ExemplarsForPerson(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(exs) != 1 {
		t.Fatalf("ExemplarsForPerson: got %d, want 1", len(exs))
	}
	if exs[0].Hash != "hash1" {
		t.Errorf("Hash = %q, want hash1", exs[0].Hash)
	}
}

func TestAddExemplarDedup(t *testing.T) {
	s := newTestStore(t)
	p := &Person{Name: "Alice"}
	if err := s.CreatePerson(p); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if err := s.AddExemplar(&Exemplar{PersonID: p.ID, Hash: "same", Embedding: []float32{1}}); err != nil {
			t.Fatal(err)
		}
	}

	exs, _ := s.ExemplarsForPerson(p.ID)
	if len(exs) != 1 {
		t.Errorf("expected dedup to 1, got %d", len(exs))
	}
}

func TestAllExemplars(t *testing.T) {
	s := newTestStore(t)
	for _, name := range []string{"Alice", "Bob"} {
		p := &Person{Name: name}
		if err := s.CreatePerson(p); err != nil {
			t.Fatal(err)
		}
		if err := s.AddExemplar(&Exemplar{PersonID: p.ID, Hash: name + "_h", Embedding: []float32{1}}); err != nil {
			t.Fatal(err)
		}
	}

	all, err := s.AllExemplars()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Errorf("AllExemplars: got %d, want 2", len(all))
	}
}

func TestPurgeAutoExemplars(t *testing.T) {
	s := newTestStore(t)
	p := &Person{Name: "Alice"}
	if err := s.CreatePerson(p); err != nil {
		t.Fatal(err)
	}

	// Seed (manual) exemplar.
	if err := s.AddExemplar(&Exemplar{PersonID: p.ID, Hash: "seed", Embedding: []float32{1}, AutoAdded: false}); err != nil {
		t.Fatal(err)
	}
	// Auto-added exemplar.
	if err := s.AddExemplar(&Exemplar{PersonID: p.ID, Hash: "auto1", Embedding: []float32{2}, AutoAdded: true}); err != nil {
		t.Fatal(err)
	}

	if err := s.PurgeAutoExemplars(p.ID); err != nil {
		t.Fatal(err)
	}

	exs, _ := s.ExemplarsForPerson(p.ID)
	if len(exs) != 1 {
		t.Fatalf("expected 1 seed exemplar, got %d", len(exs))
	}
	if exs[0].Hash != "seed" {
		t.Errorf("remaining exemplar hash = %q, want seed", exs[0].Hash)
	}
}

func TestResetToSeedExemplars(t *testing.T) {
	s := newTestStore(t)
	for _, name := range []string{"Alice", "Bob"} {
		p := &Person{Name: name}
		if err := s.CreatePerson(p); err != nil {
			t.Fatal(err)
		}
		if err := s.AddExemplar(&Exemplar{PersonID: p.ID, Hash: name + "_seed", AutoAdded: false, Embedding: []float32{1}}); err != nil {
			t.Fatal(err)
		}
		if err := s.AddExemplar(&Exemplar{PersonID: p.ID, Hash: name + "_auto", AutoAdded: true, Embedding: []float32{2}}); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.ResetToSeedExemplars(); err != nil {
		t.Fatal(err)
	}

	all, _ := s.AllExemplars()
	if len(all) != 2 {
		t.Errorf("expected 2 seed exemplars after reset, got %d", len(all))
	}
	for _, e := range all {
		if e.AutoAdded {
			t.Error("auto-added exemplar should have been purged")
		}
	}
}

func TestAssignFaceToPerson(t *testing.T) {
	s := newTestStore(t)
	p := &Person{Name: "Alice"}
	if err := s.CreatePerson(p); err != nil {
		t.Fatal(err)
	}

	fa := &FaceAssignment{PersonID: p.ID, ImagePath: "/photo.jpg", FaceIndex: 0, Confidence: 0.85}
	if err := s.AssignFaceToPerson(fa); err != nil {
		t.Fatal(err)
	}

	faces, err := s.FacesForPerson(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(faces) != 1 {
		t.Fatalf("FacesForPerson: got %d, want 1", len(faces))
	}
	if faces[0].Confidence != 0.85 {
		t.Errorf("Confidence = %f, want 0.85", faces[0].Confidence)
	}
}

func TestPersonForFace(t *testing.T) {
	s := newTestStore(t)
	p := &Person{Name: "Alice"}
	if err := s.CreatePerson(p); err != nil {
		t.Fatal(err)
	}
	if err := s.AssignFaceToPerson(&FaceAssignment{PersonID: p.ID, ImagePath: "/img.jpg", FaceIndex: 1, Confidence: 0.9}); err != nil {
		t.Fatal(err)
	}

	fa, err := s.PersonForFace("/img.jpg", 1)
	if err != nil {
		t.Fatal(err)
	}
	if fa == nil || fa.PersonID != p.ID {
		t.Errorf("PersonForFace: got %v, want person %s", fa, p.ID)
	}

	// Non-existent face.
	fa2, err := s.PersonForFace("/img.jpg", 99)
	if err != nil {
		t.Fatal(err)
	}
	if fa2 != nil {
		t.Error("expected nil for non-existent face")
	}
}

func TestIndexImageAndGetMetadata(t *testing.T) {
	s := newTestStore(t)
	meta := &ImageMetadata{
		ImagePath:  "/photos/vacation.jpg",
		DateTaken:  time.Date(2024, 7, 15, 0, 0, 0, 0, time.UTC),
		DateSource: "exif",
		FolderName: "vacation_20240715",
		EventName:  "summer-vacation",
		FaceCount:  3,
	}
	if err := s.IndexImage(meta); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetImageMetadata("/photos/vacation.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected non-nil metadata")
	}
	if got.FaceCount != 3 {
		t.Errorf("FaceCount = %d, want 3", got.FaceCount)
	}
	if got.EventName != "summer-vacation" {
		t.Errorf("EventName = %q, want summer-vacation", got.EventName)
	}
}

func TestGetImageMetadataNotFound(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetImageMetadata("/nonexistent.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil for missing image")
	}
}

func TestUpdateImageLocation(t *testing.T) {
	s := newTestStore(t)
	meta := &ImageMetadata{ImagePath: "/photo.jpg", DateTaken: time.Now(), DateSource: "exif", EventName: "trip-a"}
	if err := s.IndexImage(meta); err != nil {
		t.Fatal(err)
	}

	if err := s.UpdateImageLocation("/photo.jpg", 40.7128, -74.0060, "trip-b"); err != nil {
		t.Fatal(err)
	}

	got, _ := s.GetImageMetadata("/photo.jpg")
	if got.GPSLat != 40.7128 || got.GPSLon != -74.0060 {
		t.Errorf("GPS = (%f, %f), want (40.7128, -74.0060)", got.GPSLat, got.GPSLon)
	}
	if got.EventName != "trip-b" {
		t.Errorf("EventName = %q, want trip-b", got.EventName)
	}

	// Old event should not list the image.
	oldPaths, _ := s.ImagesForEvent("trip-a")
	if len(oldPaths) != 0 {
		t.Error("old event should not contain image after update")
	}

	// New event should list the image.
	newPaths, _ := s.ImagesForEvent("trip-b")
	if len(newPaths) != 1 {
		t.Errorf("new event should contain 1 image, got %d", len(newPaths))
	}
}

func TestImagesInDateRange(t *testing.T) {
	s := newTestStore(t)
	dates := []time.Time{
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
	}
	for i, d := range dates {
		meta := &ImageMetadata{
			ImagePath:  "/img" + string(rune('A'+i)) + ".jpg",
			DateTaken:  d,
			DateSource: "exif",
		}
		if err := s.IndexImage(meta); err != nil {
			t.Fatal(err)
		}
	}

	paths, err := s.ImagesInDateRange(
		time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Errorf("ImagesInDateRange: got %d, want 1", len(paths))
	}
}

func TestImagesForEvent(t *testing.T) {
	s := newTestStore(t)
	for _, path := range []string{"/a.jpg", "/b.jpg"} {
		meta := &ImageMetadata{
			ImagePath:  path,
			DateTaken:  time.Now(),
			DateSource: "folder",
			EventName:  "birthday",
		}
		if err := s.IndexImage(meta); err != nil {
			t.Fatal(err)
		}
	}
	// Different event.
	if err := s.IndexImage(&ImageMetadata{ImagePath: "/c.jpg", DateTaken: time.Now(), DateSource: "mtime", EventName: "wedding"}); err != nil {
		t.Fatal(err)
	}

	paths, err := s.ImagesForEvent("birthday")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Errorf("ImagesForEvent(birthday): got %d, want 2", len(paths))
	}
}

func TestMarkImageScanned(t *testing.T) {
	s := newTestStore(t)
	meta := &ImageMetadata{ImagePath: "/photo.jpg", DateTaken: time.Now(), DateSource: "mtime"}
	if err := s.IndexImage(meta); err != nil {
		t.Fatal(err)
	}

	if s.IsImageScanned("/photo.jpg") {
		t.Error("should not be scanned initially")
	}

	if err := s.MarkImageScanned("/photo.jpg"); err != nil {
		t.Fatal(err)
	}

	if !s.IsImageScanned("/photo.jpg") {
		t.Error("should be scanned after marking")
	}
}

func TestUnscannedImages(t *testing.T) {
	s := newTestStore(t)
	for _, path := range []string{"/a.jpg", "/b.jpg", "/c.jpg"} {
		if err := s.IndexImage(&ImageMetadata{ImagePath: path, DateTaken: time.Now(), DateSource: "mtime"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MarkImageScanned("/b.jpg"); err != nil {
		t.Fatal(err)
	}

	unscanned, err := s.UnscannedImages()
	if err != nil {
		t.Fatal(err)
	}
	if len(unscanned) != 2 {
		t.Errorf("UnscannedImages: got %d, want 2", len(unscanned))
	}
}

func TestImageCount(t *testing.T) {
	s := newTestStore(t)
	for _, path := range []string{"/a.jpg", "/b.jpg"} {
		if err := s.IndexImage(&ImageMetadata{ImagePath: path, DateTaken: time.Now(), DateSource: "mtime"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.ImageCount(); got != 2 {
		t.Errorf("ImageCount = %d, want 2", got)
	}
}

func TestDateRange(t *testing.T) {
	s := newTestStore(t)
	dates := []time.Time{
		time.Date(2023, 3, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 7, 20, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	for i, d := range dates {
		path := "/img" + string(rune('A'+i)) + ".jpg"
		if err := s.IndexImage(&ImageMetadata{ImagePath: path, DateTaken: d, DateSource: "exif"}); err != nil {
			t.Fatal(err)
		}
	}

	oldest, newest, err := s.DateRange()
	if err != nil {
		t.Fatal(err)
	}
	if !oldest.Equal(time.Date(2023, 3, 15, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("oldest = %v, want 2023-03-15", oldest)
	}
	if !newest.Equal(time.Date(2024, 7, 20, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("newest = %v, want 2024-07-20", newest)
	}
}

func TestDateRangeEmpty(t *testing.T) {
	s := newTestStore(t)
	oldest, newest, err := s.DateRange()
	if err != nil {
		t.Fatal(err)
	}
	if !oldest.IsZero() || !newest.IsZero() {
		t.Errorf("expected zero dates for empty store, got oldest=%v newest=%v", oldest, newest)
	}
}
