package comfyui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"loomtale/api/internal/pipeline"
)

// fakeComfy is a minimal ComfyUI: /prompt queues, the next /history poll
// finishes it, /view serves a fixed image, /system_stats reports torch
// memory that rises while a model is "loaded".
type fakeComfy struct {
	mu        sync.Mutex
	fail      string // exception message to fail the next prompt with
	reject    bool   // answer /prompt with 400
	submitted []map[string]any
	uploads   []string
	freed     int
	torchMB   int64
}

func (f *fakeComfy) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /prompt", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Prompt map[string]any `json:"prompt"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.reject {
			http.Error(w, `{"error": "value_not_in_list"}`, http.StatusBadRequest)
			return
		}
		f.submitted = append(f.submitted, body.Prompt)
		f.torchMB = 6000
		_, _ = w.Write([]byte(`{"prompt_id": "p1"}`))
	})
	mux.HandleFunc("GET /history/{id}", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.fail != "" {
			msg := `{"p1": {"status": {"status_str": "error", "completed": false, "messages": [["execution_start", {}], ["execution_error", {"node_type": "KSampler", "exception_type": "RuntimeError", "exception_message": "` + f.fail + `"}]]}, "outputs": {}}}`
			_, _ = w.Write([]byte(msg))
			return
		}
		_, _ = w.Write([]byte(`{"p1": {"status": {"status_str": "success", "completed": true}, "outputs": {"10": {"images": [{"filename": "out.png", "subfolder": "", "type": "output"}]}}}}`))
	})
	mux.HandleFunc("GET /view", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("PNG:" + r.URL.Query().Get("filename")))
	})
	mux.HandleFunc("GET /system_stats", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"devices": []map[string]any{{
			"name": "cuda:0", "vram_total": int64(16) << 30, "vram_free": int64(4) << 30, "torch_vram_total": f.torchMB << 20,
		}}})
	})
	mux.HandleFunc("POST /upload/image", func(w http.ResponseWriter, r *http.Request) {
		file, header, err := r.FormFile("image")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = io.ReadAll(file)
		f.mu.Lock()
		f.uploads = append(f.uploads, header.Filename)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"name": header.Filename, "subfolder": "", "type": "input"})
	})
	mux.HandleFunc("POST /free", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.freed++
		f.torchMB = 0
		f.mu.Unlock()
	})
	mux.HandleFunc("POST /interrupt", func(http.ResponseWriter, *http.Request) {})
	return mux
}

// snapshot copies the recorded calls under the lock, so tests never read
// fields a handler goroutine writes.
func (f *fakeComfy) snapshot() (submitted []map[string]any, uploads []string, freed int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.submitted...), append([]string(nil), f.uploads...), f.freed
}

