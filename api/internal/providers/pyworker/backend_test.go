package pyworker

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	workerv1 "loomtale/api/internal/workerpb/loomtale/worker/v1"
)

// residentWorker keeps one resident engine name, like the Python
// ModelManager.
type residentWorker struct {
	workerv1.UnimplementedWorkerServer
	mu       sync.Mutex
	engines  []string
	resident string
	loads    []string
}

func (w *residentWorker) ListEngines(context.Context, *workerv1.ListEnginesRequest) (*workerv1.ListEnginesResponse, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := &workerv1.ListEnginesResponse{}
	for _, name := range w.engines {
		out.Engines = append(out.Engines, &workerv1.EngineInfo{Name: name, Installed: true, Loaded: name == w.resident})
	}
	return out, nil
}

func (w *residentWorker) LoadModel(_ context.Context, req *workerv1.LoadModelRequest) (*workerv1.LoadModelResponse, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.loads = append(w.loads, req.Engine)
	w.resident = req.Engine
	return &workerv1.LoadModelResponse{Loaded: true, VramHeldMb: 2048}, nil
}

func (w *residentWorker) UnloadModel(_ context.Context, req *workerv1.UnloadModelRequest) (*workerv1.UnloadModelResponse, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if req.Engine == "" || req.Engine == w.resident {
		w.resident = ""
	}
	return &workerv1.UnloadModelResponse{Unloaded: true}, nil
}

func dialWorker(t *testing.T, srv workerv1.WorkerServer) *Client {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	workerv1.RegisterWorkerServer(server, srv)
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
	return New(workerv1.NewWorkerClient(conn))
}

func TestGenericBackendLoadsTheModelNamedEngine(t *testing.T) {
	w := &residentWorker{engines: []string{"chatterbox", "whisper-align"}}
	b := &Backend{Client: dialWorker(t, w)}
	ctx := context.Background()
	if b.Name() != "pyworker" {
		t.Fatalf("name = %q", b.Name())
	}
	held, err := b.Load(ctx, "whisper-align")
	if err != nil || held != 2048 {
		t.Fatalf("load = %d, %v", held, err)
	}
	if ok, err := b.Resident(ctx, "whisper-align"); err != nil || !ok {
		t.Fatalf("whisper-align should be resident: %v %v", ok, err)
	}
	if ok, _ := b.Resident(ctx, "chatterbox"); ok {
		t.Fatal("chatterbox is not the resident engine")
	}
	if err := b.Unload(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, _ := b.Resident(ctx, "whisper-align"); ok {
		t.Fatal("the generic unload must release whatever engine is resident")
	}
}

func TestBackendGateRefusesBeforeAnyLoad(t *testing.T) {
	w := &residentWorker{engines: []string{"chatterbox"}}
	refused := errors.New("not verified")
	b := &Backend{Client: dialWorker(t, w), Gate: func(context.Context, string) error { return refused }}
	if _, err := b.Load(context.Background(), "chatterbox"); !errors.Is(err, refused) {
		t.Fatalf("expected the gate's error, got %v", err)
	}
	if len(w.loads) != 0 {
		t.Fatalf("the worker must not be asked to load after the gate refused: %v", w.loads)
	}
}
