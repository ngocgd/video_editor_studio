// Package train wraps the Train streaming RPC (train.proto). Log lines
// are relayed to onLog (normally StepContext.Log) separately from
// progress, so a caller can show a live log tail.
package train

import (
	"context"
	"errors"
	"fmt"
	"io"

	"loomtale/api/internal/providers/workerconn"
	workerv1 "loomtale/api/internal/workerpb/loomtale/worker/v1"
)

// Client wraps workerv1.TrainClient.
type Client struct {
	RPC workerv1.TrainClient
}

func New(rpc workerv1.TrainClient) *Client { return &Client{RPC: rpc} }

// Request is the caller-facing training request.
type Request struct {
	Engine        string
	BaseModel     string
	DatasetGetURL string
	OutputPutURL  string
	Params        map[string]string
}

// Progress reports training-step-level progress, richer than the
// generic pct/etaS pair the other clients report.
type Progress struct {
	Pct        int
	EtaS       int
	Step       int
	TotalSteps int
}

// Result is the train call's final outcome.
type Result struct {
	OutputKey string
	Metadata  map[string]string
}

// Train streams progress/log events and returns the final Result.
func (c *Client) Train(ctx context.Context, req Request, onProgress func(Progress), onLog func(string)) (Result, error) {
	stream, err := c.RPC.Train(ctx, &workerv1.TrainRequest{
		Engine: req.Engine, BaseModel: req.BaseModel, DatasetGetUrl: req.DatasetGetURL,
		OutputPutUrl: req.OutputPutURL, Params: req.Params,
	})
	if err != nil {
		return Result{}, workerconn.TranslateErr(err)
	}

	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return Result{}, fmt.Errorf("train: stream ended without a result event")
		}
		if err != nil {
			return Result{}, workerconn.TranslateErr(err)
		}
		switch e := event.Event.(type) {
		case *workerv1.TrainEvent_Progress:
			if onProgress != nil {
				onProgress(Progress{
					Pct: int(e.Progress.Pct), EtaS: int(e.Progress.EtaS),
					Step: int(e.Progress.Step), TotalSteps: int(e.Progress.TotalSteps),
				})
			}
		case *workerv1.TrainEvent_Log:
			if onLog != nil {
				onLog(e.Log.Line)
			}
		case *workerv1.TrainEvent_Result:
			return Result{OutputKey: e.Result.OutputKey, Metadata: e.Result.Metadata}, nil
		}
	}
}
