package media

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/media/ffmpeg"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/storage"
)

// PeaksPerSecond is the waveform resolution: one min/max pair per 10ms.
const PeaksPerSecond = 100

// decodeRate is the mono sample rate audio is decoded at for peaks.
const decodeRate = 8000

// maxPeaksAudioBytes bounds the decoded PCM a peaks step reads into
// memory: three hours of 8kHz mono s16le is about 173MB.
const maxPeaksAudioBytes = 200 << 20

// Peaks is the stored peaks document. Min/Max are scaled to -127..127.
type Peaks struct {
	PeaksPerSecond int    `json:"peaksPerSecond"`
	DurationMs     int64  `json:"durationMs"`
	Min            []int8 `json:"min"`
	Max            []int8 `json:"max"`
}

// ComputePeaks folds signed 16-bit mono samples at sampleRate into
// PeaksPerSecond min/max pairs.
func ComputePeaks(samples []int16, sampleRate int) Peaks {
	per := sampleRate / PeaksPerSecond
	if per < 1 {
		per = 1
	}
	n := (len(samples) + per - 1) / per
	p := Peaks{PeaksPerSecond: PeaksPerSecond, DurationMs: int64(len(samples)) * 1000 / int64(sampleRate), Min: make([]int8, n), Max: make([]int8, n)}
	for i := 0; i < n; i++ {
		lo, hi := int16(math.MaxInt16), int16(math.MinInt16)
		end := min((i+1)*per, len(samples))
		for _, s := range samples[i*per : end] {
			lo = min(lo, s)
			hi = max(hi, s)
		}
		p.Min[i] = scale(lo)
		p.Max[i] = scale(hi)
	}
	return p
}

func scale(s int16) int8 {
	return int8(int32(s) * 127 / 32768)
}

// Window slices p to [startMs, endMs); endMs <= 0 means to the end.
func (p Peaks) Window(startMs, endMs int64) (int64, Peaks) {
	per := int64(1000 / p.PeaksPerSecond)
	from := min(max(startMs/per, 0), int64(len(p.Min)))
	to := int64(len(p.Min))
	if endMs > 0 {
		to = min(max((endMs+per-1)/per, from), to)
	}
	return from * per, Peaks{PeaksPerSecond: p.PeaksPerSecond, DurationMs: (to - from) * per, Min: p.Min[from:to], Max: p.Max[from:to]}
}

// PeaksHandler is the media.peaks step.
type PeaksHandler struct{ Deps }

var _ pipeline.StepHandler = (*PeaksHandler)(nil)

func (h *PeaksHandler) Kind() string { return KindPeaks }

func (h *PeaksHandler) Queue(context.Context, pipeline.StepRef) (string, error) {
	return pipeline.QueueCPU, nil
}

func (h *PeaksHandler) InputHash(ctx context.Context, s pipeline.StepRef) (string, error) {
	return assetHash(ctx, h.Queries, s)
}

func (h *PeaksHandler) ModelRef(context.Context, pipeline.StepRef) (*pipeline.ModelRef, error) {
	return nil, nil
}

func (h *PeaksHandler) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	if h.Runner == nil || h.Storage == nil {
		return nil, fmt.Errorf("%w: ffmpeg is not configured in this process", pipeline.ErrEngineNotInstalled)
	}
	asset, err := h.Queries.GetAssetByID(ctx, dbgen.GetAssetByIDParams{TenantID: idconv.ToPg(sc.Tenant()), ID: idconv.ToPg(sc.ScopeID())})
	if err != nil {
		return nil, fmt.Errorf("%w: asset: %v", pipeline.ErrValidation, err)
	}
	format, ok := ffmpeg.FormatForMIME(asset.Mime)
	if asset.Kind != "audio" || asset.Status != "ready" || !ok {
		return nil, fmt.Errorf("%w: asset is not ready WAV, FLAC or MP3 audio", pipeline.ErrValidation)
	}

	dir, err := os.MkdirTemp("", "peaks-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	source := filepath.Join(dir, "source"+extensionFor(asset.Mime))
	if err := h.Storage.DownloadTo(ctx, asset.StorageKey, asset.StorageVersionID.String, source); err != nil {
		return nil, fmt.Errorf("media: download source: %w", err)
	}
	pcm := filepath.Join(dir, "decoded.s16le")
	totalMs := int64(0)
	if asset.DurationMs.Valid {
		totalMs = int64(asset.DurationMs.Int32)
	}
	if err := h.Runner.Run(ctx, ffmpeg.Job{
		TempDir: dir,
		Inputs:  []ffmpeg.Input{{Format: format, Path: source}},
		Output:  ffmpeg.Output{Path: pcm, Muxer: "s16le", AudioCodec: "pcm_s16le", Channels: 1, SampleRate: decodeRate, NoVideo: true},
		TotalMs: totalMs, OnProgress: func(p int) { sc.Progress(p*9/10, 0) },
	}); err != nil {
		return nil, err
	}
	info, err := os.Stat(pcm)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxPeaksAudioBytes {
		return nil, fmt.Errorf("%w: audio longer than the peaks limit", pipeline.ErrValidation)
	}
	raw, err := os.ReadFile(pcm) //nolint:gosec // inside the step's own temp dir
	if err != nil {
		return nil, err
	}
	samples := make([]int16, len(raw)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
	}
	peaks := ComputePeaks(samples, decodeRate)
	body, err := json.Marshal(peaks)
	if err != nil {
		return nil, err
	}
	key := storage.DerivedKey(sc.Tenant().String(), idconv.FromPg(asset.ID), "peaks.json")
	if _, err := h.Storage.PutBytes(ctx, key, body, "application/json"); err != nil {
		return nil, err
	}
	patch, _ := json.Marshal(map[string]string{"peaks": key})
	if _, err := h.Queries.SetAssetVariants(ctx, dbgen.SetAssetVariantsParams{Variants: patch, TenantID: asset.TenantID, ID: asset.ID}); err != nil {
		return nil, err
	}
	if err := h.Queries.SetAssetDimensions(ctx, dbgen.SetAssetDimensionsParams{
		Width: asset.Width, Height: asset.Height, DurationMs: idconv.ToPgInt4(int32(peaks.DurationMs)),
		TenantID: asset.TenantID, ID: asset.ID,
	}); err != nil {
		return nil, err
	}
	return pipeline.Output{"peaks": len(peaks.Min), "durationMs": peaks.DurationMs, "bytes": len(body)}, nil
}

// ReadPeaks loads a stored peaks document.
func ReadPeaks(ctx context.Context, st *storage.Internal, key string) (Peaks, error) {
	body, err := st.ReadAll(ctx, key, "", 64<<20)
	if err != nil {
		return Peaks{}, err
	}
	var p Peaks
	if err := json.Unmarshal(body, &p); err != nil {
		return Peaks{}, err
	}
	if p.PeaksPerSecond <= 0 {
		p.PeaksPerSecond = PeaksPerSecond
	}
	return p, nil
}
