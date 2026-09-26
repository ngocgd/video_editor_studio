package bench

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/train"
	"loomtale/api/internal/providers/vision"
	workerv1 "loomtale/api/internal/workerpb/loomtale/worker/v1"
)

// fakeVisionWorker plays the Python worker's Vision and Train services,
// fetching inputs and uploading outputs through the sink URLs it is
// given, like the real worker does with presigned URLs.
type fakeVisionWorker struct {
	workerv1.UnimplementedVisionServer
	workerv1.UnimplementedTrainServer
	mu      sync.Mutex
	missing map[string]bool
	// badWeights makes Train upload bytes that are not safetensors.
	badWeights bool
	scores     []*workerv1.ScoreRequest
	trains     []*workerv1.TrainRequest
	// dataset lists the entry names of the last training archive.
	dataset []string
}

func (f *fakeVisionWorker) notInstalled(engine string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.missing[engine] {
		return status.Error(codes.FailedPrecondition, "engine_not_installed: "+engine)
	}
	return nil
}

func (f *fakeVisionWorker) Score(_ context.Context, req *workerv1.ScoreRequest) (*workerv1.ScoreResponse, error) {
	if err := f.notInstalled(req.Engine); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.scores = append(f.scores, req)
	f.mu.Unlock()
	urls := append([]string{req.ImageGetUrl}, strings.Split(req.Params["reference_urls"], "\n")...)
	for _, u := range urls {
		if _, err := httpGet(u); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}
	return &workerv1.ScoreResponse{Score: 0.8, Metadata: map[string]string{"vram_peak_mb": "900", "min": "0.7", "max": "0.9"}}, nil
}

func (f *fakeVisionWorker) Depth(_ context.Context, req *workerv1.DepthRequest) (*workerv1.DepthResponse, error) {
	if err := f.notInstalled(req.Engine); err != nil {
		return nil, err
	}
	if _, err := httpGet(req.ImageGetUrl); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := httpPut(req.OutputPutUrl, []byte("\x89PNG\r\n\x1a\ndepth")); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &workerv1.DepthResponse{OutputKey: req.Params["output_key"]}, nil
}

func (f *fakeVisionWorker) Train(req *workerv1.TrainRequest, stream grpc.ServerStreamingServer[workerv1.TrainEvent]) error {
	if err := f.notInstalled(req.Engine); err != nil {
		return err
	}
	data, err := httpGet(req.DatasetGetUrl)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	var names []string
	for _, e := range zr.File {
		names = append(names, e.Name)
	}
	f.mu.Lock()
	f.trains = append(f.trains, req)
	f.dataset = names
	bad := f.badWeights
	f.mu.Unlock()
	for _, pct := range []int32{0, 50, 100} {
		ev := &workerv1.TrainEvent{Event: &workerv1.TrainEvent_Progress{Progress: &workerv1.TrainProgress{Pct: pct, Step: pct, TotalSteps: 100}}}
		if err := stream.Send(ev); err != nil {
			return err
		}
	}
	weights := safetensorsBytes()
	if bad {
		weights = []byte("not a safetensors file")
	}
	if err := httpPut(req.OutputPutUrl, weights); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	return stream.Send(&workerv1.TrainEvent{Event: &workerv1.TrainEvent_Result{Result: &workerv1.TrainResult{
		OutputKey: req.Params["output_key"], Metadata: map[string]string{"train_s": "30"},
	}}})
}

// safetensorsBytes is a minimal well-formed safetensors file.
func safetensorsBytes() []byte {
	header := []byte(`{"__metadata__":{}}`)
	out := make([]byte, 8, 8+len(header))
	binary.LittleEndian.PutUint64(out, uint64(len(header)))
	return append(out, header...)
}

func newVisionRunner(t *testing.T, worker *fakeVisionWorker) (*VoiceRunner, *recordingQueries, *fakeResidency) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	workerv1.RegisterVisionServer(server, worker)
	workerv1.RegisterTrainServer(server, worker)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	sink, err := NewSink("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sink.Close() })
	q := &recordingQueries{}
	res := &fakeResidency{}
	return &VoiceRunner{
		Vision: vision.New(workerv1.NewVisionClient(conn)), Train: train.New(workerv1.NewTrainClient(conn)),
		Residency: res, Queries: q, Sink: sink, OutDir: t.TempDir(),
	}, q, res
}

