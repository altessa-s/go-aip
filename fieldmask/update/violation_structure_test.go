// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"go/parser"
	"go/token"
	"testing"
)

func TestViolationGrowthUsesDeterministicWork(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "violation_growth_test.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}

	for _, spec := range file.Imports {
		if spec.Path.Value == `"time"` {
			t.Fatal("violation-growth proof must not depend on wall-clock timing")
		}
	}
}
