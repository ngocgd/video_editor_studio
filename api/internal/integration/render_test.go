//go:build integration

// Render tests: an episode whose scenes were generated on the test
// doubles is rendered end to end by the live worker container's ffmpeg
// (segments, loudness-normalised audio, subtitles, compose, QC and the
// preview proxy), re-rendered from the segment cache, restarted after a
// scene edit, and refused below the disk watermark.
package integration

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"loomtale/api/internal/diskguard"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/render"
)

type qcReportDTO struct {
	Passed             bool     `json:"passed"`
	Failures           []string `json:"failures"`
	IntegratedLufs     float64  `json:"integratedLufs"`
	TruePeakDbtp       float64  `json:"truePeakDbtp"`
	DurationMs         int64    `json:"durationMs"`
	ExpectedDurationMs int64    `json:"expectedDurationMs"`
	AvDriftMs          float64  `json:"avDriftMs"`
	MissingScenes      []string `json:"missingScenes"`
	PlaceholderScenes  []string `json:"placeholderScenes"`
	Streams            struct {
		Video, Audio, Subtitle int
	} `json:"streams"`
	MissingKeyframes []float64 `json:"missingKeyframes"`
	Encoder          string    `json:"encoder"`
	Sha256           string    `json:"sha256"`
}

type renderDTO struct {
	ID             string      `json:"id"`
	ManifestID     string      `json:"manifestId"`
	AssetID        string      `json:"assetId"`
	SrtAssetID     string      `json:"srtAssetId"`
	PreviewAssetID string      `json:"previewAssetId"`
	DurationMs     int64       `json:"durationMs"`
	Encoder        string      `json:"encoder"`
	Report         qcReportDTO `json:"report"`
}

type renderStartedDTO struct {
	RunID      uuid.UUID `json:"runId"`
	ManifestID uuid.UUID `json:"manifestId"`
	Steps      int       `json:"steps"`
	Reused     int       `json:"reused"`
}

type renderStatusDTO struct {
	Ready    bool     `json:"ready"`
	Reasons  []string `json:"reasons"`
	Estimate *struct {
		Segments       int `json:"segments"`
		CachedSegments int `json:"cachedSegments"`
	} `json:"estimate"`
	ActiveRunID string `json:"activeRunId"`
	Latest      *struct {
		ManifestID         string `json:"manifestId"`
		RunID              string `json:"runId"`
		RestartedAfterEdit bool   `json:"restartedAfterEdit"`
		ReusedSegments     int    `json:"reusedSegments"`
	} `json:"latest"`
}

func renderStatus(t *testing.T, f renderFixture) renderStatusDTO {
	t.Helper()
	var st renderStatusDTO
	sessionJSON(t, f.sess.do(http.MethodGet, "/episodes/"+f.episodeID+"/render-status?lang=en", nil), http.StatusOK, &st)
	return st
}

func listRenders(t *testing.T, f renderFixture) []renderDTO {
	t.Helper()
	var out struct {
		Items []renderDTO `json:"items"`
	}
	sessionJSON(t, f.sess.do(http.MethodGet, "/episodes/"+f.episodeID+"/renders?lang=en&limit=10", nil), http.StatusOK, &out)
	return out.Items
}

