package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
)

// Step kinds registered by this package.
const (
	KindPull   = "models.pull"
	KindLoad   = "models.load"
	KindUnload = "models.unload"
)

// ErrNotInstalled is a load of a model whose files are not verified on
// disk; it fails the step permanently.
var ErrNotInstalled = fmt.Errorf("models: %w", pipeline.ErrEngineNotInstalled)

// errWorkerOnly is returned by a Run on a handler instance built for
// enqueueing only (the API process registers handlers so Enqueue can
// resolve queue and model; only the worker runs them).
var errWorkerOnly = errors.New("models: this step only runs in the worker")

// progressWriteInterval throttles install progress writes to the
// model_installs row the Model manager polls.
const progressWriteInterval = time.Second

// PullStep downloads a model on the io queue at training/benchmark
// priority, so it never competes with interactive work.
type PullStep struct {
	Manifest *Manifest
	Store    *Store
	// Downloader is nil in the API process.
	Downloader *Downloader
}

var _ pipeline.StepHandler = (*PullStep)(nil)

func (p *PullStep) Kind() string { return KindPull }

func (p *PullStep) Queue(context.Context, pipeline.StepRef) (string, error) {
	return pipeline.QueueIO, nil
}

// InputHash covers the entry's pinned files, so a manifest change makes
// a finished pull stale.
func (p *PullStep) InputHash(_ context.Context, s pipeline.StepRef) (string, error) {
	name, ok := p.Manifest.ScopeName(s.ScopeID)
	if !ok {
		return "", fmt.Errorf("%w: step scope is not a manifest model", pipeline.ErrValidation)
	}
	e, _ := p.Manifest.Get(name)
	h := sha256.New()
	h.Write([]byte(e.Name))
	for _, f := range e.Files {
		h.Write([]byte(f.Path + ":" + f.SHA256))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (p *PullStep) ModelRef(context.Context, pipeline.StepRef) (*pipeline.ModelRef, error) {
	return nil, nil
}

// Run installs the model and keeps model_installs in step with it. A
// cancelled context means the owner paused the download: the row is
// already "paused" and the partial files stay for the next resume.
func (p *PullStep) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	if p.Downloader == nil {
		return nil, errWorkerOnly
	}
	name, ok := p.Manifest.ScopeName(sc.ScopeID())
	if !ok {
		return nil, fmt.Errorf("%w: step scope is not a manifest model", pipeline.ErrValidation)
	}
	e, _ := p.Manifest.Get(name)

	var mu sync.Mutex
	var lastWrite time.Time
	progress := func(done, total int64) {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(lastWrite) < progressWriteInterval && done < total {
			return
		}
		lastWrite = time.Now()
		pct := 0
		if total > 0 {
			pct = int(done * 100 / total)
		}
		sc.Progress(pct, 0)
		if err := p.Store.Queries.UpdateModelInstallProgress(ctx, dbgen.UpdateModelInstallProgressParams{
			Name: name, BytesDone: done, BytesTotal: total,
		}); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "models: progress write failed", "model", name, "error", err)
		}
	}

	sc.Log(fmt.Sprintf("installing %s (%d files, %d bytes)", name, len(e.Files), e.SizeBytes()))
	err := p.Downloader.Install(ctx, e, progress)
	if ctx.Err() != nil {
		sc.Log("download paused or cancelled; partial files kept for resume")
		return nil, ctx.Err()
	}
	if err != nil {
		sc.Log("install failed: " + err.Error())
		if class, _ := pipeline.Classify(err); class == pipeline.ClassPermanent || sc.Attempt() >= pipeline.MaxTransientAttempts {
			failCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = p.Store.Queries.MarkModelInstallFailed(failCtx, dbgen.MarkModelInstallFailedParams{Name: name, Error: idconv.ToPgText(truncate(err.Error(), 500))})
		}
		return nil, err
	}
	if err := p.Store.MarkInstalled(ctx, e); err != nil {
		return nil, err
	}
	sc.Log("installed and verified " + name)
	return pipeline.Output{"model": name, "bytes": e.SizeBytes()}, nil
}

// LoadStep makes a model resident on the GPU. The GPU executor calls
// residency.Ensure with ModelRef before Run, so by the time Run executes
// the model is loaded and proven resident; Run only confirms it.
type LoadStep struct {
	Manifest *Manifest
	// Residency is nil in the API process.
	Residency pipeline.ModelResidency
}

var _ pipeline.StepHandler = (*LoadStep)(nil)

func (l *LoadStep) Kind() string { return KindLoad }

func (l *LoadStep) Queue(context.Context, pipeline.StepRef) (string, error) {
	return pipeline.QueueGPU, nil
}

func (l *LoadStep) InputHash(context.Context, pipeline.StepRef) (string, error) {
	return "", nil
}

func (l *LoadStep) ModelRef(_ context.Context, s pipeline.StepRef) (*pipeline.ModelRef, error) {
	name, ok := l.Manifest.ScopeName(s.ScopeID)
	if !ok {
		return nil, fmt.Errorf("%w: step scope is not a manifest model", pipeline.ErrValidation)
	}
	e, _ := l.Manifest.Get(name)
	return &pipeline.ModelRef{Backend: e.Engine, Model: e.Name}, nil
}

func (l *LoadStep) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	if l.Residency == nil {
		return nil, errWorkerOnly
	}
	ref, err := l.ModelRef(ctx, pipeline.StepRef{ScopeID: sc.ScopeID()})
	if err != nil {
		return nil, err
	}
	current := l.Residency.Current()
	if current == nil || *current != *ref {
		return nil, fmt.Errorf("models: %s:%s is not resident after load", ref.Backend, ref.Model)
	}
	sc.Log("resident: " + ref.Backend + ":" + ref.Model)
	return pipeline.Output{"resident": ref.Backend + ":" + ref.Model}, nil
}

// UnloadStep releases every resident GPU model. It runs on the gpu
// queue so it holds the GPU slot and never races a running job.
type UnloadStep struct {
	Residency pipeline.ModelResidency
}

var _ pipeline.StepHandler = (*UnloadStep)(nil)

func (u *UnloadStep) Kind() string { return KindUnload }

func (u *UnloadStep) Queue(context.Context, pipeline.StepRef) (string, error) {
	return pipeline.QueueGPU, nil
}

func (u *UnloadStep) InputHash(context.Context, pipeline.StepRef) (string, error) {
	return "", nil
}

func (u *UnloadStep) ModelRef(context.Context, pipeline.StepRef) (*pipeline.ModelRef, error) {
	return nil, nil
}

func (u *UnloadStep) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	if u.Residency == nil {
		return nil, errWorkerOnly
	}
	if err := u.Residency.UnloadAll(ctx); err != nil {
		return nil, err
	}
	sc.Log("unloaded every GPU model")
	return pipeline.Output{"resident": ""}, nil
}

// UnloadScopeID is the scope id unload steps run under: unloading is
// about the GPU, not one model.
var UnloadScopeID = ScopeID("gpu")

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// RegisterSteps adds every models.* handler to r. downloader and
// residency are nil in the API process, which only enqueues.
func RegisterSteps(r *pipeline.Registry, m *Manifest, store *Store, downloader *Downloader, residency pipeline.ModelResidency) {
	r.Register(&PullStep{Manifest: m, Store: store, Downloader: downloader})
	r.Register(&LoadStep{Manifest: m, Residency: residency})
	r.Register(&UnloadStep{Residency: residency})
}
