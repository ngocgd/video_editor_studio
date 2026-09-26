// Package presets implements voice presets and image styles: the
// tenant-wide settings characters and scenes pick their voice and look
// from. A cloned reference voice is only stored with a recorded consent.
package presets

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"loomtale/api/internal/audit"
	authpkg "loomtale/api/internal/auth"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/httpx"
	"loomtale/api/internal/tenant"
)

// SceneModels reports whether a manifest model can render scenes.
type SceneModels func(name string) bool

// PresetsAPI implements the presets slice of gen.StrictServerInterface.
type PresetsAPI struct {
	Pool        *pgxpool.Pool
	Queries     *dbgen.Queries
	IsSceneModel SceneModels
}

func problem(status int, title string) gen.Problem { return gen.Problem{Title: title, Status: status} }

func encodeParams(p *gen.StringParams) []byte {
	m := map[string]string{}
	if p != nil {
		m = *p
	}
	b, _ := json.Marshal(m)
	return b
}

func decodeParams(raw []byte) gen.StringParams {
	m := gen.StringParams{}
	_ = json.Unmarshal(raw, &m)
	return m
}

func voicePresetDTO(v dbgen.VoicePreset) gen.VoicePreset {
	dto := gen.VoicePreset{Id: idconv.FromPg(v.ID), Name: v.Name, Engine: v.Engine, Params: decodeParams(v.Params), Consented: v.ConsentedAt.Valid, RefAudioAssetId: idconv.FromPgPtr(v.RefAudioAssetID)}
	if v.ConsentedAt.Valid {
		t := v.ConsentedAt.Time.UTC()
		dto.ConsentedAt = &t
	}
	return dto
}

// ListVoicePresets implements gen.StrictServerInterface.
func (h *PresetsAPI) ListVoicePresets(ctx context.Context, _ gen.ListVoicePresetsRequestObject) (gen.ListVoicePresetsResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	rows, err := h.Queries.ListVoicePresets(ctx, idconv.ToPg(info.ID))
	if err != nil {
		return nil, err
	}
	out := gen.ListVoicePresets200JSONResponse{Items: make([]gen.VoicePreset, 0, len(rows))}
	for _, r := range rows {
		out.Items = append(out.Items, voicePresetDTO(r))
	}
	return out, nil
}

// errConsent is a reference voice that cannot be stored as given.
type errConsent struct{ msg string }

func (e errConsent) Error() string { return e.msg }

