package claudecli

import (
	"context"
	"io"
	"net/http"
	"time"
)

// statusTimeout bounds the /healthz probe so GetClaudeCliStatus never
// hangs the settings page waiting on a stuck sidecar.
const statusTimeout = 5 * time.Second

// Status probes the sidecar's GET /healthz, the only status surface it
// exposes today (see api/cmd/llmcli/handler.go's handleHealthz): a 200
// response means installed and authenticated (the sidecar's own startup
// self-check already verified the binary and, unless host-fallback mode
// is used, a real OAuth token); a non-200 response means it is reachable
// but unhealthy (its body is the disabled reason); a transport error
// means unreachable. The sidecar does not report a version string over
// HTTP, so Status never returns one.
func (p *Provider) Status(ctx context.Context) (installed, authenticated bool, detail string, err error) {
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, p.BaseURL+"/healthz", nil)
	if reqErr != nil {
		return false, false, "", reqErr
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	req = req.WithContext(timeoutCtx)

	resp, doErr := p.Client.Do(req)
	if doErr != nil {
		return false, false, "", doErr
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode == http.StatusOK {
		return true, true, "", nil
	}
	return true, false, string(body), nil
}
