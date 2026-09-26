// Package channelsapi serves /channels: connecting a YouTube channel
// through Google OAuth, listing connected channels with today's quota,
// recording the API-audit state and disconnecting. Refresh tokens are
// sealed in the secrets table and never reach the browser.
package channelsapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/oauthgoogle"
	"loomtale/api/internal/secrets"
	"loomtale/api/internal/tenant"
	"loomtale/api/internal/youtube"
)

// SecretStore is the part of secrets.Store the handlers use.
type SecretStore interface {
	Put(ctx context.Context, tenantID uuid.UUID, kind, ownerRef, plaintext string) error
	Open(ctx context.Context, tenantID uuid.UUID, kind, ownerRef string) (string, error)
	Delete(ctx context.Context, tenantID uuid.UUID, kind, ownerRef string) error
}

// ChannelsAPI implements the /channels operations of
// gen.StrictServerInterface.
type ChannelsAPI struct {
	Queries *dbgen.Queries
	Secrets SecretStore
	// OAuth is the Google client; nil or unconfigured disables connecting
	// (listing and disconnecting still work).
	OAuth  *oauthgoogle.Config
	Ledger *youtube.Ledger
	// YouTubeAPIBase overrides the Data API base URL (tests only).
	YouTubeAPIBase string
}

func (h *ChannelsAPI) oauthConfigured() bool { return h.OAuth.Configured() }

// ListYouTubeChannels implements gen.StrictServerInterface.
func (h *ChannelsAPI) ListYouTubeChannels(ctx context.Context, _ gen.ListYouTubeChannelsRequestObject) (gen.ListYouTubeChannelsResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	rows, err := h.Queries.ListYouTubeChannels(ctx, idconv.ToPg(info.ID))
	if err != nil {
		return nil, fmt.Errorf("channelsapi: list: %w", err)
	}
	items := make([]gen.YouTubeChannel, 0, len(rows))
	for _, r := range rows {
		items = append(items, toChannel(r))
	}
	quota := gen.YouTubeQuota{}
	if h.Ledger != nil {
		u, err := h.Ledger.Usage(ctx)
		if err != nil {
			return nil, err
		}
		quota = gen.YouTubeQuota{Used: u.Used, Limit: u.Limit, ResetsAt: u.ResetAt}
	}
	return gen.ListYouTubeChannels200JSONResponse(gen.YouTubeChannelList{
		Items:           items,
		OauthConfigured: h.oauthConfigured(),
		Quota:           quota,
	}), nil
}

// UpdateYouTubeChannelAudit implements gen.StrictServerInterface.
func (h *ChannelsAPI) UpdateYouTubeChannelAudit(ctx context.Context, request gen.UpdateYouTubeChannelAuditRequestObject) (gen.UpdateYouTubeChannelAuditResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	body := request.Body
	params := dbgen.UpdateYouTubeChannelAuditParams{
		TenantID:          idconv.ToPg(info.ID),
		ID:                idconv.ToPg(request.Id),
		ApiProjectAudited: body.ApiProjectAudited,
	}
	if body.AuditFormDate != nil {
		params.AuditFormDate = pgtype.Date{Time: body.AuditFormDate.Time, Valid: true}
	}
	if body.AuditNote != nil {
		params.AuditNote = *body.AuditNote
	}
	row, err := h.Queries.UpdateYouTubeChannelAudit(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.UpdateYouTubeChannelAudit404ApplicationProblemPlusJSONResponse(notFound()), nil
	}
	if err != nil {
		return nil, fmt.Errorf("channelsapi: update audit state: %w", err)
	}
	h.recordAudit(ctx, "youtube_channel.audit_state", request.Id, map[string]any{
		"api_project_audited": body.ApiProjectAudited,
	})
	return gen.UpdateYouTubeChannelAudit200JSONResponse(toChannel(row)), nil
}

