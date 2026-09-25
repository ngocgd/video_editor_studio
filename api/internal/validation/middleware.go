// Package validation mounts a request validator built from the same
// bundled OpenAPI spec oapi-codegen generates from (a single source of
// truth), checking full JSON Schema constraints (formats, enums, ranges,
// required fields) that the generated strict binding alone does not
// enforce.
package validation

import (
	"log/slog"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	"loomtale/api/internal/httpx"
)

// Middleware builds a request-validating middleware from spec. Body
// reads are validated without consuming the body for the downstream
// handler: openapi3filter reads it into memory and RequestValidationInput
// replaces r.Body with a fresh reader over the same bytes.
func Middleware(spec *openapi3.T) (func(http.Handler) http.Handler, error) {
	router, err := gorillamux.NewRouter(spec)
	if err != nil {
		return nil, err
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			route, pathParams, err := router.FindRoute(r)
			if err != nil {
				// Fail closed, not open: the generated chi router and this
				// validator are built from the same spec, so any request
				// chi would actually dispatch to a handler must resolve
				// here too. A route that fails to resolve is either a
				// genuinely unknown path (correctly a 404) or a
				// router-library disagreement that must never be allowed
				// to skip validation (schema constraints, and the
				// content-type check that is the main defence against a
				// login-CSRF style text/plain form POST, both depend on
				// this middleware actually running).
				httpx.WriteProblem(w, httpx.Problem{Title: "not found", Status: http.StatusNotFound})
				return
			}

			input := &openapi3filter.RequestValidationInput{
				Request:    r,
				PathParams: pathParams,
				Route:      route,
			}
			if err := openapi3filter.ValidateRequest(r.Context(), input); err != nil {
				// The error can include kin-openapi's internal schema
				// description and, for some failure modes, an echo of the
				// submitted value (e.g. a too-short password) — log it,
				// never return it to the client.
				slog.ErrorContext(r.Context(), "request validation failed", "error", err, "path", r.URL.Path)
				httpx.WriteProblem(w, httpx.Problem{Title: "request validation failed", Status: http.StatusBadRequest})
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}
