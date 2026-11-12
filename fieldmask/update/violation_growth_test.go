// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"

	"github.com/altessa-s/go-aip/internal/behavior"
)

// Check both the ordered set's behavior and its algorithmic structure: each
// insertion performs one hash lookup and at most one hash assignment, with no
// loop or helper call that could hide a scan of previously seen violations.
func TestViolationDeduplicationGrowth(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "update_check.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	var body *ast.BlockStmt

	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "fail" {
			body = fn.Body
		}
	}

	if body == nil {
		t.Fatal("missing violation insertion")
	}

	probes := 0

	ast.Inspect(body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			t.Fatal("violation insertion must not scan earlier violations")
		case *ast.CallExpr:
			fn, ok := n.Fun.(*ast.Ident)
			if !ok || (fn.Name != "make" && fn.Name != "append") {
				t.Fatalf("unaccounted insertion call: %#v", n.Fun)
			}
		case *ast.IndexExpr:
			field, ok := n.X.(*ast.SelectorExpr)
			if !ok || field.Sel.Name != "violationSet" {
				t.Fatalf("unaccounted insertion index: %#v", n.X)
			}

			probes++
		}

		return true
	})

	if probes != 2 {
		t.Fatalf("want one lookup and one insertion, got %d set operations", probes)
	}

	for _, count := range []int{1000, 30000} {
		u := &updateCheck{}

		values := make([]FieldViolation, count)
		for i := range values {
			values[i] = behavior.ProtectedViolation(fmt.Sprintf("members.%d.org", i), annotations.FieldBehavior_IMMUTABLE)
			u.fail(values[i])
		}

		for _, v := range values {
			u.fail(v)
		}

		if len(u.violationSet) != count || !slices.Equal(u.violations, values) {
			t.Fatal("violations lost distinct values or first-seen order")
		}

		t.Logf("%d distinct plus %d duplicate insertions: at most %d hash operations", count, count, probes*2*count)
	}
}
