// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func BenchmarkPreparedEvaluate(b *testing.B) {
	eval, err := evaluator.New(filtertest.MustPolicy(b, filter.WithAllowedFields("age")))
	require.NoError(b, err)
	prepared, err := eval.Prepare(&filter.BinaryOpNode{Op: filter.OpGTE, Left: &filter.IdentNode{Name: "age"}, Right: &filter.LiteralNode{Value: int64(18)}})
	require.NoError(b, err)

	data := map[string]any{"age": int64(21)}

	b.ReportAllocs()

	for b.Loop() {
		_, _ = prepared.Evaluate(data)
	}
}

// BenchmarkPreparedEvaluate_Null evaluates `age > 5 || name == "x"` on a record whose age is known and on one whose age is null:
// the unknown comparison that || absorbs must cost no more than a known one.
func BenchmarkPreparedEvaluate_Null(b *testing.B) {
	eval, err := evaluator.New(filtertest.MustPolicy(b, filter.WithAllowedFields("age", "name")))
	require.NoError(b, err)
	prepared, err := eval.Prepare(filtertest.MustParseFilter(b, `age > 5 || name == "x"`))
	require.NoError(b, err)

	for _, bc := range []struct {
		name string
		age  any
	}{{"known", int64(3)}, {"null", nil}} {
		data := map[string]any{"age": bc.age, "name": "y"}

		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, _ = prepared.Evaluate(data)
			}
		})
	}
}

// BenchmarkPreparedEvaluate_NullCalls evaluates string functions and substring() over a null argument, absorbed by ||.
func BenchmarkPreparedEvaluate_NullCalls(b *testing.B) {
	eval, err := evaluator.New(filtertest.MustPolicy(b, filter.WithAllowedFields("name", "note")))
	require.NoError(b, err)
	prepared, err := eval.Prepare(filtertest.MustParseFilter(b,
		`name.contains(note) || note.substring(1) == "x" || !(note == null) && note.matches("a") || name == "y"`))
	require.NoError(b, err)

	data := map[string]any{"name": "y", "note": nil}

	b.ReportAllocs()

	for b.Loop() {
		_, _ = prepared.Evaluate(data)
	}
}

// BenchmarkPreparedEvaluate_Matches evaluates a matches() with a literal pattern on a prepared filter, serially and in parallel.
func BenchmarkPreparedEvaluate_Matches(b *testing.B) {
	eval, err := evaluator.New(filtertest.MustPolicy(b, filter.WithAllowedFields("email")))
	require.NoError(b, err)
	prepared, err := eval.Prepare(filtertest.MustParseFilter(b, `email.matches("^[a-z]+@example\\.(com|org)$")`))
	require.NoError(b, err)

	data := map[string]any{"email": "alice@example.com"}

	b.Run("serial", func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			_, _ = prepared.Evaluate(data)
		}
	})
	b.Run("parallel", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_, _ = prepared.Evaluate(data)
			}
		})
	})
}

func BenchmarkSimpleLower15_1(b *testing.B) {
	for b.Loop() {
		_ = evaluator.SimpleLower15_1("ПрИвЕт Hello ЁЖИК")
	}
}
