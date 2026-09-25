// Package workerconn builds the shared gRPC client connection every
// Python-worker client package (pyworker, tts, align, train, vision)
// uses: a bearer token carried in call metadata (never mTLS, since the
// worker is reachable only from the api/worker network segment) and a
// 4MB max message size, per the phase 4 contract.
package workerconn

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/secretstr"
)

// maxMessageSize bounds both send and receive: inputs/outputs travel as
// presigned URLs, never raw media bytes, so 4MB is generous headroom for
// metadata/progress/log payloads.
const maxMessageSize = 4 * 1024 * 1024

// Dial opens a gRPC connection to target (e.g. "pyworker:9090") with the
// shared bearer token attached to every call via a per-RPC credential.
func Dial(target string, token secretstr.String) (*grpc.ClientConn, error) {
	return grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(bearerCreds{token: token}),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxMessageSize),
			grpc.MaxCallSendMsgSize(maxMessageSize),
		),
	)
}

// bearerCreds implements credentials.PerRPCCredentials with a static
// shared-secret token (this is an internal, gpu_net-isolated hop, not a
// user-facing auth boundary; insecure.NewCredentials means no TLS, which
// is acceptable because the network itself has no other members).
type bearerCreds struct {
	token secretstr.String
}

func (b bearerCreds) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + b.token.Reveal()}, nil
}

func (b bearerCreds) RequireTransportSecurity() bool { return false }

// TranslateErr maps the Python worker's gRPC status codes onto the phase
// 3 pipeline sentinels (pipeline.ErrGPUOOM, pipeline.ErrEngineNotInstalled)
// so every worker client package (pyworker, tts, align, train, vision)
// reports errors a StepHandler.Run can pass straight to
// pipeline.Classify.
func TranslateErr(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	switch st.Code() {
	case codes.FailedPrecondition:
		return fmt.Errorf("%w: %s", pipeline.ErrEngineNotInstalled, st.Message())
	case codes.ResourceExhausted:
		return fmt.Errorf("%w: %s", pipeline.ErrGPUOOM, st.Message())
	default:
		return err
	}
}
