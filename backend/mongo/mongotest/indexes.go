// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongotest

import (
	"slices"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/altessa-s/go-aip/listing"
)

// Plan is the shape of a query's winning plan: every stage name and the
// names of the indexes its index scans use.
type Plan struct {
	Stages  []string
	Indexes []string
}

// Indexed reports whether the plan scans index and sorts without a blocking
// SORT stage.
func (p Plan) Indexed(index string) bool {
	return slices.Contains(p.Indexes, index) && !slices.Contains(p.Stages, "SORT")
}

// String renders the plan for a failure message.
func (p Plan) String() string {
	return "stages [" + strings.Join(p.Stages, " ") + "] indexes [" + strings.Join(p.Indexes, " ") + "]"
}

// Explain returns the winning plan of a find on coll with filter and sort
// (queryPlanner verbosity, limited to one page).
func Explain(tb testing.TB, coll *mongo.Collection, filter, sort bson.D) Plan {
	tb.Helper()

	if filter == nil {
		filter = bson.D{}
	}

	cmd := bson.D{
		{Key: "explain", Value: bson.D{
			{Key: "find", Value: coll.Name()},
			{Key: "filter", Value: filter},
			{Key: "sort", Value: sort},
			{Key: "limit", Value: int32(listing.DefaultPageSize + 1)},
		}},
		{Key: "verbosity", Value: "queryPlanner"},
	}

	var res bson.Raw
	if err := coll.Database().RunCommand(tb.Context(), cmd).Decode(&res); err != nil {
		tb.Fatalf("explain find %s: %v", coll.Name(), err)
	}

	winning, err := res.LookupErr("queryPlanner", "winningPlan")
	if err != nil {
		tb.Fatalf("explain find %s: no winning plan in %s", coll.Name(), res)
	}

	return PlanOf(winning)
}

// PlanOf returns the shape of a winning plan as explain reports it (queryPlanner.winningPlan): its stage names and the index
// names of its scans, whatever the nesting (classic inputStage/inputStages, SBE queryPlan).
func PlanOf(winning bson.RawValue) Plan {
	var p Plan

	walkPlan(winning, &p)

	return p
}

// walkPlan collects the stages and index names of a plan tree, whatever its
// nesting (classic inputStage/inputStages, SBE queryPlan).
func walkPlan(v bson.RawValue, p *Plan) {
	switch v.Type {
	case bson.TypeEmbeddedDocument:
		doc := v.Document()

		elems, err := doc.Elements()
		if err != nil {
			return
		}

		for _, e := range elems {
			switch e.Key() {
			case "stage":
				if s, ok := e.Value().StringValueOK(); ok {
					p.Stages = append(p.Stages, s)
				}
			case "indexName":
				if s, ok := e.Value().StringValueOK(); ok {
					p.Indexes = append(p.Indexes, s)
				}
			default:
				walkPlan(e.Value(), p)
			}
		}
	case bson.TypeArray:
		values, err := v.Array().Values()
		if err != nil {
			return
		}

		for _, el := range values {
			walkPlan(el, p)
		}
	default:
	}
}

// Sorter is the MongoDB form of a list's orders: the full sort and the
// backing index of each order of its schema. A *mongo.ListEngine is one.
type Sorter interface {
	Schema() *listing.Schema
	Sort(o listing.Order) bson.D
	Index(o listing.Order) string
}

// AssertIndexedSorts asserts that every order the list accepts — each
// declared pattern and its reversal — runs on coll as a scan of the
// pattern's index without a blocking SORT stage, for a find restricted to
// scope only (the store's base filter, its scope keys pinned by equality).
// Call it in a store's integration test after EnsureIndexes; a few documents
// in the collection make the plan representative.
func AssertIndexedSorts(tb testing.TB, coll *mongo.Collection, list Sorter, scope bson.D) {
	tb.Helper()

	for _, o := range list.Schema().Orders() {
		sort, index := list.Sort(o), list.Index(o)
		if p := Explain(tb, coll, scope, sort); !p.Indexed(index) {
			tb.Errorf("order %q (sort %v) does not scan index %q without a blocking sort: %s", o, sort, index, p)
		}
	}
}
