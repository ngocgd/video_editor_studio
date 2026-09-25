// Package vision wraps the Score/Depth unary RPCs (vision.proto).
package vision

import (
	"context"

	"loomtale/api/internal/providers/workerconn"
	workerv1 "loomtale/api/internal/workerpb/loomtale/worker/v1"
)

// Client wraps workerv1.VisionClient.
type Client struct {
	RPC workerv1.VisionClient
}

func New(rpc workerv1.VisionClient) *Client { return &Client{RPC: rpc} }

// ScoreRequest is the caller-facing scoring request.
type ScoreRequest struct {
	Engine      string
	ImageGetURL string
	Params      map[string]string
}

// Score returns a single scalar score plus metadata (e.g. a LoRA
// render-quality score, phase 9c).
func (c *Client) Score(ctx context.Context, req ScoreRequest) (float64, map[string]string, error) {
	resp, err := c.RPC.Score(ctx, &workerv1.ScoreRequest{
		Engine: req.Engine, ImageGetUrl: req.ImageGetURL, Params: req.Params,
	})
	if err != nil {
		return 0, nil, workerconn.TranslateErr(err)
	}
	return resp.Score, resp.Metadata, nil
}

// DepthRequest is the caller-facing depth-extraction request.
type DepthRequest struct {
	Engine       string
	ImageGetURL  string
	OutputPutURL string
	Params       map[string]string
}

// Depth extracts a depth map, uploaded by the worker to OutputPutURL.
func (c *Client) Depth(ctx context.Context, req DepthRequest) (string, error) {
	resp, err := c.RPC.Depth(ctx, &workerv1.DepthRequest{
		Engine: req.Engine, ImageGetUrl: req.ImageGetURL, OutputPutUrl: req.OutputPutURL, Params: req.Params,
	})
	if err != nil {
		return "", workerconn.TranslateErr(err)
	}
	return resp.OutputKey, nil
}
