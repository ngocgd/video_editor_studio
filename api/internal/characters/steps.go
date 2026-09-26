package characters

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/media"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/image/comfyui"
	"loomtale/api/internal/providers/train"
	"loomtale/api/internal/providers/tts"
	"loomtale/api/internal/scenes"
	"loomtale/api/internal/storage"
)

// Step kinds and the scope kind of every character step.
const (
	KindSheet      = "image.character_sheet"
	KindTrain      = "lora.train"
	KindPreview    = "voice.preview"
	ScopeCharacter = "character"

	// TrainerEngine is the LoRA trainer the Python worker runs.
	TrainerEngine = "ai-toolkit"
)

const presignTTL = 3 * time.Hour

// SheetInput, TrainInput and PreviewInput are the step inputs.
type SheetInput struct {
	RefID  uuid.UUID `json:"refId"`
	Prompt string    `json:"prompt,omitempty"`
}

type TrainInput struct {
	LoraID uuid.UUID `json:"loraId"`
}

type PreviewInput struct {
	Lang string `json:"lang"`
	Text string `json:"text"`
}

// StepDeps is what the character steps need at run time; nil clients in
// the api process make Run fail with engine_not_installed.
type StepDeps struct {
	Queries *dbgen.Queries
	Storage *storage.Internal
	// Scenes gives access to the pipeline engine once it exists (the
	// handlers are registered before the engine is built).
	Scenes  *scenes.Service
	Comfy   *comfyui.Engine
	TTS     *tts.Client
	Train   *train.Client
	// SheetModel is the manifest model whose SheetWorkflow makes sheets.
	SheetModel    string
	SheetWorkflow string
}

// Handlers returns the character step handlers.
func Handlers(d StepDeps) []pipeline.StepHandler {
	return []pipeline.StepHandler{&SheetHandler{d}, &TrainHandler{d}, &PreviewHandler{d}}
}

func hashStep(s pipeline.StepRef, extra ...any) (string, error) {
	return pipeline.HashInputs(append([]any{s.Kind, s.ScopeID, s.ID}, extra...)...)
}

func loadCharacter(ctx context.Context, q *dbgen.Queries, tenantID, id uuid.UUID) (dbgen.Character, error) {
	c, err := q.GetCharacter(ctx, dbgen.GetCharacterParams{TenantID: idconv.ToPg(tenantID), ID: idconv.ToPg(id)})
	if err != nil {
		return c, fmt.Errorf("%w: character: %v", pipeline.ErrValidation, err)
	}
	return c, nil
}

func storeAsset(ctx context.Context, d StepDeps, tenantID uuid.UUID, kind, mime string, data []byte, durationMs int) (dbgen.Asset, error) {
	id := idconv.NewV7()
	key := storage.Key(tenantID.String(), kind, id)
	stored, err := d.Storage.PutBytes(ctx, key, data, mime)
	if err != nil {
		return dbgen.Asset{}, err
	}
	p := dbgen.CreateDerivedAssetParams{ID: idconv.ToPg(id), TenantID: idconv.ToPg(tenantID), Kind: kind, StorageKey: key, Mime: mime,
		Bytes: idconv.ToPgInt8(stored.Size), Sha256: idconv.ToPgText(stored.SHA256Hex), StorageVersionID: idconv.ToPgText(stored.VersionID)}
	if durationMs > 0 {
		p.DurationMs = idconv.ToPgInt4(int32(durationMs))
	}
	return d.Queries.CreateDerivedAsset(ctx, p)
}

// SheetHandler regenerates a character sheet from a reference image
// with the image-edit workflow; the result is a new, unapproved ref.
type SheetHandler struct{ StepDeps }

func (h *SheetHandler) Kind() string { return KindSheet }
func (h *SheetHandler) Queue(context.Context, pipeline.StepRef) (string, error) {
	return pipeline.QueueGPU, nil
}
func (h *SheetHandler) InputHash(_ context.Context, s pipeline.StepRef) (string, error) {
	return hashStep(s)
}
func (h *SheetHandler) ModelRef(context.Context, pipeline.StepRef) (*pipeline.ModelRef, error) {
	if h.SheetModel == "" {
		return nil, nil
	}
	return &pipeline.ModelRef{Backend: "comfyui", Model: h.SheetModel}, nil
}

