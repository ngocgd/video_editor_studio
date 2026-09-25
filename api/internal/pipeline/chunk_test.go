package pipeline

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestChunkSize(t *testing.T) {
	cases := []struct {
		perStep time.Duration
		want    int
	}{
		{0, 1},
		{-time.Second, 1},
		{time.Hour, 1},
		{1 * time.Minute, 10},
		{2 * time.Minute, 5},
		{3 * time.Minute, 3}, // floor(10/3) = 3
	}
	for _, c := range cases {
		if got := ChunkSize(c.perStep); got != c.want {
			t.Errorf("ChunkSize(%v) = %d, want %d", c.perStep, got, c.want)
		}
	}
}

func TestChunkIDsPreservesOrderAndCoversEverything(t *testing.T) {
	ids := make([]uuid.UUID, 7)
	for i := range ids {
		ids[i] = uuid.New()
	}
	chunks := ChunkIDs(ids, 3)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	if len(chunks[0]) != 3 || len(chunks[1]) != 3 || len(chunks[2]) != 1 {
		t.Fatalf("unexpected chunk sizes: %v", []int{len(chunks[0]), len(chunks[1]), len(chunks[2])})
	}
	var flat []uuid.UUID
	for _, c := range chunks {
		flat = append(flat, c...)
	}
	for i, id := range ids {
		if flat[i] != id {
			t.Fatalf("chunking reordered or dropped ids at index %d", i)
		}
	}
}

func TestChunkIDsEmpty(t *testing.T) {
	if chunks := ChunkIDs(nil, 5); chunks != nil {
		t.Fatalf("expected nil for no ids, got %v", chunks)
	}
}

func TestChunkIDsPanicsOnZeroSize(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected ChunkIDs to panic on size 0")
		}
	}()
	ChunkIDs([]uuid.UUID{uuid.New()}, 0)
}