// DisconnectYouTubeChannel implements gen.StrictServerInterface: revoke
// the refresh token at Google, delete it, and mark the channel
// disconnected. The row is kept for the publications that reference it.
// A failed revoke does not keep the token: it is deleted here either way
// (so nothing can use it again) and the audit entry records that Google
// did not confirm the revoke.
func (h *ChannelsAPI) DisconnectYouTubeChannel(ctx context.Context, request gen.DisconnectYouTubeChannelRequestObject) (gen.DisconnectYouTubeChannelResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	row, err := h.Queries.GetYouTubeChannel(ctx, dbgen.GetYouTubeChannelParams{TenantID: idconv.ToPg(info.ID), ID: idconv.ToPg(request.Id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.DisconnectYouTubeChannel404ApplicationProblemPlusJSONResponse(notFound()), nil
	}
	if err != nil {
		return nil, fmt.Errorf("channelsapi: get channel: %w", err)
	}
	if row.Status == "disconnected" {
		return gen.DisconnectYouTubeChannel204Response{}, nil
	}
	owner := request.Id.String()
	revoked := h.revokeStored(ctx, info.ID, owner)
	if err := h.Secrets.Delete(ctx, info.ID, secrets.KindYouTubeRefresh, owner); err != nil {
		return nil, err
	}
	if _, err := h.Queries.SetYouTubeChannelStatus(ctx, dbgen.SetYouTubeChannelStatusParams{
		Status: "disconnected", TenantID: idconv.ToPg(info.ID), ID: idconv.ToPg(request.Id),
	}); err != nil {
		return nil, fmt.Errorf("channelsapi: mark disconnected: %w", err)
	}
	h.recordAudit(ctx, "youtube_channel.disconnect", request.Id, map[string]any{
		"youtube_channel_id": row.YoutubeChannelID,
		"revoked_at_google":  revoked,
	})
	return gen.DisconnectYouTubeChannel204Response{}, nil
}

// revokeStored revokes the channel's stored refresh token at Google and
// reports whether Google confirmed it. No stored token (already gone)
// counts as revoked.
func (h *ChannelsAPI) revokeStored(ctx context.Context, tenantID uuid.UUID, owner string) bool {
	token, err := h.Secrets.Open(ctx, tenantID, secrets.KindYouTubeRefresh, owner)
	if errors.Is(err, secrets.ErrNotFound) {
		return true
	}
	if err != nil {
		slog.ErrorContext(ctx, "channelsapi: open refresh token for revoke", "channel", owner, "error", err)
		return false
	}
	if h.OAuth == nil || h.OAuth.HTTP == nil {
		return false
	}
	if err := h.OAuth.Revoke(ctx, token); err != nil {
		slog.WarnContext(ctx, "channelsapi: revoke at google failed", "channel", owner, "error", err)
		return false
	}
	return true
}

func notFound() gen.Problem {
	detail := "no such channel in this workspace"
	return gen.Problem{Title: "channel not found", Status: 404, Detail: &detail}
}

// toChannel maps a row to the API shape; tokens are not part of the row.
func toChannel(r dbgen.YoutubeChannel) gen.YouTubeChannel {
	c := gen.YouTubeChannel{
		Id:                 idconv.FromPg(r.ID),
		YoutubeChannelId:   r.YoutubeChannelID,
		Title:              r.Title,
		ThumbnailUrl:       r.ThumbnailUrl,
		Scopes:             r.Scopes,
		CanUpload:          r.Status == "connected" && oauthgoogle.HasScope(r.Scopes, oauthgoogle.UploadScope),
		ApiProjectAudited:  r.ApiProjectAudited,
		AuditNote:          r.AuditNote,
		LongUploadsStatus:  gen.YouTubeChannelLongUploadsStatus(r.LongUploadsStatus),
		CustomThumbnailsOk: r.CustomThumbnailsOk,
		Status:             gen.YouTubeChannelStatus(r.Status),
		CreatedAt:          idconv.FromPgTimestamptz(r.CreatedAt),
		UpdatedAt:          idconv.FromPgTimestamptz(r.UpdatedAt),
	}
	if c.Scopes == nil {
		c.Scopes = []string{}
	}
	if r.AuditFormDate.Valid {
		c.AuditFormDate = &openapi_types.Date{Time: r.AuditFormDate.Time}
	}
	if r.EligibilityCheckedAt.Valid {
		t := r.EligibilityCheckedAt.Time
		c.EligibilityCheckedAt = &t
	}
	return c
}
