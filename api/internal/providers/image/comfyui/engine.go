package comfyui

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"loomtale/api/internal/pipeline"
)

// defaultPollInterval is how often Run polls /history and samples
// /system_stats while a prompt executes.
const defaultPollInterval = 500 * time.Millisecond

// Engine runs named workflow templates on one ComfyUI server and returns
// the output images. It is the image generation surface the image steps
// and the benchmark harness call.
type Engine struct {
	Client    *Client
	Templates map[string]*Template
	// PollInterval defaults to defaultPollInterval.
	PollInterval time.Duration
}

// Result is one finished workflow run.
type Result struct {
	PromptID string
	Images   [][]byte
	// Seconds is wall time from submit to finished history.
	Seconds float64
	// VRAMPeakMB is the card-wide VRAM in use at its highest sample
	// (vram_total - vram_free, so it includes other GPU users).
	VRAMPeakMB int64
	// TorchPeakMB is the highest VRAM this ComfyUI process's PyTorch
	// allocator held during the run.
	TorchPeakMB int64
}

// ErrExecution is a workflow that ComfyUI accepted but failed to run.
var ErrExecution = errors.New("comfyui: workflow execution failed")

// Run uploads images (keyed by image parameter name), builds workflow
// with params, submits it and waits for its outputs.
func (e *Engine) Run(ctx context.Context, workflow string, params map[string]any, images map[string][]byte) (Result, error) {
	tpl, ok := e.Templates[workflow]
	if !ok {
		return Result{}, fmt.Errorf("%w: unknown workflow %q", ErrInvalidParams, workflow)
	}
	merged := make(map[string]any, len(params)+len(images))
	for k, v := range params {
		merged[k] = v
	}
	for name, data := range images {
		uploaded, err := e.Client.UploadImage(ctx, data)
		if err != nil {
			return Result{}, err
		}
		merged[name] = uploaded
	}
	graph, err := tpl.Build(merged)
	if err != nil {
		return Result{}, err
	}

	start := time.Now()
	promptID, err := e.Client.Submit(ctx, graph, "loomtale")
	if err != nil {
		return Result{}, err
	}
	res := Result{PromptID: promptID}
	entry, err := e.wait(ctx, promptID, &res)
	if err != nil {
		return res, err
	}
	res.Seconds = time.Since(start).Seconds()

	out, ok := entry.Outputs[tpl.Map.OutputNode]
	if !ok || len(out.Images) == 0 {
		return res, fmt.Errorf("%w: output node %s produced no image", ErrExecution, tpl.Map.OutputNode)
	}
	for _, img := range out.Images {
		data, err := e.Client.FetchOutput(ctx, img.Filename, img.Subfolder, img.Type)
		if err != nil {
			return res, err
		}
		res.Images = append(res.Images, data)
	}
	return res, nil
}

// wait polls history until promptID finishes, sampling VRAM on every
// poll so the result carries the run's peak.
func (e *Engine) wait(ctx context.Context, promptID string, res *Result) (HistoryEntry, error) {
	interval := e.PollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if stats, err := e.Client.SystemStats(ctx); err == nil {
			if gpu, ok := stats.GPU(); ok {
				res.VRAMPeakMB = max(res.VRAMPeakMB, (gpu.VRAMTotal-gpu.VRAMFree)>>20)
				res.TorchPeakMB = max(res.TorchPeakMB, gpu.TorchVRAMTotal>>20)
			}
		}
		history, err := e.Client.History(ctx, promptID)
		if err != nil {
			return HistoryEntry{}, err
		}
		if entry, ok := history[promptID]; ok && (entry.Status.Completed || entry.Status.StatusStr == "error") {
			if entry.Status.StatusStr == "error" {
				return entry, executionError(entry)
			}
			return entry, nil
		}
		select {
		case <-ctx.Done():
			// Stop ComfyUI from finishing work nobody will collect.
			interruptCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = e.Client.Interrupt(interruptCtx)
			cancel()
			return HistoryEntry{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

// executionError turns ComfyUI's execution_error message into an error
// the pipeline classifies: CUDA out-of-memory becomes gpu_oom, anything
// else a plain (transient) execution failure.
func executionError(entry HistoryEntry) error {
	for _, msg := range entry.Status.Messages {
		if len(msg) != 2 {
			continue
		}
		var event string
		if json.Unmarshal(msg[0], &event) != nil || event != "execution_error" {
			continue
		}
		var payload struct {
			NodeType         string `json:"node_type"`
			ExceptionType    string `json:"exception_type"`
			ExceptionMessage string `json:"exception_message"`
		}
		_ = json.Unmarshal(msg[1], &payload)
		text := payload.ExceptionType + ": " + payload.ExceptionMessage
		if isOOM(text) {
			return fmt.Errorf("%w: %s in %s: %s", pipeline.ErrGPUOOM, ErrExecution, payload.NodeType, strings.TrimSpace(text))
		}
		return fmt.Errorf("%w in %s: %s", ErrExecution, payload.NodeType, strings.TrimSpace(text))
	}
	return ErrExecution
}

func isOOM(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "out of memory") || strings.Contains(lower, "outofmemoryerror")
}

// Interrupt stops the prompt ComfyUI is currently executing.
func (c *Client) Interrupt(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/interrupt", nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("comfyui: interrupt request failed: %w", err)
	}
	_ = resp.Body.Close()
	return nil
}

// UploadImage stores data in ComfyUI's input directory under a random
// name and returns that name, for a LoadImage node to reference. The
// name is generated here, never taken from the caller, so an upload can
// never overwrite another run's input or escape the input directory.
func (c *Client) UploadImage(ctx context.Context, data []byte) (string, error) {
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	name := "loomtale-" + hex.EncodeToString(nonce[:]) + ".png"

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("image", name)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	if err := form.WriteField("type", "input"); err != nil {
		return "", err
	}
	if err := form.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/upload/image", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("comfyui: upload request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("comfyui: upload unexpected status %d: %s", resp.StatusCode, b)
	}
	var out struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("comfyui: decode upload response: %w", err)
	}
	if out.Name != name {
		return "", fmt.Errorf("comfyui: upload stored %q, expected %q", out.Name, name)
	}
	return out.Name, nil
}
