//go:build integration

// Scene layer tests: splitting a draft into scenes, per-scene image,
// voice and align steps run in this process against test doubles of
// ComfyUI and the Python worker (the api and worker containers have no
// GPU here), stale propagation, the scenes.Changed hook, and the media
// derivative steps run by the live worker container's real ffmpeg.
package integration

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/media"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/scenes"
)

type sceneDTO struct {
	ID       string `json:"id"`
	Idx      int    `json:"idx"`
	Version  int64  `json:"version"`
	Narration string `json:"narration"`
	Segments []struct {
		SpeakerCharacterID string `json:"speakerCharacterId"`
		Text               string `json:"text"`
	} `json:"segments"`
	CharacterIDs []string `json:"characterIds"`
	Pips         []struct {
		Kind        string `json:"kind"`
		State       string `json:"state"`
		StaleReason string `json:"staleReason"`
		ErrorCode   string `json:"errorCode"`
		ErrorMessage string `json:"errorMessage"`
	} `json:"pips"`
	ImageAssetID string `json:"imageAssetId"`
	VoiceAssetID string `json:"voiceAssetId"`
	Peaks        bool   `json:"peaks"`
	DurationMs   int    `json:"durationMs"`
	DurationMeasured bool `json:"durationMeasured"`
}

func (s sceneDTO) pip(kind string) (state, reason, code string) {
	for _, p := range s.Pips {
		if p.Kind == kind {
			return p.State, p.StaleReason + p.ErrorMessage, p.ErrorCode
		}
	}
	return "", "", ""
}

type sceneListDTO struct {
	Items  []sceneDTO `json:"items"`
	Counts struct {
		All, Stale, Failed, Missing, InQueue int
	} `json:"counts"`
	MissingCount int `json:"missingCount"`
}

func listScenes(t *testing.T, sess *session, episodeID, filter string) sceneListDTO {
	t.Helper()
	var out sceneListDTO
	sessionJSON(t, sess.do(http.MethodGet, "/episodes/"+episodeID+"/scenes?lang=en&filter="+filter, nil), http.StatusOK, &out)
	return out
}

// storyboardFixture is a series with two voiced characters, a style and
// an English draft with dialogue.
type storyboardFixture struct {
	sess      *session
	fx        fixtureUser
	seriesID  string
	episodeID string
	linMo     string
	elderQiu  string
}

func newStoryboardFixture(t *testing.T, paragraphs []string) storyboardFixture {
	t.Helper()
	q := dbgen.New(ownerPool(t))
	fx := createFixtureUser(t, q, "storyboard", uniqueEmail("storyboard"), "owner")
	sess := login(t, fx.Email, fx.Password)
	f := storyboardFixture{sess: sess, fx: fx, seriesID: createSeries(t, sess)}

	var style struct{ ID string `json:"id"` }
	sessionJSON(t, sess.do(http.MethodPost, "/settings/image-styles", map[string]any{"name": "Ink wash", "baseModel": "z-image-turbo", "stylePrompt": "ink wash painting", "steps": 8, "width": 1024, "height": 576}), http.StatusCreated, &style)
	sessionJSON(t, sess.do(http.MethodPut, "/series/"+f.seriesID+"/storyboard-settings", map[string]any{"imageStyleId": style.ID, "cadenceMinS": 20, "cadenceMaxS": 40, "segmentGapMs": 150}), http.StatusOK, nil)

	create := func(orig, en, vi string) string {
		var c struct{ ID string `json:"id"` }
		sessionJSON(t, sess.do(http.MethodPost, "/series/"+f.seriesID+"/characters", map[string]any{
			"names": map[string]string{"orig": orig, "en": en, "vi": vi}, "appearancePrompt": "a " + en, "profile": en + " is a cultivator.",
		}), http.StatusCreated, &c)
		sessionJSON(t, sess.do(http.MethodPut, "/characters/"+c.ID+"/voices/en", map[string]any{"engine": "chatterbox", "params": map[string]string{"exaggeration": "0.4"}}), http.StatusOK, nil)
		return c.ID
	}
	f.linMo = create("林默", "Lin Mo", "Lâm Mặc")
	f.elderQiu = create("邱长老", "Elder Qiu", "Trưởng lão Khâu")
	sessionJSON(t, sess.do(http.MethodPut, "/series/"+f.seriesID+"/narrator-voices/en", map[string]any{"engine": "chatterbox"}), http.StatusOK, nil)

	var ep struct{ ID string `json:"id"` }
	sessionJSON(t, sess.do(http.MethodPost, "/episodes?seriesId="+f.seriesID, nil), http.StatusCreated, &ep)
	f.episodeID = ep.ID
	sessionJSON(t, sess.do(http.MethodPut, "/episodes/"+ep.ID+"/drafts/en", nil), http.StatusCreated, nil)
	version := 0
	for start := 0; start < len(paragraphs); start += 200 {
		var ops []map[string]any
		for i := start; i < min(start+200, len(paragraphs)); i++ {
			ops = append(ops, map[string]any{"op": "upsert", "paragraphId": fmt.Sprintf("p%04d", i), "text": paragraphs[i]})
		}
		var d struct{ Version int `json:"version"` }
		sessionJSON(t, sess.do(http.MethodPatch, "/episodes/"+ep.ID+"/drafts/en", map[string]any{"expectedVersion": version, "ops": ops}), http.StatusOK, &d)
		version = d.Version
	}
	return f
}

