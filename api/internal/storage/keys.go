package storage

import (
	"strings"

	"github.com/google/uuid"
)

// Key builds the storage key for an asset: t/{tenant}/{kind}/{assetID}.
func Key(tenantID, kind string, assetID uuid.UUID) string {
	return "t/" + tenantID + "/" + kind + "/" + assetID.String()
}

// RenderKey builds a workers-only render output key.
func RenderKey(tenantID string, rest string) string {
	return renderPrefix(tenantID) + strings.TrimPrefix(rest, "/")
}

func renderPrefix(tenantID string) string {
	return "t/" + tenantID + "/render/"
}

// IsRenderKey reports whether key falls under the tenant's workers-only
// render/ prefix.
func IsRenderKey(tenantID, key string) bool {
	return strings.HasPrefix(key, renderPrefix(tenantID))
}
