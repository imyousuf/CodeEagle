package store

import (
	"testing"
	"time"
)

func TestGetReturnsWhatPutStored(t *testing.T) {
	s := NewStore()
	s.Put("k", "v", time.Unix(0, 0))
	if v, ok := s.Get("k"); !ok || v != "v" {
		t.Fatalf("Get = %q, %v", v, ok)
	}
}
