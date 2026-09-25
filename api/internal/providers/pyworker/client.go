// Package pyworker wraps the control-plane RPCs (worker.proto: Health,
// ListEngines, LoadModel, UnloadModel, GpuStatus) every Python worker
// implements, and adapts them to residency.Backend.
package pyworker

import (
	"context"

	"loomtale/api/internal/providers/workerconn"
	workerv1 "loomtale/api/internal/workerpb/loomtale/worker/v1"
)

// Client wraps workerv1.WorkerClient.
type Client struct {
	RPC workerv1.WorkerClient
}

func New(rpc workerv1.WorkerClient) *Client { return &Client{RPC: rpc} }

func (c *Client) Health(ctx context.Context) (bool, error) {
	resp, err := c.RPC.Health(ctx, &workerv1.HealthRequest{})
	if err != nil {
		return false, workerconn.TranslateErr(err)
	}
	return resp.Ok, nil
}

// EngineInfo mirrors workerv1.EngineInfo without leaking the generated
// protobuf type to callers outside this package.
type EngineInfo struct {
	Name       string
	Task       string
	License    string
	Installed  bool
	Loaded     bool
	VRAMHeldMB int64
}

func (c *Client) ListEngines(ctx context.Context) ([]EngineInfo, error) {
	resp, err := c.RPC.ListEngines(ctx, &workerv1.ListEnginesRequest{})
	if err != nil {
		return nil, workerconn.TranslateErr(err)
	}
	out := make([]EngineInfo, 0, len(resp.Engines))
	for _, e := range resp.Engines {
		out = append(out, EngineInfo{
			Name: e.Name, Task: e.Task, License: e.License,
			Installed: e.Installed, Loaded: e.Loaded, VRAMHeldMB: e.VramHeldMb,
		})
	}
	return out, nil
}

func (c *Client) LoadModel(ctx context.Context, engine, model string) (int64, error) {
	resp, err := c.RPC.LoadModel(ctx, &workerv1.LoadModelRequest{Engine: engine, Model: model})
	if err != nil {
		return 0, workerconn.TranslateErr(err)
	}
	return resp.VramHeldMb, nil
}

func (c *Client) UnloadModel(ctx context.Context, engine string) error {
	_, err := c.RPC.UnloadModel(ctx, &workerv1.UnloadModelRequest{Engine: engine})
	return workerconn.TranslateErr(err)
}

// GpuStatus reports the pyworker's own view of GPU state (used as one of
// the app-side residency proof sources, alongside ComfyUI /system_stats
// and Ollama /api/ps).
type GpuStatus struct {
	GPUPresent     bool
	TotalMB        int64
	FreeMB         int64
	ResidentEngine string
}

func (c *Client) GpuStatus(ctx context.Context) (GpuStatus, error) {
	resp, err := c.RPC.GpuStatus(ctx, &workerv1.GpuStatusRequest{})
	if err != nil {
		return GpuStatus{}, workerconn.TranslateErr(err)
	}
	return GpuStatus{
		GPUPresent: resp.GpuPresent, TotalMB: resp.TotalMb, FreeMB: resp.FreeMb,
		ResidentEngine: resp.ResidentEngine,
	}, nil
}
