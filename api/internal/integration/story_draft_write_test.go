//go:build integration

package integration

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"loomtale/api/internal/db/gen"
)

// importedEpisode imports a one-chapter text and returns the path of the
// episode it created, which already has an EN draft with one paragraph.
func importedEpisode(t *testing.T, sess *session, seriesID string) string {
	t.Helper()
	assetID := uploadTextAsset(t, sess, []byte("Chapter 1\nThe first paragraph."), "text/plain")
	createResp := sess.do(http.MethodPost, "/imports", map[string]any{"assetId": assetID, "seriesId": seriesID})
	requireStatus(t, createResp, http.StatusCreated)
	var imp struct {
		Id string `json:"id"`
	}
	decodeJSON(t, createResp, &imp)
	requireStatus(t, sess.do(http.MethodPost, "/imports/"+imp.Id+"/preview", map[string]any{"splitPreset": "en_chapter"}), http.StatusOK)
	commitResp := sess.do(http.MethodPost, "/imports/"+imp.Id+"/commit", map[string]any{"seriesId": seriesID})
	requireStatus(t, commitResp, http.StatusOK)
	var commit struct {
		EpisodeIds []string `json:"episodeIds"`
	}
	decodeJSON(t, commitResp, &commit)
	return "/episodes/" + commit.EpisodeIds[0]
}

// TestAcceptingAnInsertingStepTwiceInsertsItOnce covers a retried accept
// and two accepts racing each other: a continuation's text must land in
// the draft once, and every repeat is refused as already applied.
func TestAcceptingAnInsertingStepTwiceInsertsItOnce(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	fx := createFixtureUser(t, q, "story-apply-once", uniqueEmail("apply-once"), "editor")
	sess := login(t, fx.Email, fx.Password)
	episodePath := importedEpisode(t, sess, createSeries(t, sess))

	newContinueStep := func() string {
		resp := sess.do(http.MethodPost, episodePath+"/ai-actions", map[string]any{"action": "continue", "lang": "en"})
		requireStatus(t, resp, http.StatusAccepted)
		var created struct {
			StepId string `json:"stepId"`
		}
		decodeJSON(t, resp, &created)
		finishStep(t, pool, created.StepId, `{"provider":"ollama","lang":"en","text":"A continued paragraph.","tainted":false}`)
		return created.StepId
	}
	apply := func(stepID string) *http.Response {
		return sess.do(http.MethodPost, episodePath+"/drafts/en/apply-step", map[string]any{"stepId": stepID})
	}
	paragraphCount := func() int {
		resp := sess.do(http.MethodGet, episodePath+"/drafts/en", nil)
		requireStatus(t, resp, http.StatusOK)
		var d draftView
		decodeJSON(t, resp, &d)
		return len(d.Paragraphs)
	}

	before := paragraphCount()

	// A retry after a successful accept.
	stepID := newContinueStep()
	requireStatus(t, apply(stepID), http.StatusOK)
	requireProblem(t, apply(stepID), http.StatusConflict, "already applied")
	if got := paragraphCount(); got != before+1 {
		t.Fatalf("after a retried accept the draft has %d paragraphs, want %d", got, before+1)
	}

	// Two accepts of one step in flight at once.
	stepID = newContinueStep()
	statuses := make([]int, 2)
	var wg sync.WaitGroup
	for i := range statuses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp := apply(stepID)
			statuses[i] = resp.StatusCode
			_ = resp.Body.Close()
		}(i)
	}
	wg.Wait()
	ok, conflict := 0, 0
	for _, s := range statuses {
		switch s {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
		}
	}
	// The loser is refused either as already applied or, if it read the
	// draft before the winner wrote it, as a version conflict; both are 409.
	if ok != 1 || conflict != 1 {
		t.Fatalf("concurrent accepts returned %v, want one 200 and one 409", statuses)
	}
	if got := paragraphCount(); got != before+2 {
		t.Fatalf("after concurrent accepts the draft has %d paragraphs, want %d", got, before+2)
	}
}

// TestDraftPatchRejectsOversizedParagraphs checks the request bounds on a
// draft write: an over-long paragraph text or id never reaches the draft.
func TestDraftPatchRejectsOversizedParagraphs(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	fx := createFixtureUser(t, q, "story-patch-bounds", uniqueEmail("patch-bounds"), "editor")
	sess := login(t, fx.Email, fx.Password)
	episodePath := importedEpisode(t, sess, createSeries(t, sess))

	resp := sess.do(http.MethodGet, episodePath+"/drafts/en", nil)
	requireStatus(t, resp, http.StatusOK)
	var d draftView
	decodeJSON(t, resp, &d)

	patch := func(paragraphID, text string) *http.Response {
		return sess.do(http.MethodPatch, episodePath+"/drafts/en", map[string]any{
			"expectedVersion": d.Version,
			"ops":             []map[string]any{{"op": "upsert", "paragraphId": paragraphID, "text": text}},
		})
	}
	requireProblem(t, patch("p_long_text", strings.Repeat("a", 20001)), http.StatusBadRequest, "request validation failed")
	requireProblem(t, patch(strings.Repeat("p", 65), "short"), http.StatusBadRequest, "request validation failed")
	requireStatus(t, patch("p_ok", strings.Repeat("a", 20000)), http.StatusOK)
}
