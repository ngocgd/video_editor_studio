// Package assetsapi implements the assets slice of the generated strict
// server interface: presign, finalize, get and list.
package assetsapi

import (
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/dbpool"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/storage"
)

// AssetsAPI implements the assets slice of gen.StrictServerInterface.
// Named distinctly (not "Handler") so the composite server in cmd/api can
// embed every domain handler without a field-name collision.
type AssetsAPI struct {
	Pool     *dbpool.Pool
	Queries  *dbgen.Queries
	Internal *storage.Internal
	Browser  *storage.Browser
}

func toAssetDTO(a dbgen.Asset, downloadURL *string) gen.Asset {
	dto := gen.Asset{
		Id:        idconv.FromPg(a.ID),
		Kind:      gen.AssetKind(a.Kind),
		Mime:      a.Mime,
		Status:    gen.AssetStatus(a.Status),
		CreatedAt: a.CreatedAt.Time,
	}
	if a.Bytes.Valid {
		dto.Bytes = &a.Bytes.Int64
	}
	if a.Sha256.Valid {
		dto.Sha256 = &a.Sha256.String
	}
	if a.Width.Valid {
		w := int(a.Width.Int32)
		dto.Width = &w
	}
	if a.Height.Valid {
		h := int(a.Height.Int32)
		dto.Height = &h
	}
	if a.DurationMs.Valid {
		d := int(a.DurationMs.Int32)
		dto.DurationMs = &d
	}
	dto.DownloadUrl = downloadURL
	return dto
}
