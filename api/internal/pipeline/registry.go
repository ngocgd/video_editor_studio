package pipeline

import "fmt"

// Registry maps a step kind to the handler that runs it. It is not a
// package-level global: cmd/worker and cmd/api each own one instance,
// built at startup from every domain package's registered handlers, so
// tests can construct a Registry with only the fakes they need.
type Registry struct {
	handlers map[string]StepHandler
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]StepHandler)}
}

// Register adds h under h.Kind(). It panics on a duplicate kind: two
// handlers silently racing to serve the same step kind is a startup-time
// wiring bug, not a runtime condition to recover from.
func (r *Registry) Register(h StepHandler) {
	if _, exists := r.handlers[h.Kind()]; exists {
		panic(fmt.Sprintf("pipeline: handler for kind %q already registered", h.Kind()))
	}
	r.handlers[h.Kind()] = h
}

// Lookup returns the handler for kind, or false if none is registered.
func (r *Registry) Lookup(kind string) (StepHandler, bool) {
	h, ok := r.handlers[kind]
	return h, ok
}

// Len returns how many kinds have a registered handler. Used at worker
// startup to decide whether to enable River queues at all: a worker with
// an empty registry (every phase before the first one that registers a
// real handler) must never fetch any job, since it cannot run anything
// it would claim without destroying it.
func (r *Registry) Len() int {
	return len(r.handlers)
}
