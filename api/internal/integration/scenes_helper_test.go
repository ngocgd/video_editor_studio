//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/media"
	"loomtale/api/internal/media/ffmpeg"
	"loomtale/api/internal/models"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/align"
	"loomtale/api/internal/providers/image/comfyui"
	"loomtale/api/internal/providers/tts"
	"loomtale/api/internal/scenes"
	"loomtale/api/internal/storage"
	workerv1 "loomtale/api/internal/workerpb/loomtale/worker/v1"
)

// testStorage is the in-network MinIO client, from the same env the api
// and worker containers use.
func testStorage(t *testing.T) *storage.Internal {
	t.Helper()
	cfg := storage.Config{
		Endpoint: os.Getenv("MINIO_ENDPOINT"), AccessKey: os.Getenv("MINIO_APP_ACCESS_KEY"),
		SecretKey: os.Getenv("MINIO_APP_SECRET_KEY"), Bucket: os.Getenv("MINIO_BUCKET"), Region: "us-east-1",
	}
	if cfg.Endpoint == "" || cfg.AccessKey == "" {
		missingEnv(t, "MINIO_ENDPOINT", "the scene step tests write and read objects")
	}
	st, err := storage.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// testPNG is a small solid-colour PNG.
func testPNG(w, h int, c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// fakeComfy is a test double of the ComfyUI HTTP API the engine uses:
// /prompt, /history/{id}, /view and /system_stats. Every prompt finishes
// at once with one PNG output on the workflow's output node.
type fakeComfy struct {
	mu      sync.Mutex
	prompts []map[string]any
}

func (f *fakeComfy) handler(outputNode string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /prompt", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Prompt map[string]any `json:"prompt"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.prompts = append(f.prompts, body.Prompt)
		n := len(f.prompts)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"prompt_id": fmt.Sprintf("p%d", n)})
	})
	mux.HandleFunc("GET /history/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		_ = json.NewEncoder(w).Encode(map[string]any{id: map[string]any{
			"status":  map[string]any{"status_str": "success", "completed": true, "messages": []any{}},
			"outputs": map[string]any{outputNode: map[string]any{"images": []any{map[string]string{"filename": id + ".png", "subfolder": "", "type": "output"}}}},
		}})
	})
	mux.HandleFunc("GET /view", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(testPNG(64, 36, color.RGBA{R: 40, G: 90, B: 160, A: 255}))
	})
	mux.HandleFunc("GET /system_stats", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"devices": []any{map[string]any{"name": "fake", "vram_total": 16 << 30, "vram_free": 8 << 30, "torch_vram_total": 4 << 30}}})
	})
	return mux
}

func (f *fakeComfy) lastPrompt() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.prompts) == 0 {
		return nil
	}
	return f.prompts[len(f.prompts)-1]
}

// sineWav is a 24kHz mono 16-bit tone of the given length.
func sineWav(ms int) []byte {
	f := scenes.WavFormat{Channels: 1, SampleRate: 24000, BitsPerSample: 16}
	b := scenes.SilenceWav(f, ms)
	data := b[44:]
	for i := 0; i+1 < len(data); i += 2 {
		v := int16(8000)
		if (i/2/30)%2 == 0 {
			v = -8000
		}
		data[i], data[i+1] = byte(v), byte(uint16(v)>>8)
	}
	return b
}

func httpPut(ctx context.Context, url, contentType string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("put %d: %s", resp.StatusCode, b)
	}
	return nil
}

// fakeTTS uploads a tone per request to the presigned PUT URL, like the
// Python worker; with fail set it answers FAILED_PRECONDITION, which is
// how the real worker reports an engine that is not installed.
type fakeTTS struct {
	workerv1.UnimplementedTTSServer
	fail  atomic.Bool
	calls atomic.Int32
	mu    sync.Mutex
	reqs  []*workerv1.SynthesizeRequest
}

func (f *fakeTTS) Synthesize(req *workerv1.SynthesizeRequest, stream grpc.ServerStreamingServer[workerv1.SynthesizeEvent]) error {
	f.calls.Add(1)
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()
	if f.fail.Load() {
		return status.Error(codes.FailedPrecondition, "engine chatterbox is not installed")
	}
	_ = stream.Send(&workerv1.SynthesizeEvent{Event: &workerv1.SynthesizeEvent_Progress{Progress: &workerv1.SynthesizeProgress{Pct: 50}}})
	ms := 300 + 20*len(strings.Fields(req.Text))
	if err := httpPut(stream.Context(), req.OutputPutUrl, "audio/wav", sineWav(ms)); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	return stream.Send(&workerv1.SynthesizeEvent{Event: &workerv1.SynthesizeEvent_Result{Result: &workerv1.SynthesizeResult{OutputKey: req.Params["output_key"], DurationS: float64(ms) / 1000}}})
}

// fakeAlign fetches the audio and uploads word cues, like the worker.
type fakeAlign struct {
	workerv1.UnimplementedAlignServer
}

