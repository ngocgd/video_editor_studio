package workerconn

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"loomtale/api/internal/pipeline"
)

func TestTranslateErrClassifiesWorkerStatuses(t *testing.T) {
	cases := []struct {
		name   string
		code   codes.Code
		class  pipeline.FailureClass
		reason string
	}{
		// The worker's answer to an expired presigned URL: retried.
		{"expired transfer URL", codes.Unavailable, pipeline.ClassTransient, "transient"},
		{"invalid request", codes.InvalidArgument, pipeline.ClassPermanent, "validation"},
		{"consent missing", codes.PermissionDenied, pipeline.ClassPermanent, "validation"},
		{"engine missing", codes.FailedPrecondition, pipeline.ClassPermanent, "engine_not_installed"},
		{"gpu oom", codes.ResourceExhausted, pipeline.ClassGPUOOM, "gpu_oom"},
		{"engine crash", codes.Internal, pipeline.ClassTransient, "transient"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := TranslateErr(status.Error(tc.code, "output upload failed with HTTP 403"))
			if err == nil {
				t.Fatal("TranslateErr returned nil for a failed call")
			}
			class, reason := pipeline.Classify(err)
			if class != tc.class || reason != tc.reason {
				t.Fatalf("Classify = (%v, %q), want (%v, %q)", class, reason, tc.class, tc.reason)
			}
		})
	}
}

func TestTranslateErrPassesThroughNonStatusErrors(t *testing.T) {
	if TranslateErr(nil) != nil {
		t.Fatal("TranslateErr(nil) is not nil")
	}
	plain := errors.New("dial refused")
	if got := TranslateErr(plain); !errors.Is(got, plain) {
		t.Fatalf("TranslateErr(plain) = %v, want the same error", got)
	}
}
