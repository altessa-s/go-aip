// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

// The evaluator cases of this module run filter/evaluator with the Schema as its profile, beside the translator cases, so the
// storage rules of the translator and of the evaluator profile are pinned together.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

// mustMongoSchema is mongo.NewSchema that fails tb on an error.
func mustMongoSchema(tb testing.TB, opts ...mongo.SchemaOption) *mongo.Schema {
	tb.Helper()

	schema, err := mongo.NewSchema(opts...)
	require.NoError(tb, err)

	return schema
}

// mustSchemaEvaluator is an evaluator over any field and the policy options given that applies schema.
func mustSchemaEvaluator(tb testing.TB, schema *mongo.Schema, policyOpts ...filter.PolicyOption) *filtereval.Evaluator {
	tb.Helper()

	policy, err := filter.NewPolicy(append([]filter.PolicyOption{filter.WithAnyField(true)}, policyOpts...)...)
	require.NoError(tb, err)

	eval, err := filtereval.New(policy, filtereval.WithProfile(schema))
	require.NoError(tb, err)

	return eval
}
