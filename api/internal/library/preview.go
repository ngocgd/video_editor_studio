package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"time"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// Candidate is one thing a cleanup would delete.
type Candidate struct {
	// ID is the segment's input hash or the take's id.
	ID      string
	Kind    string
	AssetID uuid.UUID
	Bytes   int64
	// Since is when a segment was last used or a take was created.
	Since time.Time
}

// Preview is a dry run: what a cleanup would delete right now under the
// tenant's retention, and the token that confirms exactly this set.
type Preview struct {
	Settings Settings
	Segments []Candidate
	Takes    []Candidate
	Bytes    int64
	// Truncated is true when a list hit PreviewLimit; confirming cleans
	// the listed part and a later run the rest.
	Truncated bool
	Token     string
}

// Preview lists the current cleanup candidates without deleting anything.
func (s *Service) Preview(ctx context.Context, tenantID uuid.UUID) (Preview, error) {
	settings, err := loadSettings(ctx, s.Queries, tenantID)
	if err != nil {
		return Preview{}, err
	}
	segs, err := s.Queries.ListExpiredSegments(ctx, dbgen.ListExpiredSegmentsParams{
		TenantID: idconv.ToPg(tenantID), TtlDays: int32(settings.SegmentTTLDays), MaxRows: PreviewLimit, //nolint:gosec // 1..365
	})
	if err != nil {
		return Preview{}, err
	}
	takes, err := s.Queries.ListExpiredTakes(ctx, dbgen.ListExpiredTakesParams{
		TenantID: idconv.ToPg(tenantID), TtlDays: int32(settings.TakeTTLDays), MaxRows: PreviewLimit, //nolint:gosec // 1..365
	})
	if err != nil {
		return Preview{}, err
	}
	p := Preview{Settings: settings, Truncated: len(segs) == PreviewLimit || len(takes) == PreviewLimit}
	for _, r := range segs {
		p.Segments = append(p.Segments, Candidate{ID: r.InputHash, Kind: r.Kind, AssetID: idconv.FromPg(r.AssetID), Bytes: r.Bytes, Since: r.LastUsedAt.Time})
		p.Bytes += r.Bytes
	}
	for _, r := range takes {
		p.Takes = append(p.Takes, Candidate{ID: idconv.FromPg(r.TakeID).String(), Kind: r.Kind, AssetID: idconv.FromPg(r.AssetID), Bytes: r.Bytes, Since: r.CreatedAt.Time})
		p.Bytes += r.Bytes
	}
	p.Token = previewToken(settings, p.Segments, p.Takes)
	return p, nil
}

// previewToken identifies a candidate set under a retention setting,
// independent of list order.
func previewToken(settings Settings, segs, takes []Candidate) string {
	ids := func(cs []Candidate) []string {
		out := make([]string, len(cs))
		for i, c := range cs {
			out[i] = c.ID
		}
		sort.Strings(out)
		return out
	}
	h := sha256.New()
	_, _ = h.Write([]byte{byte(settings.SegmentTTLDays), byte(settings.SegmentTTLDays >> 8), byte(settings.TakeTTLDays), byte(settings.TakeTTLDays >> 8)})
	for _, group := range [][]string{ids(segs), ids(takes)} {
		for _, id := range group {
			_, _ = h.Write([]byte(id))
			_, _ = h.Write([]byte{0})
		}
		_, _ = h.Write([]byte{1})
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}
