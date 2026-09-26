package render

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/storage"
)

// Cache kinds, matching render_segments.kind.
const (
	cacheBody       = "body"
	cacheTransition = "transition"
	cacheAudio      = "audio"
	cacheSubtitles  = "subtitles"
	cachePreview    = "preview"
)

// cached is a render object whose stored bytes were verified against
// the checksum recorded when it was written.
type cached struct {
	AssetID uuid.UUID
	Key     string
	Version string
	SHA256  string
	Frames  int
}

// lookup returns the cache entry for hash, or nil when there is none or
// it no longer verifies. An entry is reused only while the object's
// stored x-amz-checksum-sha256 equals assets.sha256; anything else (a
// missing object, a missing or different checksum) deletes the asset row,
// which drops the cache row with it, so the caller encodes afresh.
func (d Deps) lookup(ctx context.Context, j *job, hash string, logf func(string)) (*cached, error) {
	row, err := d.Queries.GetRenderSegment(ctx, dbgen.GetRenderSegmentParams{TenantID: idconv.ToPg(j.Tenant), InputHash: hash})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sum, err := d.Storage.ObjectSHA256(ctx, row.StorageKey, row.StorageVersionID.String)
	if err != nil && !errors.Is(err, storage.ErrObjectNotFound) {
		return nil, err
	}
	if err == nil && row.Status == "ready" && row.Sha256.Valid && row.Sha256.String != "" && sum == row.Sha256.String {
		if err := d.Queries.TouchRenderSegments(ctx, dbgen.TouchRenderSegmentsParams{TenantID: idconv.ToPg(j.Tenant), InputHashes: []string{hash}}); err != nil {
			return nil, err
		}
		return &cached{AssetID: idconv.FromPg(row.AssetID), Key: row.StorageKey, Version: row.StorageVersionID.String, SHA256: sum, Frames: int(row.DurationFrames)}, nil
	}
	logf(fmt.Sprintf("cached %s %s failed its checksum check; encoding it again", row.Kind, shortHash(hash)))
	if err := d.Queries.DeleteAssetRow(ctx, dbgen.DeleteAssetRowParams{TenantID: idconv.ToPg(j.Tenant), ID: row.AssetID}); err != nil {
		return nil, err
	}
	if storage.IsRenderKey(j.Tenant.String(), row.StorageKey) {
		_ = d.Storage.RemoveVersion(ctx, row.StorageKey, row.StorageVersionID.String)
	}
	return nil, nil
}

// storeObject is one encoded render object to put in the cache.
type storeObject struct {
	Hash       string
	CacheKind  string
	AssetKind  string
	Path       string
	Ext        string
	Mime       string
	Frames     int
	DurationMs int64
}

// store uploads a freshly encoded object with its checksum and records
// it as the cache entry for its hash. When another step stored the same
// hash first (a superseded run still finishing), that entry wins and
// this upload's object version is dropped.
func (d Deps) store(ctx context.Context, j *job, o storeObject, logf func(string)) (*cached, error) {
	key := j.segmentKey(o.Hash, o.Ext)
	stored, err := d.Storage.PutFileChecksummed(ctx, key, o.Path, o.Mime)
	if err != nil {
		return nil, err
	}
	params := dbgen.CreateDerivedAssetParams{
		ID: idconv.ToPg(idconv.NewV7()), TenantID: idconv.ToPg(j.Tenant), Kind: o.AssetKind, StorageKey: key, Mime: o.Mime,
		Bytes: idconv.ToPgInt8(stored.Size), Sha256: idconv.ToPgText(stored.SHA256Hex), StorageVersionID: idconv.ToPgText(stored.VersionID),
	}
	if o.DurationMs > 0 {
		params.DurationMs = idconv.ToPgInt4(int32(o.DurationMs))
	}
	if o.AssetKind == "video" {
		params.Width, params.Height = idconv.ToPgInt4(int32(j.Manifest.Settings.Width)), idconv.ToPgInt4(int32(j.Manifest.Settings.Height))
	}
	asset, err := d.Queries.CreateDerivedAsset(ctx, params)
	if isUniqueViolation(err) {
		_ = d.Storage.RemoveVersion(ctx, key, stored.VersionID)
		existing, lerr := d.lookup(ctx, j, o.Hash, logf)
		if lerr != nil || existing != nil {
			return existing, lerr
		}
		return nil, fmt.Errorf("render: %s is being written by another step: %w", shortHash(o.Hash), err)
	}
	if err != nil {
		return nil, err
	}
	if _, err := d.Queries.UpsertRenderSegment(ctx, dbgen.UpsertRenderSegmentParams{
		TenantID: idconv.ToPg(j.Tenant), InputHash: o.Hash, Kind: o.CacheKind, EpisodeID: idconv.ToPg(j.Episode),
		Lang: j.Lang, AssetID: asset.ID, DurationFrames: int32(o.Frames),
	}); err != nil {
		return nil, err
	}
	return &cached{AssetID: idconv.FromPg(asset.ID), Key: key, Version: stored.VersionID, SHA256: stored.SHA256Hex, Frames: o.Frames}, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
