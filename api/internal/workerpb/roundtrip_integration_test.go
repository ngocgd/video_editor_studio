//go:build integration

// Package workerpb holds the buf-generated Go stubs for proto/loomtale/worker/v1.
// This file proves the one-toolchain codegen pipeline: a Go client talks to
// the Python-generated server, and a Python client talks to a Go server,
// both over the same health.proto contract.
package workerpb

import (
	"context"
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	workerv1 "loomtale/api/internal/workerpb/loomtale/worker/v1"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine caller for repo root lookup")
	}
	// this file lives at api/internal/workerpb/roundtrip_integration_test.go
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
}

// TestProtoRoundTripGoClientToPythonServer starts the Python health server
// (uv run) and calls it from a Go grpc client using the generated stubs.
func TestProtoRoundTripGoClientToPythonServer(t *testing.T) {
	addr := "127.0.0.1:50151"
	pyDir := filepath.Join(repoRoot(t), "workers-python")

	cmd := exec.Command("uv", "run", "python", "-m", "loomtale_worker.health_server", "--bind", addr)
	cmd.Dir = pyDir
	if err := cmd.Start(); err != nil {
		t.Fatalf("start python health server: %v", err)
	}
	defer func() { _ = cmd.Process.Kill() }()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	client := workerv1.NewWorkerHealthClient(conn)

	deadline := time.Now().Add(10 * time.Second)
	var resp *workerv1.HealthCheckResponse
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		resp, err = client.Check(ctx, &workerv1.HealthCheckRequest{})
		cancel()
		if err == nil {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("Check (server may not have started in time): %v", err)
	}
	if resp.GetStatus() != workerv1.HealthCheckResponse_STATUS_SERVING {
		t.Fatalf("expected STATUS_SERVING, got %v", resp.GetStatus())
	}
}

// TestProtoRoundTripPythonClientToGoServer starts a Go grpc server using the
// generated stubs and calls it from a Python client script.
func TestProtoRoundTripPythonClientToGoServer(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := lis.Addr().String()

	srv := grpc.NewServer()
	workerv1.RegisterWorkerHealthServer(srv, &staticHealthServer{})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	pyDir := filepath.Join(repoRoot(t), "workers-python")
	script := `
import sys
import grpc
from loomtale.worker.v1 import health_pb2, health_pb2_grpc

channel = grpc.insecure_channel(sys.argv[1])
stub = health_pb2_grpc.WorkerHealthStub(channel)
resp = stub.Check(health_pb2.HealthCheckRequest())
assert resp.status == health_pb2.HealthCheckResponse.Status.STATUS_SERVING, resp.status
print("ok")
`
	cmd := exec.Command("uv", "run", "python", "-c", script, addr)
	cmd.Dir = pyDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python client failed: %v\n%s", err, out)
	}
}

type staticHealthServer struct {
	workerv1.UnimplementedWorkerHealthServer
}

func (s *staticHealthServer) Check(context.Context, *workerv1.HealthCheckRequest) (*workerv1.HealthCheckResponse, error) {
	return &workerv1.HealthCheckResponse{Status: workerv1.HealthCheckResponse_STATUS_SERVING}, nil
}

