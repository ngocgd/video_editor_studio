// Package tts wraps the Synthesize streaming RPC (tts.proto) for a
// StepHandler.Run implementation (phase 7+): progress deltas go to the
// caller's onProgress callback (normally StepContext.Progress), and a
// gRPC error is pre-translated to the phase 3 pipeline sentinels.
package tts

import (
	"context"
	"errors"
	"fmt"
	"io"

	"loomtale/api/internal/providers/workerconn"
	workerv1 "loomtale/api/internal/workerpb/loomtale/worker/v1"
)

// Client wraps workerv1.TTSClient.
type Client struct {
	RPC workerv1.TTSClient
}

func New(rpc workerv1.TTSClient) *Client { return &Client{RPC: rpc} }

// Request is the caller-facing synthesize request; OutputPutURL is an
// internal presigned PUT URL from storage.Internal (the worker uploads
// directly, so this client never sees audio bytes).
type Request struct {
	Engine       string
	Voice        string
	Text         string
	OutputPutURL string
	Params       map[string]string
}

// Result is the synthesize call's final outcome.
type Result struct {
	OutputKey  string
	DurationS  float64
	Metadata   map[string]string
}

// Synthesize streams progress to onProgress(pct, etaS) and returns the
// final Result once the pyworker reports done.
func (c *Client) Synthesize(ctx context.Context, req Request, onProgress func(pct, etaS int)) (Result, error) {
	stream, err := c.RPC.Synthesize(ctx, &workerv1.SynthesizeRequest{
		Engine: req.Engine, Voice: req.Voice, Text: req.Text,
		OutputPutUrl: req.OutputPutURL, Params: req.Params,
	})
	if err != nil {
		return Result{}, workerconn.TranslateErr(err)
	}

	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return Result{}, fmt.Errorf("tts: stream ended without a result event")
		}
		if err != nil {
			return Result{}, workerconn.TranslateErr(err)
		}
		switch e := event.Event.(type) {
		case *workerv1.SynthesizeEvent_Progress:
			if onProgress != nil {
				onProgress(int(e.Progress.Pct), int(e.Progress.EtaS))
			}
		case *workerv1.SynthesizeEvent_Result:
			return Result{OutputKey: e.Result.OutputKey, DurationS: e.Result.DurationS, Metadata: e.Result.Metadata}, nil
		}
	}
}