// requireQCPassed checks the stored QC report of a finished render.
func requireQCPassed(t *testing.T, r renderDTO) {
	t.Helper()
	q := r.Report
	const frameMs = 1000.0 / 30
	switch {
	case !q.Passed || len(q.Failures) > 0:
		t.Fatalf("QC failed: %+v", q)
	case q.Streams.Video != 1 || q.Streams.Audio != 1 || q.Streams.Subtitle != 1:
		t.Fatalf("expected one video, audio and subtitle stream, got %+v", q.Streams)
	case float64(abs64(q.DurationMs-q.ExpectedDurationMs)) > frameMs:
		t.Fatalf("duration %d ms is over a frame off the expected %d ms", q.DurationMs, q.ExpectedDurationMs)
	case q.AvDriftMs > 2*frameMs:
		t.Fatalf("A/V drift %.1f ms is over two frames", q.AvDriftMs)
	case q.IntegratedLufs < -15 || q.IntegratedLufs > -13 || q.TruePeakDbtp > -0.5:
		t.Fatalf("loudness %.1f LUFS / %.1f dBTP misses the -14 LUFS / -1 dBTP target", q.IntegratedLufs, q.TruePeakDbtp)
	case len(q.MissingKeyframes) > 0:
		t.Fatalf("segment starts without a keyframe: %v", q.MissingKeyframes)
	case len(q.MissingScenes) > 0 || len(q.PlaceholderScenes) > 0:
		t.Fatalf("missing %v / placeholder %v scenes", q.MissingScenes, q.PlaceholderScenes)
	case q.Encoder != "libx264" || r.Encoder != "libx264" || len(q.Sha256) != 64:
		t.Fatalf("encoder %q/%q sha256 %q", q.Encoder, r.Encoder, q.Sha256)
	case r.SrtAssetID == "" || r.PreviewAssetID == "":
		t.Fatalf("a finished render needs an SRT and a preview proxy: %+v", r)
	}
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func TestRenderEpisodeCacheReuseAndRestartAfterEdit(t *testing.T) {
	skipIfAPIUnreachable(t)
	f := newRenderFixture(t)
	ctx := context.Background()
	sessionJSON(t, f.sess.do(http.MethodPut, "/episodes/"+f.episodeID+"/render-settings/en", smallRenderSettings), http.StatusOK, nil)
	episodeID := uuid.MustParse(f.episodeID)

	st := renderStatus(t, f)
	if !st.Ready || len(st.Reasons) > 0 || st.Estimate == nil || st.Estimate.Segments < len(f.scenes) {
		t.Fatalf("a fully generated episode must be ready with an estimate: %+v", st)
	}

	// Below the disk watermark the freeze is refused and leaves no
	// manifest behind.
	full := &diskguard.Watermark{Path: "/data", MinFree: 40 * diskguard.GB, WarnFree: 60 * diskguard.GB,
		FreeBytes: func(string) (uint64, error) { return diskguard.GB, nil }}
	_, err := renderFreezer(t, false, []pipeline.AdmissionCheck{full.Check}).Freeze(ctx, render.FreezeRequest{TenantID: f.fx.TenantID, EpisodeID: episodeID, Lang: "en"})
	if !errors.Is(err, pipeline.ErrAdmissionDenied) {
		t.Fatalf("a freeze below the disk watermark must be denied, got %v", err)
	}
	var manifests int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM render_manifests WHERE episode_id = $1`, episodeID).Scan(&manifests); err != nil {
		t.Fatal(err)
	}
	if manifests != 0 {
		t.Fatalf("a refused freeze left %d manifests", manifests)
	}

	// First render: every segment, the audio master and the subtitles are
	// encoded by the live worker, then composed and checked.
	var first renderStartedDTO
	sessionJSON(t, f.sess.do(http.MethodPost, "/episodes/"+f.episodeID+"/renders", map[string]any{"lang": "en"}), http.StatusAccepted, &first)
	if first.Steps == 0 || first.Reused != 0 {
		t.Fatalf("a first render must encode everything: %+v", first)
	}
	// A second start while the first is running is refused.
	requireStatus(t, f.sess.do(http.MethodPost, "/episodes/"+f.episodeID+"/renders", map[string]any{"lang": "en"}), http.StatusConflict)
	waitRunDone(t, f.pool, first.RunID, 10*time.Minute)
	renders := listRenders(t, f)
	if len(renders) != 1 || renders[0].ManifestID != first.ManifestID.String() {
		t.Fatalf("expected the first render, got %+v", renders)
	}
	requireQCPassed(t, renders[0])
	var one renderDTO
	sessionJSON(t, f.sess.do(http.MethodGet, "/renders/"+renders[0].ID, nil), http.StatusOK, &one)
	if one.AssetID != renders[0].AssetID {
		t.Fatalf("GET /renders/{id} = %+v", one)
	}

	// Second render of the unchanged episode: every cacheable output is
	// reused, only compose and the preview run.
	var second renderStartedDTO
	sessionJSON(t, f.sess.do(http.MethodPost, "/episodes/"+f.episodeID+"/renders", map[string]any{"lang": "en"}), http.StatusAccepted, &second)
	if second.Steps != 0 || second.Reused != first.Steps {
		t.Fatalf("an unchanged re-render must reuse all %d cache entries: %+v", first.Steps, second)
	}
	if kinds := stepKindCounts(t, f.pool, second.RunID); len(kinds) != 2 || kinds[render.KindCompose] != 1 || kinds[render.KindPreview] != 1 {
		t.Fatalf("a cached re-render must only compose and preview, got %v", kinds)
	}
	waitRunDone(t, f.pool, second.RunID, 5*time.Minute)
	if renders = listRenders(t, f); len(renders) != 2 {
		t.Fatalf("expected two renders, got %d", len(renders))
	}
	requireQCPassed(t, renders[0])

	// A render held in the queue is superseded by a scene edit: after the
	// debounce the api freezes a new manifest that re-encodes only the
	// edited scene and its transitions.
	held, err := renderFreezer(t, true, nil).Freeze(ctx, render.FreezeRequest{TenantID: f.fx.TenantID, EpisodeID: episodeID, Lang: "en"})
	if err != nil {
		t.Fatalf("freeze the held render: %v", err)
	}
	if st := renderStatus(t, f); st.Ready || st.ActiveRunID != held.RunID.String() {
		t.Fatalf("a running render must disable the button and be reported: %+v", st)
	}
	var sceneList struct {
		Items []struct {
			ID           string `json:"id"`
			Version      int64  `json:"version"`
			MotionPreset string `json:"motionPreset"`
		} `json:"items"`
	}
	sessionJSON(t, f.sess.do(http.MethodGet, "/episodes/"+f.episodeID+"/scenes?lang=en&filter=all", nil), http.StatusOK, &sceneList)
	edited := sceneList.Items[1]
	motion := "static"
	if edited.MotionPreset == "static" {
		motion = "ken_burns"
	}
	sessionJSON(t, f.sess.do(http.MethodPatch, "/scenes/"+edited.ID, map[string]any{"expectedVersion": edited.Version, "motionPreset": motion}), http.StatusOK, nil)

	var old runRow
	waitFor(t, time.Minute, "the held render to be superseded after the edit", func() bool {
		old = readRun(t, f.pool, held.RunID)
		return old.Status == "superseded" && old.SupersededBy != nil
	})
	restarted := *old.SupersededBy
	kinds := stepKindCounts(t, f.pool, restarted)
	if kinds[render.KindSceneBody] != 1 || kinds[render.KindTransition] > 2 || kinds[render.KindAudioMaster] != 0 || kinds[render.KindSubtitles] != 0 {
		t.Fatalf("an edit of one scene's motion must re-encode only that scene and its transitions, got %v", kinds)
	}
	st = renderStatus(t, f)
	if st.Latest == nil || !st.Latest.RestartedAfterEdit || st.Latest.ReusedSegments == 0 || st.Latest.RunID != restarted.String() {
		t.Fatalf("the status must report the restart and the reused segments: %+v", st.Latest)
	}
	waitRunDone(t, f.pool, restarted, 5*time.Minute)
	if renders = listRenders(t, f); len(renders) != 3 {
		t.Fatalf("expected three renders after the restart, got %d", len(renders))
	}
	requireQCPassed(t, renders[0])
}
