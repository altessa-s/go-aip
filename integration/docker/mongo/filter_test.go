// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package mongo_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/backend/mongo/mongotest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"

	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
)

// TestFilterConformanceLive runs the filter conformance suite with every translation executed by MongoDB over the conformance
// records: the documents the server selects must be the records the evaluator selects under the MongoDB profile
// (mongo.DefaultSchema, the semantics the translator renders), reading the records through the MongoDB value encoding.
func TestFilterConformanceLive(t *testing.T) {
	t.Parallel()

	coll := newCollection(t, "conformance")
	storeConformanceRecords(t, coll)

	refused := liveErrors(t)
	b := mongotest.FilterConformanceBackend()
	b.Reference = func(policy *filter.Policy, opts ...filter.TranslatorOption) (filtertest.MatchFunc, error) {
		e, err := evaluator.New(policy, evaluator.WithFilterOptions(opts...), evaluator.WithProfile(mongo.DefaultSchema()),
			evaluator.WithValueEncoding(mongo.ValueEncoding()))
		if err != nil {
			return nil, err
		}

		return e.Evaluate, nil
	}
	b.Match = func(policy *filter.Policy, opts ...filter.TranslatorOption) (filtertest.MatchFunc, error) {
		tr, err := mongo.NewFilterTranslator(policy, mongo.WithFilterOptions(opts...))
		if err != nil {
			return nil, err
		}

		return selectedBy(t, coll, tr.TranslateD, refused), nil
	}
	filtertest.RunConformance(t, b)
}

// storeConformanceRecords stores filtertest.ConformanceRecords as documents of coll, through the backend/mongo registry the
// client is configured with (a decimal is a Decimal128).
func storeConformanceRecords(t *testing.T, coll *drivermongo.Collection) {
	t.Helper()

	records := filtertest.ConformanceRecords()

	docs := make([]any, len(records))
	for i, r := range records {
		docs[i] = r
	}

	_, err := coll.InsertMany(t.Context(), docs)
	require.NoError(t, err)
}

// liveErrors collects the queries the server refused while the suite ran. A refused query selects no record, which the suite
// counts as agreeing with a reference that selects none, so the test fails on every refusal once its cases are done.
func liveErrors(t *testing.T) *sync.Map {
	t.Helper()

	var refused sync.Map

	t.Cleanup(func() {
		refused.Range(func(query, err any) bool {
			t.Errorf("the server refused %s: %v", query, err)

			return true
		})
	})

	return &refused
}

// selectedBy returns a filtertest.MatchFunc reporting whether the server selects a record: it translates each filter once, runs
// it as a find on coll and answers for every record of that filter from the selected ids.
func selectedBy(t *testing.T, coll *drivermongo.Collection, translate func(filter.Node) (bson.D, error), refused *sync.Map) filtertest.MatchFunc {
	var (
		mu       sync.Mutex
		node     filter.Node
		selected map[string]bool
		failure  error
	)

	return func(n filter.Node, record map[string]any) (bool, error) {
		mu.Lock()
		defer mu.Unlock()

		if selected == nil || n != node {
			node, failure = n, nil
			selected = map[string]bool{}

			query, err := translate(n)
			if err == nil {
				err = find(t, coll, query, selected)
				if err != nil {
					ext, _ := bson.MarshalExtJSON(query, false, false)
					refused.Store(string(ext), err)
				}
			}

			failure = err
		}

		if failure != nil {
			return false, failure
		}

		id, _ := record["id"].(string)

		return selected[id], nil
	}
}

// find adds the id of every document of coll query selects to selected.
func find(t *testing.T, coll *drivermongo.Collection, query bson.D, selected map[string]bool) error {
	if query == nil {
		query = bson.D{}
	}

	cur, err := coll.Find(t.Context(), query)
	if err != nil {
		return err
	}

	var docs []struct {
		ID string `bson:"id"`
	}

	if err := cur.All(t.Context(), &docs); err != nil {
		return err
	}

	for _, d := range docs {
		selected[d.ID] = true
	}

	return nil
}
