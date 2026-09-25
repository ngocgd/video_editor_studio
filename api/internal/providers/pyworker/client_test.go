package pyworker

import (
	"context"
	"errors"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"loomtale/api/internal/pipeline"
	workerv1 "loomtale/api/internal/workerpb/loomtale/worker/v1"
)

// fakeWorkerServer implements workerv1.WorkerServer with an empty engine
// registry, matching the phase 4 contract (no real engines before 9a-9c).
type fakeWorkerServer struct {
	workerv1.UnimplementedWorkerServer
}

func (fakeWorkerServer) Health(context.Context, *workerv1.HealthRequest) (*workerv1.HealthResponse, error) {
	return &workerv1.HealthResponse{Ok: true}, nil
}

func (fakeWorkerServer) ListEngines(context.Context, *workerv1.ListEnginesRequest) (*workerv1.ListEnginesResponse, error) {
	return &workerv1.ListEnginesResponse{}, nil
}

func (fakeWorkerServer) LoadModel(context.Context, *workerv1.LoadModelRequest) (*workerv1.LoadModelResponse, error) {
	return nil, status.Error(codes.FailedPrecondition, "engine_not_installed")
}

func newTestClient(t *testing.T) *Client {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	workerv1.RegisterWorkerServer(server, fakeWorkerServer{})
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

func TestHealthReturnsOK(t *testing.T) {
	c := newTestClient(t)
	ok, err := c.Health(context.Background())
	if err != nil || !ok {
		t.Fatalf("Health() = %v, %v", ok, err)
	}
}

func TestListEnginesEmptyBeforePhase9(t *testing.T) {
	c := newTestClient(t)
	engines, err := c.ListEngines(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(engines) != 0 {
		t.Fatalf("expected no engines, got %v", engines)
	}
}

func TestLoadModelTranslatesFailedPreconditionToEngineNotInstalled(t *testing.T) {
	c := newTestClient(t)
	_, err := c.LoadModel(context.Background(), "tts", "some-model")
	if !errors.Is(err, pipeline.ErrEngineNotInstalled) {
		t.Fatalf("expected pipeline.ErrEngineNotInstalled, got %v", err)
	}
}