func testRefs(n int) []RefImage {
	refs := make([]RefImage, n)
	for i := range refs {
		refs[i] = RefImage{Name: string(rune('a'+i)) + ".png", Data: []byte{byte(i), 1, 2, 3}}
	}
	return refs
}

func TestLoadRefImagesKeepsImagesInNameOrder(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"b.JPG", "a.png", "notes.txt", "c.webp"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "sub.png"), 0o755); err != nil {
		t.Fatal(err)
	}
	refs, err := LoadRefImages(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range refs {
		names = append(names, r.Name)
		if string(r.Data) != r.Name {
			t.Fatalf("%s has the wrong contents", r.Name)
		}
	}
	if strings.Join(names, ",") != "a.png,b.JPG,c.webp" {
		t.Fatalf("names %v", names)
	}
	if _, err := LoadRefImages(""); err == nil {
		t.Fatal("an empty --refs must be rejected")
	}
}

func TestRunVisionScoresLeaveOneOutAndExtractsDepth(t *testing.T) {
	worker := &fakeVisionWorker{}
	r, q, res := newVisionRunner(t, worker)
	results, budgets, err := r.RunVision(context.Background(), testRefs(3))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 6 || len(q.rows) != 6 {
		t.Fatalf("results %d rows %d", len(results), len(q.rows))
	}
	for _, rr := range results {
		if rr.Err != nil {
			t.Fatalf("%s: %v", rr.Name, rr.Err)
		}
	}
	for i, s := range worker.scores {
		refs := strings.Split(s.Params["reference_urls"], "\n")
		if len(refs) != 2 || strings.Contains(s.Params["reference_urls"], s.ImageGetUrl) {
			t.Fatalf("score %d must use the two other images as references: %v", i, refs)
		}
	}
	if results[0].Score != 0.8 || results[0].VRAMPeakMB != 900 {
		t.Fatalf("score result %+v", results[0])
	}
	if _, err := os.Stat(filepath.Join(r.OutDir, "depth-a.png")); err != nil {
		t.Fatalf("depth map not saved: %v", err)
	}
	// One load for the scorer, one switch to the depth engine.
	if res.ensures != 2 || res.current.Model != EngineDepth {
		t.Fatalf("ensures %d current %v", res.ensures, res.current)
	}
	if len(budgets) != 1 || !budgets[0].Known {
		t.Fatalf("budgets %+v", budgets)
	}
	if _, _, err := r.RunVision(context.Background(), testRefs(1)); err == nil {
		t.Fatal("one image cannot be scored against the others")
	}
}

func TestRunVisionReportsANotInstalledEngine(t *testing.T) {
	r, q, _ := newVisionRunner(t, &fakeVisionWorker{missing: map[string]bool{EngineScore: true, EngineDepth: true}})
	results, _, err := r.RunVision(context.Background(), testRefs(2))
	if err != nil {
		t.Fatal(err)
	}
	for _, rr := range results {
		if rr.Err == nil || !strings.Contains(rr.Err.Error(), pipeline.ErrEngineNotInstalled.Error()) {
			t.Fatalf("%s must report engine_not_installed: %v", rr.Name, rr.Err)
		}
	}
	for _, row := range q.rows {
		if row.Ok {
			t.Fatalf("%s recorded as ok", row.CaseName)
		}
	}
}

func TestTrainDatasetIsFlatWithOneCaptionPerImage(t *testing.T) {
	refs := testRefs(4)
	refs[1].Name = "Portrait.JPEG"
	data, err := TrainDataset(refs, "loomtale_character")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range zr.File {
		names = append(names, e.Name)
		if strings.HasSuffix(e.Name, ".txt") {
			rc, _ := e.Open()
			caption, _ := io.ReadAll(rc)
			_ = rc.Close()
			if string(caption) != "loomtale_character" {
				t.Fatalf("%s caption %q", e.Name, caption)
			}
		}
	}
	sort.Strings(names)
	want := "ref-01.png,ref-01.txt,ref-02.jpg,ref-02.txt,ref-03.png,ref-03.txt,ref-04.png,ref-04.txt"
	if strings.Join(names, ",") != want {
		t.Fatalf("entries %v", names)
	}
}

