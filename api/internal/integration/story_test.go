//go:build integration

// Package integration story tests exercise the phase 6 story-writer slice
// (series/bible/episodes/drafts/imports/LLM settings) over the same live
// HTTP+Postgres stack the phase 2 suite uses.
package integration

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"testing"

	"loomtale/api/internal/db/gen"
)

func createSeries(t *testing.T, sess *session) string {
	t.Helper()
	resp := sess.do(http.MethodPost, "/series", map[string]any{
		"title":                "Integration Test Series",
		"targetLanguages":      []string{"en"},
		"targetEpisodeMinutes": 30,
		"plannedEpisodeCount":  1,
	})
	requireStatus(t, resp, http.StatusCreated)
	var series struct {
		Id string `json:"id"`
	}
	decodeJSON(t, resp, &series)
	return series.Id
}

func TestSeriesCRUD(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	fx := createFixtureUser(t, q, "story-series-crud", uniqueEmail("series-crud"), "editor")
	sess := login(t, fx.Email, fx.Password)

	seriesID := createSeries(t, sess)

	getResp := sess.do(http.MethodGet, "/series/"+seriesID, nil)
	requireStatus(t, getResp, http.StatusOK)
	var got struct {
		Title  string `json:"title"`
		Status string `json:"status"`
	}
	decodeJSON(t, getResp, &got)
	if got.Title != "Integration Test Series" {
		t.Fatalf("title = %q", got.Title)
	}

	updateResp := sess.do(http.MethodPatch, "/series/"+seriesID, map[string]any{
		"title":                "Renamed Series",
		"targetLanguages":      []string{"en", "vi"},
		"targetEpisodeMinutes": 45,
		"plannedEpisodeCount":  2,
	})
	requireStatus(t, updateResp, http.StatusOK)

	bibleResp := sess.do(http.MethodGet, "/series/"+seriesID+"/bible", nil)
	requireStatus(t, bibleResp, http.StatusOK)
}

// TestDraftPatchVersionConflict is the paragraph-ops CAS regression test:
// a PATCH whose expectedVersion is stale must return 409, never silently
// overwrite a concurrent edit.
func TestDraftPatchVersionConflict(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	fx := createFixtureUser(t, q, "story-draft-conflict", uniqueEmail("draft-conflict"), "editor")
	sess := login(t, fx.Email, fx.Password)

	seriesID := createSeries(t, sess)
	episodeResp := sess.do(http.MethodPost, "/episodes?seriesId="+seriesID, nil)
	requireStatus(t, episodeResp, http.StatusCreated)
	var episode struct {
		Id string `json:"id"`
	}
	decodeJSON(t, episodeResp, &episode)

	// No draft exists yet for "en": patching it must report a conflict,
	// not a crash, since PatchDraft's contract has no 404 variant.
	patchResp := sess.do(http.MethodPatch, "/episodes/"+episode.Id+"/drafts/en", map[string]any{
		"expectedVersion": 0,
		"ops": []map[string]any{
			{"op": "upsert", "paragraphId": "p1", "text": "hello"},
		},
	})
	requireStatus(t, patchResp, http.StatusConflict)
}

// TestTenantIsolationOnSeriesEpisodeDraft asserts tenant B gets 404 (not
// a data leak) for every story resource created under tenant A.
func TestTenantIsolationOnSeriesEpisodeDraft(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	tenantA := createFixtureUser(t, q, "story-isolation-a", uniqueEmail("story-a"), "editor")
	tenantB := createFixtureUser(t, q, "story-isolation-b", uniqueEmail("story-b"), "editor")

	sessA := login(t, tenantA.Email, tenantA.Password)
	seriesID := createSeries(t, sessA)
	episodeResp := sessA.do(http.MethodPost, "/episodes?seriesId="+seriesID, nil)
	requireStatus(t, episodeResp, http.StatusCreated)
	var episode struct {
		Id string `json:"id"`
	}
	decodeJSON(t, episodeResp, &episode)

	sessB := login(t, tenantB.Email, tenantB.Password)
	requireStatus(t, sessB.do(http.MethodGet, "/series/"+seriesID, nil), http.StatusNotFound)
	requireStatus(t, sessB.do(http.MethodGet, "/series/"+seriesID+"/bible", nil), http.StatusNotFound)
	requireStatus(t, sessB.do(http.MethodGet, "/episodes/"+episode.Id, nil), http.StatusNotFound)
	requireStatus(t, sessB.do(http.MethodGet, "/episodes/"+episode.Id+"/drafts/en", nil), http.StatusNotFound)
}

// uploadTextAsset drives a real presign -> upload -> finalize round trip
// for a document asset, returning its id.
func uploadTextAsset(t *testing.T, sess *session, body []byte, mime string) string {
	t.Helper()
	presignResp := sess.do(http.MethodPost, "/assets/presign", map[string]any{
		"kind": "document", "mime": mime, "bytes": len(body),
	})
	requireStatus(t, presignResp, http.StatusCreated)
	var presigned struct {
		AssetId   string            `json:"assetId"`
		UploadUrl string            `json:"uploadUrl"`
		Fields    map[string]string `json:"fields"`
	}
	decodeJSON(t, presignResp, &presigned)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range presigned.Fields {
		_ = w.WriteField(k, v)
	}
	fw, err := w.CreateFormFile("file", "manuscript.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(body); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	req, err := http.NewRequest(http.MethodPost, presigned.UploadUrl, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	uploadResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusNoContent && uploadResp.StatusCode != http.StatusOK {
		t.Fatalf("upload: got status %d", uploadResp.StatusCode)
	}

	finalizeResp := sess.do(http.MethodPost, "/assets/"+presigned.AssetId+"/finalize", nil)
	requireStatus(t, finalizeResp, http.StatusOK)
	return presigned.AssetId
}

