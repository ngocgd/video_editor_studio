package pipelineapi

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	authpkg "loomtale/api/internal/auth"
	"loomtale/api/internal/audit"
	"loomtale/api/internal/httpx"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/tenant"
)

// recordJobAudit records a retry/cancel action against a run or step.
// Best effort: an audit write failure is logged, never surfaces to the
// client and never rolls back the action it describes (retry/cancel
// already committed through the engine's own queries before this runs).
func recordJobAudit(ctx context.Context, e *pipeline.Engine, action, targetType string, targetID uuid.UUID) {
	info, err := tenant.FromCtx(ctx)
	if err != nil {
		return
	}
	sess, _ := authpkg.FromCtx(ctx)
	r := httpx.RequestFromCtx(ctx)

	entry := audit.Entry{
		TenantID:   &info.ID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID.String(),
	}
	if sess.UserID != uuid.Nil {
		entry.ActorUserID = &sess.UserID
	}
	if r != nil {
		entry.RemoteAddr = r.RemoteAddr
		entry.UserAgent = r.UserAgent()
	}
	if err := audit.Record(ctx, e.Queries, entry); err != nil {
		slog.ErrorContext(ctx, "pipelineapi: failed to write audit entry", "action", action, "error", err)
	}
}
