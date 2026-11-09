// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func BenchmarkEncodedStruct(b *testing.B) {
	eval, err := evaluator.New(filtertest.MustPolicy(b, filter.WithAllowedFields("doc.active")), evaluator.WithProfile(mongo.DefaultSchema()),
		evaluator.WithValueEncoding(mongo.ValueEncoding()))
	require.NoError(b, err)
	prepared, err := eval.Prepare(&filter.IdentNode{Name: "doc.active"})
	require.NoError(b, err)

	data := map[string]any{"doc": struct {
		Active bool `bson:"active"`
	}{Active: true}}

	b.ReportAllocs()

	for b.Loop() {
		_, _ = prepared.Evaluate(data)
	}
}