func (f *fakeAlign) Align(req *workerv1.AlignRequest, stream grpc.ServerStreamingServer[workerv1.AlignEvent]) error {
	resp, err := http.Get(req.AudioGetUrl)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	audio, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if _, err := scenes.ParseWav(audio); err != nil {
		return status.Error(codes.InvalidArgument, "audio is not a wav")
	}
	cues, _ := json.Marshal(map[string]any{"segments": []map[string]any{{"start": 0, "end": 1, "text": req.Text}}})
	if err := httpPut(stream.Context(), req.OutputPutUrl, "application/json", cues); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	return stream.Send(&workerv1.AlignEvent{Event: &workerv1.AlignEvent_Result{Result: &workerv1.AlignResult{OutputKey: req.Params["output_key"], SegmentCount: 1}}})
}

// sceneDoubles runs the ComfyUI, TTS and align test doubles and builds
// step dependencies against them.
type sceneDoubles struct {
	comfy *fakeComfy
	tts   *fakeTTS
	deps  scenes.StepDeps
}

func startSceneDoubles(t *testing.T, service *scenes.Service, st *storage.Internal) *sceneDoubles {
	t.Helper()
	templates, err := models.EmbeddedTemplates()
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := models.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	workflow, _ := scenes.SceneWorkflows(manifest)("z-image-turbo")
	d := &sceneDoubles{comfy: &fakeComfy{}, tts: &fakeTTS{}}
	srv := httptest.NewServer(d.comfy.handler(templates[workflow].Map.OutputNode))
	t.Cleanup(srv.Close)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g := grpc.NewServer()
	workerv1.RegisterTTSServer(g, d.tts)
	workerv1.RegisterAlignServer(g, &fakeAlign{})
	go func() { _ = g.Serve(lis) }()
	t.Cleanup(g.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	d.deps = scenes.StepDeps{
		Service: service, Storage: st,
		Comfy:         &comfyui.Engine{Client: comfyui.New(srv.URL, srv.Client()), Templates: templates, PollInterval: 10 * time.Millisecond},
		TTS:           tts.New(workerv1.NewTTSClient(conn)),
		Align:         align.New(workerv1.NewAlignClient(conn)),
		Runner:        &ffmpeg.Runner{},
		SceneWorkflow: scenes.SceneWorkflows(manifest),
	}
	return d
}

// sceneEngine builds an in-process engine whose registry has the scene
// handlers (on the test doubles) and the media handlers (enqueue only:
// the live worker container runs those with its real ffmpeg).
func sceneEngine(t *testing.T, build func(*scenes.Service) []pipeline.StepHandler) (*pipeline.Engine, *scenes.Service, *pgxpool.Pool) {
	t.Helper()
	registry := pipeline.NewRegistry()
	pool := appPool(t)
	service := &scenes.Service{Pool: pool, Queries: dbgen.New(pool), Hooks: &scenes.Hooks{}}
	for _, h := range build(service) {
		registry.Register(h)
	}
	for _, h := range media.Handlers(media.Deps{Queries: dbgen.New(pool)}) {
		registry.Register(h)
	}
	engine, _ := pipelineEngineOnPool(t, registry, pool)
	engine.Estimator = scenes.Estimate
	service.Engine = engine
	return engine, service, pool
}

// waitFor polls cond until it holds or the timeout passes.
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

// assetVariants reads an asset's variants map straight from Postgres.
func assetVariants(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) media.VariantsMap {
	t.Helper()
	var raw []byte
	if err := pool.QueryRow(context.Background(), `SELECT variants FROM assets WHERE id = $1`, id).Scan(&raw); err != nil {
		t.Fatalf("read variants: %v", err)
	}
	return media.DecodeVariants(raw)
}

// stepsOf lists a run's steps (kind, status, queue, priority, error code).
type stepInfo struct {
	ID        uuid.UUID
	Kind      string
	Status    string
	Queue     string
	Priority  int16
	ErrorCode *string
}

func stepsWhere(t *testing.T, pool *pgxpool.Pool, where string, args ...any) []stepInfo {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT id, kind, status, queue, priority, error_code FROM pipeline_steps WHERE `+where+` ORDER BY id`, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []stepInfo
	for rows.Next() {
		var s stepInfo
		if err := rows.Scan(&s.ID, &s.Kind, &s.Status, &s.Queue, &s.Priority, &s.ErrorCode); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// sessionJSON decodes a response body into v after checking the status.
func sessionJSON(t *testing.T, resp *http.Response, want int, v any) {
	t.Helper()
	requireStatus(t, resp, want)
	if v != nil {
		decodeJSON(t, resp, v)
	} else {
		_ = resp.Body.Close()
	}
}

// postMultipart submits a presigned POST form with the file part last.
func postMultipart(t *testing.T, url string, fields map[string]string, name string, body []byte) int {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	fw, err := w.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(body)
	_ = w.Close()
	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}
