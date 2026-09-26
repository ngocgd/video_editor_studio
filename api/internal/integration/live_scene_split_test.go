//go:build integration && live

// Live LLM check of the scene split, run by hand against a stack with a
// real provider (COMPOSE_PROFILES=claude-cli): a draft of at least 6,000
// words is grown with the writer's own Continue action, split with the
// LLM, and its dialogue must be attributed to at least two characters
// that have voices. Step ids, word counts and the provider are logged as
// evidence. Not part of the default integration run (build tag "live").
package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

const liveTargetWords = 6000

var liveOpening = []string{
	`Dawn broke over the Azure Cloud range, and the nine peaks of the Jade Crane Sect rose out of the mist like the spines of a sleeping dragon. Lin Mo climbed the last of the thousand stone steps with a cracked pill furnace strapped to his back and blood drying on his left palm.`,
	`Elder Qiu waited at the top of the stair, his grey sleeves folded, his eyes on the incense rather than on the disciple. "You carry the scent of a broken meridian," Elder Qiu said, without turning. "Tell me how a third-rank outer disciple manages to shatter a furnace that has survived four hundred years."`,
	`Lin Mo set the furnace down and bowed, lower than the rules required. "Forgive me, Elder. I tried to refine the Nine-Turn pill without a teacher. The fire answered me, and then it stopped answering."`,
	`"Fire does not answer anyone," Elder Qiu said. "It listens. You shouted at it." He finally turned, and his gaze fell on the blood. "Show me your hand."`,
}

func TestLiveLLMSceneSplitOfASixThousandWordDraft(t *testing.T) {
	skipIfAPIUnreachable(t)
	f := newStoryboardFixture(t, liveOpening)
	sess := f.sess
	ctx := context.Background()

	words := func() (int, []string) {
		var d struct {
			WordCount  int `json:"wordCount"`
			Paragraphs []struct {
				ID string `json:"id"`
			} `json:"paragraphs"`
		}
		sessionJSON(t, sess.do(http.MethodGet, "/episodes/"+f.episodeID+"/drafts/en", nil), http.StatusOK, &d)
		ids := make([]string, len(d.Paragraphs))
		for i, p := range d.Paragraphs {
			ids[i] = p.ID
		}
		return d.WordCount, ids
	}

	instruction := "Continue with a long scene of about 900 words: mostly spoken dialogue between Lin Mo and Elder Qiu, in quotation marks, each line clearly attributed by name."
	for round := 1; ; round++ {
		n, ids := words()
		t.Logf("draft: %d words after %d continue rounds", n, round-1)
		if n >= liveTargetWords {
			break
		}
		if round > 20 {
			t.Fatalf("only %d words after 20 continue rounds", n)
		}
		tail := ids[max(0, len(ids)-3):]
		var acc struct {
			StepID string `json:"stepId"`
		}
		sessionJSON(t, sess.do(http.MethodPost, "/episodes/"+f.episodeID+"/ai-actions", map[string]any{"action": "continue", "lang": "en", "paragraphIds": tail, "instruction": instruction}), http.StatusAccepted, &acc)
		var res struct {
			Status, Provider, Text, ErrorDetail string
		}
		deadline := time.Now().Add(6 * time.Minute)
		for time.Now().Before(deadline) {
			sessionJSON(t, sess.do(http.MethodGet, "/episodes/"+f.episodeID+"/ai-actions/"+acc.StepID, nil), http.StatusOK, &res)
			if res.Status == "done" || res.Status == "failed" || res.Status == "canceled" {
				break
			}
			time.Sleep(3 * time.Second)
		}
		if res.Status != "done" {
			t.Fatalf("continue step %s ended %s: %s", acc.StepID, res.Status, res.ErrorDetail)
		}
		t.Logf("continue step %s by %s: %d chars", acc.StepID, res.Provider, len(res.Text))
		sessionJSON(t, sess.do(http.MethodPost, "/episodes/"+f.episodeID+"/drafts/en/apply-step", map[string]any{"stepId": acc.StepID}), http.StatusOK, nil)
	}

	var split struct {
		RunID  uuid.UUID `json:"runId"`
		StepID uuid.UUID `json:"stepId"`
	}
	sessionJSON(t, sess.do(http.MethodPost, "/episodes/"+f.episodeID+"/scenes/split", map[string]any{"lang": "en", "mode": "llm"}), http.StatusAccepted, &split)
	start := time.Now()
	var run struct{ Status string }
	for time.Since(start) < 15*time.Minute {
		sessionJSON(t, sess.do(http.MethodGet, "/runs/"+split.RunID.String(), nil), http.StatusOK, &run)
		if run.Status != "active" {
			break
		}
		time.Sleep(5 * time.Second)
	}
	var output []byte
	var errMsg *string
	if err := ownerPool(t).QueryRow(ctx, `SELECT output, error_msg FROM pipeline_steps WHERE id = $1`, split.StepID).Scan(&output, &errMsg); err != nil {
		t.Fatal(err)
	}
	if run.Status != "done" {
		t.Fatalf("scene split run %s ended %s: %v", split.RunID, run.Status, errMsg)
	}
	var out struct {
		Provider             string `json:"provider"`
		SceneCount           int    `json:"sceneCount"`
		UnrecognisedSpeakers int    `json:"unrecognisedSpeakers"`
	}
	_ = json.Unmarshal(output, &out)
	n, _ := words()
	t.Logf("llm.scene_split step %s (run %s) by %s in %s: %d words -> %d scenes, %d unrecognised speakers", split.StepID, split.RunID, out.Provider, time.Since(start).Round(time.Second), n, out.SceneCount, out.UnrecognisedSpeakers)

	list := listScenes(t, sess, f.episodeID, "all")
	perSpeaker := map[string]int{}
	for _, s := range list.Items {
		for _, seg := range s.Segments {
			if seg.SpeakerCharacterID != "" {
				perSpeaker[seg.SpeakerCharacterID]++
			}
		}
	}
	t.Logf("dialogue segments: Lin Mo %d, Elder Qiu %d, scenes %d", perSpeaker[f.linMo], perSpeaker[f.elderQiu], len(list.Items))
	if perSpeaker[f.linMo] == 0 || perSpeaker[f.elderQiu] == 0 {
		t.Fatalf("dialogue must be attributed to both voiced characters: %v", perSpeaker)
	}
	if len(list.Items) < 10 {
		t.Fatalf("a %d-word draft split into only %d scenes", n, len(list.Items))
	}
}
