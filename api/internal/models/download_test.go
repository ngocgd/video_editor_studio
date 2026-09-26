package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"loomtale/api/internal/pipeline"
)

// memFiles is an in-memory FileStore.
type memFiles struct {
	mu    sync.Mutex
	files map[string][2]string
}

func (m *memFiles) VerifiedFile(_ context.Context, path string) (string, int64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.files[path]
	if !ok {
		return "", 0, false, nil
	}
	size, _ := strconv.ParseInt(v[1], 10, 64)
	return v[0], size, true, nil
}

func (m *memFiles) MarkFileVerified(_ context.Context, path, sha string, size int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.files == nil {
		m.files = map[string][2]string{}
	}
	m.files[path] = [2]string{sha, strconv.FormatInt(size, 10)}
	return nil
}

// fakeHub serves files by remote path and honours Range requests unless
// ignoreRange is set. It records every Range header it received.
type fakeHub struct {
	mu          sync.Mutex
	content     map[string][]byte
	ignoreRange bool
	ranges      []string
	requests    int
}

func (h *fakeHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.requests++
	h.ranges = append(h.ranges, r.Header.Get("Range"))
	ignoreRange := h.ignoreRange
	h.mu.Unlock()
	// /<repo>/resolve/<rev>/<remote>
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/resolve/", 2)
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	remote := parts[1][strings.Index(parts[1], "/")+1:]
	remote, _ = url.PathUnescape(remote)
	data, ok := h.content[remote]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if rng := r.Header.Get("Range"); rng != "" && !ignoreRange {
		var start int
		_, _ = fmt.Sscanf(rng, "bytes=%d-", &start)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(data)-1, len(data)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(data[start:])
		return
	}
	_, _ = w.Write(data)
}

func (h *fakeHub) stats() ([]string, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.ranges...), h.requests
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

type fixture struct {
	dir   string
	hub   *fakeHub
	files *memFiles
	d     *Downloader
	entry Entry
	data  map[string][]byte
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	data := map[string][]byte{
		"a.safetensors": []byte(strings.Repeat("A", 5000)),
		"b.gguf":        []byte(strings.Repeat("B", 3000)),
	}
	hub := &fakeHub{content: data}
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	files := &memFiles{}
	entry := validEntry()
	entry.Files = []File{
		{Path: "diffusion_models/a.safetensors", Remote: "a.safetensors", SHA256: sum(data["a.safetensors"]), Size: 5000},
		{Path: "text_encoders/b.gguf", Remote: "b.gguf", SHA256: sum(data["b.gguf"]), Size: 3000},
	}
	d := &Downloader{
		Dir: dir, BaseURL: srv.URL, Files: files, AllowedHosts: []string{"127.0.0.1"},
		FreeBytes: func(string) (int64, error) { return 1 << 50, nil },
	}
	return &fixture{dir: dir, hub: hub, files: files, d: d, entry: entry, data: data}
}

func (f *fixture) readFinal(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.dir, path))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestInstallDownloadsAndVerifiesEveryFile(t *testing.T) {
	f := newFixture(t)
	var last [2]int64
	if err := f.d.Install(context.Background(), f.entry, func(done, total int64) { last = [2]int64{done, total} }); err != nil {
		t.Fatal(err)
	}
	if string(f.readFinal(t, "diffusion_models/a.safetensors")) != string(f.data["a.safetensors"]) {
		t.Fatal("a.safetensors content mismatch")
	}
	if last != [2]int64{8000, 8000} {
		t.Fatalf("final progress = %v, want 8000/8000", last)
	}
	if sha, size, ok, _ := f.files.VerifiedFile(context.Background(), "text_encoders/b.gguf"); !ok || size != 3000 || sha != f.entry.Files[1].SHA256 {
		t.Fatal("b.gguf was not recorded as verified")
	}
	if _, err := os.Stat(filepath.Join(f.dir, stagingDir, f.entry.Files[0].SHA256+".part")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the .part file must be renamed away after verification")
	}
}

