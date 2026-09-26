package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"loomtale/api/internal/audit"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/media"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/storage"
)

// Step and run kinds, and the scope both use.
const (
	KindCleanup    = "library.cleanup"
	KindTTLCleanup = "library.ttl_cleanup"
	ScopeTenant    = "tenant"
)

// deleteBatch is how many candidates of one kind a single pass deletes.
const deleteBatch = 500

// maxTTLPasses bounds one daily run; a larger backlog continues the next
// day.
const maxTTLPasses = 40

// CleanupInput is a cleanup step's input. A manual cleanup lists the
// exact candidates its dry run showed; the daily cleanup lists none and
// takes every expired candidate. Token makes each run's input hash
// unique, so a cleanup is never skipped as an unchanged step.
type CleanupInput struct {
	Token         string   `json:"token"`
	SegmentHashes []string `json:"segmentHashes,omitempty"`
	TakeIDs       []string `json:"takeIds,omitempty"`
}

func jsonInput(in CleanupInput) (json.RawMessage, error) {
	return json.Marshal(in)
}

// Result is what one cleanup deleted.
type Result struct {
	Segments int
	Takes    int
	Bytes    int64
	// ObjectErrors counts stored objects that could not be removed after
	// their rows were deleted (logged; they are orphans, not data loss).
	ObjectErrors int
}

// Deps is what the cleanup steps need; they run on a worker.
type Deps struct {
	Queries *dbgen.Queries
	Storage *storage.Internal
}

// Handlers returns the manual and daily cleanup step handlers.
func Handlers(d Deps) []pipeline.StepHandler {
	return []pipeline.StepHandler{&cleanupHandler{Deps: d, kind: KindCleanup}, &cleanupHandler{Deps: d, kind: KindTTLCleanup}}
}

// EstimateWith answers for this package's step kinds and asks next
// for every other kind, so one engine estimator covers all domains.
func EstimateWith(next pipeline.StepEstimator) pipeline.StepEstimator {
	return func(kind string) time.Duration {
		if kind == KindCleanup || kind == KindTTLCleanup {
			return Estimate(kind)
		}
		return next(kind)
	}
}

// Estimate is the per-kind duration estimate.
func Estimate(kind string) time.Duration {
	switch kind {
	case KindCleanup, KindTTLCleanup:
		return time.Minute
	default:
		return pipeline.DefaultStepEstimate(kind)
	}
}

type cleanupHandler struct {
	Deps
	kind string
}

func (h *cleanupHandler) Kind() string { return h.kind }

func (h *cleanupHandler) Queue(context.Context, pipeline.StepRef) (string, error) {
	return pipeline.QueueCPU, nil
}

func (h *cleanupHandler) InputHash(_ context.Context, s pipeline.StepRef) (string, error) {
	sum := sha256.Sum256(s.Input)
	return hex.EncodeToString(sum[:]), nil
}

func (h *cleanupHandler) ModelRef(context.Context, pipeline.StepRef) (*pipeline.ModelRef, error) {
	return nil, nil
}

func (h *cleanupHandler) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	if h.Queries == nil || h.Storage == nil {
		return nil, errors.New("library: cleanup runs on a worker with storage access")
	}
	var in CleanupInput
	if err := sc.Input(&in); err != nil {
		return nil, fmt.Errorf("%w: cleanup input: %w", pipeline.ErrValidation, err)
	}
	tenantID := sc.Tenant()
	settings, err := loadSettings(ctx, h.Queries, tenantID)
	if err != nil {
		return nil, err
	}
	c := cleaner{Deps: h.Deps, tenant: tenantID, settings: settings, log: sc.Log}
	var res Result
	if h.kind == KindCleanup {
		res, err = c.manual(ctx, in)
	} else {
		res, err = c.expired(ctx)
	}
	if err != nil {
		return nil, err
	}
	sc.Log(fmt.Sprintf("deleted %d segments and %d takes, %d bytes", res.Segments, res.Takes, res.Bytes))
	if h.kind == KindTTLCleanup && res.Segments+res.Takes > 0 {
		if err := audit.Record(ctx, h.Queries, audit.Entry{
			TenantID: &tenantID, Action: "library.ttl_cleanup", TargetType: "pipeline_run", TargetID: sc.RunID().String(),
			Metadata: map[string]any{"segments": res.Segments, "takes": res.Takes, "bytes": res.Bytes},
		}); err != nil {
			slog.WarnContext(ctx, "library: audit daily cleanup", "error", err)
		}
	}
	return pipeline.Output{"segments": res.Segments, "takes": res.Takes, "bytes": res.Bytes, "objectErrors": res.ObjectErrors}, nil
}

