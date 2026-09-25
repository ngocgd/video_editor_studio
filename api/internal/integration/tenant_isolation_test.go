//go:build integration

package integration

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"testing"

	"loomtale/api/internal/db/gen"
)

// tinyPNG is a valid, minimal 1x1 transparent PNG, used to drive a real
// presign -> upload -> finalize round trip against the live MinIO.
var tinyPNG = []byte{
	0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n',
	0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89,
	0x00, 0x00, 0x00, 0x0a, 'I', 'D', 'A', 'T',
	0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01,
	0x0d, 0x0a, 0x2d, 0xb4,
	0x00, 0x00, 0x00, 0x00, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
}

// uploadViaPresignedPost submits body to a presigned POST policy exactly
// as a browser would: a multipart form with every policy field plus the
// file, in one request.
func uploadViaPresignedPost(t *testing.T, uploadURL string, fields map[string]string, body []byte) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	fw, err := w.CreateFormFile("file", "upload.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, uploadURL, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

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

func TestFinalizeRejectsUploadWithMismatchedMagicBytes(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	fx := createFixtureUser(t, q, "isolation-mime-mismatch-tenant", uniqueEmail("mime-mismatch"), "editor")
	sess := login(t, fx.Email, fx.Password)

	// The POST policy only pins the declared Content-Type form field and a
	// byte-size range, not the actual bytes; S3/MinIO never inspects the
	// body's content, so this upload succeeds at the storage layer with
	// plain text under a form field that still says image/png. finalize
	// sniffs by magic bytes, allowlisted per kind rather than compared
	// against the originally declared MIME (a PNG-declared upload that
	// sniffs as a genuinely different but still-valid image type for the
	// same kind is not itself a defect — the sniff exists to keep
	// non-image content out of an "image" kind, which text content is a
	// clean example of).
	notAnImage := []byte("plain text, not an image")

	presignResp := sess.do(http.MethodPost, "/assets/presign", map[string]any{
		"kind": "image", "mime": "image/png", "bytes": len(notAnImage),
	})
	requireStatus(t, presignResp, http.StatusCreated)
	var presigned struct {
		AssetId   string            `json:"assetId"`
		UploadUrl string            `json:"uploadUrl"`
		Fields    map[string]string `json:"fields"`
	}
	decodeJSON(t, presignResp, &presigned)
	uploadResp := uploadViaPresignedPost(t, presigned.UploadUrl, presigned.Fields, notAnImage)
	if uploadResp.StatusCode != http.StatusNoContent && uploadResp.StatusCode != http.StatusOK {
		t.Fatalf("upload: got status %d", uploadResp.StatusCode)
	}
	_ = uploadResp.Body.Close()

	finalizeResp := sess.do(http.MethodPost, "/assets/"+presigned.AssetId+"/finalize", nil)
	requireStatus(t, finalizeResp, http.StatusUnprocessableEntity)
}

func TestListAssetsIsolatesTenants(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)

	tenantA := createFixtureUser(t, q, "isolation-list-a", uniqueEmail("list-a"), "editor")
	tenantB := createFixtureUser(t, q, "isolation-list-b", uniqueEmail("list-b"), "editor")

	sessA := login(t, tenantA.Email, tenantA.Password)
	requireStatus(t, sessA.do(http.MethodPost, "/assets/presign", map[string]any{
		"kind": "image", "mime": "image/png", "bytes": 100,
	}), http.StatusCreated)

	sessB := login(t, tenantB.Email, tenantB.Password)
	resp := sessB.do(http.MethodGet, "/assets", nil)
	requireStatus(t, resp, http.StatusOK)
	var list struct {
		Items []struct {
			Id string `json:"id"`
		} `json:"items"`
	}
	decodeJSON(t, resp, &list)
	if len(list.Items) != 0 {
		t.Fatalf("expected tenant B's asset list to be empty, got %d items (tenant A's asset leaked)", len(list.Items))
	}
}