// TestImportUTF8RoundTripsThroughPreviewAndCommit covers a plain UTF-8
// manuscript end to end: register, preview (encoding detected, chapters
// split), commit (episodes + drafts created, tainted).
func TestImportUTF8RoundTripsThroughPreviewAndCommit(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	fx := createFixtureUser(t, q, "story-import-utf8", uniqueEmail("import-utf8"), "editor")
	sess := login(t, fx.Email, fx.Password)
	seriesID := createSeries(t, sess)

	manuscript := []byte("Chapter 1\nOnce upon a time in a small village.\n\nChapter 2\nThe story continues with more words here.")
	assetID := uploadTextAsset(t, sess, manuscript, "text/plain")

	createResp := sess.do(http.MethodPost, "/imports", map[string]any{"assetId": assetID, "seriesId": seriesID})
	requireStatus(t, createResp, http.StatusCreated)
	var imp struct {
		Id string `json:"id"`
	}
	decodeJSON(t, createResp, &imp)

	previewResp := sess.do(http.MethodPost, "/imports/"+imp.Id+"/preview", map[string]any{"splitPreset": "en_chapter"})
	requireStatus(t, previewResp, http.StatusOK)
	var preview struct {
		Encoding string `json:"encoding"`
		Chapters []struct {
			Index int    `json:"index"`
			Title string `json:"title"`
		} `json:"chapters"`
	}
	decodeJSON(t, previewResp, &preview)
	if preview.Encoding != "utf-8" {
		t.Fatalf("encoding = %q, want utf-8", preview.Encoding)
	}
	if len(preview.Chapters) != 2 {
		t.Fatalf("expected 2 chapters, got %d", len(preview.Chapters))
	}

	commitResp := sess.do(http.MethodPost, "/imports/"+imp.Id+"/commit", map[string]any{"seriesId": seriesID})
	requireStatus(t, commitResp, http.StatusOK)
	var commit struct {
		EpisodeIds []string `json:"episodeIds"`
	}
	decodeJSON(t, commitResp, &commit)
	if len(commit.EpisodeIds) != 2 {
		t.Fatalf("expected 2 episodes committed, got %d", len(commit.EpisodeIds))
	}

	draftResp := sess.do(http.MethodGet, "/episodes/"+commit.EpisodeIds[0]+"/drafts/en", nil)
	requireStatus(t, draftResp, http.StatusOK)
	var draft struct {
		Paragraphs []struct {
			Tainted bool   `json:"tainted"`
			Origin  string `json:"origin"`
		} `json:"paragraphs"`
	}
	decodeJSON(t, draftResp, &draft)
	if len(draft.Paragraphs) == 0 {
		t.Fatal("expected at least one paragraph in the imported draft")
	}
	for _, p := range draft.Paragraphs {
		if !p.Tainted || p.Origin != "import" {
			t.Fatalf("expected every imported paragraph tainted with origin=import, got %+v", p)
		}
	}
}

// TestImportRejectsOversizedAsset covers the 10MB import-path cap. The
// generic "document" asset kind cap is 5MiB (storage.MaxBytesByKind),
// which already enforces a stricter bound than the import path's own
// 10MB, so presign itself rejects an over-cap request.
func TestImportRejectsOversizedAsset(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	fx := createFixtureUser(t, q, "story-import-oversized", uniqueEmail("import-oversized"), "editor")
	sess := login(t, fx.Email, fx.Password)

	presignResp := sess.do(http.MethodPost, "/assets/presign", map[string]any{
		"kind": "document", "mime": "text/plain", "bytes": 6 << 20,
	})
	requireStatus(t, presignResp, http.StatusBadRequest)
}

// TestBYOKKeyConfiguredNeverLeaksTheKey covers AC8's BYOK half: PUT an
// API key, then confirm GET /settings/llm reports configured:true for
// that provider and the raw key never appears anywhere in the response.
func TestBYOKKeyConfiguredNeverLeaksTheKey(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	// PutLLMApiKey requires owner (x-min-role: owner in
	// openapi/paths/settings_llm.yaml), stricter than the editor role
	// every other story mutation needs.
	fx := createFixtureUser(t, q, "story-byok", uniqueEmail("byok"), "owner")
	sess := login(t, fx.Email, fx.Password)

	secretKey := "sk-integration-" + base64.RawURLEncoding.EncodeToString([]byte(uniqueEmail("x")))
	putResp := sess.do(http.MethodPut, "/settings/llm/keys/anthropic-api", map[string]any{"apiKey": secretKey})
	requireStatus(t, putResp, http.StatusNoContent)

	getResp := sess.do(http.MethodGet, "/settings/llm", nil)
	requireStatus(t, getResp, http.StatusOK)
	bodyBytes := new(bytes.Buffer)
	_, _ = bodyBytes.ReadFrom(getResp.Body)
	_ = getResp.Body.Close()
	if bytes.Contains(bodyBytes.Bytes(), []byte(secretKey)) {
		t.Fatal("the raw API key must never appear in GET /settings/llm's response body")
	}

	var settings struct {
		Providers []struct {
			Name       string `json:"name"`
			Configured *bool  `json:"configured"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(bodyBytes.Bytes(), &settings); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range settings.Providers {
		if p.Name == "anthropic-api" {
			found = true
			if p.Configured == nil || !*p.Configured {
				t.Fatalf("expected anthropic-api to report configured:true after PUT, got %+v", p)
			}
		}
	}
	if !found {
		t.Fatal("expected anthropic-api to appear in the providers list")
	}
}
