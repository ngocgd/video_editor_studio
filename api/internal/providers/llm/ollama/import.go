package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
)

// Importer creates an Ollama model from a local GGUF without any
// network egress: the ollama container has none, so `ollama pull` can
// never work there. It does what `ollama create -f Modelfile` does on the
// client side: upload the weights as a content-addressed blob (skipped
// when Ollama already holds that digest), then create the model from the
// blob plus the Modelfile's parameters.
type Importer struct {
	BaseURL string
	Client  *http.Client
}

// Exists reports whether Ollama already has model (POST /api/show).
func (i *Importer) Exists(ctx context.Context, model string) (bool, error) {
	status, _, err := i.do(ctx, http.MethodPost, "/api/show", map[string]any{"model": model})
	if err != nil {
		return false, err
	}
	switch status {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("ollama: /api/show unexpected status %d", status)
	}
}

// Import creates model from the GGUF at localPath, whose sha256 the
// caller has already verified against the manifest pin. Ollama checks
// the uploaded bytes against that digest itself as well.
func (i *Importer) Import(ctx context.Context, model string, mf Modelfile, localPath, sha256Hex string) error {
	digest := "sha256:" + sha256Hex
	if err := i.ensureBlob(ctx, digest, localPath); err != nil {
		return err
	}
	body := map[string]any{
		"model":  model,
		"files":  map[string]string{path.Base(mf.From): digest},
		"stream": false,
	}
	if len(mf.Parameters) > 0 {
		body["parameters"] = mf.Parameters
	}
	if mf.Template != "" {
		body["template"] = mf.Template
	}
	if mf.System != "" {
		body["system"] = mf.System
	}
	status, msg, err := i.do(ctx, http.MethodPost, "/api/create", body)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("ollama: create %s: status %d: %s", model, status, msg)
	}
	return nil
}

// ensureBlob uploads localPath as blob digest unless Ollama has it.
func (i *Importer) ensureBlob(ctx context.Context, digest, localPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, i.BaseURL+"/api/blobs/"+digest, nil)
	if err != nil {
		return err
	}
	resp, err := i.Client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama: blob check: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}

	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, i.BaseURL+"/api/blobs/"+digest, f)
	if err != nil {
		return err
	}
	req.ContentLength = info.Size()
	resp, err = i.Client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama: blob upload: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("ollama: blob upload: status %d: %s", resp.StatusCode, msg)
	}
	return nil
}

func (i *Importer) do(ctx context.Context, method, route string, body any) (int, string, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, "", err
	}
	req, err := http.NewRequestWithContext(ctx, method, i.BaseURL+route, bytes.NewReader(payload))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := i.Client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("ollama: %s: %w", route, err)
	}
	defer func() { _ = resp.Body.Close() }()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, string(msg), nil
}