// saveVoicePreset validates the reference audio and consent, writes the
// preset and, when a reference voice is consented to, the audit entry,
// in one transaction.
func (h *PresetsAPI) saveVoicePreset(ctx context.Context, id *uuid.UUID, body gen.VoicePresetInput) (dbgen.VoicePreset, error) {
	info := tenant.MustFromCtx(ctx)
	sess, _ := authpkg.FromCtx(ctx)
	tid := idconv.ToPg(info.ID)
	var consentedAt pgtype.Timestamptz
	var consentedBy pgtype.UUID
	var existing *dbgen.VoicePreset
	if id != nil {
		row, err := h.Queries.GetVoicePreset(ctx, dbgen.GetVoicePresetParams{TenantID: tid, ID: idconv.ToPg(*id)})
		if err != nil {
			return dbgen.VoicePreset{}, err
		}
		existing = &row
	}
	newConsent := false
	if body.RefAudioAssetId != nil {
		asset, err := h.Queries.GetAssetByID(ctx, dbgen.GetAssetByIDParams{TenantID: tid, ID: idconv.ToPg(*body.RefAudioAssetId)})
		if err != nil || asset.Kind != "audio" || asset.Status != "ready" {
			return dbgen.VoicePreset{}, errConsent{"the reference must be a ready audio upload of this workspace"}
		}
		sameRef := existing != nil && existing.RefAudioAssetID.Valid && idconv.FromPg(existing.RefAudioAssetID) == *body.RefAudioAssetId && existing.ConsentedAt.Valid
		switch {
		case body.Consent != nil && *body.Consent:
			consentedAt, consentedBy = idconv.ToPgTimestamptz(time.Now()), idconv.ToPg(sess.UserID)
			newConsent = !sameRef
		case sameRef:
			consentedAt, consentedBy = existing.ConsentedAt, existing.ConsentedBy
		default:
			return dbgen.VoicePreset{}, errConsent{"confirm the reference voice is your own or licensed before cloning it"}
		}
	}

	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		return dbgen.VoicePreset{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := h.Queries.WithTx(tx)
	var row dbgen.VoicePreset
	if id == nil {
		row, err = q.CreateVoicePreset(ctx, dbgen.CreateVoicePresetParams{
			ID: idconv.ToPg(idconv.NewV7()), TenantID: tid, Name: body.Name, Engine: body.Engine, RefAudioAssetID: idconv.ToPgPtr(body.RefAudioAssetId),
			Params: encodeParams(body.Params), ConsentedAt: consentedAt, ConsentedBy: consentedBy,
		})
	} else {
		row, err = q.UpdateVoicePreset(ctx, dbgen.UpdateVoicePresetParams{
			Name: body.Name, Engine: body.Engine, RefAudioAssetID: idconv.ToPgPtr(body.RefAudioAssetId), Params: encodeParams(body.Params),
			ConsentedAt: consentedAt, ConsentedBy: consentedBy, TenantID: tid, ID: idconv.ToPg(*id),
		})
	}
	if err != nil {
		return dbgen.VoicePreset{}, err
	}
	if newConsent {
		r := httpx.RequestFromCtx(ctx)
		entry := audit.Entry{
			TenantID: &info.ID, ActorUserID: &sess.UserID, Action: "voice_reference_consented", TargetType: "voice_preset",
			TargetID: idconv.FromPg(row.ID).String(), Metadata: map[string]any{"asset_id": body.RefAudioAssetId.String(), "statement": "own or licensed voice"},
		}
		if r != nil {
			entry.RemoteAddr, entry.UserAgent = r.RemoteAddr, r.UserAgent()
		}
		if err := audit.Record(ctx, q, entry); err != nil {
			return dbgen.VoicePreset{}, err
		}
	}
	return row, tx.Commit(ctx)
}

// CreateVoicePreset implements gen.StrictServerInterface.
func (h *PresetsAPI) CreateVoicePreset(ctx context.Context, req gen.CreateVoicePresetRequestObject) (gen.CreateVoicePresetResponseObject, error) {
	row, err := h.saveVoicePreset(ctx, nil, *req.Body)
	var ce errConsent
	if errors.As(err, &ce) {
		detail := ce.msg
		return gen.CreateVoicePreset422ApplicationProblemPlusJSONResponse{Title: "reference voice refused", Status: http.StatusUnprocessableEntity, Detail: &detail}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.CreateVoicePreset201JSONResponse(voicePresetDTO(row)), nil
}

// UpdateVoicePreset implements gen.StrictServerInterface.
func (h *PresetsAPI) UpdateVoicePreset(ctx context.Context, req gen.UpdateVoicePresetRequestObject) (gen.UpdateVoicePresetResponseObject, error) {
	row, err := h.saveVoicePreset(ctx, &req.Id, *req.Body)
	var ce errConsent
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return gen.UpdateVoicePreset404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "voice preset not found")), nil
	case errors.As(err, &ce):
		detail := ce.msg
		return gen.UpdateVoicePreset422ApplicationProblemPlusJSONResponse{Title: "reference voice refused", Status: http.StatusUnprocessableEntity, Detail: &detail}, nil
	case err != nil:
		return nil, err
	}
	return gen.UpdateVoicePreset200JSONResponse(voicePresetDTO(row)), nil
}

// DeleteVoicePreset implements gen.StrictServerInterface.
func (h *PresetsAPI) DeleteVoicePreset(ctx context.Context, req gen.DeleteVoicePresetRequestObject) (gen.DeleteVoicePresetResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	tid := idconv.ToPg(info.ID)
	uses, err := h.Queries.CountVoicePresetUses(ctx, dbgen.CountVoicePresetUsesParams{TenantID: tid, ID: idconv.ToPg(req.Id)})
	if err != nil {
		return nil, err
	}
	if uses > 0 {
		return gen.DeleteVoicePreset409ApplicationProblemPlusJSONResponse(problem(http.StatusConflict, "a character or narrator voice still uses this preset")), nil
	}
	n, err := h.Queries.DeleteVoicePreset(ctx, dbgen.DeleteVoicePresetParams{TenantID: tid, ID: idconv.ToPg(req.Id)})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return gen.DeleteVoicePreset409ApplicationProblemPlusJSONResponse(problem(http.StatusConflict, "a character or narrator voice still uses this preset")), nil
	}
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return gen.DeleteVoicePreset404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "voice preset not found")), nil
	}
	return gen.DeleteVoicePreset204Response{}, nil
}

func imageStyleDTO(s dbgen.ImageStyle) gen.ImageStyle {
	loras := []gen.ImageStyleLora{}
	_ = json.Unmarshal(s.Loras, &loras)
	return gen.ImageStyle{
		Id: idconv.FromPg(s.ID), Name: s.Name, StylePrompt: s.StylePrompt, NegativePrompt: s.NegativePrompt, BaseModel: s.BaseModel,
		Sampler: s.Sampler, Steps: int(s.Steps), Width: int(s.Width), Height: int(s.Height), Loras: loras,
	}
}

