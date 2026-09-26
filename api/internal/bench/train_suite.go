package bench

import (
	"context"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"

	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/providers/train"
)

// LoRA training budgets and sizes. DefaultTrainSteps mirrors the
// worker's default so a short run can be extrapolated to a full one.
const (
	BudgetLoRAMinutes = 60.0
	DefaultTrainSteps = 1500
	smokeTrainSteps   = 50
	minTrainImages    = 4
	trainBaseModel    = "z-image-turbo"
	trainTriggerWord  = "loomtale_character"
	loraOutputName    = "lora.safetensors"
	datasetName       = "dataset.zip"
)

// TrainResult is one LoRA training run.
type TrainResult struct {
	Name          string
	Images, Steps int
	WallSeconds   float64
	// TrainSeconds is the trainer's own time (the worker's train_s),
	// falling back to WallSeconds when the worker did not report it.
	TrainSeconds  float64
	WeightsBytes  int
	SwitchSeconds float64
	Switched      bool
	Err           error
}

// TrainReport is a train or train-smoke run: the training and, for the
// smoke run, one consistency score after it.
type TrainReport struct {
	Train   TrainResult
	Score   *VisionResult
	Budgets []Budget
}

// RunTrain trains one LoRA with the worker's default step count on all
// reference images (4 to 64) and checks the per-character time budget.
func (r *VoiceRunner) RunTrain(ctx context.Context, refs []RefImage) (TrainReport, error) {
	if len(refs) < minTrainImages {
		return TrainReport{}, fmt.Errorf("bench train: need at least %d reference images, got %d", minTrainImages, len(refs))
	}
	runID := idconv.NewV7()
	res := r.trainCase(ctx, "lora-full", refs, 0)
	if err := r.recordTrain(ctx, runID, "train", res); err != nil {
		return TrainReport{}, err
	}
	return TrainReport{Train: res, Budgets: []Budget{loraBudget(res, false)}}, nil
}

// RunTrainSmoke trains a short LoRA on the first four reference images,
// then scores a held-out image (the fifth, or the first when there are
// only four) against those four, so one run exercises the trainer, the
// trainer-to-scorer residency switch and the scorer. The time budget is
// extrapolated from the short run to the default step count.
func (r *VoiceRunner) RunTrainSmoke(ctx context.Context, refs []RefImage) (TrainReport, error) {
	if len(refs) < minTrainImages {
		return TrainReport{}, fmt.Errorf("bench train-smoke: need at least %d reference images, got %d", minTrainImages, len(refs))
	}
	const suite = "train-smoke"
	runID := idconv.NewV7()
	set := refs[:minTrainImages]
	res := r.trainCase(ctx, "lora-smoke", set, smokeTrainSteps)
	if err := r.recordTrain(ctx, runID, suite, res); err != nil {
		return TrainReport{}, err
	}
	report := TrainReport{Train: res, Budgets: []Budget{loraBudget(res, true)}}
	if res.Err != nil {
		return report, nil
	}
	heldOut := refs[0]
	if len(refs) > minTrainImages {
		heldOut = refs[minTrainImages]
	}
	r.putRefs(append([]RefImage{heldOut}, set...))
	score := r.scoreCase(ctx, "score-"+heldOut.Name, heldOut, set)
	if err := r.recordVision(ctx, runID, suite, score); err != nil {
		return report, err
	}
	report.Score = &score
	report.Budgets = append(report.Budgets, visionBudgets([]VisionResult{score})...)
	return report, nil
}

