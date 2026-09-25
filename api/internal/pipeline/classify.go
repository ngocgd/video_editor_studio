package pipeline

import "errors"

// FailureClass is how a StepHandler.Run error is treated by the
// dispatcher.
type FailureClass int

const (
	// ClassTransient covers network errors, 5xx responses and timeouts:
	// retried with backoff, up to MaxTransientAttempts.
	ClassTransient FailureClass = iota
	// ClassGPUOOM is a CUDA out-of-memory error from any backend: the
	// dispatcher fully unloads every resident model through
	// ModelResidency.UnloadAll and allows exactly one retry; a second OOM
	// for the same step is permanent.
	ClassGPUOOM
	// ClassPermanent covers validation errors, a missing engine
	// installation, and a refused licence: the step is cancelled and
	// marked failed on first occurrence, never retried.
	ClassPermanent
)

// MaxTransientAttempts bounds River-level retries for a transient
// failure. It is enforced via river.InsertOpts.MaxAttempts at enqueue
// time, not the client's global default.
const MaxTransientAttempts = 3

// Sentinel errors a StepHandler.Run implementation wraps (fmt.Errorf("%w:
// ...", pipeline.ErrGPUOOM)) to drive classification. Any error that does
// not match one of these is treated as transient, which is the safe
// default: an unrecognised error retries a bounded number of times rather
// than either looping forever or silently giving up on the first attempt.
var (
	ErrGPUOOM             = errors.New("gpu_oom")
	ErrValidation         = errors.New("validation")
	ErrEngineNotInstalled = errors.New("engine_not_installed")
	ErrLicenceRefused     = errors.New("licence_refused")
)

// Classify maps a StepHandler.Run error to a FailureClass and the
// error_code stored on the step row.
func Classify(err error) (FailureClass, string) {
	switch {
	case err == nil:
		return ClassTransient, ""
	case errors.Is(err, ErrGPUOOM):
		return ClassGPUOOM, "gpu_oom"
	case errors.Is(err, ErrValidation):
		return ClassPermanent, "validation"
	case errors.Is(err, ErrEngineNotInstalled):
		return ClassPermanent, "engine_not_installed"
	case errors.Is(err, ErrLicenceRefused):
		return ClassPermanent, "licence_refused"
	default:
		return ClassTransient, "transient"
	}
}