func TestRunTrainSmokeTrainsOnFourImagesThenScoresAHeldOutOne(t *testing.T) {
	worker := &fakeVisionWorker{}
	r, q, res := newVisionRunner(t, worker)
	refs := testRefs(5)
	report, err := r.RunTrainSmoke(context.Background(), refs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Train.Err != nil || report.Score == nil || report.Score.Err != nil {
		t.Fatalf("report %+v", report)
	}
	req := worker.trains[0]
	if req.Engine != EngineTrainer || req.BaseModel != "z-image-turbo" || req.Params["steps"] != "50" {
		t.Fatalf("train request %+v", req)
	}
	if len(worker.dataset) != 8 {
		t.Fatalf("dataset %v", worker.dataset)
	}
	if report.Train.TrainSeconds != 30 || report.Train.WeightsBytes != len(safetensorsBytes()) {
		t.Fatalf("train result %+v", report.Train)
	}
	if _, err := os.Stat(filepath.Join(r.OutDir, "lora-smoke.safetensors")); err != nil {
		t.Fatalf("LoRA not saved: %v", err)
	}
	score := worker.scores[0]
	if !strings.HasSuffix(score.ImageGetUrl, "ref-e.png") || strings.Count(score.Params["reference_urls"], "\n") != 3 {
		t.Fatalf("score must use the held-out fifth image against the four training images: %+v", score)
	}
	if res.ensures != 2 || res.current.Model != EngineScore {
		t.Fatalf("ensures %d current %v", res.ensures, res.current)
	}
	// 30 s for 50 steps scales to 15 min for the default 1500 steps.
	lora := report.Budgets[0]
	if !lora.Known || lora.Measured != 15 || !lora.OK() {
		t.Fatalf("LoRA budget %+v", lora)
	}
	if len(q.rows) != 2 || q.rows[0].Suite != "train-smoke" || q.rows[1].Suite != "train-smoke" {
		t.Fatalf("rows %+v", q.rows)
	}
}

func TestRunTrainUsesTheDefaultStepsOnEveryImage(t *testing.T) {
	worker := &fakeVisionWorker{}
	r, _, _ := newVisionRunner(t, worker)
	report, err := r.RunTrain(context.Background(), testRefs(6))
	if err != nil {
		t.Fatal(err)
	}
	if report.Train.Err != nil || report.Score != nil {
		t.Fatalf("report %+v", report)
	}
	if _, set := worker.trains[0].Params["steps"]; set || len(worker.dataset) != 12 {
		t.Fatalf("params %v dataset %v", worker.trains[0].Params, worker.dataset)
	}
	if b := report.Budgets[0]; !b.Known || b.Measured != 0.5 {
		t.Fatalf("budget %+v", b)
	}
	if _, err := r.RunTrain(context.Background(), testRefs(3)); err == nil {
		t.Fatal("three images are too few to train on")
	}
}

func TestRunTrainSmokeStopsWhenTheTrainerFails(t *testing.T) {
	for name, worker := range map[string]*fakeVisionWorker{
		"not installed": {missing: map[string]bool{EngineTrainer: true}},
		"bad weights":   {badWeights: true},
	} {
		t.Run(name, func(t *testing.T) {
			r, q, _ := newVisionRunner(t, worker)
			report, err := r.RunTrainSmoke(context.Background(), testRefs(4))
			if err != nil {
				t.Fatal(err)
			}
			if report.Train.Err == nil || report.Score != nil || len(q.rows) != 1 || q.rows[0].Ok {
				t.Fatalf("report %+v rows %d", report, len(q.rows))
			}
			if report.Budgets[0].Known {
				t.Fatal("a failed run must not report a training time")
			}
		})
	}
}

func TestIsSafetensors(t *testing.T) {
	good := safetensorsBytes()
	if !IsSafetensors(good) {
		t.Fatal("well-formed header rejected")
	}
	short := append([]byte(nil), good...)
	binary.LittleEndian.PutUint64(short, 1<<40)
	for name, data := range map[string][]byte{"too long": short, "empty": nil, "text": []byte("hello world!")} {
		if IsSafetensors(data) {
			t.Fatalf("%s accepted", name)
		}
	}
}
