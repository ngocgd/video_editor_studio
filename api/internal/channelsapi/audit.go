package channelsapi

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"loomtale/api/internal/audit"
	authpkg "loomtale/api/internal/auth"
	"loomtale/api/internal/httpx"
	"loomtale/api/internal/tenant"
)

// recordAudit records a channel action. Best effort, like the other
// domain audit helpers: a failed write is logged, never fails the request.
func (h *ChannelsAPI) recordAudit(ctx context.Context, action string, channelID uuid.UUID, metadata map[string]any) {
	if h.Queries == nil {
		return
	}
	info, err := tenant.FromCtx(ctx)
	if err != nil {
		return
	}
	entry := audit.Entry{
		TenantID:   &info.ID,
		Action:     action,
		TargetType: "youtube_channel",
		TargetID:   channelID.String(),
		Metadata:   metadata,
	}
	if sess, ok := authpkg.FromCtx(ctx); ok && sess.UserID != uuid.Nil {
		entry.ActorUserID = &sess.UserID
	}
	if r := httpx.RequestFromCtx(ctx); r != nil {
		entry.RemoteAddr = r.RemoteAddr
		entry.UserAgent = r.UserAgent()
	}
	if err := audit.Record(ctx, h.Queries, entry); err != nil {
		slog.ErrorContext(ctx, "channelsapi: failed to write audit entry", "action", action, "error", err)
	}
}
