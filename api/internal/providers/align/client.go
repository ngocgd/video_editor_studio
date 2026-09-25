// Package align wraps the Align streaming RPC (align.proto).
package align

import (
	"context"
	"errors"
	"fmt"
	"io"

	"loomtale/api/internal/providers/workerconn"
	workerv1 "loomtale/api/internal/workerpb/loomtale/worker/v1"
)

// Client wraps workerv1.AlignClient.
type Client struct {
	RPC workerv1.AlignClient
}

func New(rpc workerv1.AlignClient) *Client { return &Client{RPC: rpc} }

// Request is the caller-facing align request.
type Request struct {
	Engine       string
	AudioGetURL  string
	Text         string
	OutputPutURL string
	Params       map[string]string
}

// Result is the align call's final outcome.
type Result struct {
	OutputKey    string
	SegmentCount int
	Metadata     map[string]string
}

// Align streams progress to onProgress(pct, etaS) and returns the final
// Result once the pyworker reports done.
func (c *Client) Align(ctx context.Context, req Request, onProgress func(pct, etaS int)) (Result, error) {
	stream, err := c.RPC.Align(ctx, &workerv1.AlignRequest{
		Engine: req.Engine, AudioGetUrl: req.AudioGetURL, Text: req.Text,
		OutputPutUrl: req.OutputPutURL, Params: req.Params,
	})
	if err != nil {
		return Result{}, workerconn.TranslateErr(err)
	}

	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return Result{}, fmt.Errorf("align: stream ended without a result event")
		}
		if err != nil {
			return Result{}, workerconn.TranslateErr(err)
		}
		switch e := event.Event.(type) {
		case *workerv1.AlignEvent_Progress:
			if onProgress != nil {
				onProgress(int(e.Progress.Pct), int(e.Progress.EtaS))
			}
		case *workerv1.AlignEvent_Result:
			return Result{OutputKey: e.Result.OutputKey, SegmentCount: int(e.Result.SegmentCount), Metadata: e.Result.Metadata}, nil
		}
	}
}