func (f *fakeComfy) set(fn func(*fakeComfy)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func newTestEngine(t *testing.T) (*Engine, *fakeComfy) {
	t.Helper()
	fake := &fakeComfy{}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	tpl := loadScene(t)
	edit := &Template{
		Name: "edit",
		Graph: map[string]any{
			"4":  map[string]any{"class_type": "LoadImage", "inputs": map[string]any{"image": ""}},
			"6":  map[string]any{"class_type": "TextEncodeQwenImageEditPlus", "inputs": map[string]any{"prompt": ""}},
			"10": map[string]any{"class_type": "SaveImage", "inputs": map[string]any{}},
		},
		Map: ParamMap{OutputNode: "10", Params: map[string]ParamSpec{
			"prompt":    {Node: "6", Input: "prompt", Required: true},
			"ref_image": {Node: "4", Input: "image", Kind: "image", Required: true},
		}},
	}
	return &Engine{Client: New(srv.URL, srv.Client()), Templates: map[string]*Template{"scene": tpl, "edit": edit}, PollInterval: 5}, fake
}

func TestEngineRunReturnsOutputImagesAndVRAMPeak(t *testing.T) {
	engine, _ := newTestEngine(t)
	res, err := engine.Run(context.Background(), "scene", map[string]any{"prompt": "a fox"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Images) != 1 || string(res.Images[0]) != "PNG:out.png" {
		t.Fatalf("unexpected images %q", res.Images)
	}
	if res.VRAMPeakMB != 12*1024 || res.TorchPeakMB != 6000 {
		t.Fatalf("VRAM peak %d / torch %d", res.VRAMPeakMB, res.TorchPeakMB)
	}
}

func TestEngineRunUploadsImageUnderGeneratedName(t *testing.T) {
	engine, fake := newTestEngine(t)
	if _, err := engine.Run(context.Background(), "edit", map[string]any{"prompt": "sheet"}, map[string][]byte{"ref_image": []byte("img")}); err != nil {
		t.Fatal(err)
	}
	submitted, uploads, _ := fake.snapshot()
	if len(uploads) != 1 || !strings.HasPrefix(uploads[0], "loomtale-") {
		t.Fatalf("uploads = %v", uploads)
	}
	if got := input(submitted[0], "4", "image"); got != uploads[0] {
		t.Fatalf("LoadImage must reference the uploaded name, got %v", got)
	}
}

func TestEngineRunClassifiesOOMAndRejectedWorkflows(t *testing.T) {
	engine, fake := newTestEngine(t)
	fake.set(func(f *fakeComfy) { f.fail = "CUDA out of memory. Tried to allocate 2.00 GiB" })
	_, err := engine.Run(context.Background(), "scene", map[string]any{"prompt": "x"}, nil)
	if !errors.Is(err, pipeline.ErrGPUOOM) {
		t.Fatalf("expected gpu_oom, got %v", err)
	}

	fake.set(func(f *fakeComfy) { f.fail = "shape mismatch" })
	_, err = engine.Run(context.Background(), "scene", map[string]any{"prompt": "x"}, nil)
	if !errors.Is(err, ErrExecution) || errors.Is(err, pipeline.ErrGPUOOM) {
		t.Fatalf("expected a plain execution error, got %v", err)
	}

	fake.set(func(f *fakeComfy) { f.fail, f.reject = "", true })
	_, err = engine.Run(context.Background(), "scene", map[string]any{"prompt": "x"}, nil)
	if class, _ := pipeline.Classify(err); class != pipeline.ClassPermanent {
		t.Fatalf("a workflow ComfyUI rejects must fail permanently, got %v", err)
	}
}

func TestBackendLoadRunsWarmupAndProvesResidency(t *testing.T) {
	engine, fake := newTestEngine(t)
	gated := ""
	b := &Backend{Engine: engine, Warmups: map[string]string{"z": "scene", "e": "edit"}, Gate: func(_ context.Context, m string) error {
		gated = m
		return nil
	}}
	ctx := context.Background()
	if ok, _ := b.Resident(ctx, "z"); ok {
		t.Fatal("nothing is resident before a load")
	}
	if _, err := b.Load(ctx, "z"); err != nil {
		t.Fatal(err)
	}
	if gated != "z" {
		t.Fatal("Load must run the gate first")
	}
	submitted, _, _ := fake.snapshot()
	if got := input(submitted[0], "8", "seed"); got != float64(1) {
		t.Fatalf("warm-up params must be applied, seed = %v", got)
	}
	if ok, err := b.Resident(ctx, "z"); !ok || err != nil {
		t.Fatalf("z must be resident after load: %v %v", ok, err)
	}
	if ok, _ := b.Resident(ctx, "e"); ok {
		t.Fatal("only the loaded model is resident")
	}
	reachable, loaded := b.Probe(ctx)
	if !reachable || len(loaded) != 1 || loaded[0] != "z" {
		t.Fatalf("probe = %v %v", reachable, loaded)
	}

	err := b.Unload(ctx)
	if _, _, freed := fake.snapshot(); err != nil || freed != 1 {
		t.Fatalf("unload must call /free: %v %d", err, freed)
	}
	if ok, _ := b.Resident(ctx, "z"); ok {
		t.Fatal("nothing is resident after an unload")
	}

	// An image-edit warm-up uploads a placeholder reference image.
	if _, err := b.Load(ctx, "e"); err != nil {
		t.Fatal(err)
	}
	if _, uploads, _ := fake.snapshot(); len(uploads) != 1 {
		t.Fatal("the edit warm-up must upload a reference image")
	}
}

func TestBackendLoadRefusesGatedOrUnknownModels(t *testing.T) {
	engine, fake := newTestEngine(t)
	refused := errors.New("licence refused")
	b := &Backend{Engine: engine, Warmups: map[string]string{"z": "scene"}, Gate: func(context.Context, string) error { return refused }}
	if _, err := b.Load(context.Background(), "z"); !errors.Is(err, refused) {
		t.Fatalf("expected the gate error, got %v", err)
	}
	if submitted, _, _ := fake.snapshot(); len(submitted) != 0 {
		t.Fatal("a gated model must never reach ComfyUI")
	}
	if _, err := b.Load(context.Background(), "unknown"); !errors.Is(err, pipeline.ErrEngineNotInstalled) {
		t.Fatalf("expected engine_not_installed, got %v", err)
	}
}
