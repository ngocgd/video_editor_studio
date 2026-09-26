package render

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/media/ffmpeg"
	"loomtale/api/internal/pipeline"
)

// maxAlignBytes caps an alignment document read into memory.
const maxAlignBytes = 4 << 20

// newTempDir is a step's private scratch directory (os.MkdirTemp creates
// it 0700); the caller removes it on success and failure alike.
func newTempDir(prefix string) (string, func(), error) {
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", nil, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

// manifestAsset reads a ready asset a manifest names.
func (d Deps) manifestAsset(ctx context.Context, j *job, id string) (dbgen.Asset, error) {
	assetID, err := uuid.Parse(id)
	if err != nil {
		return dbgen.Asset{}, fmt.Errorf("%w: manifest names an invalid asset", pipeline.ErrValidation)
	}
	a, err := d.Queries.GetAssetByID(ctx, dbgen.GetAssetByIDParams{TenantID: idconv.ToPg(j.Tenant), ID: idconv.ToPg(assetID)})
	if err != nil {
		return dbgen.Asset{}, fmt.Errorf("%w: manifest asset %s: %v", pipeline.ErrValidation, assetID, err)
	}
	if a.Status != "ready" {
		return dbgen.Asset{}, fmt.Errorf("%w: manifest asset %s is not ready", pipeline.ErrValidation, assetID)
	}
	return a, nil
}

// fetch downloads a manifest asset into dir as name plus the extension
// of its type, pinned to the version the asset row recorded, and returns
// the local path and the demuxer ffmpeg must use for it.
func (d Deps) fetch(ctx context.Context, j *job, dir, id, name string) (string, ffmpeg.Format, error) {
	a, err := d.manifestAsset(ctx, j, id)
	if err != nil {
		return "", "", err
	}
	format, ok := ffmpeg.FormatForMIME(a.Mime)
	if !ok {
		return "", "", fmt.Errorf("%w: asset %s has unsupported type %s", pipeline.ErrValidation, id, a.Mime)
	}
	path := filepath.Join(dir, name+extensionFor(a.Mime))
	if err := d.Storage.DownloadTo(ctx, a.StorageKey, a.StorageVersionID.String, path); err != nil {
		return "", "", fmt.Errorf("render: download %s: %w", name, err)
	}
	return path, format, nil
}

// fetchCached downloads a verified cache object into dir as name.
func (d Deps) fetchCached(ctx context.Context, c *cached, dir, name string) (string, error) {
	path := filepath.Join(dir, name)
	if err := d.Storage.DownloadTo(ctx, c.Key, c.Version, path); err != nil {
		return "", fmt.Errorf("render: download %s: %w", name, err)
	}
	return path, nil
}

// alignDoc reads scene i's alignment take.
func (d Deps) alignDoc(ctx context.Context, j *job, i int) (AlignDoc, error) {
	s := j.Manifest.Scenes[i]
	if s.AlignAssetID == "" {
		return AlignDoc{}, nil
	}
	a, err := d.manifestAsset(ctx, j, s.AlignAssetID)
	if err != nil {
		return AlignDoc{}, err
	}
	data, err := d.Storage.ReadAll(ctx, a.StorageKey, a.StorageVersionID.String, maxAlignBytes)
	if err != nil {
		return AlignDoc{}, fmt.Errorf("render: read alignment of scene %d: %w", s.Idx, err)
	}
	doc, err := ParseAlignDoc(data)
	if err != nil {
		return AlignDoc{}, fmt.Errorf("%w: alignment of scene %d: %v", pipeline.ErrValidation, s.Idx, err)
	}
	return doc, nil
}

// localCues returns scene i's cues on the scene's own clock.
func (d Deps) localCues(ctx context.Context, j *job, i int) ([]Cue, error) {
	doc, err := d.alignDoc(ctx, j, i)
	if err != nil {
		return nil, err
	}
	return SceneCues(doc, 0, FrameMs(j.Timeline.Scenes[i].Frames, j.Timeline.FPS)), nil
}

func extensionFor(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/flac":
		return ".flac"
	case "audio/mpeg":
		return ".mp3"
	default:
		return ".bin"
	}
}

// writeFile writes a step's own scratch file.
func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}
