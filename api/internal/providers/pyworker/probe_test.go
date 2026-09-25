package pyworker

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	workerv1 "loomtale/api/internal/workerpb/loomtale/worker/v1"
)

type gpuStatusServer struct {
	workerv1.UnimplementedWorkerServer
	resp *workerv1.GpuStatusResponse
	err  error
}

func (s gpuStatusServer) GpuStatus(context.Context, *workerv1.GpuStatusRequest) (*workerv1.GpuStatusResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

func newProbeTestClient(t *testing.T, srv workerv1.WorkerServer) *Client {
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

func TestProbeSnapshotReportsGpuPresent(t *testing.T) {
	client := newProbeTestClient(t, gpuStatusServer{
		resp: &workerv1.GpuStatusResponse{GpuPresent: true, TotalMb: 16384, FreeMb: 10900},
	})
	probe := &Probe{Client: client, RenderReserveMB: 1024}

	snap, err := probe.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.TotalMB != 16384 || snap.FreeMB != 10900 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if snap.RenderReserveMB != 1024 {
		t.Fatalf("RenderReserveMB = %d", snap.RenderReserveMB)
	}
}

func TestProbeSnapshotDegradesToNoGPUOnFailure(t *testing.T) {
	client := newProbeTestClient(t, gpuStatusServer{err: context.DeadlineExceeded})
	probe := &Probe{Client: client}

	snap, err := probe.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("expected no error on probe failure (no_gpu handled), got %v", err)
	}
	if snap.TotalMB != 0 || snap.FreeMB != 0 {
		t.Fatalf("expected a zero snapshot, got %+v", snap)
	}
}

func TestProbeSnapshotDegradesWhenGpuNotPresent(t *testing.T) {
	client := newProbeTestClient(t, gpuStatusServer{resp: &workerv1.GpuStatusResponse{GpuPresent: false}})
	probe := &Probe{Client: client}

	snap, err := probe.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.TotalMB != 0 {
		t.Fatalf("expected TotalMB=0 when GpuPresent=false, got %+v", snap)
	}
}