type styleFields struct {
	stylePrompt, negativePrompt, sampler string
	steps, width, height                 int32
	loras                                []byte
}

func styleFieldsOf(b gen.ImageStyleInput) styleFields {
	f := styleFields{steps: 8, width: 1920, height: 1080}
	if b.StylePrompt != nil {
		f.stylePrompt = *b.StylePrompt
	}
	if b.NegativePrompt != nil {
		f.negativePrompt = *b.NegativePrompt
	}
	if b.Sampler != nil {
		f.sampler = *b.Sampler
	}
	if b.Steps != nil {
		f.steps = int32(*b.Steps)
	}
	if b.Width != nil {
		f.width = int32(*b.Width)
	}
	if b.Height != nil {
		f.height = int32(*b.Height)
	}
	loras := []gen.ImageStyleLora{}
	if b.Loras != nil {
		loras = *b.Loras
	}
	f.loras, _ = json.Marshal(loras)
	return f
}

// ListImageStyles implements gen.StrictServerInterface.
func (h *PresetsAPI) ListImageStyles(ctx context.Context, _ gen.ListImageStylesRequestObject) (gen.ListImageStylesResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	rows, err := h.Queries.ListImageStyles(ctx, idconv.ToPg(info.ID))
	if err != nil {
		return nil, err
	}
	out := gen.ListImageStyles200JSONResponse{Items: make([]gen.ImageStyle, 0, len(rows))}
	for _, r := range rows {
		out.Items = append(out.Items, imageStyleDTO(r))
	}
	return out, nil
}

// CreateImageStyle implements gen.StrictServerInterface.
func (h *PresetsAPI) CreateImageStyle(ctx context.Context, req gen.CreateImageStyleRequestObject) (gen.CreateImageStyleResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	if h.IsSceneModel != nil && !h.IsSceneModel(req.Body.BaseModel) {
		return gen.CreateImageStyle422ApplicationProblemPlusJSONResponse(problem(http.StatusUnprocessableEntity, "the base model is not a scene model in the manifest")), nil
	}
	f := styleFieldsOf(*req.Body)
	row, err := h.Queries.CreateImageStyle(ctx, dbgen.CreateImageStyleParams{
		ID: idconv.ToPg(idconv.NewV7()), TenantID: idconv.ToPg(info.ID), Name: req.Body.Name, StylePrompt: f.stylePrompt, NegativePrompt: f.negativePrompt,
		BaseModel: req.Body.BaseModel, Sampler: f.sampler, Steps: f.steps, Width: f.width, Height: f.height, Loras: f.loras,
	})
	if err != nil {
		return nil, err
	}
	return gen.CreateImageStyle201JSONResponse(imageStyleDTO(row)), nil
}

// UpdateImageStyle implements gen.StrictServerInterface.
func (h *PresetsAPI) UpdateImageStyle(ctx context.Context, req gen.UpdateImageStyleRequestObject) (gen.UpdateImageStyleResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	if h.IsSceneModel != nil && !h.IsSceneModel(req.Body.BaseModel) {
		return gen.UpdateImageStyle422ApplicationProblemPlusJSONResponse(problem(http.StatusUnprocessableEntity, "the base model is not a scene model in the manifest")), nil
	}
	f := styleFieldsOf(*req.Body)
	row, err := h.Queries.UpdateImageStyle(ctx, dbgen.UpdateImageStyleParams{
		Name: req.Body.Name, StylePrompt: f.stylePrompt, NegativePrompt: f.negativePrompt, BaseModel: req.Body.BaseModel,
		Sampler: f.sampler, Steps: f.steps, Width: f.width, Height: f.height, Loras: f.loras, TenantID: idconv.ToPg(info.ID), ID: idconv.ToPg(req.Id),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.UpdateImageStyle404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "image style not found")), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.UpdateImageStyle200JSONResponse(imageStyleDTO(row)), nil
}

// DeleteImageStyle implements gen.StrictServerInterface.
func (h *PresetsAPI) DeleteImageStyle(ctx context.Context, req gen.DeleteImageStyleRequestObject) (gen.DeleteImageStyleResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	n, err := h.Queries.DeleteImageStyle(ctx, dbgen.DeleteImageStyleParams{TenantID: idconv.ToPg(info.ID), ID: idconv.ToPg(req.Id)})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return gen.DeleteImageStyle404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "image style not found")), nil
	}
	return gen.DeleteImageStyle204Response{}, nil
}
