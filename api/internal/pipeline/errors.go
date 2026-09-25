package pipeline

import "errors"

// errGPUDisabled is returned by NoopResidency, which is wired in only when
// WORKER_GPU=false; reaching it means a gpu-queue step was admitted while
// the GPU is disabled, which is itself a bug in admission or queue
// resolution.
var errGPUDisabled = errors.New("pipeline: GPU worker disabled, no residency backend configured")

// ErrQuotaExceeded is returned by an AdmissionCheck (see quota.Check) when
// a tenant is over its configured limit. Engine.Enqueue maps it to a 429.
var ErrQuotaExceeded = errors.New("pipeline: tenant quota exceeded")

// ErrAdmissionDenied is a generic admission failure (e.g. disk watermark,
// registered by phase 8) mapped to a 507 by Engine.Enqueue.
var ErrAdmissionDenied = errors.New("pipeline: admission check denied")
