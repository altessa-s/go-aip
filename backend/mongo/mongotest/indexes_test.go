// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongotest_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/backend/mongo/mongotest"
)

// The explain path runs against MongoDB in the live suite of integration/docker; this covers the plan verdict and the plan walk.
func TestPlanIndexed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		plan mongotest.Plan
		want bool
	}{
		{"index scan", mongotest.Plan{Stages: []string{"LIMIT", "FETCH", "IXSCAN"}, Indexes: []string{"idx_name"}}, true},
		{"other index", mongotest.Plan{Stages: []string{"FETCH", "IXSCAN"}, Indexes: []string{"idx_other"}}, false},
		{"blocking sort", mongotest.Plan{Stages: []string{"SORT", "FETCH", "IXSCAN"}, Indexes: []string{"idx_name"}}, false},
		{"collection scan", mongotest.Plan{Stages: []string{"SORT", "COLLSCAN"}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, tc.plan.Indexed("idx_name"), "%s", tc.plan)
		})
	}
}

func TestPlanOf(t *testing.T) {
	t.Parallel()

	classic := bson.D{{Key: "stage", Value: "LIMIT"}, {Key: "inputStage", Value: bson.D{
		{Key: "stage", Value: "FETCH"},
		{Key: "inputStage", Value: bson.D{{Key: "stage", Value: "IXSCAN"}, {Key: "indexName", Value: "idx_name"}}},
	}}}
	or := bson.D{{Key: "stage", Value: "OR"}, {Key: "inputStages", Value: bson.A{
		bson.D{{Key: "stage", Value: "IXSCAN"}, {Key: "indexName", Value: "a"}},
		bson.D{{Key: "stage", Value: "COLLSCAN"}},
	}}}
	sbe := bson.D{{Key: "queryPlan", Value: bson.D{{Key: "stage", Value: "SORT"}, {Key: "inputStage", Value: bson.D{{Key: "stage", Value: "COLLSCAN"}}}}}}

	tests := []struct {
		name string
		plan bson.D
		want mongotest.Plan
	}{
		{"classic", classic, mongotest.Plan{Stages: []string{"LIMIT", "FETCH", "IXSCAN"}, Indexes: []string{"idx_name"}}},
		{"inputStages", or, mongotest.Plan{Stages: []string{"OR", "IXSCAN", "COLLSCAN"}, Indexes: []string{"a"}}},
		{"sbe", sbe, mongotest.Plan{Stages: []string{"SORT", "COLLSCAN"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			typ, data, err := bson.MarshalValue(tc.plan)
			require.NoError(t, err)
			require.Equal(t, tc.want, mongotest.PlanOf(bson.RawValue{Type: typ, Value: data}))
		})
	}

	require.Equal(t, mongotest.Plan{}, mongotest.PlanOf(bson.RawValue{Type: bson.TypeString}), "a scalar is no plan")
	require.Equal(t, "stages [LIMIT] indexes [i]", mongotest.Plan{Stages: []string{"LIMIT"}, Indexes: []string{"i"}}.String())
}
