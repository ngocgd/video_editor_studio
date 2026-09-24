package main

import (
	"context"
	"errors"
)

// runBench will drive render/provider benchmarks starting in phase 9. It is
// a stub in phase 1 so the CLI surface (loomtale bench) is stable.
func runBench(_ context.Context, _ []string) error {
	return errors.New("bench is not implemented until the worker pipeline lands (phase 9)")
}
