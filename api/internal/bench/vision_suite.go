package bench

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/providers/vision"
)

// Engines of the vision and train suites (manifest entry names).
const (
	EngineScore   = "dinov2-base"
	EngineDepth   = "depth-anything-v2-small"
	EngineTrainer = "z-image-turbo-trainer"
)

// Limits on the reference images the vision and train suites read,
// matching what the worker accepts per image and per score call.
const (
	maxRefImages     = 64
	maxRefImageBytes = 40 << 20
	maxScoreRefs     = 32
)

// RefImage is one reference image read from disk.
type RefImage struct {
	Name string
	Data []byte
}

// refImageExt lists the image types the worker's scorer, depth engine
// and training dataset accept.
var refImageExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".webp": true}

// LoadRefImages reads the PNG, JPEG and WebP files directly inside dir,
// in name order (e.g. the portraits an image-suite run saved with
// --out). Subdirectories and other files are ignored.
func LoadRefImages(dir string) ([]RefImage, error) {
	if dir == "" {
		return nil, errors.New("bench: a reference image directory is required (--refs)")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("bench: read reference images: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && refImageExt[strings.ToLower(filepath.Ext(e.Name()))] {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) > maxRefImages {
		return nil, fmt.Errorf("bench: %s holds %d images, at most %d are used", dir, len(names), maxRefImages)
	}
	refs := make([]RefImage, 0, len(names))
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if info.Size() > maxRefImageBytes {
			return nil, fmt.Errorf("bench: %s is larger than %d MB", path, maxRefImageBytes>>20)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		refs = append(refs, RefImage{Name: name, Data: data})
	}
	return refs, nil
}

// VisionResult is one score or depth case.
type VisionResult struct {
	Name, Engine  string
	Score         float64
	WallSeconds   float64
	VRAMPeakMB    int64
	SwitchSeconds float64
	Switched      bool
	Err           error
	Meta          map[string]any
}

// RunVision scores every reference image against the others with DINOv2
// (leave-one-out, so each score is a same-character consistency
// measure) and extracts a depth map of each with Depth-Anything-V2-Small.
// It needs at least two images.
func (r *VoiceRunner) RunVision(ctx context.Context, refs []RefImage) ([]VisionResult, []Budget, error) {
	if len(refs) < 2 {
		return nil, nil, fmt.Errorf("bench vision: need at least 2 reference images, got %d", len(refs))
	}
	const suite = "vision"
	runID := idconv.NewV7()
	r.putRefs(refs)
	var results []VisionResult
	for i, img := range refs {
		var others []RefImage
		for j, o := range refs {
			if j != i && len(others) < maxScoreRefs {
				others = append(others, o)
			}
		}
		res := r.scoreCase(ctx, "score-"+img.Name, img, others)
		if err := r.recordVision(ctx, runID, suite, res); err != nil {
			return results, nil, err
		}
		results = append(results, res)
	}
	for _, img := range refs {
		res := r.depthCase(ctx, img)
		if err := r.recordVision(ctx, runID, suite, res); err != nil {
			return results, nil, err
		}
		results = append(results, res)
	}
	return results, visionBudgets(results), nil
}

// putRefs stores the reference images in the sink under ref-<name>.
func (r *VoiceRunner) putRefs(refs []RefImage) {
	for _, img := range refs {
		r.Sink.Put(refSinkName(img.Name), img.Data)
	}
}

func refSinkName(name string) string { return "ref-" + name }

// scoreCase scores img against refs, which must already be in the sink.
func (r *VoiceRunner) scoreCase(ctx context.Context, name string, img RefImage, refs []RefImage) VisionResult {
	res := VisionResult{Name: name, Engine: EngineScore}
	res.SwitchSeconds, res.Switched, res.Err = r.ensure(ctx, pyworkerBackend, EngineScore)
	if res.Err != nil {
		return res
	}
	urls := make([]string, len(refs))
	for i, ref := range refs {
		urls[i] = r.Sink.URL(refSinkName(ref.Name))
	}
	start := time.Now()
	score, meta, err := r.Vision.Score(ctx, vision.ScoreRequest{
		Engine: EngineScore, ImageGetURL: r.Sink.URL(refSinkName(img.Name)),
		Params: map[string]string{"reference_urls": strings.Join(urls, "\n")},
	})
	res.WallSeconds = time.Since(start).Seconds()
	if err != nil {
		res.Err = err
		return res
	}
	res.Score = score
	res.VRAMPeakMB, _ = strconv.ParseInt(meta["vram_peak_mb"], 10, 64)
	res.Meta = map[string]any{"score": score, "references": len(refs), "min": meta["min"], "max": meta["max"]}
	return res
}

// depthCase extracts img's depth map, saved as depth-<stem>.png.
func (r *VoiceRunner) depthCase(ctx context.Context, img RefImage) VisionResult {
	out := "depth-" + strings.TrimSuffix(img.Name, filepath.Ext(img.Name)) + ".png"
	res := VisionResult{Name: out, Engine: EngineDepth}
	res.SwitchSeconds, res.Switched, res.Err = r.ensure(ctx, pyworkerBackend, EngineDepth)
	if res.Err != nil {
		return res
	}
	start := time.Now()
	_, err := r.Vision.Depth(ctx, vision.DepthRequest{
		Engine: EngineDepth, ImageGetURL: r.Sink.URL(refSinkName(img.Name)), OutputPutURL: r.Sink.URL(out),
		Params: map[string]string{"output_key": out},
	})
	res.WallSeconds = time.Since(start).Seconds()
	if err != nil {
		res.Err = err
		return res
	}
	data, ok := r.Sink.Get(out)
	if !ok || !isPNG(data) {
		res.Err = fmt.Errorf("bench: %s: the worker reported success but uploaded no PNG depth map", out)
		return res
	}
	res.Meta = map[string]any{"bytes": len(data)}
	r.save(out, data)
	return res
}

func isPNG(data []byte) bool {
	return len(data) > 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n"
}

func (r *VoiceRunner) recordVision(ctx context.Context, runID uuid.UUID, suite string, res VisionResult) error {
	meta := res.Meta
	if meta == nil {
		meta = map[string]any{}
	}
	meta["switched"] = res.Switched
	r.logResult(res.Name, res.Engine, res.Err, "score %.4f in %6.2fs  vram %5d MB  switch %5.1fs",
		res.Score, res.WallSeconds, res.VRAMPeakMB, res.SwitchSeconds)
	return recordRow(ctx, r.Queries, runID, row{
		Suite: suite, Case: res.Name, Model: res.Engine, Seconds: res.WallSeconds, VRAMPeakMB: res.VRAMPeakMB,
		SwitchSeconds: res.SwitchSeconds, Switched: res.Switched, Err: res.Err, Meta: meta,
	})
}

// visionBudgets checks the residency switch budget over the cases that
// loaded a model.
func visionBudgets(results []VisionResult) []Budget {
	switches := Budget{Name: "residency switch (worst)", Limit: BudgetResidencySwitchS, Unit: "s"}
	for _, res := range results {
		if res.Err == nil && res.Switched {
			switches.Measured, switches.Known = max(switches.Measured, res.SwitchSeconds), true
		}
	}
	return []Budget{switches}
}