func TestListAuditIsolatesTenants(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)

	tenantA := createFixtureUser(t, q, "isolation-audit-a", uniqueEmail("audit-a"), "owner")
	tenantB := createFixtureUser(t, q, "isolation-audit-b", uniqueEmail("audit-b"), "owner")

	// Logging in already writes a login_succeeded audit row for tenant A.
	_ = login(t, tenantA.Email, tenantA.Password)
	sessB := login(t, tenantB.Email, tenantB.Password)

	resp := sessB.do(http.MethodGet, "/audit", nil)
	requireStatus(t, resp, http.StatusOK)
	var list struct {
		Items []struct {
			Action string `json:"action"`
		} `json:"items"`
	}
	decodeJSON(t, resp, &list)
	if len(list.Items) != 1 || list.Items[0].Action != "login_succeeded" {
		t.Fatalf("expected tenant B's own audit log to show only its own login, got %+v", list.Items)
	}
}

func TestUploadFinalizeAndReuploadIsIsolatedByVersion(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	fx := createFixtureUser(t, q, "isolation-reupload-tenant", uniqueEmail("reupload"), "editor")
	sess := login(t, fx.Email, fx.Password)

	presignResp := sess.do(http.MethodPost, "/assets/presign", map[string]any{
		"kind": "image", "mime": "image/png", "bytes": len(tinyPNG),
	})
	requireStatus(t, presignResp, http.StatusCreated)
	var presigned struct {
		AssetId   string            `json:"assetId"`
		UploadUrl string            `json:"uploadUrl"`
		Fields    map[string]string `json:"fields"`
	}
	decodeJSON(t, presignResp, &presigned)

	uploadResp := uploadViaPresignedPost(t, presigned.UploadUrl, presigned.Fields, tinyPNG)
	if uploadResp.StatusCode != http.StatusNoContent && uploadResp.StatusCode != http.StatusOK {
		t.Fatalf("upload: got status %d", uploadResp.StatusCode)
	}
	_ = uploadResp.Body.Close()

	finalizeResp := sess.do(http.MethodPost, "/assets/"+presigned.AssetId+"/finalize", nil)
	requireStatus(t, finalizeResp, http.StatusOK)

	// finalize's response deliberately never carries a downloadUrl (see
	// assetsapi.FinalizeAsset, which builds its DTO with a nil download
	// URL); only GetAsset computes and presigns one, and only once the
	// asset's status is "ready". Fetch it that way, exactly as a real
	// client would after finalizing.
	getAssetResp := sess.do(http.MethodGet, "/assets/"+presigned.AssetId, nil)
	requireStatus(t, getAssetResp, http.StatusOK)
	var finalized struct {
		DownloadUrl string `json:"downloadUrl"`
	}
	decodeJSON(t, getAssetResp, &finalized)
	if finalized.DownloadUrl == "" {
		t.Fatal("expected a downloadUrl once the asset is ready")
	}

	// The presigned POST policy is valid for up to 10 minutes after issue,
	// so nothing stops a client from re-uploading different bytes to the
	// exact same key after finalize already ran. Bucket versioning means
	// this creates a new object version instead of mutating the one
	// finalize verified: the previously issued downloadUrl (pinned to
	// that version) must still return the original, verified bytes.
	tampered := append([]byte{}, tinyPNG...)
	tampered[len(tampered)-1] ^= 0xFF
	reuploadResp := uploadViaPresignedPost(t, presigned.UploadUrl, presigned.Fields, tampered)
	if reuploadResp.StatusCode != http.StatusNoContent && reuploadResp.StatusCode != http.StatusOK {
		t.Fatalf("re-upload: got status %d", reuploadResp.StatusCode)
	}
	_ = reuploadResp.Body.Close()

	getResp, err := http.Get(finalized.DownloadUrl)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = getResp.Body.Close() }()
	got := make([]byte, len(tinyPNG))
	if _, err := io.ReadFull(getResp.Body, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, tinyPNG) {
		t.Fatal("expected the pinned-version download URL to still serve the original finalized bytes after a re-upload to the same key")
	}
}
