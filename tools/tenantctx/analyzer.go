// Package tenantctx defines a go/analysis analyzer that fails the build if
// a handler populates a struct-literal field named TenantID/TenantId with
// an expression that directly references a request object's Params or
// Body (e.g. `TenantID: idconv.ToPg(req.Params.TenantId)`) rather than
// tenant.FromCtx(ctx). The tenant a request operates on must come only
// from the session; a value from the path, query, header or body scoping
// a database query is a cross-tenant access bug waiting to happen.
//
// The check is syntactic (it inspects the field's own value expression,
// not the whole function's data flow), so it catches the direct form
// above but not a value laundered through an intermediate local variable a
// few statements earlier. That is a deliberate scope tradeoff for a fast,
// dependency-free check; code review is still expected to catch the
// indirect form. One verified exception exists: POST /auth/switch-tenant
// reads a target tenant id from its request body by design (that is the
// whole point of the endpoint) and checks membership before using it. A
// line with that shape can carry a `// tenantctx:allow: <reason>` comment,
// the same escape-hatch pattern as `//nolint`, to document a reviewed
// exception rather than being silently special-cased.
package tenantctx

import (
	"go/ast"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// Analyzer is the tenantctx go/analysis analyzer.
var Analyzer = &analysis.Analyzer{
	Name:     "tenantctx",
	Doc:      "flags TenantID/TenantId struct fields populated from a request object instead of tenant.FromCtx",
	Run:      run,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
}

var tenantFieldNames = map[string]bool{"TenantID": true, "TenantId": true}

func run(pass *analysis.Pass) (any, error) {
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	allowLines := collectAllowComments(pass)

	insp.Preorder([]ast.Node{(*ast.CompositeLit)(nil)}, func(n ast.Node) {
		lit, _ := n.(*ast.CompositeLit)
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || !tenantFieldNames[key.Name] {
				continue
			}
			if !sourcedFromRequest(kv.Value) {
				continue
			}
			line := pass.Fset.Position(kv.Pos()).Line
			if allowLines[line] || allowLines[line-1] {
				continue
			}
			pass.Reportf(kv.Pos(),
				"tenantctx: %s is populated from a request object; use tenant.FromCtx(ctx) instead "+
					"(add a `// tenantctx:allow: <reason>` comment on or above this line for a verified exception)",
				key.Name)
		}
	})

	return nil, nil
}

// sourcedFromRequest reports whether expr's subtree contains a selector
// naming "Params" or "Body" (oapi-codegen's request object shape is always
// RequestObject{Params ...; Body *...}), which is how a value read from an
// HTTP request reaches a struct literal, however many helper calls
// (idconv.ToPg(...), string(...), &x) wrap it.
func sourcedFromRequest(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name == "Params" || sel.Sel.Name == "Body" {
			found = true
			return false
		}
		return true
	})
	return found
}

// collectAllowComments returns the set of source lines carrying a
// `tenantctx:allow` comment, across every file in the package.
func collectAllowComments(pass *analysis.Pass) map[int]bool {
	lines := make(map[int]bool)
	for _, f := range pass.Files {
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				if strings.Contains(c.Text, "tenantctx:allow") {
					lines[pass.Fset.Position(c.Pos()).Line] = true
				}
			}
		}
	}
	return lines
}
