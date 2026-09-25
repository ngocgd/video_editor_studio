// Package audit writes append-only entries to audit_log. Record must be
// called with a *gen.Queries scoped to the same transaction as the action
// being audited, so the audit entry and the action it describes commit or
// roll back together.
package audit

import (
	"context"
	"encoding/json"
	"net"
	"net/netip"

	"github.com/google/uuid"

	"loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// Entry describes one audit event. TenantID is nil for account-level
// events that precede tenant resolution (e.g. a failed login).
type Entry struct {
	TenantID    *uuid.UUID
	ActorUserID *uuid.UUID
	// ActorEmail denormalizes the actor's email at write time so the
	// entry stays readable after the user row itself is gone (audit_log
	// intentionally has no FK to users — see the audit migration).
	ActorEmail string
	Action     string
	TargetType string
	TargetID   string
	Metadata   map[string]any
	RemoteAddr string // net/http Request.RemoteAddr; parsed to strip the port
	UserAgent  string
}

// Record inserts e into audit_log. A nil or empty Metadata encodes as {}.
func Record(ctx context.Context, q *gen.Queries, e Entry) error {
	metadata := e.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return err
	}

	return q.InsertAuditLog(ctx, gen.InsertAuditLogParams{
		ID:          idconv.ToPg(idconv.NewV7()),
		TenantID:    idconv.ToPgPtr(e.TenantID),
		ActorUserID: idconv.ToPgPtr(e.ActorUserID),
		ActorEmail:  idconv.ToPgText(e.ActorEmail),
		Action:      e.Action,
		TargetType:  idconv.ToPgText(e.TargetType),
		TargetID:    idconv.ToPgText(e.TargetID),
		Metadata:    metadataJSON,
		Ip:          parseIP(e.RemoteAddr),
		UserAgent:   idconv.ToPgText(e.UserAgent),
	})
}

// parseIP strips a port from a net/http RemoteAddr and parses the
// remaining host as an IP; it returns nil (SQL NULL) if that fails, which
// happens for unix sockets or malformed values and must never abort the
// audit write itself.
func parseIP(remoteAddr string) *netip.Addr {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return nil
	}
	return &addr
}
