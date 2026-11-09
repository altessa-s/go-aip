// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"testing"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

func BenchmarkEvaluate_ArrayFields(b *testing.B) {
	// Without WithProfile the absent field fails the ordering comparison, so the benchmark tests it for null instead.
	eval, err := newEvaluator(withPolicy(filter.WithArrayFields("tags")))
	if err != nil {
		b.Fatal(err)
	}

	node := filtertest.MustParseFilter(b, `(missing == null || age >= 18) && tags == "green" && "red" in tags && tags.size() > 1`)
	data := map[string]any{"age": int64(30), "tags": []any{"red", "green"}}

	b.ReportAllocs()

	for b.Loop() {
		if _, err := eval.Evaluate(node, data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParse_EnumNames(b *testing.B) {
	p, err := filterparser.New(filterparser.WithNoCache(true), filterparser.WithEnumNames(map[string]map[string]any{
		"state": {"STATE_ACTIVE": 1, "STATE_PAUSED": 2, "STATE_DONE": 3},
	}))
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()

	for b.Loop() {
		if _, err := p.Parse(b.Context(), `state in ["STATE_ACTIVE", "STATE_PAUSED"] && name == "x"`); err != nil {
			b.Fatal(err)
		}
	}
}