func (h *SheetHandler) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	var in SheetInput
	if err := sc.Input(&in); err != nil {
		return nil, fmt.Errorf("%w: %v", pipeline.ErrValidation, err)
	}
	c, err := loadCharacter(ctx, h.Queries, sc.Tenant(), sc.ScopeID())
	if err != nil {
		return nil, err
	}
	tid := idconv.ToPg(sc.Tenant())
	ref, err := h.Queries.GetCharacterRef(ctx, dbgen.GetCharacterRefParams{TenantID: tid, ID: idconv.ToPg(in.RefID)})
	if err != nil || ref.CharacterID != c.ID {
		return nil, fmt.Errorf("%w: reference not found", pipeline.ErrValidation)
	}
	if h.Comfy == nil || h.Storage == nil || h.SheetWorkflow == "" {
		return nil, fmt.Errorf("%w: the image-edit engine is not available on this worker", pipeline.ErrEngineNotInstalled)
	}
	asset, err := h.Queries.GetAssetByID(ctx, dbgen.GetAssetByIDParams{TenantID: tid, ID: ref.AssetID})
	if err != nil {
		return nil, fmt.Errorf("%w: reference asset: %v", pipeline.ErrValidation, err)
	}
	src, err := h.Storage.ReadAll(ctx, asset.StorageKey, asset.StorageVersionID.String, storage.MaxBytesByKind["image"])
	if err != nil {
		return nil, err
	}
	prompt := strings.TrimSpace(in.Prompt)
	if prompt == "" {
		prompt = "character turnaround sheet: front, three-quarter and profile views, full body, neutral pose, plain light background"
	}
	if c.AppearancePrompt != "" {
		prompt += ", " + c.AppearancePrompt
	}
	res, err := h.Comfy.Run(ctx, h.SheetWorkflow, map[string]any{"prompt": prompt}, map[string][]byte{"ref_image": src})
	if err != nil {
		return nil, err
	}
	if len(res.Images) == 0 {
		return nil, fmt.Errorf("characters: sheet workflow returned no image")
	}
	out, err := storeAsset(ctx, h.StepDeps, sc.Tenant(), "image", "image/png", res.Images[0], 0)
	if err != nil {
		return nil, err
	}
	newRef, err := h.Queries.CreateCharacterRef(ctx, dbgen.CreateCharacterRefParams{
		ID: idconv.ToPg(idconv.NewV7()), TenantID: tid, CharacterID: c.ID, AssetID: out.ID, Angle: "sheet", Approved: false, Origin: "generated",
	})
	if err != nil {
		return nil, err
	}
	if h.Scenes != nil && h.Scenes.Engine != nil {
		if _, _, err := media.EnqueueDerivatives(ctx, h.Scenes.Engine, sc.Tenant(), []dbgen.Asset{out}, nil); err != nil {
			sc.Log("queueing variants failed: " + err.Error())
		}
	}
	return pipeline.Output{"refId": idconv.FromPg(newRef.ID).String(), "assetId": idconv.FromPg(out.ID).String(), "seconds": res.Seconds}, nil
}

// TrainHandler trains a LoRA version through the Python worker's Train
// RPC. The dataset is handed over as a JSON manifest of presigned image
// URLs; the weights come back through a presigned PUT.
type TrainHandler struct{ StepDeps }

func (h *TrainHandler) Kind() string { return KindTrain }
func (h *TrainHandler) Queue(context.Context, pipeline.StepRef) (string, error) {
	return pipeline.QueueGPU, nil
}
func (h *TrainHandler) InputHash(_ context.Context, s pipeline.StepRef) (string, error) {
	return hashStep(s)
}

// ModelRef is nil: the trainer is not a residency-managed model (the
// trainer engine and its VRAM handling arrive with the LoRA phase).
func (h *TrainHandler) ModelRef(context.Context, pipeline.StepRef) (*pipeline.ModelRef, error) {
	return nil, nil
}

