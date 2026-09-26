package scenes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/media/ffmpeg"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/tts"
	"loomtale/api/internal/storage"
)

// pyworkerBackend is the residency backend of the Python worker engines;
// the model named in a ModelRef is the engine to load.
const pyworkerBackend = "pyworker"

// maxSegmentAudioBytes bounds one downloaded segment.
const maxSegmentAudioBytes = 200 << 20

// VoiceHandler is voice.synthesize: one TTS call per segment with the
// speaker's voice, concatenated with the series' segment gap.
type VoiceHandler struct{ StepDeps }

var _ pipeline.StepHandler = (*VoiceHandler)(nil)

func (h *VoiceHandler) Kind() string { return KindVoice }

func (h *VoiceHandler) Queue(context.Context, pipeline.StepRef) (string, error) {
	return pipeline.QueueGPU, nil
}

func (h *VoiceHandler) InputHash(ctx context.Context, s pipeline.StepRef) (string, error) {
	sc, err := stepContext(ctx, h.StepDeps, s)
	if err != nil {
		return "", err
	}
	return VoiceComponents(sc.Inputs, sc.sceneInputs()).Hash(), nil
}

// ModelRef is the TTS engine of the scene's first assigned voice.
func (h *VoiceHandler) ModelRef(ctx context.Context, s pipeline.StepRef) (*pipeline.ModelRef, error) {
	sc, err := stepContext(ctx, h.StepDeps, s)
	if err != nil {
		return nil, nil //nolint:nilerr // Run reports the real error
	}
	if engine := sceneEngine(sc.Inputs, sc.sceneInputs().Segments); engine != "" {
		return &pipeline.ModelRef{Backend: pyworkerBackend, Model: engine}, nil
	}
	return nil, nil
}

func sceneEngine(in EpisodeInputs, segs []Segment) string {
	for _, seg := range segs {
		if v := in.VoiceFor(seg.SpeakerCharacterID); v != nil && v.Engine != "" {
			return v.Engine
		}
	}
	return ""
}

// voicePlan is one TTS call.
type voicePlan struct {
	Text  string
	Voice Voice
}

// PlanVoice checks every segment has a voice on the scene's one engine.
func PlanVoice(in EpisodeInputs, segs []Segment, names map[uuid.UUID]string) ([]voicePlan, string, error) {
	if len(segs) == 0 {
		return nil, "", fmt.Errorf("%w: the scene has no narration to voice", pipeline.ErrValidation)
	}
	engine := sceneEngine(in, segs)
	plans := make([]voicePlan, 0, len(segs))
	for _, seg := range segs {
		v := in.VoiceFor(seg.SpeakerCharacterID)
		if v == nil || v.Engine == "" {
			who := "the narrator"
			if seg.SpeakerCharacterID != nil {
				who = names[*seg.SpeakerCharacterID]
			}
			return nil, "", fmt.Errorf("%w: no %s voice assigned for %s", pipeline.ErrValidation, strings.ToUpper(in.Lang), who)
		}
		if v.Engine != engine {
			return nil, "", fmt.Errorf("%w: every voice of one scene must use the same TTS engine (%s and %s)", pipeline.ErrValidation, engine, v.Engine)
		}
		if strings.TrimSpace(seg.Text) != "" {
			plans = append(plans, voicePlan{Text: seg.Text, Voice: *v})
		}
	}
	return plans, engine, nil
}

