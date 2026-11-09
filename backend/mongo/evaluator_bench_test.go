// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

// benchMongoExpr mixes the predicates WithProfile changes: an absent field, a list, a string function and size().
const benchMongoExpr = `(missing < 3 || age >= 18) && tags == "green" && !(name.startsWith("x")) && tags.size() > 1`

func benchEvaluate(b *testing.B, opts ...any) {
	b.Helper()

	eval, err := newEvaluator(opts...)
	if err != nil {
		b.Fatal(err)
	}

	node := filtertest.MustParseFilter(b, benchMongoExpr)
	data := map[string]any{"age": int64(30), "name": "John", "tags": []any{"red", "green"}}

	b.ReportAllocs()

	for b.Loop() {
		if _, err := eval.Evaluate(node, data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEvaluate_MongoSemantics(b *testing.B) {
	benchEvaluate(b, filtereval.WithProfile(mongo.DefaultSchema()))
}

// BenchmarkEvaluate_MongoSemanticsTyped is BenchmarkEvaluate_MongoSemantics on typed values ([]string, a named string, an int32
// kind): the slow path that converts them to their stored form.
func BenchmarkEvaluate_MongoSemanticsTyped(b *testing.B) {
	eval, err := newEvaluator(filtereval.WithProfile(mongo.DefaultSchema()))
	if err != nil {
		b.Fatal(err)
	}

	node := filtertest.MustParseFilter(b, benchMongoExpr)
	data := map[string]any{"age": counter(30), "name": label("John"), "tags": []string{"red", "green"}}

	b.ReportAllocs()

	for b.Loop() {
		if _, err := eval.Evaluate(node, data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEvaluator_Lookup(b *testing.B) {
	eval, err := newEvaluator(filtereval.WithProfile(mongo.DefaultSchema()))
	if err != nil {
		b.Fatal(err)
	}

	data := map[string]any{"meta": map[string]any{"name": "alpha"}}

	b.ReportAllocs()

	for b.Loop() {
		if _, _, err := eval.Lookup(data, "meta.name"); err != nil {
			b.Fatal(err)
		}
	}
}