func (h *TrainHandler) Run(ctx context.Context, sc *pipeline.StepContext) (out pipeline.Output, err error) {
	var in TrainInput
	if err := sc.Input(&in); err != nil {
		return nil, fmt.Errorf("%w: %v", pipeline.ErrValidation, err)
	}
	tid := idconv.ToPg(sc.Tenant())
	lora, err := h.Queries.GetCharacterLora(ctx, dbgen.GetCharacterLoraParams{TenantID: tid, ID: idconv.ToPg(in.LoraID)})
	if err != nil {
		return nil, fmt.Errorf("%w: lora: %v", pipeline.ErrValidation, err)
	}
	c, err := loadCharacter(ctx, h.Queries, sc.Tenant(), idconv.FromPg(lora.CharacterID))
	if err != nil {
		return nil, err
	}
	setStatus := func(status string, weights *dbgen.Asset, file string) {
		p := dbgen.UpdateCharacterLoraStatusParams{Status: status, WeightsFile: file, TenantID: tid, ID: lora.ID}
		if weights != nil {
			p.WeightsAssetID = weights.ID
		}
		if _, e := h.Queries.UpdateCharacterLoraStatus(ctx, p); e != nil {
			sc.Log("updating the lora status failed: " + e.Error())
		}
	}
	defer func() {
		if err != nil {
			setStatus("failed", nil, "")
		}
	}()
	if h.Train == nil || h.Storage == nil {
		return nil, fmt.Errorf("%w: the LoRA trainer is not available on this worker", pipeline.ErrEngineNotInstalled)
	}
	setStatus("training", nil, "")

	type item struct {
		URL     string `json:"url"`
		Caption string `json:"caption"`
	}
	var dataset []item
	for _, id := range lora.DatasetAssetIds {
		a, err := h.Queries.GetAssetByID(ctx, dbgen.GetAssetByIDParams{TenantID: tid, ID: id})
		if err != nil {
			return nil, fmt.Errorf("%w: dataset asset: %v", pipeline.ErrValidation, err)
		}
		u, err := h.Storage.PresignGet(ctx, a.StorageKey, a.StorageVersionID.String, presignTTL)
		if err != nil {
			return nil, err
		}
		dataset = append(dataset, item{URL: u, Caption: strings.TrimSpace(c.TriggerToken + " " + c.AppearancePrompt)})
	}
	manifest, _ := json.Marshal(map[string]any{"images": dataset})
	manifestKey := storage.DerivedKey(sc.Tenant().String(), idconv.FromPg(lora.ID), "dataset.json")
	if _, err := h.Storage.PutBytes(ctx, manifestKey, manifest, "application/json"); err != nil {
		return nil, err
	}
	manifestURL, err := h.Storage.PresignGet(ctx, manifestKey, "", presignTTL)
	if err != nil {
		return nil, err
	}
	weightsID := idconv.NewV7()
	weightsKey := storage.Key(sc.Tenant().String(), "document", weightsID)
	putURL, err := h.Storage.PresignPut(ctx, weightsKey, presignTTL)
	if err != nil {
		return nil, err
	}
	var params map[string]string
	_ = json.Unmarshal(lora.TrainerParams, &params)
	if params == nil {
		params = map[string]string{}
	}
	params["trigger"] = c.TriggerToken
	params["output_key"] = weightsKey
	res, err := h.Train.Train(ctx, train.Request{Engine: TrainerEngine, DatasetGetURL: manifestURL, OutputPutURL: putURL, Params: params},
		func(p train.Progress) { sc.Progress(p.Pct, p.EtaS) }, sc.Log)
	if err != nil {
		return nil, err
	}
	stored, err := h.Storage.Stat(ctx, weightsKey)
	if err != nil {
		return nil, fmt.Errorf("characters: the trainer did not upload weights: %w", err)
	}
	weights, err := h.Queries.CreateDerivedAsset(ctx, dbgen.CreateDerivedAssetParams{
		ID: idconv.ToPg(weightsID), TenantID: tid, Kind: "document", StorageKey: weightsKey, Mime: "application/octet-stream",
		Bytes: idconv.ToPgInt8(stored.Size), StorageVersionID: idconv.ToPgText(stored.VersionID),
	})
	if err != nil {
		return nil, err
	}
	file := "loomtale-" + idconv.FromPg(c.ID).String() + "-v" + strconv.Itoa(int(lora.Version)) + ".safetensors"
	setStatus("ready", &weights, file)
	return pipeline.Output{"loraId": in.LoraID.String(), "weightsAssetId": weightsID.String(), "metadata": res.Metadata}, nil
}

// PreviewHandler synthesizes one line with a character's voice.
type PreviewHandler struct{ StepDeps }

func (h *PreviewHandler) Kind() string { return KindPreview }
func (h *PreviewHandler) Queue(context.Context, pipeline.StepRef) (string, error) {
	return pipeline.QueueGPU, nil
}
func (h *PreviewHandler) InputHash(_ context.Context, s pipeline.StepRef) (string, error) {
	return hashStep(s)
}

