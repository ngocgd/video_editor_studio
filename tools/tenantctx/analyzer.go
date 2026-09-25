// Package tenantctx defines a go/analysis analyzer that fails the build if
// a handler populates a struct-literal field named TenantID/TenantId with
// a value read from a request object's Params or Body (e.g.
// `TenantID: idconv.ToPg(req.Params.TenantId)`) rather than
// tenant.FromCtx(ctx). The tenant a request operates on must come only
// from the session; a value from the path, query, header or body scoping
// a database query is a cross-tenant access bug waiting to happen.
//
// The check tracks simple local-variable assignment within one function
// body: a value read from Params/Body, assigned to a local (directly, or
// through one or more further plain assignments/redeclarations), taints
// that local for the rest of the function, so `id := req.Body.TenantId;
// ...; Params{TenantID: id}` is flagged exactly like the direct form.
// It does not follow a value across function calls, channels, structs,
// or slices — a full data-flow analysis (SSA) would catch those too, but
// is a much larger dependency for a lint check whose real backstop is
// code review. One verified exception exists: POST /auth/switch-tenant
// reads a target tenant id from its request body by design (that is the
// whole point of the endpoint) and checks membership before using it. A
// line with that shape can carry a `// tenantctx:allow: <reason>`
// comment, the same escape-hatch pattern as `//nolint`, to document a
// reviewed exception rather than being silently special-cased.
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

	checkBody := func(body *ast.BlockStmt) {
		if body == nil {
			return
		}
		tainted := taintedLocals(body)
		checkCompositeLits(pass, body, tainted, allowLines)
	}

	insp.Preorder([]ast.Node{(*ast.FuncDecl)(nil), (*ast.FuncLit)(nil)}, func(n ast.Node) {
		switch f := n.(type) {
		case *ast.FuncDecl:
			checkBody(f.Body)
		case *ast.FuncLit:
			checkBody(f.Body)
		}
	})

	return nil, nil
}

// taintedLocals returns the set of local variable names anywhere in body
// that are ever assigned directly from a request object (e.g.
// `id := req.Body.TenantId`). This is deliberately a single hop, not
// transitive taint propagation: a variable derived from a tainted one
// through an unrelated function call (e.g. a DB lookup keyed by a tainted
// value, like `user, _ := q.GetUserByEmail(ctx, email)`) is NOT itself
// tainted, since that call's result is conceptually unrelated data, not a
// copy or wrapping of the request value. Chasing taint through arbitrary
// call boundaries produced false positives on exactly that shape in
// practice (a lookup keyed by a request-sourced value, whose *own*
// results are then otherwise-legitimately used to populate a TenantID
// field sourced from tenant.FromCtx elsewhere in the same function).
func taintedLocals(body *ast.BlockStmt) map[string]bool {
	tainted := make(map[string]bool)
	empty := map[string]bool{}

	ast.Inspect(body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range s.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok || i >= len(s.Rhs) {
					continue
				}
				if sourcedFromRequest(s.Rhs[i], empty) {
					tainted[id.Name] = true
				}
			}
		case *ast.ValueSpec:
			for i, name := range s.Names {
				if i >= len(s.Values) {
					continue
				}
				if sourcedFromRequest(s.Values[i], empty) {
					tainted[name.Name] = true
				}
			}
		}
		return true
	})
	return tainted
}

func checkCompositeLits(pass *analysis.Pass, body *ast.BlockStmt, tainted map[string]bool, allowLines map[int]bool) {
	ast.Inspect(body, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		// A `// tenantctx:allow:` comment is recognised directly above
		// the flagged field itself, or directly above the composite
		// literal it belongs to (the common case: one comment covering a
		// whole multi-field struct literal like Params{TenantID: ...,
		// UserID: ...}), not just the exact field line.
		litLine := pass.Fset.Position(lit.Pos()).Line
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || !tenantFieldNames[key.Name] {
				continue
			}
			if !sourcedFromRequest(kv.Value, tainted) {
				continue
			}
			line := pass.Fset.Position(kv.Pos()).Line
			if allowLines[line] || allowLines[line-1] || allowLines[litLine-1] {
				continue
			}
			pass.Reportf(kv.Pos(),
				"tenantctx: %s is populated from a request object; use tenant.FromCtx(ctx) instead "+
					"(add a `// tenantctx:allow: <reason>` comment on or above this line for a verified exception)",
				key.Name)
		}
		return true
	})
}

// sourcedFromRequest reports whether expr's subtree contains a selector
// naming "Params" or "Body" (oapi-codegen's request object shape is always
// RequestObject{Params ...; Body *...}), which is how a value read from an
// HTTP request reaches a struct literal, however many helper calls
// (idconv.ToPg(...), string(...), &x) wrap it — or a bare reference to a
// local variable already known to be tainted (see taintedLocals).
func sourcedFromRequest(expr ast.Expr, tainted map[string]bool) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.SelectorExpr:
			if v.Sel.Name == "Params" || v.Sel.Name == "Body" {
				found = true
				return false
			}
		case *ast.Ident:
			if tainted[v.Name] {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// collectAllowComments returns the set of source lines covered by a
// comment group that contains a `tenantctx:allow` marker anywhere in it,
// across every file in the package. A whole multi-line `//` block (each
// line of which is its own *ast.Comment token) counts as covered if any
// one of its lines carries the marker — not just that exact line — so a
// short justification does not have to be repeated on the specific line
// immediately adjacent to what it is documenting.
func collectAllowComments(pass *analysis.Pass) map[int]bool {
	lines := make(map[int]bool)
	for _, f := range pass.Files {
		for _, cg := range f.Comments {
			marked := false
			for _, c := range cg.List {
				if strings.Contains(c.Text, "tenantctx:allow") {
					marked = true
					break
				}
			}
			if !marked {
				continue
			}
			start := pass.Fset.Position(cg.Pos()).Line
			end := pass.Fset.Position(cg.End()).Line
			for l := start; l <= end; l++ {
				lines[l] = true
			}
		}
	}
	return lines
}
