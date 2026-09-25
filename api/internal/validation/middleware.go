// Package validation mounts a request validator built from the same
// bundled OpenAPI spec oapi-codegen generates from (a single source of
// truth), checking full JSON Schema constraints (formats, enums, ranges,
// required fields) that the generated strict binding alone does not
// enforce.
package validation

import (
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
				// The generated chi router and this validator are built
				// from the same spec, so a route that chi will dispatch
				// should always resolve here too; if it somehow does not,
				// fail open to the handler rather than take the whole API
				// down on a router-library edge case.
				next.ServeHTTP(w, r)
				return
			}

			input := &openapi3filter.RequestValidationInput{
				Request:    r,
				PathParams: pathParams,
				Route:      route,
			}
			if err := openapi3filter.ValidateRequest(r.Context(), input); err != nil {
				detail := err.Error()
				httpx.WriteProblem(w, httpx.Problem{Title: "request validation failed", Status: http.StatusBadRequest, Detail: detail})
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}
