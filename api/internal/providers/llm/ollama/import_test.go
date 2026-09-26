package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestParseModelfileReadsFromParametersAndBlocks(t *testing.T) {
	mf, err := ParseModelfile("# comment\nFROM /models/llm/x.gguf\nPARAMETER num_ctx 16384\nPARAMETER temperature 0.7\nPARAMETER stop <end>\nPARAMETER stop \"<eot>\"\nSYSTEM \"\"\"You narrate\nstories.\"\"\"\nTEMPLATE {{ .Prompt }}\n")
	if err != nil {
		t.Fatal(err)
	}
	if mf.From != "/models/llm/x.gguf" {
		t.Fatalf("from = %q", mf.From)
	}
	if mf.Parameters["num_ctx"] != int64(16384) || mf.Parameters["temperature"] != 0.7 {
		t.Fatalf("numeric parameters = %v", mf.Parameters)
	}
	if stops, _ := mf.Parameters["stop"].([]string); len(stops) != 2 || stops[1] != "<eot>" {
		t.Fatalf("stop = %v", mf.Parameters["stop"])
	}
	if mf.System != "You narrate\nstories." || mf.Template != "{{ .Prompt }}" {
		t.Fatalf("system %q template %q", mf.System, mf.Template)
	}
}

func TestParseModelfileRefusesAnythingButALocalGGUF(t *testing.T) {
	for name, text := range map[string]string{
		"registry model": "FROM qwen3:8b\n",
		"relative path":  "FROM ./x.gguf\n",
		"safetensors":    "FROM /models/x.safetensors\n",
		"no from":        "PARAMETER num_ctx 1\n",
		"two froms":      "FROM /a.gguf\nFROM /b.gguf\n",
		"adapter":        "FROM /a.gguf\nADAPTER /b.gguf\n",
		"unterminated":   "FROM /a.gguf\nSYSTEM \"\"\"open\n",
	} {
		if _, err := ParseModelfile(text); err == nil {
			t.Errorf("%s: expected a refusal", name)
		}
	}
}

// fakeOllama records blob uploads and create calls.
type fakeOllama struct {
	mu      sync.Mutex
	blobs   map[string][]byte
	models  map[string]map[string]any
	uploads int
}

func (f *fakeOllama) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/blobs/"):
		digest := strings.TrimPrefix(r.URL.Path, "/api/blobs/")
		if r.Method == http.MethodHead {
			if _, ok := f.blobs[digest]; ok {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.blobs[digest] = body
		f.uploads++
		w.WriteHeader(http.StatusCreated)
	case r.URL.Path == "/api/show":
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if _, ok := f.models[req["model"].(string)]; ok {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	case r.URL.Path == "/api/create":
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		files, _ := req["files"].(map[string]any)
		for _, digest := range files {
			if _, ok := f.blobs[digest.(string)]; !ok {
				http.Error(w, `{"error":"blob missing"}`, http.StatusBadRequest)
				return
			}
		}
		f.models[req["model"].(string)] = req
		_, _ = w.Write([]byte(`{"status":"success"}`))
	default:
		http.NotFound(w, r)
	}
}

func TestImporterUploadsTheBlobOnceAndCreatesTheModel(t *testing.T) {
	fake := &fakeOllama{blobs: map[string][]byte{}, models: map[string]map[string]any{}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	gguf := filepath.Join(t.TempDir(), "x.gguf")
	if err := os.WriteFile(gguf, []byte("GGUF-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	imp := &Importer{BaseURL: srv.URL, Client: srv.Client()}
	mf := Modelfile{From: "/models/llm/x.gguf", Parameters: map[string]any{"num_ctx": int64(4096)}}
	ctx := context.Background()

	if ok, err := imp.Exists(ctx, "tiny"); err != nil || ok {
		t.Fatalf("exists before import = %v, %v", ok, err)
	}
	for range 2 {
		if err := imp.Import(ctx, "tiny", mf, gguf, "abc123"); err != nil {
			t.Fatal(err)
		}
	}
	if fake.uploads != 1 {
		t.Fatalf("the blob must be uploaded once and reused, got %d uploads", fake.uploads)
	}
	if string(fake.blobs["sha256:abc123"]) != "GGUF-bytes" {
		t.Fatal("uploaded blob content mismatch")
	}
	created := fake.models["tiny"]
	if files := created["files"].(map[string]any); files["x.gguf"] != "sha256:abc123" {
		t.Fatalf("create files = %v", files)
	}
	if params := created["parameters"].(map[string]any); params["num_ctx"] != float64(4096) {
		t.Fatalf("create parameters = %v", params)
	}
	if ok, err := imp.Exists(ctx, "tiny"); err != nil || !ok {
		t.Fatalf("exists after import = %v, %v", ok, err)
	}
}

func TestBackendRunsPrepareBeforeLoadAndUnloadsEveryRunningModel(t *testing.T) {
	var mu sync.Mutex
	var unloaded []string
	running := []string{"a:latest", "b:latest"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/ps":
			models := []map[string]string{}
			for _, n := range running {
				models = append(models, map[string]string{"name": n})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
		case "/api/chat":
			var req chatRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.KeepAlive == "0" {
				unloaded = append(unloaded, req.Model)
			}
			_, _ = w.Write([]byte(`{"done":true}`))
		case "/api/generate":
			_, _ = w.Write([]byte(`{"done":true}`))
		}
	}))
	defer srv.Close()

	prepared := ""
	b := &Backend{
		Provider: New(srv.URL, "a", srv.Client()),
		Prepare:  func(_ context.Context, model string) error { prepared = model; return nil },
	}
	ctx := context.Background()
	if err := b.Unload(ctx); err != nil {
		t.Fatal(err)
	}
	if len(unloaded) != 2 {
		t.Fatalf("every running model must be unloaded, got %v", unloaded)
	}
	if _, err := b.Load(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if prepared != "b" {
		t.Fatalf("Prepare must run for the loaded model, got %q", prepared)
	}
	if ok, err := b.Resident(ctx, "b"); err != nil || !ok {
		t.Fatalf("b should be resident via /api/ps: %v %v", ok, err)
	}
	if ok, _ := b.Resident(ctx, "c"); ok {
		t.Fatal("c is not in /api/ps")
	}
}
