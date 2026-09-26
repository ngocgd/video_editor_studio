package claudecli

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

// statusTimeout bounds the /healthz probe so a stuck sidecar never
// stalls the caller (the worker's status heartbeat).
const statusTimeout = 5 * time.Second

// versionHeader is the header the sidecar's /healthz sets to the claude
// CLI version banner (see api/cmd/llmcli/handler.go).
const versionHeader = "X-Claude-CLI-Version"

// CLIStatus is the sidecar's health as seen by a caller on llm_net.
type CLIStatus struct {
	// Installed is true when the sidecar answered at all, even if it is
	// currently unhealthy.
	Installed bool
	// Authenticated is true only on a healthy response: the sidecar's
	// startup self-check verified the pinned binary, its no-tools flag
	// surface and, outside host-fallback mode, a configured OAuth token.
	Authenticated bool
	// Version is the CLI's version banner, when the sidecar reports it.
	Version string
	// Detail explains an unhealthy state (the sidecar's disabled reason).
	Detail string
}

// Status probes the sidecar's GET /healthz. A transport error means the
// sidecar is unreachable and is returned as err.
func (p *Provider) Status(ctx context.Context) (CLIStatus, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodGet, p.BaseURL+"/healthz", nil)
	if err != nil {
		return CLIStatus{}, err
	}

	resp, err := p.Client.Do(req)
	if err != nil {
		return CLIStatus{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	status := CLIStatus{Installed: true, Version: strings.TrimSpace(resp.Header.Get(versionHeader))}
	if resp.StatusCode == http.StatusOK {
		status.Authenticated = true
		return status, nil
	}
	status.Detail = strings.TrimSpace(string(body))
	return status, nil
}