func (h *VoiceHandler) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	scx, err := loadSceneContext(ctx, h.Service.Queries, sc.Tenant(), sc.ScopeID())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", pipeline.ErrValidation, err)
	}
	in := scx.sceneInputs()
	names := map[uuid.UUID]string{}
	for id, l := range scx.Inputs.Characters {
		names[id] = l.Name
	}
	plans, engine, err := PlanVoice(scx.Inputs, in.Segments, names)
	if err != nil {
		return nil, err
	}
	if h.TTS == nil || h.Storage == nil || h.Runner == nil {
		return nil, fmt.Errorf("%w: the TTS engine is not available on this worker", pipeline.ErrEngineNotInstalled)
	}

	dir, err := os.MkdirTemp("", "voice-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	tenant := sc.Tenant().String()
	var files []string
	var format WavFormat
	for i, plan := range plans {
		key := storage.DerivedKey(tenant, in.ID, fmt.Sprintf("voice-%s-%03d.wav", sc.StepID(), i))
		putURL, err := h.Storage.PresignPut(ctx, key, presignTTL)
		if err != nil {
			return nil, err
		}
		params := plan.Voice.MergedParams()
		params["language"] = scx.Inputs.Lang
		params["output_key"] = key
		if plan.Voice.RefAssetID != nil {
			ref, err := h.Service.Queries.GetAssetByID(ctx, dbgen.GetAssetByIDParams{TenantID: idconv.ToPg(sc.Tenant()), ID: idconv.ToPg(*plan.Voice.RefAssetID)})
			if err != nil {
				return nil, fmt.Errorf("%w: reference voice asset: %v", pipeline.ErrValidation, err)
			}
			refURL, err := h.Storage.PresignGet(ctx, ref.StorageKey, ref.StorageVersionID.String, presignTTL)
			if err != nil {
				return nil, err
			}
			params["reference_url"] = refURL
			if plan.Voice.Consented {
				params["consent"] = "granted"
			}
		}
		voiceName := ""
		if plan.Voice.PresetID != nil {
			voiceName = plan.Voice.PresetID.String()
		}
		base, span := i*90/len(plans), 90/len(plans)
		if _, err := h.TTS.Synthesize(ctx, tts.Request{Engine: engine, Voice: voiceName, Text: plan.Text, OutputPutURL: putURL, Params: params},
			func(pct, eta int) { sc.Progress(base+pct*span/100, eta) }); err != nil {
			return nil, err
		}
		data, err := h.Storage.ReadAll(ctx, key, "", maxSegmentAudioBytes)
		if err != nil {
			return nil, fmt.Errorf("scenes: read synthesized segment: %w", err)
		}
		f, err := ParseWav(data)
		if err != nil {
			return nil, fmt.Errorf("%w: segment %d: %v", pipeline.ErrValidation, i, err)
		}
		format = f
		name := fmt.Sprintf("seg-%03d.wav", i)
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return nil, err
		}
		files = append(files, name)
	}

	final, err := h.concat(ctx, dir, files, format, scx.Inputs.GapMs)
	if err != nil {
		return nil, err
	}
	out, err := ParseWav(final)
	if err != nil {
		return nil, err
	}
	asset, err := storeDerived(ctx, h.StepDeps, sc.Tenant(), "audio", "audio/wav", final, out.DurationMs())
	if err != nil {
		return nil, err
	}
	take, err := h.Service.RecordTake(ctx, sc.Tenant(), in.ID, TakeVoice, idOf(asset.ID), VoiceComponents(scx.Inputs, in),
		map[string]any{"engine": engine, "segments": len(plans), "durationMs": out.DurationMs()}, sc.StepID(), sc.RunID())
	if err != nil {
		return nil, err
	}
	enqueueDerivatives(ctx, h.StepDeps, sc, asset)
	h.enqueueAlignIfAlone(ctx, sc, in.ID)
	sc.Progress(100, 0)
	return pipeline.Output{"assetId": idOf(asset.ID).String(), "takeId": idOf(take.ID).String(), "durationMs": out.DurationMs(), "segments": len(plans)}, nil
}

// concat joins the segment files with a gap of silence between them
// through the ffmpeg concat demuxer (a list of plain file names in the
// temp dir); a single segment is used as is.
func (h *VoiceHandler) concat(ctx context.Context, dir string, files []string, format WavFormat, gapMs int) ([]byte, error) {
	if len(files) == 1 {
		return os.ReadFile(filepath.Join(dir, files[0])) //nolint:gosec // inside the step's temp dir
	}
	list := make([]string, 0, len(files)*2)
	if gapMs > 0 {
		if err := os.WriteFile(filepath.Join(dir, "gap.wav"), SilenceWav(format, gapMs), 0o600); err != nil {
			return nil, err
		}
	}
	for i, f := range files {
		if i > 0 && gapMs > 0 {
			list = append(list, "gap.wav")
		}
		list = append(list, f)
	}
	listPath, err := ffmpeg.WriteConcatList(dir, "list.txt", list, func(p string, b []byte) error { return os.WriteFile(p, b, 0o600) })
	if err != nil {
		return nil, err
	}
	target := filepath.Join(dir, "voice.wav")
	if err := h.Runner.Run(ctx, ffmpeg.Job{
		TempDir: dir,
		Inputs:  []ffmpeg.Input{{Format: ffmpeg.FormatConcat, Path: listPath}},
		Output:  ffmpeg.Output{Path: target, Muxer: "wav", AudioCodec: "pcm_s16le", NoVideo: true},
	}); err != nil {
		return nil, err
	}
	return os.ReadFile(target) //nolint:gosec // inside the step's temp dir
}

// enqueueAlignIfAlone queues the align step after a voice take when this
// voice step was queued on its own (a single regenerate); in a batch the
// align step already depends on it.
func (h *VoiceHandler) enqueueAlignIfAlone(ctx context.Context, sc *pipeline.StepContext, sceneID uuid.UUID) {
	if h.Service.Engine == nil {
		return
	}
	deps, err := h.Service.Queries.GetDependents(ctx, dbgen.GetDependentsParams{TenantID: idconv.ToPg(sc.Tenant()), Ids: pgIDs([]uuid.UUID{sc.StepID()})})
	if err != nil || len(deps) > 0 {
		return
	}
	if _, err := h.Service.Engine.Enqueue(ctx, sc.Tenant(), pipeline.RunSpec{
		ID: idconv.NewV7(), ScopeKind: ScopeScene, ScopeID: sceneID, Kind: "scene.align_after_voice",
		Steps: []pipeline.StepSpec{{ID: idconv.NewV7(), Kind: KindAlign, ScopeKind: ScopeScene, ScopeID: sceneID, Priority: pipeline.PriorityScene}},
	}); err != nil {
		sc.Log(fmt.Sprintf("queueing the align step failed: %v", err))
	}
}