func TestInstallResumesFromPartialFileWithRangeOffset(t *testing.T) {
	f := newFixture(t)
	part := filepath.Join(f.dir, stagingDir, f.entry.Files[0].SHA256+".part")
	if err := os.MkdirAll(filepath.Dir(part), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(part, f.data["a.safetensors"][:1234], 0o644); err != nil {
		t.Fatal(err)
	}
	var first int64 = -1
	err := f.d.Install(context.Background(), f.entry, func(done, _ int64) {
		if first < 0 {
			first = done
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if first != 1234 {
		t.Fatalf("progress should start at the resumed offset 1234, got %d", first)
	}
	if ranges, _ := f.hub.stats(); ranges[0] != "bytes=1234-" {
		t.Fatalf("first request Range = %q, want bytes=1234-", ranges[0])
	}
	if string(f.readFinal(t, "diffusion_models/a.safetensors")) != string(f.data["a.safetensors"]) {
		t.Fatal("resumed file content mismatch")
	}
}

func TestInstallRestartsWhenServerIgnoresRange(t *testing.T) {
	f := newFixture(t)
	f.hub.mu.Lock()
	f.hub.ignoreRange = true
	f.hub.mu.Unlock()
	part := filepath.Join(f.dir, stagingDir, f.entry.Files[0].SHA256+".part")
	_ = os.MkdirAll(filepath.Dir(part), 0o755)
	_ = os.WriteFile(part, []byte("garbage-that-must-be-discarded"), 0o644)
	if err := f.d.Install(context.Background(), f.entry, nil); err != nil {
		t.Fatal(err)
	}
	if string(f.readFinal(t, "diffusion_models/a.safetensors")) != string(f.data["a.safetensors"]) {
		t.Fatal("restarted download content mismatch")
	}
}

func TestInstallRejectsChecksumMismatch(t *testing.T) {
	f := newFixture(t)
	f.entry.Files[1].SHA256 = strings.Repeat("0", 64)
	err := f.d.Install(context.Background(), f.entry, nil)
	if !errors.Is(err, ErrChecksumMismatch) || !errors.Is(err, pipeline.ErrValidation) {
		t.Fatalf("expected a permanent checksum mismatch, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "text_encoders/b.gguf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a file failing verification must never be moved into place")
	}
	if _, err := os.Stat(filepath.Join(f.dir, stagingDir, f.entry.Files[1].SHA256+".part")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a corrupt partial must be deleted, not resumed")
	}
}

func TestInstallAdoptsExistingVerifiedFileWithoutDownloading(t *testing.T) {
	f := newFixture(t)
	for _, file := range f.entry.Files {
		p := filepath.Join(f.dir, file.Path)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, f.data[file.Remote], 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.d.Install(context.Background(), f.entry, nil); err != nil {
		t.Fatal(err)
	}
	if _, requests := f.hub.stats(); requests != 0 {
		t.Fatalf("adopting files already on disk must not download anything, got %d requests", requests)
	}
	if _, _, ok, _ := f.files.VerifiedFile(context.Background(), "diffusion_models/a.safetensors"); !ok {
		t.Fatal("adopted file must be recorded as verified")
	}
}

func TestInstallRedownloadsExistingFileWithWrongContent(t *testing.T) {
	f := newFixture(t)
	p := filepath.Join(f.dir, "diffusion_models/a.safetensors")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte(strings.Repeat("Z", 5000)), 0o644)
	if err := f.d.Install(context.Background(), f.entry, nil); err != nil {
		t.Fatal(err)
	}
	if string(f.readFinal(t, "diffusion_models/a.safetensors")) != string(f.data["a.safetensors"]) {
		t.Fatal("a same-size file with the wrong checksum must be replaced")
	}
}

func TestInstallDiskPreflightRefusesWithoutHeadroom(t *testing.T) {
	f := newFixture(t)
	f.d.HeadroomBytes = 1000
	f.d.FreeBytes = func(string) (int64, error) { return 8999, nil } // needs 8000 + 1000
	err := f.d.Install(context.Background(), f.entry, nil)
	if !errors.Is(err, ErrInsufficientDisk) {
		t.Fatalf("expected the disk pre-flight to refuse, got %v", err)
	}
	if _, requests := f.hub.stats(); requests != 0 {
		t.Fatal("the pre-flight must run before any download")
	}
	f.d.FreeBytes = func(string) (int64, error) { return 9000, nil }
	if err := f.d.Install(context.Background(), f.entry, nil); err != nil {
		t.Fatalf("exactly enough space must pass: %v", err)
	}
}

func TestInstallRefusesDisallowedHostAndLicence(t *testing.T) {
	f := newFixture(t)
	f.d.AllowedHosts = []string{"huggingface.co"}
	if err := f.d.Install(context.Background(), f.entry, nil); !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("expected the host allowlist to refuse 127.0.0.1, got %v", err)
	}
	f = newFixture(t)
	f.entry.Licence.SPDX = "CC-BY-NC-4.0"
	if err := f.d.Install(context.Background(), f.entry, nil); !errors.Is(err, pipeline.ErrLicenceRefused) {
		t.Fatalf("expected the licence gate to refuse, got %v", err)
	}
	if _, requests := f.hub.stats(); requests != 0 {
		t.Fatal("a refused licence must never download anything")
	}
}

func TestCheckHostMatchesSubdomainsOnly(t *testing.T) {
	d := &Downloader{}
	for _, ok := range []string{"https://huggingface.co/x", "https://cdn-lfs.huggingface.co/x", "https://cas-bridge.xethub.hf.co/x"} {
		if err := d.checkHost(ok); err != nil {
			t.Fatalf("%s should be allowed: %v", ok, err)
		}
	}
	for _, bad := range []string{"https://evilhuggingface.co/x", "https://example.com/x", "http://huggingface.co/x"} {
		if err := d.checkHost(bad); err == nil {
			t.Fatalf("%s should be refused", bad)
		}
	}
}