// trainCase runs one training job; steps 0 keeps the worker's default.
func (r *VoiceRunner) trainCase(ctx context.Context, name string, refs []RefImage, steps int) TrainResult {
	res := TrainResult{Name: name, Images: len(refs), Steps: steps}
	if steps == 0 {
		res.Steps = DefaultTrainSteps
	}
	dataset, err := TrainDataset(refs, trainTriggerWord)
	if err != nil {
		res.Err = err
		return res
	}
	res.SwitchSeconds, res.Switched, res.Err = r.ensure(ctx, pyworkerBackend, EngineTrainer)
	if res.Err != nil {
		return res
	}
	r.Sink.Put(datasetName, dataset)
	params := map[string]string{"trigger_word": trainTriggerWord, "output_key": loraOutputName}
	if steps > 0 {
		params["steps"] = strconv.Itoa(steps)
	}
	lastDecile := -1
	onProgress := func(p train.Progress) {
		if d := p.Pct / 10; d > lastDecile {
			lastDecile = d
			r.logf("%-20s step %d/%d (%d%%), eta %ds", name, p.Step, p.TotalSteps, p.Pct, p.EtaS)
		}
	}
	start := time.Now()
	out, err := r.Train.Train(ctx, train.Request{
		Engine: EngineTrainer, BaseModel: trainBaseModel, DatasetGetURL: r.Sink.URL(datasetName),
		OutputPutURL: r.Sink.URL(loraOutputName), Params: params,
	}, onProgress, nil)
	res.WallSeconds = time.Since(start).Seconds()
	if err != nil {
		res.Err = err
		return res
	}
	res.TrainSeconds = res.WallSeconds
	if v, err := strconv.ParseFloat(out.Metadata["train_s"], 64); err == nil && v > 0 {
		res.TrainSeconds = v
	}
	data, ok := r.Sink.Get(loraOutputName)
	if !ok || !IsSafetensors(data) {
		res.Err = fmt.Errorf("bench: %s: the worker reported success but uploaded no safetensors LoRA", name)
		return res
	}
	res.WeightsBytes = len(data)
	r.save(name+".safetensors", data)
	return res
}

// TrainDataset zips the reference images with one caption file per
// image holding the trigger word, the dataset layout the worker's
// trainer accepts.
func TrainDataset(refs []RefImage, trigger string) ([]byte, error) {
	images := make([]train.DatasetImage, len(refs))
	for i, img := range refs {
		images[i] = train.DatasetImage{Ext: filepath.Ext(img.Name), Data: img.Data, Caption: trigger}
	}
	return train.DatasetZip(images)
}

// IsSafetensors reports whether data starts with a well-formed
// safetensors header: a little-endian uint64 length followed by that
// many bytes of JSON object inside the file.
func IsSafetensors(data []byte) bool {
	if len(data) < 10 {
		return false
	}
	n := binary.LittleEndian.Uint64(data[:8])
	return n >= 2 && n <= uint64(len(data)-8) && data[8] == '{'
}

func (r *VoiceRunner) recordTrain(ctx context.Context, runID uuid.UUID, suite string, res TrainResult) error {
	meta := map[string]any{
		"images": res.Images, "steps": res.Steps, "train_s": res.TrainSeconds,
		"weights_bytes": res.WeightsBytes, "switched": res.Switched,
	}
	r.logResult(res.Name, EngineTrainer, res.Err, "%d steps on %d images in %6.1f min  weights %d MB  switch %5.1fs",
		res.Steps, res.Images, res.TrainSeconds/60, res.WeightsBytes>>20, res.SwitchSeconds)
	return recordRow(ctx, r.Queries, runID, row{
		Suite: suite, Case: res.Name, Model: EngineTrainer, Seconds: res.WallSeconds,
		SwitchSeconds: res.SwitchSeconds, Switched: res.Switched, Err: res.Err, Meta: meta,
	})
}

// loraBudget checks the per-character training time. A short run is
// scaled linearly to DefaultTrainSteps; that over-estimates slightly,
// since the fixed model-load time is scaled too.
func loraBudget(res TrainResult, extrapolate bool) Budget {
	b := Budget{Name: "LoRA training time", Limit: BudgetLoRAMinutes, Unit: "min"}
	if extrapolate {
		b.Name = fmt.Sprintf("LoRA training time (%d steps, extrapolated)", DefaultTrainSteps)
	}
	if res.Err != nil || res.Steps <= 0 {
		return b
	}
	minutes := res.TrainSeconds / 60
	if extrapolate {
		minutes *= float64(DefaultTrainSteps) / float64(res.Steps)
	}
	b.Measured, b.Known = minutes, true
	return b
}