func filler(n int) string { return strings.TrimSpace(strings.Repeat("The mist rolled over the peaks. ", n)) }

var dialogueDraft = []string{
	`"You carry the scent of a broken meridian," Elder Qiu said, without turning from the incense. ` + filler(8),
	`Lin Mo bowed low. "Forgive me, Elder. The pill furnace cracked at dawn." ` + filler(10),
	filler(14),
	`Lin Mo looked up at the ninth bolt. "I kneel to no heaven that would not answer me." ` + filler(9),
	`Elder Qiu laughed. "Then stand, and be struck." ` + filler(12),
	filler(15),
}

// dispatchStep runs one queued step in this process (the live worker has
// no gpu queue, so nothing else claims it).
func dispatchStep(t *testing.T, engine *pipeline.Engine, stepID uuid.UUID) {
	t.Helper()
	if err := engine.Dispatch(context.Background(), time.Now().UnixNano(), []uuid.UUID{stepID}, pipeline.DispatchOpts{}); err != nil {
		t.Fatalf("dispatch %s: %v", stepID, err)
	}
}

func TestStoryboardSplitStepsStaleAndTakes(t *testing.T) {
	skipIfAPIUnreachable(t)
	f := newStoryboardFixture(t, dialogueDraft)
	sess := f.sess

	// Paragraph split: whole paragraphs grouped to the 20–40s cadence.
	var split struct{ SceneCount, KeptCount, UnrecognisedSpeakers int }
	sessionJSON(t, sess.do(http.MethodPost, "/episodes/"+f.episodeID+"/scenes/split", map[string]any{"lang": "en", "mode": "paragraphs"}), http.StatusOK, &split)
	list := listScenes(t, sess, f.episodeID, "all")
	if split.SceneCount < 3 || len(list.Items) != split.SceneCount {
		t.Fatalf("split = %+v, listed %d", split, len(list.Items))
	}
	speakers := map[string]bool{}
	for _, s := range list.Items {
		for _, seg := range s.Segments {
			if seg.SpeakerCharacterID != "" {
				speakers[seg.SpeakerCharacterID] = true
			}
		}
	}
	if !speakers[f.linMo] || !speakers[f.elderQiu] {
		t.Fatalf("dialogue must be attributed to both characters, got %v", speakers)
	}
	if list.Counts.Missing != len(list.Items) || list.MissingCount != 3*len(list.Items) {
		t.Fatalf("fresh scenes must all be missing: %+v missing=%d", list.Counts, list.MissingCount)
	}

	st := testStorage(t)
	var doubles *sceneDoubles
	engine, service, pool := sceneEngine(t, func(svc *scenes.Service) []pipeline.StepHandler {
		doubles = startSceneDoubles(t, svc, st)
		return scenes.Handlers(doubles.deps)
	})
	first, second := list.Items[0], list.Items[1]

	// Regenerate queues exactly one gpu step at the per-scene priority.
	regen := func(sceneID, kind string) uuid.UUID {
		var acc struct {
			RunID   uuid.UUID   `json:"runId"`
			StepIDs []uuid.UUID `json:"stepIds"`
		}
		sessionJSON(t, sess.do(http.MethodPost, "/scenes/"+sceneID+"/regenerate", map[string]any{"kind": kind}), http.StatusAccepted, &acc)
		steps := stepsWhere(t, pool, "run_id = $1", acc.RunID)
		if len(acc.StepIDs) != 1 || len(steps) != 1 || steps[0].Queue != "gpu" || steps[0].Priority != pipeline.PriorityScene {
			t.Fatalf("regenerate %s queued %+v", kind, steps)
		}
		return acc.StepIDs[0]
	}

	// Image: the style, the characters' look and the prompt go to ComfyUI
	// as JSON values of the server's own workflow template.
	dispatchStep(t, engine, regen(first.ID, "image"))
	prompt := doubles.comfy.lastPrompt()
	encoded := fmt.Sprint(prompt)
	if !strings.Contains(encoded, "ink wash painting") {
		t.Fatalf("the style prompt did not reach the workflow: %s", encoded)
	}
	dispatchStep(t, engine, regen(second.ID, "image"))

	// Voice: one TTS call per segment, concatenated with the gap by ffmpeg.
	calls := doubles.tts.calls.Load()
	dispatchStep(t, engine, regen(first.ID, "voice"))
	if got := int(doubles.tts.calls.Load() - calls); got != len(first.Segments) {
		t.Fatalf("expected one TTS call per segment (%d), got %d", len(first.Segments), got)
	}
	// The voice step queued the align step on its own (a single regenerate).
	aligns := stepsWhere(t, pool, "scope_kind = 'scene' AND scope_id = $1 AND kind = 'align.subtitles' AND status = 'queued'", first.ID)
	if len(aligns) != 1 {
		t.Fatalf("expected one queued align step after the voice take, got %+v", aligns)
	}
	dispatchStep(t, engine, aligns[0].ID)

	// The live worker's real ffmpeg makes the image variants and peaks.
	list = listScenes(t, sess, f.episodeID, "all")
	s0 := list.Items[0]
	waitFor(t, 90*time.Second, "image variants and waveform peaks from the worker", func() bool {
		img := assetVariants(t, pool, uuid.MustParse(s0.ImageAssetID))
		voice := assetVariants(t, pool, uuid.MustParse(s0.VoiceAssetID))
		return len(img.WebP) > 0 && len(img.AVIF) > 0 && voice.Peaks != ""
	})
	list = listScenes(t, sess, f.episodeID, "all")
	s0 = list.Items[0]
	for _, kind := range []string{"image", "voice", "align"} {
		if state, reason, _ := s0.pip(kind); state != "done" {
			t.Fatalf("%s pip = %s (%s)", kind, state, reason)
		}
	}
	if !s0.Peaks || !s0.DurationMeasured || s0.DurationMs <= 0 {
		t.Fatalf("scene 1 after voice: %+v", s0)
	}
	var peaks struct {
		PeaksPerSecond int   `json:"peaksPerSecond"`
		Min, Max       []int
	}
	sessionJSON(t, sess.do(http.MethodGet, "/scenes/"+s0.ID+"/peaks?startMs=0&endMs=500", nil), http.StatusOK, &peaks)
	if peaks.PeaksPerSecond != 100 || len(peaks.Max) != 50 || slices.Max(peaks.Max) <= 0 {
		t.Fatalf("peaks window = %d per s, %d values, max %d", peaks.PeaksPerSecond, len(peaks.Max), slices.Max(peaks.Max))
	}

	// A narration edit makes only that scene's voice and align stale.
	edited := strings.Replace(s0.Narration, "broken meridian", "shattered meridian", 1)
	var patched sceneDTO
	sessionJSON(t, sess.do(http.MethodPatch, "/scenes/"+s0.ID, map[string]any{"expectedVersion": s0.Version, "narration": edited}), http.StatusOK, &patched)
	if state, _, _ := patched.pip("image"); state != "done" {
		t.Fatalf("image must stay done after a narration edit, got %s", state)
	}
	for _, kind := range []string{"voice", "align"} {
		state, reason, _ := patched.pip(kind)
		if state != "stale" || !strings.Contains(reason, "Narration edited") {
			t.Fatalf("%s after the edit = %s (%q)", kind, state, reason)
		}
	}
	after := listScenes(t, sess, f.episodeID, "all")
	if state, _, _ := after.Items[1].pip("image"); state != "done" {
		t.Fatalf("the other scene's image changed state: %s", state)
	}
	for _, s := range after.Items[1:] {
		for _, kind := range []string{"voice", "align"} {
			if state, _, _ := s.pip(kind); state == "stale" {
				t.Fatalf("scene %d %s went stale from another scene's edit", s.Idx, kind)
			}
		}
	}
	if stale := listScenes(t, sess, f.episodeID, "stale"); len(stale.Items) != 1 || stale.Items[0].ID != s0.ID {
		t.Fatalf("the stale filter must return exactly the edited scene, got %d", len(stale.Items))
	}
	// A stale version is refused.
	requireStatus(t, sess.do(http.MethodPatch, "/scenes/"+s0.ID, map[string]any{"expectedVersion": s0.Version, "imagePrompt": "x"}), http.StatusConflict)

	// engine_not_installed surfaces as a failed pip with its message.
	doubles.tts.fail.Store(true)
	dispatchStep(t, engine, regen(s0.ID, "voice"))
	failed := listScenes(t, sess, f.episodeID, "failed")
	if len(failed.Items) != 1 {
		t.Fatalf("failed filter = %d", len(failed.Items))
	}
	state, msg, code := failed.Items[0].pip("voice")
	if state != "failed" || code != "engine_not_installed" || !strings.Contains(msg, "not installed") {
		t.Fatalf("voice pip = %s %s %q", state, code, msg)
	}
	doubles.tts.fail.Store(false)

	// Takes: two image takes for scene 2; selecting the first is a revert.
	dispatchStep(t, engine, regen(second.ID, "image"))
	var takes struct {
		Items []struct {
			ID       string `json:"id"`
			Kind     string `json:"kind"`
			Selected bool   `json:"selected"`
		} `json:"items"`
	}
	sessionJSON(t, sess.do(http.MethodGet, "/scenes/"+second.ID+"/takes", nil), http.StatusOK, &takes)
	var imageTakes []string
	var selectedFlags []bool
	for _, tk := range takes.Items {
		if tk.Kind == "image" {
			imageTakes = append(imageTakes, tk.ID)
			selectedFlags = append(selectedFlags, tk.Selected)
		}
	}
	if !slices.Equal(selectedFlags, []bool{false, true}) {
		t.Fatalf("expected two image takes with the newest selected, got %v", selectedFlags)
	}
	sessionJSON(t, sess.do(http.MethodPost, "/scenes/"+second.ID+"/takes/"+imageTakes[0]+"/select", nil), http.StatusOK, nil)

	// scenes.Changed is emitted once per mutation.
	var events []scenes.Changed
	service.Hooks.Register(func(_ context.Context, evt scenes.Changed) error { events = append(events, evt); return nil })
	tenantID := f.fx.TenantID
	cur, err := service.Queries.GetScene(context.Background(), dbgen.GetSceneParams{TenantID: idconv.ToPg(tenantID), ID: idconv.ToPg(uuid.MustParse(second.ID))})
	if err != nil {
		t.Fatal(err)
	}
	motion := "static"
	if _, err := service.UpdateScene(context.Background(), tenantID, uuid.MustParse(second.ID), scenes.Patch{ExpectedVersion: cur.Version, MotionPreset: &motion}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SelectTake(context.Background(), tenantID, uuid.MustParse(second.ID), uuid.MustParse(imageTakes[1])); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Reason != "edit" || events[1].Reason != "take" || len(events[0].SceneIDs) != 1 {
		t.Fatalf("events = %+v", events)
	}

	// Re-split: every scene whose narration is unchanged keeps its id and
	// takes; only the edited scene is replaced. Run in-process so the
	// split's own Changed event reaches this registry too.
	res, err := service.SplitByParagraphsNow(context.Background(), tenantID, uuid.MustParse(f.episodeID), "en", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kept != res.Total-1 {
		t.Fatalf("re-split kept %d of %d", res.Kept, res.Total)
	}
	if len(events) != 3 || events[2].Reason != "split" || len(events[2].SceneIDs) != res.Total {
		t.Fatalf("events after re-split = %+v", events)
	}
	resplit := listScenes(t, sess, f.episodeID, "all")
	if resplit.Items[1].ID != second.ID || resplit.Items[0].ID == s0.ID {
		t.Fatal("re-split did not keep the unchanged scene / replace the edited one")
	}
	if state, _, _ := resplit.Items[1].pip("image"); state != "done" {
		t.Fatalf("the kept scene lost its image take: %s", state)
	}
}

// countingResidency records every model switch.
type countingResidency struct {
	current  *pipeline.ModelRef
	switches []string
}

func (c *countingResidency) Ensure(_ context.Context, m pipeline.ModelRef) error {
	if c.current == nil || *c.current != m {
		c.switches = append(c.switches, m.Backend+":"+m.Model)
		ref := m
		c.current = &ref
	}
	return nil
}
func (c *countingResidency) UnloadAll(context.Context) error { c.current = nil; return nil }
func (c *countingResidency) Current() *pipeline.ModelRef   { return c.current }

// instantRun keeps a real handler's queue, hash and model but succeeds
// at once, so the batching test measures scheduling, not generation.
type instantRun struct{ pipeline.StepHandler }

func (instantRun) Run(context.Context, *pipeline.StepContext) (pipeline.Output, error) {
	return pipeline.Output{"ok": true}, nil
}

func TestGenerateMissingSwitchesModelsAtMostOncePerStage(t *testing.T) {
	skipIfAPIUnreachable(t)
	paragraphs := make([]string, 48)
	for i := range paragraphs {
		paragraphs[i] = fmt.Sprintf("Scene %d. ", i+1) + filler(9) // ~60 words: one scene each at 20–40s
	}
	f := newStoryboardFixture(t, paragraphs)
	var split struct{ SceneCount int }
	sessionJSON(t, f.sess.do(http.MethodPost, "/episodes/"+f.episodeID+"/scenes/split", map[string]any{"lang": "en", "mode": "paragraphs"}), http.StatusOK, &split)
	if split.SceneCount != 48 {
		t.Fatalf("expected 48 scenes, got %d", split.SceneCount)
	}
	engine, _, pool := sceneEngine(t, func(svc *scenes.Service) []pipeline.StepHandler {
		var out []pipeline.StepHandler
		for _, h := range scenes.Handlers(scenes.StepDeps{Service: svc}) {
			out = append(out, instantRun{h})
		}
		return out
	})
	var resp struct {
		RunID  uuid.UUID `json:"runId"`
		Queued struct{ Image, Voice, Align int }
	}
	sessionJSON(t, f.sess.do(http.MethodPost, "/episodes/"+f.episodeID+"/generate-missing", map[string]any{"lang": "en"}), http.StatusAccepted, &resp)
	if resp.Queued.Image != 48 || resp.Queued.Voice != 48 || resp.Queued.Align != 48 {
		t.Fatalf("queued = %+v", resp.Queued)
	}

	// Work the run's gpu jobs one at a time in River's own order
	// (priority, scheduled time, id), through the real GPU executor.
	residency := &countingResidency{}
	gpu := pipeline.NewGPUExecutor(engine, residency, 0)
	ctx := context.Background()
	for range 400 {
		var jobID int64
		var raw []string
		err := pool.QueryRow(ctx, `
			SELECT j.id, ARRAY(SELECT jsonb_array_elements_text(j.args->'step_ids'))
			FROM river_job j
			WHERE j.kind = $1 AND j.queue = 'gpu' AND j.state = 'available'
			  AND EXISTS (SELECT 1 FROM pipeline_steps s WHERE s.run_id = $2 AND (j.args->'step_ids') ? s.id::text)
			ORDER BY j.priority, j.scheduled_at, j.id LIMIT 1`, pipeline.JobKind, resp.RunID).Scan(&jobID, &raw)
		if err != nil {
			break // no job left
		}
		ids := make([]uuid.UUID, len(raw))
		for i, s := range raw {
			ids[i] = uuid.MustParse(s)
		}
		runErr := gpu.Run(ctx, jobID, 1, pipeline.MaxTransientAttempts, ids, nil)
		var snooze *river.JobSnoozeError
		if errors.As(runErr, &snooze) {
			_, _ = pool.Exec(ctx, `UPDATE river_job SET scheduled_at = now() + interval '1 second' WHERE id = $1`, jobID)
			continue
		}
		if runErr != nil {
			t.Fatalf("gpu job %d: %v", jobID, runErr)
		}
		_, _ = pool.Exec(ctx, `UPDATE river_job SET state = 'completed', finalized_at = now() WHERE id = $1`, jobID)
	}
	left := stepsWhere(t, pool, "run_id = $1 AND status <> 'done'", resp.RunID)
	if len(left) != 0 {
		t.Fatalf("%d steps did not finish, e.g. %+v", len(left), left[0])
	}
	if len(residency.switches) > 3 {
		t.Fatalf("48 scenes caused %d residency switches: %v", len(residency.switches), residency.switches)
	}
	want := []string{"comfyui:z-image-turbo", "pyworker:chatterbox", "pyworker:whisper-align"}
	if !slices.Equal(residency.switches, want) {
		t.Fatalf("switch order = %v, want %v", residency.switches, want)
	}
	t.Logf("48 scenes: %d residency switches %v", len(residency.switches), residency.switches)
}

// lavfi makes a fixture with the toolbox's ffmpeg.
func lavfi(t *testing.T, name string, args ...string) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("ffmpeg", append(append([]string{"-hide_banner", "-loglevel", "error", "-y"}, args...), out)...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg fixture %s: %v: %s", name, err, b)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func uploadMedia(t *testing.T, sess *session, kind, mime, name string, body []byte) string {
	t.Helper()
	var presigned struct {
		AssetID   string            `json:"assetId"`
		UploadURL string            `json:"uploadUrl"`
		Fields    map[string]string `json:"fields"`
	}
	sessionJSON(t, sess.do(http.MethodPost, "/assets/presign", map[string]any{"kind": kind, "mime": mime, "bytes": len(body), "filename": name}), http.StatusCreated, &presigned)
	status := postMultipart(t, presigned.UploadURL, presigned.Fields, name, body)
	if status/100 != 2 {
		t.Fatalf("upload %s: %d", name, status)
	}
	sessionJSON(t, sess.do(http.MethodPost, "/assets/"+presigned.AssetID+"/finalize", nil), http.StatusOK, nil)
	return presigned.AssetID
}

func TestMediaVariantsAndPeaksOnLavfiFixtures(t *testing.T) {
	skipIfAPIUnreachable(t)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		missingEnv(t, "ffmpeg", "the toolbox image ships ffmpeg for the lavfi fixtures")
	}
	q := dbgen.New(ownerPool(t))
	fx := createFixtureUser(t, q, "media-backfill", uniqueEmail("media"), "owner")
	sess := login(t, fx.Email, fx.Password)
	png := lavfi(t, "frame.png", "-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=1", "-frames:v", "1")
	wav := lavfi(t, "tone.wav", "-f", "lavfi", "-i", "sine=frequency=440:duration=3:sample_rate=24000", "-ac", "1", "-c:a", "pcm_s16le")
	imageID := uploadMedia(t, sess, "image", "image/png", "frame.png", png)
	audioID := uploadMedia(t, sess, "audio", "audio/wav", "tone.wav", wav)

	var backfill struct{ Variants, Peaks int }
	sessionJSON(t, sess.do(http.MethodPost, "/media/backfill", nil), http.StatusAccepted, &backfill)
	if backfill.Variants != 1 || backfill.Peaks != 1 {
		t.Fatalf("backfill = %+v", backfill)
	}
	pool := appPool(t)
	waitFor(t, 90*time.Second, "variants and peaks from the worker's ffmpeg", func() bool {
		return len(assetVariants(t, pool, uuid.MustParse(imageID)).AVIF) == 3 && assetVariants(t, pool, uuid.MustParse(audioID)).Peaks != ""
	})

	// The variant route redirects to the object; the bytes are real WebP/AVIF.
	client := &http.Client{Timeout: httpTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for variant, magic := range map[string]string{"webp-320": "WEBP", "avif-640": "avif", "original": "PNG"} {
		req, _ := http.NewRequest(http.MethodGet, baseURL()+"/assets/"+imageID+"/variants/"+variant, nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sess.cookie})
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("%s: status %d", variant, resp.StatusCode)
		}
		obj, err := http.Get(resp.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(obj.Body)
		_ = obj.Body.Close()
		if !strings.Contains(string(body[:min(len(body), 32)]), magic) {
			t.Fatalf("%s is not %s (%d bytes)", variant, magic, len(body))
		}
		if variant == "webp-320" {
			t.Logf("webp-320 of a 1280x720 testsrc2 frame: %d bytes", len(body))
		}
	}
	peaks, err := media.ReadPeaks(context.Background(), testStorage(t), assetVariants(t, pool, uuid.MustParse(audioID)).Peaks)
	if err != nil {
		t.Fatal(err)
	}
	if peaks.PeaksPerSecond != 100 || len(peaks.Max) < 295 || len(peaks.Max) > 305 || slices.Max(peaks.Max) < 5 {
		t.Fatalf("peaks: %d per s, %d values, max %d", peaks.PeaksPerSecond, len(peaks.Max), slices.Max(peaks.Max))
	}
	var width, height, durationMs *int32
	if err := pool.QueryRow(context.Background(), `SELECT (SELECT width FROM assets WHERE id = $1), (SELECT height FROM assets WHERE id = $1), (SELECT duration_ms FROM assets WHERE id = $2)`, imageID, audioID).Scan(&width, &height, &durationMs); err != nil {
		t.Fatal(err)
	}
	if width == nil || *width != 1280 || height == nil || *height != 720 || durationMs == nil || *durationMs < 2900 {
		t.Fatalf("dimensions not recorded: %v %v %v", width, height, durationMs)
	}
}

func TestSceneRollupStaysFastAt400Scenes(t *testing.T) {
	skipIfAPIUnreachable(t)
	paragraphs := make([]string, 400)
	for i := range paragraphs {
		paragraphs[i] = fmt.Sprintf("Scene %d. ", i+1) + filler(9)
	}
	f := newStoryboardFixture(t, paragraphs)
	var split struct{ SceneCount int }
	sessionJSON(t, f.sess.do(http.MethodPost, "/episodes/"+f.episodeID+"/scenes/split", map[string]any{"lang": "en", "mode": "paragraphs"}), http.StatusOK, &split)
	if split.SceneCount != 400 {
		t.Fatalf("expected 400 scenes, got %d", split.SceneCount)
	}
	q := dbgen.New(appPool(t))
	var episode dbgen.Episode
	episode, err := q.GetEpisodeByID(context.Background(), dbgen.GetEpisodeByIDParams{TenantID: idconv.ToPg(f.fx.TenantID), ID: idconv.ToPg(uuid.MustParse(f.episodeID))})
	if err != nil {
		t.Fatal(err)
	}
	var times []time.Duration
	for range 7 {
		start := time.Now()
		rows, err := q.SceneRollup(context.Background(), dbgen.SceneRollupParams{TenantID: episode.TenantID, EpisodeID: episode.ID, Lang: "en"})
		if err != nil || len(rows) != 400 {
			t.Fatalf("rollup: %d rows, %v", len(rows), err)
		}
		times = append(times, time.Since(start))
	}
	slices.Sort(times)
	median := times[len(times)/2]
	t.Logf("rollup query for 400 scenes: median %s (min %s, max %s)", median, times[0], times[len(times)-1])
	if median > 60*time.Millisecond {
		t.Fatalf("rollup median %s exceeds 60ms", median)
	}
	start := time.Now()
	list := listScenes(t, f.sess, f.episodeID, "all")
	t.Logf("GET scenes (400, over HTTP): %s", time.Since(start))
	if len(list.Items) != 400 {
		t.Fatalf("listed %d", len(list.Items))
	}
}

func TestVoiceCloneNeedsAuditedConsentAndScenesStayInTheirTenant(t *testing.T) {
	skipIfAPIUnreachable(t)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		missingEnv(t, "ffmpeg", "the toolbox image ships ffmpeg for the lavfi fixtures")
	}
	f := newStoryboardFixture(t, dialogueDraft)
	sess := f.sess
	wav := lavfi(t, "voice.wav", "-f", "lavfi", "-i", "sine=frequency=220:duration=2:sample_rate=24000", "-ac", "1", "-c:a", "pcm_s16le")
	refID := uploadMedia(t, sess, "audio", "audio/wav", "voice.wav", wav)

	body := map[string]any{"name": "Cloned", "engine": "chatterbox", "refAudioAssetId": refID}
	requireStatus(t, sess.do(http.MethodPost, "/settings/voice-presets", body), http.StatusUnprocessableEntity)
	body["consent"] = true
	var preset struct {
		ID        string `json:"id"`
		Consented bool   `json:"consented"`
	}
	sessionJSON(t, sess.do(http.MethodPost, "/settings/voice-presets", body), http.StatusCreated, &preset)
	if !preset.Consented {
		t.Fatal("the preset must record the consent")
	}
	var audits int
	if err := ownerPool(t).QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = 'voice_reference_consented' AND target_id = $1 AND tenant_id = $2`, preset.ID, f.fx.TenantID,
	).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("expected one consent audit entry, got %d", audits)
	}
	// An image is not a voice reference.
	img := uploadMedia(t, sess, "image", "image/png", "x.png", testPNG(8, 8, color.RGBA{R: 1, G: 2, B: 3, A: 255}))
	requireStatus(t, sess.do(http.MethodPost, "/settings/voice-presets", map[string]any{"name": "Bad", "engine": "chatterbox", "refAudioAssetId": img, "consent": true}), http.StatusUnprocessableEntity)
	// A preset in use cannot be deleted.
	sessionJSON(t, sess.do(http.MethodPut, "/characters/"+f.linMo+"/voices/en", map[string]any{"engine": "chatterbox", "voicePresetId": preset.ID}), http.StatusOK, nil)
	requireStatus(t, sess.do(http.MethodDelete, "/settings/voice-presets/"+preset.ID, nil), http.StatusConflict)

	sessionJSON(t, sess.do(http.MethodPost, "/episodes/"+f.episodeID+"/scenes/split", map[string]any{"lang": "en", "mode": "paragraphs"}), http.StatusOK, nil)
	sceneID := listScenes(t, sess, f.episodeID, "all").Items[0].ID

	q := dbgen.New(ownerPool(t))
	other := createFixtureUser(t, q, "storyboard-other", uniqueEmail("storyboard-other"), "owner")
	intruder := login(t, other.Email, other.Password)
	requireStatus(t, intruder.do(http.MethodGet, "/episodes/"+f.episodeID+"/scenes?lang=en", nil), http.StatusNotFound)
	requireStatus(t, intruder.do(http.MethodPatch, "/scenes/"+sceneID, map[string]any{"expectedVersion": 0, "imagePrompt": "x"}), http.StatusNotFound)
	requireStatus(t, intruder.do(http.MethodPost, "/scenes/"+sceneID+"/regenerate", map[string]any{"kind": "image"}), http.StatusNotFound)
	requireStatus(t, intruder.do(http.MethodGet, "/series/"+f.seriesID+"/characters", nil), http.StatusNotFound)
	requireStatus(t, intruder.do(http.MethodPut, "/characters/"+f.linMo+"/voices/en", map[string]any{"engine": "chatterbox"}), http.StatusNotFound)
	requireStatus(t, intruder.do(http.MethodGet, "/assets/"+refID+"/variants/original", nil), http.StatusNotFound)
	// Another tenant's character can never become a speaker or a scene character.
	var foreignChar struct{ ID string `json:"id"` }
	otherSeries := createSeries(t, intruder)
	sessionJSON(t, intruder.do(http.MethodPost, "/series/"+otherSeries+"/characters", map[string]any{"names": map[string]string{"orig": "", "en": "Spy", "vi": ""}}), http.StatusCreated, &foreignChar)
	scene := listScenes(t, sess, f.episodeID, "all").Items[0]
	requireStatus(t, sess.do(http.MethodPatch, "/scenes/"+sceneID, map[string]any{"expectedVersion": scene.Version, "characterIds": []string{foreignChar.ID}}), http.StatusBadRequest)
}

// A voice preset or assignment cannot carry the TTS control keys: a
// stored reference_url with consent would clone any voice without the
// audited consent and make the worker fetch any URL.
func TestVoiceParamsCannotCarryServerControlKeys(t *testing.T) {
	skipIfAPIUnreachable(t)
	f := newStoryboardFixture(t, dialogueDraft)
	sess := f.sess
	for _, params := range []map[string]string{
		{"reference_url": "http://pyworker:50051/clip.wav", "consent": "granted"},
		{"consent": "granted"},
		{"output_key": "other-tenant/x.wav"},
		{"language": "vi"},
		{"not_a_tuning_key": "1"},
	} {
		requireStatus(t, sess.do(http.MethodPost, "/settings/voice-presets", map[string]any{"name": "Forged", "engine": "chatterbox", "params": params}), http.StatusUnprocessableEntity)
		requireStatus(t, sess.do(http.MethodPut, "/characters/"+f.linMo+"/voices/en", map[string]any{"engine": "chatterbox", "params": params}), http.StatusUnprocessableEntity)
		requireStatus(t, sess.do(http.MethodPut, "/series/"+f.seriesID+"/narrator-voices/en", map[string]any{"engine": "chatterbox", "params": params}), http.StatusUnprocessableEntity)
	}
	var preset struct{ ID string `json:"id"` }
	sessionJSON(t, sess.do(http.MethodPost, "/settings/voice-presets", map[string]any{"name": "Tuned", "engine": "vieneu-v3-turbo", "params": map[string]string{"temperature": "0.7", "voice": "Ly"}}), http.StatusCreated, &preset)
	requireStatus(t, sess.do(http.MethodPut, "/settings/voice-presets/"+preset.ID, map[string]any{"name": "Tuned", "engine": "vieneu-v3-turbo", "params": map[string]string{"reference_url": "http://x"}}), http.StatusUnprocessableEntity)
	var audits int
	if err := ownerPool(t).QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = 'voice_reference_consented' AND tenant_id = $1`, f.fx.TenantID,
	).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 0 {
		t.Fatalf("a refused preset must not record a consent, got %d audit entries", audits)
	}
}
