// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

// structEncoding is a minimal value encoding for these tests: it stores a struct as the document of its exported fields, keyed
// by their bson tag names, the way the MongoDB driver's encoding (ValueEncoding) does for these
// plain structs.
type structEncoding struct{}

func (structEncoding) Stored(v any, _ filtereval.Budget) (any, bool) { return v, false }

func (e structEncoding) Encode(v any, budget filtereval.Budget) any {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer && !rv.IsNil() {
		rv = rv.Elem()
	}

	if rv.Kind() != reflect.Struct || !budget.Charge(rv.NumField()) {
		return v
	}

	doc := make(map[string]any, rv.NumField())
	for i := range rv.NumField() {
		f := rv.Type().Field(i)

		name, _, _ := strings.Cut(f.Tag.Get("bson"), ",")
		if name == "" {
			name = strings.ToLower(f.Name)
		}

		value := rv.Field(i).Interface()
		if rv.Field(i).Kind() == reflect.Struct {
			value = e.Encode(value, budget)
		}

		doc[name] = value
	}

	return doc
}

func (structEncoding) EncodesItself(reflect.Type) bool { return false }

func (structEncoding) Owns(reflect.Type) bool { return false }

// countingEncoding is structEncoding counting the values it encodes.
type countingEncoding struct {
	structEncoding
	encoded *atomic.Int64
}

func (c countingEncoding) Encode(v any, budget filtereval.Budget) any {
	c.encoded.Add(1)
	return c.structEncoding.Encode(v, budget)
}

// TestEvaluate_StructRootEncodedOnce pins that one evaluation reads a struct at the top of the data in its stored form once,
// however many predicates read paths into it, with the results unchanged.
func TestEvaluate_StructRootEncodedOnce(t *testing.T) {
	t.Parallel()

	type inner struct {
		Tags []string `bson:"tags"`
	}

	type record struct {
		Name   string `bson:"name"`
		Age    int    `bson:"age"`
		Status int    `bson:"status"`
		Inner  inner  `bson:"inner"`
	}

	rec := record{Name: "John", Age: 30, Status: 1, Inner: inner{Tags: []string{"red", "green"}}}
	data := map[string]any{"v": rec, "p": &rec}

	for _, tc := range []struct {
		expr string
		want bool
	}{
		{`v.name == "John" && v.age > 18 && v.status == 1 && p.inner.tags == "red"`, true},
		{`v.name == "John" && v.age > 40`, false},
		{`has(v.inner.tags) && v.inner.tags.size() == 2 && !(p.name in ["Bob"])`, true},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			var encoded atomic.Int64

			eval, err := filtereval.New(filtertest.MustPolicy(t, filter.WithAnyField(true)), filtereval.WithProfile(mongo.DefaultSchema()), filtereval.WithValueEncoding(countingEncoding{encoded: &encoded}))
			require.NoError(t, err)
			node := filtertest.MustParseFilter(t, tc.expr)

			for range 2 {
				encoded.Store(0)

				got, err := eval.Evaluate(node, data)
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
				require.LessOrEqual(t, encoded.Load(), int64(2), "each root struct is encoded at most once per evaluation")
			}
		})
	}
}
