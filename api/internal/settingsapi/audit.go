package settingsapi

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"loomtale/api/internal/audit"
	authpkg "loomtale/api/internal/auth"
	"loomtale/api/internal/httpx"
	"loomtale/api/internal/tenant"
)

// recordSettingsAudit records a PUT/test action against the tenant's LLM
// settings, mirroring pipelineapi's recordJobAudit: best effort, never
// blocks or fails the request it describes.
func (h *SettingsAPI) recordSettingsAudit(ctx context.Context, action string) {
	if h.Queries == nil {
		return
	}
	info, err := tenant.FromCtx(ctx)
	if err != nil {
		return
	}
	sess, _ := authpkg.FromCtx(ctx)
	r := httpx.RequestFromCtx(ctx)

	entry := audit.Entry{
		TenantID:   &info.ID,
		Action:     action,
		TargetType: "llm_settings",
		TargetID:   info.ID.String(),
	}
	if sess.UserID != uuid.Nil {
		entry.ActorUserID = &sess.UserID
	}
	if r != nil {
		entry.RemoteAddr = r.RemoteAddr
		entry.UserAgent = r.UserAgent()
	}
	if err := audit.Record(ctx, h.Queries, entry); err != nil {
		slog.ErrorContext(ctx, "settingsapi: failed to write audit entry", "action", action, "error", err)
	}
}
