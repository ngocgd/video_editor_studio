//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/library"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/render"
	"loomtale/api/internal/scenes"
)

// renderDraft is three narration paragraphs, split into one scene each.
var renderDraft = []string{filler(14), filler(15), filler(16)}

// renderFixture is an episode whose English scenes all have a selected
// image, a ready voice and an align take, so it can be rendered.
type renderFixture struct {
	storyboardFixture
	engine *pipeline.Engine
	pool   *pgxpool.Pool
	scenes []sceneDTO
}

// regen queues one scene regenerate over HTTP and runs its gpu step in
// this process on the test doubles (the live worker has no gpu queue).
func (f renderFixture) regen(t *testing.T, sceneID, kind string) {
	t.Helper()
	var acc struct {
		StepIDs []uuid.UUID `json:"stepIds"`
	}
	sessionJSON(t, f.sess.do(http.MethodPost, "/scenes/"+sceneID+"/regenerate", map[string]any{"kind": kind}), http.StatusAccepted, &acc)
	if len(acc.StepIDs) != 1 {
		t.Fatalf("regenerate %s queued %v", kind, acc.StepIDs)
	}
	dispatchStep(t, f.engine, acc.StepIDs[0])
	if kind != "voice" {
		return
	}
	aligns := stepsWhere(t, f.pool, "scope_kind = 'scene' AND scope_id = $1 AND kind = 'align.subtitles' AND status = 'queued'", sceneID)
	if len(aligns) != 1 {
		t.Fatalf("expected one queued align step after the voice take, got %+v", aligns)
	}
	dispatchStep(t, f.engine, aligns[0].ID)
}

// newRenderFixture splits renderDraft into scenes and generates every
// scene's image, voice and alignment on the test doubles. The engine's
// registry also knows the render and library kinds, so enqueues from
// this process go to the live worker's render and cpu queues.
func newRenderFixture(t *testing.T) renderFixture {
	t.Helper()
	f := renderFixture{storyboardFixture: newStoryboardFixture(t, renderDraft)}
	sessionJSON(t, f.sess.do(http.MethodPost, "/episodes/"+f.episodeID+"/scenes/split", map[string]any{"lang": "en", "mode": "paragraphs"}), http.StatusOK, nil)

	st := testStorage(t)
	f.engine, _, f.pool = sceneEngine(t, func(svc *scenes.Service) []pipeline.StepHandler {
		doubles := startSceneDoubles(t, svc, st)
		handlers := scenes.Handlers(doubles.deps)
		handlers = append(handlers, render.Handlers(render.Deps{Queries: svc.Queries})...)
		return append(handlers, library.Handlers(library.Deps{Queries: svc.Queries})...)
	})
	list := listScenes(t, f.sess, f.episodeID, "all")
	if len(list.Items) < 2 {
		t.Fatalf("the render fixture needs at least two scenes for a transition, got %d", len(list.Items))
	}
	for _, s := range list.Items {
		f.regen(t, s.ID, "image")
		f.regen(t, s.ID, "voice")
	}
	// The live worker measures each voice and draws its peaks.
	waitFor(t, 90*time.Second, "measured voice durations from the worker", func() bool {
		f.scenes = listScenes(t, f.sess, f.episodeID, "all").Items
		for _, s := range f.scenes {
			if !s.DurationMeasured || !s.Peaks {
				return false
			}
		}
		return true
	})
	for _, s := range f.scenes {
		for _, kind := range []string{"image", "voice", "align"} {
			if state, reason, _ := s.pip(kind); state != "done" {
				t.Fatalf("scene %d %s pip = %s %s", s.Idx, kind, state, reason)
			}
		}
	}
	return f
}

// smallRenderSettings keeps the fixture renders fast: 640x360 x264 with
// burned and soft subtitles (the worker image ships the Literata font).
var smallRenderSettings = map[string]any{
	"width": 640, "height": 360, "fps": 30, "encoder": "libx264", "subtitles": "both",
	"subtitleStyle": map[string]any{"font": "Literata", "sizePx": 24, "position": "bottom", "shadowPx": 2},
	"defaultMotion": "ken_burns", "crossfadeMs": 600, "loudnessLufs": -14, "truePeakDbtp": -1,
}

// heldHandler puts a render step on the gpu queue, which nothing claims
// in the integration stack, so a run stays active for as long as a test
// needs it to.
type heldHandler struct{ pipeline.StepHandler }

func (heldHandler) Queue(context.Context, pipeline.StepRef) (string, error) {
	return pipeline.QueueGPU, nil
}

// renderFreezer builds an in-process Freezer on its own engine. hold
// parks the render steps (see heldHandler); checks are the engine's
// admission checks.
func renderFreezer(t *testing.T, hold bool, checks []pipeline.AdmissionCheck) *render.Freezer {
	t.Helper()
	registry := pipeline.NewRegistry()
	pool := appPool(t)
	queries := dbgen.New(pool)
	for _, h := range render.Handlers(render.Deps{Queries: queries}) {
		if hold {
			h = heldHandler{h}
		}
		registry.Register(h)
	}
	engine, _ := pipelineEngineWithChecks(t, registry, checks)
	return &render.Freezer{Pool: pool, Queries: queries, Engine: engine}
}

// runRow is a pipeline run's status and the run that superseded it.
type runRow struct {
	Kind         string
	Status       string
	SupersededBy *uuid.UUID
}

func readRun(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) runRow {
	t.Helper()
	var r runRow
	if err := pool.QueryRow(context.Background(), `SELECT kind, status, superseded_by FROM pipeline_runs WHERE id = $1`, id).Scan(&r.Kind, &r.Status, &r.SupersededBy); err != nil {
		t.Fatalf("read run %s: %v", id, err)
	}
	return r
}

// waitRunDone waits for a run to end and fails with its failed steps'
// errors unless it ended done.
func waitRunDone(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, timeout time.Duration) {
	t.Helper()
	var r runRow
	waitFor(t, timeout, "run "+id.String()+" to end", func() bool {
		r = readRun(t, pool, id)
		return r.Status != "active"
	})
	if r.Status == "done" {
		return
	}
	rows, err := pool.Query(context.Background(), `SELECT kind, status, COALESCE(error_code, ''), COALESCE(error_msg, '') FROM pipeline_steps WHERE run_id = $1 AND status <> 'done' ORDER BY id`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind, status, code, msg string
		if err := rows.Scan(&kind, &status, &code, &msg); err != nil {
			t.Fatal(err)
		}
		t.Logf("step %s %s %s: %s", kind, status, code, msg)
	}
	t.Fatalf("run %s (%s) ended %s", id, r.Kind, r.Status)
}

// stepKindCounts counts a run's steps by kind.
func stepKindCounts(t *testing.T, pool *pgxpool.Pool, runID uuid.UUID) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, s := range stepsWhere(t, pool, "run_id = $1", runID) {
		out[s.Kind]++
	}
	return out
}
