package health

import (
	"context"
	"net/http"
	"time"

	"loomtale/api/internal/httpapi/gen"
)

// Handler implements the generated strict server interface for the health
// domain (/healthz, /readyz).
type Handler struct {
	Version string
	DB      Pinger
	Storage Pinger
}

var _ gen.StrictServerInterface = (*Handler)(nil)

// GetHealthz answers the liveness probe: process is up, no dependency check.
func (h *Handler) GetHealthz(ctx context.Context, _ gen.GetHealthzRequestObject) (gen.GetHealthzResponseObject, error) {
	return gen.GetHealthz200JSONResponse{
		Status:  "ok",
		Version: h.Version,
	}, nil
}

// GetReadyz answers the readiness probe: Postgres and MinIO must both
// respond within a short budget.
func (h *Handler) GetReadyz(ctx context.Context, _ gen.GetReadyzRequestObject) (gen.GetReadyzResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	checks := map[string]string{}
	ready := true

	if err := h.DB.Ping(ctx); err != nil {
		checks["database"] = err.Error()
		ready = false
	} else {
		checks["database"] = "ok"
	}

	if err := h.Storage.Ping(ctx); err != nil {
		checks["storage"] = err.Error()
		ready = false
	} else {
		checks["storage"] = "ok"
	}

	if !ready {
		detail := "one or more dependencies failed the readiness check"
		return gen.GetReadyz503ApplicationProblemPlusJSONResponse{
			Title:  "service not ready",
			Status: http.StatusServiceUnavailable,
			Detail: &detail,
		}, nil
	}

	return gen.GetReadyz200JSONResponse{
		Status: "ok",
		Checks: &checks,
	}, nil
}
