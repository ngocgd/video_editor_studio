package characters

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

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
	KindTrain      = "train.lora"
	KindPreview    = "voice.preview"
	ScopeCharacter = "character"

	// TrainerEngine is the LoRA trainer engine the Python worker runs
	// (its manifest entry holds the trainer's base weights), and
	// TrainerBaseModel is the scene model the LoRA is trained for.
	TrainerEngine    = "z-image-turbo-trainer"
	TrainerBaseModel = "z-image-turbo"
)

// trainerParamKeys are the stored trainer params passed through to the
// worker; the worker validates their ranges.
var trainerParamKeys = []string{"steps", "rank", "learning_rate", "max_minutes"}

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
	Scenes *scenes.Service
	Comfy  *comfyui.Engine
	TTS    *tts.Client
	Train  *train.Client
	// SheetModel is the manifest model whose SheetWorkflow makes sheets.
	SheetModel    string
	SheetWorkflow string
	// ModelsDir is the models volume ComfyUI reads LoRAs from (its
	// loras/ folder); empty on a worker without it, where trained
	// weights stay an asset only and scenes render without the LoRA.
	ModelsDir string
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

// ModelRef makes the trainer the resident model of the Python worker,
// so training takes the single GPU slot like any other model.
func (h *TrainHandler) ModelRef(context.Context, pipeline.StepRef) (*pipeline.ModelRef, error) {
	return &pipeline.ModelRef{Backend: "pyworker", Model: TrainerEngine}, nil
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
	if n := len(lora.DatasetAssetIds); n < train.MinDatasetImages || n > train.MaxDatasetImages {
		return nil, fmt.Errorf("%w: a LoRA dataset needs %d to %d images, got %d", pipeline.ErrValidation,
			train.MinDatasetImages, train.MaxDatasetImages, n)
	}
	setStatus("training", nil, "")

	trigger := TrainerTriggerWord(c)
	caption := strings.TrimSpace(trigger + ", " + c.AppearancePrompt)
	caption = strings.TrimSuffix(caption, ",")
	images := make([]train.DatasetImage, 0, len(lora.DatasetAssetIds))
	for _, id := range lora.DatasetAssetIds {
		a, err := h.Queries.GetAssetByID(ctx, dbgen.GetAssetByIDParams{TenantID: tid, ID: id})
		if err != nil {
			return nil, fmt.Errorf("%w: dataset asset: %v", pipeline.ErrValidation, err)
		}
		ext, ok := train.DatasetExt(a.Mime)
		if a.Kind != "image" || !ok {
			return nil, fmt.Errorf("%w: dataset asset %s is not a PNG, JPEG or WebP image", pipeline.ErrValidation, idconv.FromPg(a.ID))
		}
		data, err := h.Storage.ReadAll(ctx, a.StorageKey, a.StorageVersionID.String, storage.MaxBytesByKind["image"])
		if err != nil {
			return nil, err
		}
		images = append(images, train.DatasetImage{Ext: ext, Data: data, Caption: caption})
	}
	archive, err := train.DatasetZip(images)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", pipeline.ErrValidation, err)
	}
	datasetKey := storage.DerivedKey(sc.Tenant().String(), idconv.FromPg(lora.ID), "dataset.zip")
	if _, err := h.Storage.PutBytes(ctx, datasetKey, archive, "application/zip"); err != nil {
		return nil, err
	}
	datasetURL, err := h.Storage.PresignGet(ctx, datasetKey, "", presignTTL)
	if err != nil {
		return nil, err
	}
	weightsID := idconv.NewV7()
	weightsKey := storage.Key(sc.Tenant().String(), "document", weightsID)
	putURL, err := h.Storage.PresignPut(ctx, weightsKey, presignTTL)
	if err != nil {
		return nil, err
	}
	res, err := h.Train.Train(ctx, train.Request{Engine: TrainerEngine, BaseModel: TrainerBaseModel, DatasetGetURL: datasetURL,
		OutputPutURL: putURL, Params: TrainerParams(lora.TrainerParams, trigger, weightsKey)},
		func(p train.Progress) { sc.Progress(p.Pct, p.EtaS) }, sc.Log)
	if err != nil {
		return nil, err
	}
	stored, err := h.Storage.Stat(ctx, weightsKey)
	if err != nil {
		return nil, fmt.Errorf("characters: the trainer did not upload weights: %w", err)
	}
	if stored.Size <= 0 {
		return nil, fmt.Errorf("characters: the trainer uploaded empty weights")
	}
	weights, err := h.Queries.CreateDerivedAsset(ctx, dbgen.CreateDerivedAssetParams{
		ID: idconv.ToPg(weightsID), TenantID: tid, Kind: "document", StorageKey: weightsKey, Mime: "application/octet-stream",
		Bytes: idconv.ToPgInt8(stored.Size), StorageVersionID: idconv.ToPgText(stored.VersionID),
	})
	if err != nil {
		return nil, err
	}
	file, err := h.installLora(ctx, weightsKey, stored.VersionID, LoraWeightsFile(c.ID, lora.Version))
	if err != nil {
		return nil, err
	}
	if file == "" {
		sc.Log("no models volume on this worker: the LoRA is kept as an asset and scenes render without it")
	}
	setStatus("ready", &weights, file)
	return pipeline.Output{"loraId": in.LoraID.String(), "weightsAssetId": weightsID.String(), "triggerWord": trigger,
		"images": len(images), "metadata": res.Metadata}, nil
}

