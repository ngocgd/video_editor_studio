package pipeline

import (
	"errors"
	"fmt"
	"testing"
)

func TestClassifyKnownSentinels(t *testing.T) {
	cases := []struct {
		err       error
		wantClass FailureClass
		wantCode  string
	}{
		{fmt.Errorf("wrap: %w", ErrGPUOOM), ClassGPUOOM, "gpu_oom"},
		{fmt.Errorf("wrap: %w", ErrValidation), ClassPermanent, "validation"},
		{fmt.Errorf("wrap: %w", ErrEngineNotInstalled), ClassPermanent, "engine_not_installed"},
		{fmt.Errorf("wrap: %w", ErrLicenceRefused), ClassPermanent, "licence_refused"},
		{errors.New("connection reset by peer"), ClassTransient, "transient"},
		{nil, ClassTransient, ""},
	}
	for _, c := range cases {
		gotClass, gotCode := Classify(c.err)
		if gotClass != c.wantClass || gotCode != c.wantCode {
			t.Errorf("Classify(%v) = (%v, %q), want (%v, %q)", c.err, gotClass, gotCode, c.wantClass, c.wantCode)
		}
	}
}
