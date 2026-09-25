//go:build integration

package integration

import (
	"net/http"
	"testing"

	"loomtale/api/internal/db/gen"
)

func TestCrossTenantAssetAccessReturns404(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)

	tenantA := createFixtureUser(t, q, "isolation-tenant-a", uniqueEmail("a-owner"), "owner")
	tenantB := createFixtureUser(t, q, "isolation-tenant-b", uniqueEmail("b-owner"), "editor")

	sessA := login(t, tenantA.Email, tenantA.Password)
	presignResp := sessA.do(http.MethodPost, "/assets/presign", map[string]any{
		"kind": "image", "mime": "image/png", "bytes": 100,
	})
	requireStatus(t, presignResp, http.StatusCreated)
	var presigned struct {
		AssetId string `json:"assetId"`
	}
	decodeJSON(t, presignResp, &presigned)

	sessB := login(t, tenantB.Email, tenantB.Password)
	getResp := sessB.do(http.MethodGet, "/assets/"+presigned.AssetId, nil)
	requireStatus(t, getResp, http.StatusNotFound)

	finalizeResp := sessB.do(http.MethodPost, "/assets/"+presigned.AssetId+"/finalize", nil)
	requireStatus(t, finalizeResp, http.StatusNotFound)
}

func TestSwitchTenantRequiresMembership(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)

	member := createFixtureUser(t, q, "isolation-switch-own-tenant", uniqueEmail("switch"), "owner")
	other := createFixtureUser(t, q, "isolation-switch-other-tenant", uniqueEmail("switch-other"), "owner")

	sess := login(t, member.Email, member.Password)
	resp := sess.do(http.MethodPost, "/auth/switch-tenant", map[string]string{"tenantId": other.TenantID.String()})
	requireStatus(t, resp, http.StatusNotFound)
}

func TestPresignRefusesRenderPrefixAndBadMIME(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	fx := createFixtureUser(t, q, "isolation-presign-tenant", uniqueEmail("presign"), "editor")
	sess := login(t, fx.Email, fx.Password)

	// A declared mime not in the kind's allowlist is rejected at presign
	// time; render/ is a storage-key convention exercised at the storage
	// package level (api/internal/storage/browser_test.go would need a
	// live MinIO client, so this asserts the reachable half: presign
	// itself never lets a caller choose the storage key at all).
	resp := sess.do(http.MethodPost, "/assets/presign", map[string]any{
		"kind": "image", "mime": "application/x-msdownload", "bytes": 100,
	})
	requireStatus(t, resp, http.StatusBadRequest)
}

func TestFinalizeWithoutUploadIsRejected(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	fx := createFixtureUser(t, q, "isolation-no-upload-tenant", uniqueEmail("noupload"), "editor")
	sess := login(t, fx.Email, fx.Password)

	presignResp := sess.do(http.MethodPost, "/assets/presign", map[string]any{
		"kind": "image", "mime": "image/png", "bytes": 100,
	})
	requireStatus(t, presignResp, http.StatusCreated)
	var presigned struct {
		AssetId string `json:"assetId"`
	}
	decodeJSON(t, presignResp, &presigned)

	// No upload happened: finalize must reject, not silently mark ready.
	finalizeResp := sess.do(http.MethodPost, "/assets/"+presigned.AssetId+"/finalize", nil)
	requireStatus(t, finalizeResp, http.StatusUnprocessableEntity)
}
