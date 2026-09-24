// Command loomtale is the operator CLI: migrate, create-owner, bench.
package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: loomtale <migrate|create-owner|bench> [args]")
		os.Exit(2)
	}

	ctx := context.Background()
	var err error
	switch os.Args[1] {
	case "migrate":
		err = runMigrate(ctx, os.Args[2:])
	case "create-owner":
		err = runCreateOwner(ctx, os.Args[2:])
	case "bench":
		err = runBench(ctx, os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
