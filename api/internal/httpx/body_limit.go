package httpx

import "net/http"

// MaxBodyBytes caps every request body the API accepts. The API never
// receives file uploads directly (those go straight to MinIO via a
// presigned POST); every JSON request body is small, so this is generous
// for legitimate traffic while ruling out a multi-hundred-MB body being
// buffered in memory by the OpenAPI validator or a JSON decoder.
const MaxBodyBytes = 256 * 1024 // 256KiB

// MaxBodyMiddleware wraps r.Body in http.MaxBytesReader so a body over
// MaxBodyBytes aborts the read with an error instead of being fully
// buffered into memory first.
func MaxBodyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
		next.ServeHTTP(w, r)
	})
}
