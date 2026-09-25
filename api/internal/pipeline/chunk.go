package pipeline

import (
	"time"

	"github.com/google/uuid"
)

// BatchChunkTarget is the target wall-clock duration of one batch GPU
// job, so an interactive job never waits behind more than one chunk.
const BatchChunkTarget = 10 * time.Minute

// StepEstimator returns the estimated wall-clock duration of a single
// step of the given kind, used to size batch chunks. Phase 3 has no real
// per-model timing data (that belongs to the phases that implement each
// stage), so DefaultStepEstimate is deliberately conservative: an unknown
// kind chunks one step per job rather than guessing a batch size that
// could starve interactive work.
type StepEstimator func(kind string) time.Duration

// DefaultStepEstimate is used when no estimator is registered for a kind.
func DefaultStepEstimate(string) time.Duration { return BatchChunkTarget }

// ChunkSize returns how many steps of estimated duration perStep fit in
// one BatchChunkTarget window, always at least 1.
func ChunkSize(perStep time.Duration) int {
	if perStep <= 0 {
		return 1
	}
	n := int(BatchChunkTarget / perStep)
	if n < 1 {
		return 1
	}
	return n
}

// ChunkIDs splits ids into groups of at most size, preserving order. It
// panics if size < 1, since a zero-size chunk would silently drop steps.
func ChunkIDs(ids []uuid.UUID, size int) [][]uuid.UUID {
	if size < 1 {
		panic("pipeline: ChunkIDs size must be >= 1")
	}
	if len(ids) == 0 {
		return nil
	}
	chunks := make([][]uuid.UUID, 0, (len(ids)+size-1)/size)
	for start := 0; start < len(ids); start += size {
		end := start + size
		if end > len(ids) {
			end = len(ids)
		}
		chunks = append(chunks, ids[start:end])
	}
	return chunks
}
