//go:build integration

// Package integration story tests exercise the phase 6 story-writer slice
// (series/bible/episodes/drafts/imports/LLM settings) over the same live
// HTTP+Postgres stack the phase 2 suite uses.
package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

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

	// Writes into another tenant's series are refused the same way as an
	// unknown series id, so the response doesn't reveal that it exists.
	requireStatus(t, sessB.do(http.MethodPost, "/episodes?seriesId="+seriesID, nil), http.StatusNotFound)
	requireStatus(t, sessB.do(http.MethodPost, "/episodes?seriesId="+uuid.NewString(), nil), http.StatusNotFound)

	// The schema refuses a cross-tenant parent even if a handler forgets to check.
	_, err := pool.Exec(context.Background(),
		`INSERT INTO episodes (id, tenant_id, series_id, idx, title, outline, status)
		 VALUES ($1, $2, $3, 999, 'foreign', '[]'::jsonb, 'planned')`,
		uuid.New(), tenantB.TenantID, seriesID)
	if err == nil {
		t.Fatal("expected a foreign key violation for an episode referencing another tenant's series")
	}
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

	// A committed import can't be committed again or reopened by a new preview.
	requireStatus(t, sess.do(http.MethodPost, "/imports/"+imp.Id+"/commit", map[string]any{"seriesId": seriesID}), http.StatusConflict)
	requireStatus(t, sess.do(http.MethodPost, "/imports/"+imp.Id+"/preview", map[string]any{"splitPreset": "en_chapter"}), http.StatusUnprocessableEntity)
}

func TestImportConcurrentCommitsCreateEpisodesOnce(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	fx := createFixtureUser(t, q, "story-import-once", uniqueEmail("import-once"), "editor")
	sess := login(t, fx.Email, fx.Password)
	seriesID := createSeries(t, sess)

	assetID := uploadTextAsset(t, sess, []byte("Chapter 1\nFirst.\n\nChapter 2\nSecond.\n\nChapter 3\nThird."), "text/plain")
	createResp := sess.do(http.MethodPost, "/imports", map[string]any{"assetId": assetID, "seriesId": seriesID})
	requireStatus(t, createResp, http.StatusCreated)
	var imp struct {
		Id string `json:"id"`
	}
	decodeJSON(t, createResp, &imp)
	requireStatus(t, sess.do(http.MethodPost, "/imports/"+imp.Id+"/preview", map[string]any{"splitPreset": "en_chapter"}), http.StatusOK)

	// A double-click: both requests race for the same import.
	statuses := make(chan int, 2)
	for range 2 {
		go func() {
			resp := sess.do(http.MethodPost, "/imports/"+imp.Id+"/commit", map[string]any{"seriesId": seriesID})
			_ = resp.Body.Close()
			statuses <- resp.StatusCode
		}()
	}
	got := map[int]int{}
	for range 2 {
		got[<-statuses]++
	}
	if got[http.StatusOK] != 1 || got[http.StatusConflict] != 1 {
		t.Fatalf("expected one 200 and one 409, got %v", got)
	}

	listResp := sess.do(http.MethodGet, "/episodes?seriesId="+seriesID, nil)
	requireStatus(t, listResp, http.StatusOK)
	var list struct {
		Items []struct {
			Id string `json:"id"`
		} `json:"items"`
	}
	decodeJSON(t, listResp, &list)
	if len(list.Items) != 3 {
		t.Fatalf("expected 3 episodes after two commits of one import, got %d", len(list.Items))
	}
}

