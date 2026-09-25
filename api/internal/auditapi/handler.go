// Package auditapi implements the audit slice of the generated strict
// server interface: the owner-only GET /audit.
package auditapi

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/httpx"
	"loomtale/api/internal/tenant"
)

// AuditAPI implements the audit slice of gen.StrictServerInterface. Named
// distinctly (not "Handler") so the composite server in cmd/api can embed
// every domain handler without a field-name collision.
type AuditAPI struct {
	Queries *dbgen.Queries
}

// ListAudit implements gen.StrictServerInterface. RBAC guarantees an
// owner role in a resolved tenant for this route.
func (h *AuditAPI) ListAudit(ctx context.Context, req gen.ListAuditRequestObject) (gen.ListAuditResponseObject, error) {
	info := tenant.MustFromCtx(ctx)

	cursorStr := ""
	if req.Params.Cursor != nil {
		cursorStr = *req.Params.Cursor
	}
	cursor, err := httpx.DecodeCursor(cursorStr)
	if err != nil {
		cursor = uuid.Nil
	}
	limit := httpx.PageLimit(req.Params.Limit)

	rows, err := h.Queries.ListAuditLog(ctx, dbgen.ListAuditLogParams{
		TenantID:  idconv.ToPg(info.ID),
		Cursor:    idconv.ToPg(cursor),
		PageLimit: limit,
	})
	if err != nil {
		return nil, err
	}

	items := make([]gen.AuditEntry, 0, len(rows))
	for _, row := range rows {
		entry := gen.AuditEntry{
			Id:        idconv.FromPg(row.ID),
			Action:    row.Action,
			CreatedAt: row.CreatedAt.Time,
		}
		if row.ActorUserID.Valid {
			actor := idconv.FromPg(row.ActorUserID)
			entry.ActorUserId = &actor
		}
		if row.TargetType.Valid {
			entry.TargetType = &row.TargetType.String
		}
		if row.TargetID.Valid {
			entry.TargetId = &row.TargetID.String
		}
		if len(row.Metadata) > 0 {
			var metadata map[string]any
			if json.Unmarshal(row.Metadata, &metadata) == nil && len(metadata) > 0 {
				entry.Metadata = &metadata
			}
		}
		items = append(items, entry)
	}

	list := gen.AuditList{Items: items}
	if int32(len(rows)) == limit && len(rows) > 0 {
		next := httpx.EncodeCursor(idconv.FromPg(rows[len(rows)-1].ID))
		list.NextCursor = &next
	}
	return gen.ListAudit200JSONResponse(list), nil
}
