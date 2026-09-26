// Package diskguard refuses disk-hungry work (renders, model pulls)
// before it is enqueued when free space on the data disk runs low.
package diskguard

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/pipeline"
)

// GB is one gigabyte (10^9 bytes), the unit the thresholds are set in.
const GB = uint64(1_000_000_000)

// Defaults: refuse below 40 GB free, warn below 60 GB.
const (
	DefaultMinFree  = 40 * GB
	DefaultWarnFree = 60 * GB
)

// Level is how close the disk is to the watermark.
type Level string

const (
	LevelOK      Level = "ok"
	LevelWarning Level = "warning"
	LevelBlocked Level = "blocked"
)

// Status is one free-space reading.
type Status struct {
	FreeBytes uint64
	MinFree   uint64
	WarnFree  uint64
	Level     Level
}

// Message is the sentence shown next to a disabled action or a warning
// chip; empty when the disk is fine.
func (s Status) Message() string {
	switch s.Level {
	case LevelBlocked:
		return fmt.Sprintf("Only %s free on the data disk; renders and model downloads need at least %s. Clean up the Library first.", formatGB(s.FreeBytes), formatGB(s.MinFree))
	case LevelWarning:
		return fmt.Sprintf("Only %s free on the data disk; renders stop below %s.", formatGB(s.FreeBytes), formatGB(s.MinFree))
	}
	return ""
}

// Watermark measures free space on the filesystem holding Path, which
// should be a mount on the Docker data disk (for example the object
// store's volume) so image layers and VHD growth are counted too.
type Watermark struct {
	Path     string
	MinFree  uint64
	WarnFree uint64
	// FreeBytes overrides the filesystem reading; tests use it to fake a
	// full disk. nil reads the filesystem.
	FreeBytes func(path string) (uint64, error)
}

// New returns a Watermark on path with the given thresholds in GB; a
// zero threshold takes its default.
func New(path string, minFreeGB, warnFreeGB uint64) *Watermark {
	w := &Watermark{Path: path, MinFree: minFreeGB * GB, WarnFree: warnFreeGB * GB}
	if w.MinFree == 0 {
		w.MinFree = DefaultMinFree
	}
	if w.WarnFree == 0 {
		w.WarnFree = DefaultWarnFree
	}
	if w.WarnFree < w.MinFree {
		w.WarnFree = w.MinFree
	}
	return w
}

// Status reads the disk now.
func (w *Watermark) Status(context.Context) (Status, error) {
	read := w.FreeBytes
	if read == nil {
		read = freeBytes
	}
	free, err := read(w.Path)
	if err != nil {
		return Status{}, fmt.Errorf("diskguard: measure free space on %s: %w", w.Path, err)
	}
	s := Status{FreeBytes: free, MinFree: w.MinFree, WarnFree: w.WarnFree, Level: LevelOK}
	switch {
	case free < w.MinFree:
		s.Level = LevelBlocked
	case free < w.WarnFree:
		s.Level = LevelWarning
	}
	return s, nil
}

// Guarded reports whether an enqueue of these kinds needs disk headroom:
// any render step, or a model pull.
func Guarded(k pipeline.EnqueueKinds) bool {
	if k.RunKind == "render" {
		return true
	}
	for _, kind := range k.StepKinds {
		if strings.HasPrefix(kind, "render.") || kind == "models.pull" {
			return true
		}
	}
	return false
}

// Check is the pipeline.AdmissionCheck: it refuses a guarded enqueue
// with pipeline.ErrAdmissionDenied (a 507) when the disk is under the
// watermark, or when free space cannot be read at all (failing closed
// keeps a misconfigured path from filling the disk unnoticed). Other
// enqueues pass without a reading.
func (w *Watermark) Check(ctx context.Context, _ *dbgen.Queries, _ uuid.UUID, _ int) error {
	kinds, ok := pipeline.EnqueueKindsFrom(ctx)
	if !ok || !Guarded(kinds) {
		return nil
	}
	s, err := w.Status(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", pipeline.ErrAdmissionDenied, err)
	}
	if s.Level == LevelBlocked {
		return fmt.Errorf("%w: %s", pipeline.ErrAdmissionDenied, s.Message())
	}
	return nil
}

func formatGB(b uint64) string {
	return fmt.Sprintf("%.1f GB", float64(b)/float64(GB))
}