func TestBibleSectionEditsDoNotOverwriteEachOther(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	fx := createFixtureUser(t, q, "story-bible-cas", uniqueEmail("bible-cas"), "editor")
	sess := login(t, fx.Email, fx.Password)
	seriesID := createSeries(t, sess)

	patch := func(section, content string, expected int) int {
		resp := sess.do(http.MethodPatch, "/series/"+seriesID+"/bible", map[string]any{
			"section": section, "content": content, "expectedVersion": expected,
		})
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	// Two editors, each saving a different section from the same starting point.
	if s := patch("world", "A floating continent.", 0); s != http.StatusOK {
		t.Fatalf("world: status %d", s)
	}
	if s := patch("glossary", "Qi: life energy.", 0); s != http.StatusOK {
		t.Fatalf("glossary: status %d", s)
	}
	// A second save of the same section from the stale version is refused.
	if s := patch("world", "An overwrite.", 0); s != http.StatusConflict {
		t.Fatalf("stale world save: status %d, want 409", s)
	}

	bibleResp := sess.do(http.MethodGet, "/series/"+seriesID+"/bible", nil)
	requireStatus(t, bibleResp, http.StatusOK)
	var bible struct {
		Sections map[string]struct {
			Content string `json:"content"`
			Version int    `json:"version"`
		} `json:"sections"`
	}
	decodeJSON(t, bibleResp, &bible)
	if bible.Sections["world"].Content != "A floating continent." || bible.Sections["world"].Version != 1 {
		t.Fatalf("world section = %+v", bible.Sections["world"])
	}
	if bible.Sections["glossary"].Content != "Qi: life energy." {
		t.Fatalf("glossary section lost: %+v", bible.Sections["glossary"])
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

// TestAiActionResultPollingIsScopedToItsEpisode covers the writer's AI
// action round trip: create returns a step id, polling it returns the step
// status and, once done, the generated text. The step must only be
// readable through its own episode and tenant.
func TestAiActionResultPollingIsScopedToItsEpisode(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	fxA := createFixtureUser(t, q, "story-ai-action-a", uniqueEmail("ai-action-a"), "editor")
	fxB := createFixtureUser(t, q, "story-ai-action-b", uniqueEmail("ai-action-b"), "editor")
	sessA := login(t, fxA.Email, fxA.Password)

	seriesID := createSeries(t, sessA)
	newEpisode := func() string {
		resp := sessA.do(http.MethodPost, "/episodes?seriesId="+seriesID, nil)
		requireStatus(t, resp, http.StatusCreated)
		var episode struct {
			Id string `json:"id"`
		}
		decodeJSON(t, resp, &episode)
		return episode.Id
	}
	episodeID := newEpisode()
	otherEpisodeID := newEpisode()

	createResp := sessA.do(http.MethodPost, "/episodes/"+episodeID+"/ai-actions", map[string]any{
		"action":       "rewrite",
		"lang":         "en",
		"paragraphIds": []string{"p1"},
	})
	requireStatus(t, createResp, http.StatusAccepted)
	var created struct {
		RunId  string `json:"runId"`
		StepId string `json:"stepId"`
	}
	decodeJSON(t, createResp, &created)
	resultPath := "/episodes/" + episodeID + "/ai-actions/" + created.StepId

	pollResp := sessA.do(http.MethodGet, resultPath, nil)
	requireStatus(t, pollResp, http.StatusOK)
	var polled struct {
		Status string `json:"status"`
	}
	decodeJSON(t, pollResp, &polled)
	switch polled.Status {
	case "pending", "queued", "running", "done", "failed", "canceled":
	default:
		t.Fatalf("unexpected step status %q", polled.Status)
	}

	// Another episode's URL, an unknown step and another tenant all get 404.
	requireStatus(t, sessA.do(http.MethodGet, "/episodes/"+otherEpisodeID+"/ai-actions/"+created.StepId, nil), http.StatusNotFound)
	requireStatus(t, sessA.do(http.MethodGet, "/episodes/"+episodeID+"/ai-actions/"+uuid.NewString(), nil), http.StatusNotFound)
	sessB := login(t, fxB.Email, fxB.Password)
	requireStatus(t, sessB.do(http.MethodGet, resultPath, nil), http.StatusNotFound)

	// Finish the step as a provider would (no LLM is guaranteed in the test
	// stack). Only a step that is not mid-run is overwritten, so a worker
	// that already claimed it cannot race this update.
	output := `{"provider":"ollama","lang":"en","text":"The rewritten paragraph.","tainted":false}`
	deadline := time.Now().Add(30 * time.Second)
	for {
		tag, err := pool.Exec(context.Background(),
			`UPDATE pipeline_steps SET status = 'done', output = $1::jsonb, error_msg = NULL, finished_at = now()
			 WHERE id = $2 AND status <> 'running'`, output, created.StepId)
		if err != nil {
			t.Fatalf("finish step: %v", err)
		}
		if tag.RowsAffected() == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("step stayed running for 30s")
		}
		time.Sleep(200 * time.Millisecond)
	}

	doneResp := sessA.do(http.MethodGet, resultPath, nil)
	requireStatus(t, doneResp, http.StatusOK)
	var done struct {
		Status   string `json:"status"`
		Text     string `json:"text"`
		Provider string `json:"provider"`
		Tainted  *bool  `json:"tainted"`
	}
	decodeJSON(t, doneResp, &done)
	if done.Status != "done" || done.Text != "The rewritten paragraph." || done.Provider != "ollama" || done.Tainted == nil || *done.Tainted {
		t.Fatalf("unexpected done result %+v", done)
	}
}
