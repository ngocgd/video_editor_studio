package tenantctx_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"loomtale/tools/tenantctx"
)

func TestAnalyzer(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, tenantctx.Analyzer, "a")
}