func (h *PreviewHandler) voice(ctx context.Context, tenantID, characterID uuid.UUID, lang string) (*scenes.Voice, error) {
	c, err := loadCharacter(ctx, h.Queries, tenantID, characterID)
	if err != nil {
		return nil, err
	}
	in, _, err := scenes.LoadEpisodeInputs(ctx, h.Queries, tenantID, idconv.FromPg(c.SeriesID), lang)
	if err != nil {
		return nil, err
	}
	return in.VoiceFor(&characterID), nil
}

// ModelRef is the character's TTS engine for the step's language. At
// enqueue time the step row does not exist yet, so the language falls
// back to English for the stored provider_ref; the GPU executor calls
// ModelRef again right before the step runs, when the input is readable,
// and loads the right engine then.
func (h *PreviewHandler) ModelRef(ctx context.Context, s pipeline.StepRef) (*pipeline.ModelRef, error) {
	step, err := h.Queries.GetStepByID(ctx, dbgen.GetStepByIDParams{TenantID: idconv.ToPg(s.TenantID), ID: idconv.ToPg(s.ID)})
	lang := "en"
	if err == nil {
		var in PreviewInput
		if json.Unmarshal(step.Input, &in) == nil && in.Lang != "" {
			lang = in.Lang
		}
	}
	v, err := h.voice(ctx, s.TenantID, s.ScopeID, lang)
	if err != nil || v == nil || v.Engine == "" {
		return nil, nil //nolint:nilerr // Run reports the real error
	}
	return &pipeline.ModelRef{Backend: "pyworker", Model: v.Engine}, nil
}

func (h *PreviewHandler) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	var in PreviewInput
	if err := sc.Input(&in); err != nil {
		return nil, fmt.Errorf("%w: %v", pipeline.ErrValidation, err)
	}
	v, err := h.voice(ctx, sc.Tenant(), sc.ScopeID(), in.Lang)
	if err != nil {
		return nil, err
	}
	if v == nil || v.Engine == "" {
		return nil, fmt.Errorf("%w: no %s voice assigned", pipeline.ErrValidation, strings.ToUpper(in.Lang))
	}
	if h.TTS == nil || h.Storage == nil {
		return nil, fmt.Errorf("%w: the TTS engine is not available on this worker", pipeline.ErrEngineNotInstalled)
	}
	assetID := idconv.NewV7()
	key := storage.Key(sc.Tenant().String(), "audio", assetID)
	putURL, err := h.Storage.PresignPut(ctx, key, presignTTL)
	if err != nil {
		return nil, err
	}
	params := v.MergedParams()
	params["language"], params["output_key"] = in.Lang, key
	if v.RefAssetID != nil {
		ref, err := h.Queries.GetAssetByID(ctx, dbgen.GetAssetByIDParams{TenantID: idconv.ToPg(sc.Tenant()), ID: idconv.ToPg(*v.RefAssetID)})
		if err != nil {
			return nil, fmt.Errorf("%w: reference voice: %v", pipeline.ErrValidation, err)
		}
		if params["reference_url"], err = h.Storage.PresignGet(ctx, ref.StorageKey, ref.StorageVersionID.String, presignTTL); err != nil {
			return nil, err
		}
		if v.Consented {
			params["consent"] = "granted"
		}
	}
	res, err := h.TTS.Synthesize(ctx, tts.Request{Engine: v.Engine, Text: in.Text, OutputPutURL: putURL, Params: params}, func(p, e int) { sc.Progress(p, e) })
	if err != nil {
		return nil, err
	}
	stored, err := h.Storage.Stat(ctx, key)
	if err != nil {
		return nil, err
	}
	if _, err := h.Queries.CreateDerivedAsset(ctx, dbgen.CreateDerivedAssetParams{
		ID: idconv.ToPg(assetID), TenantID: idconv.ToPg(sc.Tenant()), Kind: "audio", StorageKey: key, Mime: "audio/wav",
		Bytes: idconv.ToPgInt8(stored.Size), StorageVersionID: idconv.ToPgText(stored.VersionID), DurationMs: idconv.ToPgInt4(int32(res.DurationS * 1000)),
	}); err != nil {
		return nil, err
	}
	return pipeline.Output{"assetId": assetID.String(), "durationMs": int(res.DurationS * 1000)}, nil
}

func itoa(n int) string { return strconv.Itoa(n) }