// cleaner deletes one tenant's candidates.
type cleaner struct {
	Deps
	tenant   uuid.UUID
	settings Settings
	log      func(string)
}

// manual deletes the listed candidates that are still expired.
func (c cleaner) manual(ctx context.Context, in CleanupInput) (Result, error) {
	var res Result
	for start := 0; start < len(in.SegmentHashes); start += deleteBatch {
		part := in.SegmentHashes[start:min(start+deleteBatch, len(in.SegmentHashes))]
		if _, err := c.segments(ctx, part, &res); err != nil {
			return res, err
		}
	}
	for start := 0; start < len(in.TakeIDs); start += deleteBatch {
		ids := make([]pgtype.UUID, 0, deleteBatch)
		for _, s := range in.TakeIDs[start:min(start+deleteBatch, len(in.TakeIDs))] {
			id, err := uuid.Parse(s)
			if err != nil {
				return res, fmt.Errorf("%w: take id %q", pipeline.ErrValidation, s)
			}
			ids = append(ids, idconv.ToPg(id))
		}
		if _, err := c.takes(ctx, ids, &res); err != nil {
			return res, err
		}
	}
	return res, nil
}

// expired deletes every expired candidate, a batch at a time.
func (c cleaner) expired(ctx context.Context) (Result, error) {
	var res Result
	for range maxTTLPasses {
		before := res.Segments + res.Takes
		nSeg, err := c.segments(ctx, nil, &res)
		if err != nil {
			return res, err
		}
		nTake, err := c.takes(ctx, nil, &res)
		if err != nil {
			return res, err
		}
		// Done when a pass found less than a full batch, or deleted nothing
		// (the rest gained references since they were listed).
		if (nSeg < deleteBatch && nTake < deleteBatch) || res.Segments+res.Takes == before {
			break
		}
	}
	return res, nil
}

// segments deletes one batch of expired segments (limited to hashes when
// set) and returns how many candidates it found.
func (c cleaner) segments(ctx context.Context, hashes []string, res *Result) (int, error) {
	rows, err := c.Queries.ListExpiredSegments(ctx, dbgen.ListExpiredSegmentsParams{
		TenantID: idconv.ToPg(c.tenant), InputHashes: hashes,
		TtlDays: int32(c.settings.SegmentTTLDays), MaxRows: deleteBatch, //nolint:gosec // 1..365
	})
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	ids := make([]pgtype.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.AssetID
	}
	n, err := c.deleteAssets(ctx, ids, res)
	res.Segments += n
	return len(rows), err
}

// takes deletes one batch of expired takes (limited to ids when set) and
// returns how many candidates it found.
func (c cleaner) takes(ctx context.Context, ids []pgtype.UUID, res *Result) (int, error) {
	rows, err := c.Queries.ListExpiredTakes(ctx, dbgen.ListExpiredTakesParams{
		TenantID: idconv.ToPg(c.tenant), TakeIds: ids,
		TtlDays: int32(c.settings.TakeTTLDays), MaxRows: deleteBatch, //nolint:gosec // 1..365
	})
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	assetIDs := make([]pgtype.UUID, len(rows))
	for i, r := range rows {
		assetIDs[i] = r.AssetID
	}
	n, err := c.deleteAssets(ctx, assetIDs, res)
	res.Takes += n
	return len(rows), err
}

// deleteAssets drops the asset rows (re-checked against new references
// in the same statement), then every stored version of their objects and
// variants. It returns how many rows it deleted.
func (c cleaner) deleteAssets(ctx context.Context, ids []pgtype.UUID, res *Result) (int, error) {
	deleted, err := c.Queries.DeleteLibraryAssets(ctx, dbgen.DeleteLibraryAssetsParams{TenantID: idconv.ToPg(c.tenant), AssetIds: ids})
	if err != nil {
		return 0, err
	}
	for _, d := range deleted {
		res.Bytes += d.Bytes
		for _, key := range objectKeys(d.StorageKey, d.Variants) {
			if !storage.IsTenantKey(c.tenant.String(), key) {
				continue // never touch another tenant's prefix
			}
			if err := c.Storage.RemoveAllVersions(ctx, key); err != nil {
				res.ObjectErrors++
				slog.WarnContext(ctx, "library: remove object", "error", err, "key", key)
			}
		}
	}
	return len(deleted), nil
}

// objectKeys is an asset's own key plus its variant and peaks keys.
func objectKeys(key string, variants []byte) []string {
	keys := []string{key}
	v := media.DecodeVariants(variants)
	for _, m := range []map[string]string{v.WebP, v.AVIF} {
		for _, k := range m {
			keys = append(keys, k)
		}
	}
	if v.Peaks != "" {
		keys = append(keys, v.Peaks)
	}
	return keys
}