// installLora copies trained weights into the models volume's loras/
// folder, where ComfyUI loads them by file name. It returns the file
// name, or "" when this worker has no models volume.
func (h *TrainHandler) installLora(ctx context.Context, key, versionID, file string) (string, error) {
	if h.ModelsDir == "" {
		return "", nil
	}
	dir := filepath.Join(h.ModelsDir, "loras")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("characters: loras folder: %w", err)
	}
	if err := h.Storage.DownloadTo(ctx, key, versionID, filepath.Join(dir, file)); err != nil {
		return "", fmt.Errorf("characters: installing the LoRA: %w", err)
	}
	return file, nil
}

// TrainerTriggerWord is the trigger word a character's LoRA is trained
// with, in the form the trainer accepts: the character's trigger token,
// else its English name, else a word derived from its id so two
// characters never share one.
func TrainerTriggerWord(c dbgen.Character) string {
	for _, candidate := range []string{c.TriggerToken, c.NameEn} {
		if w := train.TriggerWord(candidate); w != train.DefaultTriggerWord {
			return w
		}
	}
	return "char_" + strings.ReplaceAll(idconv.FromPg(c.ID).String(), "-", "")[:12]
}

// TrainerParams builds the worker params from a LoRA version's stored
// trainer params: only the known keys pass through, and the trigger
// word and output key always come from the caller.
func TrainerParams(stored []byte, trigger, outputKey string) map[string]string {
	var raw map[string]any
	_ = json.Unmarshal(stored, &raw)
	params := map[string]string{"trigger_word": trigger, "output_key": outputKey}
	for _, k := range trainerParamKeys {
		switch v := raw[k].(type) {
		case string:
			if v != "" {
				params[k] = v
			}
		case float64:
			params[k] = strconv.FormatFloat(v, 'f', -1, 64)
		}
	}
	return params
}

// LoraWeightsFile is the file name a LoRA version is installed under on
// the models volume.
func LoraWeightsFile(characterID pgtype.UUID, version int32) string {
	return "loomtale-" + idconv.FromPg(characterID).String() + "-v" + strconv.Itoa(int(version)) + ".safetensors"
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
	voiceName := ""
	if v.RefAssetID == nil {
		voiceName = v.BuiltinVoice()
	}
	res, err := h.TTS.Synthesize(ctx, tts.Request{Engine: v.Engine, Voice: voiceName, Text: in.Text, OutputPutURL: putURL, Params: params}, func(p, e int) { sc.Progress(p, e) })
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
	if err := h.Queries.SetCharacterVoicePreview(ctx, dbgen.SetCharacterVoicePreviewParams{
		PreviewAssetID: idconv.ToPg(assetID), TenantID: idconv.ToPg(sc.Tenant()), CharacterID: idconv.ToPg(sc.ScopeID()), Lang: in.Lang,
	}); err != nil {
		return nil, err
	}
	return pipeline.Output{"assetId": assetID.String(), "durationMs": int(res.DurationS * 1000)}, nil
}

func itoa(n int) string { return strconv.Itoa(n) }
