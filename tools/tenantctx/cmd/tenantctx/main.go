// Command tenantctx runs the tenantctx analyzer as a standalone checker,
// wired into `make lint`.
package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"loomtale/tools/tenantctx"
)

func main() {
	singlechecker.Main(tenantctx.Analyzer)
}
