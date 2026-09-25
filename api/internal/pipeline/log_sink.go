package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/storage"
)

// AssetLogSink flushes a step's scrubbed log buffer straight to object
// storage and records it as a "document" asset, reusing the same assets
// table phase 2 built rather than inventing a parallel log store. Unlike
// a browser upload, this is a direct server-side write (no presign round
// trip): the content is already fully known in memory.
type AssetLogSink struct {
	Queries *dbgen.Queries
	Storage *storage.Internal
}

var _ logSink = (*AssetLogSink)(nil)

// FlushLog implements logSink.
func (s *AssetLogSink) FlushLog(ctx context.Context, tenantIDStr, stepIDStr string, attempt int32, body []byte) (string, error) {
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		return "", fmt.Errorf("pipeline: invalid tenant id for log flush: %w", err)
	}
	key := fmt.Sprintf("pipeline-logs/%s/%s/%d.log", tenantIDStr, stepIDStr, attempt)
	sum := sha256.Sum256(body)

	info, err := s.Storage.PutObject(ctx, s.Storage.Bucket, key, bytes.NewReader(body), int64(len(body)), minio.PutObjectOptions{
		ContentType: "text/plain; charset=utf-8",
	})
	if err != nil {
		return "", fmt.Errorf("pipeline: upload step log: %w", err)
	}

	tid := idconv.ToPg(tenantID)
	assetID := idconv.ToPg(idconv.NewV7())
	asset, err := s.Queries.CreateAsset(ctx, dbgen.CreateAssetParams{
		ID:         assetID,
		TenantID:   tid,
		Kind:       "document",
		StorageKey: key,
		Mime:       "text/plain; charset=utf-8",
		CreatedBy:  idconv.ToPgPtr(nil),
	})
	if err != nil {
		return "", fmt.Errorf("pipeline: record log asset: %w", err)
	}
	if _, err := s.Queries.MarkAssetReady(ctx, dbgen.MarkAssetReadyParams{
		TenantID:         tid,
		ID:               asset.ID,
		Bytes:            idconv.ToPgInt8(int64(len(body))),
		Sha256:           idconv.ToPgText(hex.EncodeToString(sum[:])),
		Mime:             "text/plain; charset=utf-8",
		StorageVersionID: idconv.ToPgText(info.VersionID),
	}); err != nil {
		return "", fmt.Errorf("pipeline: mark log asset ready: %w", err)
	}
	return idconv.FromPg(asset.ID).String(), nil
}
