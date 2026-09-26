package models

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/llm/ollama"
)

// recordingOllama answers /api/show, blob and create calls.
type recordingOllama struct {
	mu      sync.Mutex
	models  map[string]bool
	blobs   map[string]int
	creates int
}

func (o *recordingOllama) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	o.mu.Lock()
	defer o.mu.Unlock()
	switch {
	case r.URL.Path == "/api/show":
		var req struct{ Model string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		if o.models[req.Model] {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	case strings.HasPrefix(r.URL.Path, "/api/blobs/"):
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		n, _ := io.Copy(io.Discard, r.Body)
		o.blobs[strings.TrimPrefix(r.URL.Path, "/api/blobs/")] = int(n)
		w.WriteHeader(http.StatusCreated)
	case r.URL.Path == "/api/create":
		var req struct{ Model string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		o.models[req.Model] = true
		o.creates++
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}
}

func TestOllamaPreparerGatesThenImportsOnce(t *testing.T) {
	srv := &recordingOllama{models: map[string]bool{}, blobs: map[string]int{}}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "llm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "llm", "tiny.gguf"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	gateCalls := 0
	p := &OllamaPreparer{
		Manifest:   &Manifest{Version: 1, Models: []Entry{ollamaEntry(), validEntry()}},
		Modelfiles: map[string]string{"tiny-llm": "FROM /models/llm/tiny.gguf\nPARAMETER num_ctx 4096\n"},
		Gate:       func(context.Context, string) error { gateCalls++; return nil },
		Importer:   &ollama.Importer{BaseURL: hs.URL, Client: hs.Client()},
		Dir:        dir,
	}
	ctx := context.Background()
	for range 2 {
		if err := p.Prepare(ctx, "tiny-llm"); err != nil {
			t.Fatal(err)
		}
	}
	if gateCalls != 2 {
		t.Fatalf("the gate must run before every load, ran %d times", gateCalls)
	}
	if srv.creates != 1 || srv.blobs["sha256:"+sha] != 10 {
		t.Fatalf("expected one import of the pinned file, got creates=%d blobs=%v", srv.creates, srv.blobs)
	}

	if err := p.Prepare(ctx, "tiny-model"); !errors.Is(err, pipeline.ErrEngineNotInstalled) {
		t.Fatalf("a non-Ollama entry must be refused as not installed, got %v", err)
	}
	refused := errors.New("not verified")
	p.Gate = func(context.Context, string) error { return refused }
	srv.models = map[string]bool{}
	if err := p.Prepare(ctx, "tiny-llm"); !errors.Is(err, refused) || srv.creates != 1 {
		t.Fatalf("a gate refusal must stop the import, got %v (creates=%d)", err, srv.creates)
	}
}
