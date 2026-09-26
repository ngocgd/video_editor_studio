package bench

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/models"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/image/comfyui"
)

type recordingQueries struct {
	dbgen.Querier
	mu   sync.Mutex
	rows []dbgen.InsertModelBenchmarkParams
}

func (q *recordingQueries) InsertModelBenchmark(_ context.Context, p dbgen.InsertModelBenchmarkParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.rows = append(q.rows, p)
	return nil
}

type fakeResidency struct {
	current *pipeline.ModelRef
	ensures int
}

func (f *fakeResidency) Ensure(_ context.Context, m pipeline.ModelRef) error {
	f.ensures++
	f.current = &m
	return nil
}
func (f *fakeResidency) UnloadAll(context.Context) error { f.current = nil; return nil }
func (f *fakeResidency) Current() *pipeline.ModelRef      { return f.current }

// fakeComfy answers every prompt with one image named after the prompt's
// output prefix, or fails prompts whose prefix is in failPrefixes.
func fakeComfy(t *testing.T, fail string) string {
	t.Helper()
	var mu sync.Mutex
	prompts := map[string]string{}
	n := 0
	mux := http.NewServeMux()
	mux.HandleFunc("POST /prompt", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		n++
		id := fmt.Sprintf("p%d", n)
		prompts[id] = id
		mu.Unlock()
		_, _ = fmt.Fprintf(w, `{"prompt_id": %q}`, id)
	})
	mux.HandleFunc("GET /history/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if fail != "" && id == fail {
			_, _ = fmt.Fprintf(w, `{%q: {"status": {"status_str": "error", "messages": [["execution_error", {"node_type": "KSampler", "exception_type": "RuntimeError", "exception_message": "CUDA out of memory"}]]}, "outputs": {}}}`, id)
			return
		}
		_, _ = fmt.Fprintf(w, `{%q: {"status": {"status_str": "success", "completed": true}, "outputs": {"10": {"images": [{"filename": "%s.png", "subfolder": "", "type": "output"}]}, "11": {"images": [{"filename": "%s.png", "subfolder": "", "type": "output"}]}}}}`, id, id, id)
	})
	mux.HandleFunc("GET /view", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(r.URL.Query().Get("filename"))) })
	mux.HandleFunc("GET /system_stats", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"devices": [{"vram_total": 17179869184, "vram_free": 4294967296, "torch_vram_total": 8589934592}]}`))
	})
	mux.HandleFunc("POST /upload/image", func(w http.ResponseWriter, r *http.Request) {
		_, header, _ := r.FormFile("image")
		_, _ = fmt.Fprintf(w, `{"name": %q}`, header.Filename)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func newHarness(t *testing.T, fail string) (*Harness, *recordingQueries, *fakeResidency) {
	t.Helper()
	templates, err := models.EmbeddedTemplates()
	if err != nil {
		t.Fatal(err)
	}
	url := fakeComfy(t, fail)
	q := &recordingQueries{}
	res := &fakeResidency{}
	h := &Harness{
		Engine:    &comfyui.Engine{Client: comfyui.New(url, http.DefaultClient), Templates: templates, PollInterval: time.Millisecond},
		Residency: res, Queries: q,
		RSS:        func(time.Time, time.Time) (int64, bool) { return 5000, true },
		SwapUsedMB: func() (int64, error) { return 100, nil },
	}
	return h, q, res
}

func TestSmokeSuiteRecordsEveryCaseAndSwitch(t *testing.T) {
	h, q, res := newHarness(t, "")
	h.OutDir = t.TempDir()
	report, err := h.Run(context.Background(), Suites()["image-smoke"])
	if err != nil {
		t.Fatal(err)
	}
	if len(q.rows) != 3 || len(report.Results) != 3 {
		t.Fatalf("expected 3 recorded cases, got %d", len(q.rows))
	}
	for _, row := range q.rows {
		if !row.Ok || !row.Seconds.Valid || !row.VramPeakMb.Valid || row.VramPeakMb.Int64 != 12*1024 || !row.RssPeakMb.Valid {
			t.Fatalf("incomplete row %+v", row)
		}
		if !row.SwitchSeconds.Valid {
			t.Fatalf("every smoke case changes model, so each must record a switch: %s", row.CaseName)
		}
	}
	if res.ensures != 3 {
		t.Fatalf("expected 3 residency switches, got %d", res.ensures)
	}
	if _, err := os.Stat(filepath.Join(h.OutDir, "charsheet.png")); err != nil {
		t.Fatal("output images must be saved to OutDir")
	}
}

func TestImageSuiteHasTwentyScenesAndThreeCharsheets(t *testing.T) {
	s := Suites()["image"]
	scenes, sheets := 0, 0
	for _, c := range s.Cases {
		switch c.Workflow {
		case "scene_txt2img_zimage":
			scenes++
		case "charsheet_qwenedit":
			sheets++
			if c.RefFrom == "" {
				t.Fatal("character sheets need a reference case")
			}
		}
	}
	if scenes != 20 || sheets != 3 {
		t.Fatalf("image suite has %d scenes and %d charsheets", scenes, sheets)
	}
}

func TestGateFailsOnOOMAndMissingReference(t *testing.T) {
	h, _, _ := newHarness(t, "p1") // the first scene hits CUDA OOM
	report, err := h.Run(context.Background(), Suites()["image-gate"])
	if err != nil {
		t.Fatal(err)
	}
	if report.GateOK {
		t.Fatal("an OOM must fail the gate")
	}
	if !errors.Is(report.Results[0].Err, pipeline.ErrGPUOOM) {
		t.Fatalf("first case error = %v", report.Results[0].Err)
	}
	if report.Results[2].Err == nil {
		t.Fatal("the charsheet whose reference scene failed must fail too")
	}
}

func TestGatePassesWithinLimitsAndFailsOverThem(t *testing.T) {
	h, _, _ := newHarness(t, "")
	report, err := h.Run(context.Background(), Suites()["image-gate"])
	if err != nil {
		t.Fatal(err)
	}
	if !report.GateOK {
		t.Fatalf("gate should pass: %v", report.GateReasons)
	}
	h.RSS = func(time.Time, time.Time) (int64, bool) { return GateRSSLimitMB + 1, true }
	h.SwapUsedMB = func() (int64, error) { return GateSwapLimitMB + 1, nil }
	report, _ = h.Run(context.Background(), Suites()["image-gate"])
	if report.GateOK || len(report.GateReasons) < 2 {
		t.Fatalf("RSS and swap over the limits must fail the gate: %v", report.GateReasons)
	}
}

func TestRSSFromSamplesFileTakesPeakInWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rss.tsv")
	base := time.Now()
	lines := fmt.Sprintf("%d %d\n%d %d\n%d %d\nnot a sample\n",
		base.Add(-time.Minute).UnixMilli(), int64(9000)<<20,
		base.UnixMilli(), int64(3000)<<20,
		base.Add(time.Second).UnixMilli(), int64(4000)<<20)
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	peak, ok := RSSFromSamplesFile(path)(base, base.Add(time.Second))
	if !ok || peak != 4000 {
		t.Fatalf("peak = %d %v, want 4000 (the out-of-window 9000 MB sample must be ignored)", peak, ok)
	}
	if _, ok := RSSFromSamplesFile(path)(base.Add(time.Hour), base.Add(2*time.Hour)); ok {
		t.Fatal("a window with no samples must report none")
	}
}
