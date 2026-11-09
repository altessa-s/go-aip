// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-atlas-core/types/optional"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

// TestEvaluatorWithoutValueEncoding pins the evaluator without a value encoding: it reads pointers, named types, typed slices and
// maps with string keys by their kind and an optional.Optional as its value or null, while a struct, whose stored form only an
// encoding defines, is opaque; with the MongoDB encoding the struct is the document the driver stores.
func TestEvaluatorWithoutValueEncoding(t *testing.T) {
	t.Parallel()

	type label struct {
		Kind string `bson:"kind"`
	}

	type name string

	kind := "tool"
	data := map[string]any{
		"ptr":      &kind,
		"named":    name("tool"),
		"list":     []string{"a", "tool"},
		"doc":      map[string]string{"kind": "tool"},
		"opt":      optional.Some("tool"),
		"none":     optional.None[string](),
		"label":    label{Kind: "tool"},
		"labelPtr": &label{Kind: "tool"},
	}
	cases := map[string]struct {
		expr         string
		plain, mongo bool
	}{
		"pointer":         {expr: `ptr == "tool"`, plain: true, mongo: true},
		"named":           {expr: `named == "tool"`, plain: true, mongo: true},
		"typed list":      {expr: `list == "tool"`, plain: true, mongo: true},
		"typed map":       {expr: `doc.kind == "tool"`, plain: true, mongo: true},
		"optional":        {expr: `opt == "tool"`, plain: true, mongo: true},
		"empty optional":  {expr: `none == null`, plain: true, mongo: true},
		"struct":          {expr: `label.kind == "tool"`, plain: false, mongo: true},
		"struct pointer":  {expr: `labelPtr.kind == "tool"`, plain: false, mongo: true},
		"struct negation": {expr: `label.kind != "tool"`, plain: true, mongo: false},
	}
	opts := []any{withPolicy(filter.WithAnyField(true)), filtereval.WithProfile(mongo.DefaultSchema()), withPolicy(filter.WithArrayFields("list"))}
	plain, err := newEvaluator(opts...)
	require.NoError(t, err)

	encoded, err := newEvaluator(append(opts, filtereval.WithValueEncoding(mongo.ValueEncoding()))...)
	require.NoError(t, err)

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			node := filtertest.MustParseFilter(t, tc.expr)
			got, err := plain.Evaluate(node, data)
			require.NoError(t, err)
			require.Equal(t, tc.plain, got, "without an encoding")

			got, err = encoded.Evaluate(node, data)
			require.NoError(t, err)
			require.Equal(t, tc.mongo, got, "with the MongoDB encoding")
		})
	}
}
