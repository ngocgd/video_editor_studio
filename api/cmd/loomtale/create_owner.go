package main

import (
	"context"
	"errors"
)

// runCreateOwner will provision the first tenant owner account once the
// auth schema lands in phase 2. It is a stub in phase 1 so the CLI surface
// (loomtale create-owner) is stable for later phases to fill in.
func runCreateOwner(_ context.Context, _ []string) error {
	return errors.New("create-owner is not implemented until the auth schema lands (phase 2)")
}
