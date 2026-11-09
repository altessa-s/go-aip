// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package mongo_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"

	mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"
)

type shiftedInner struct {
	At bson.DateTime `bson:"at"`
	ID bson.ObjectID `bson:"id"`
}

type shiftedRecord struct {
	At    bson.DateTime   `bson:"at"`
	Inner shiftedInner    `bson:"inner"`
	Deep  bson.D          `bson:"deep"`
	List  []bson.DateTime `bson:"list"`
	Docs  []shiftedInner  `bson:"docs"`
	IDs   []bson.ObjectID `bson:"ids"`
	Other []bson.ObjectID `bson:"other"`
}

// shiftingRegistry returns a registry whose encoders are not idempotent: a bson.DateTime is written one day later, a
// bson.ObjectID with its last byte incremented.
func shiftingRegistry() *bson.Registry {
	registry := bson.NewRegistry()
	registry.RegisterTypeEncoder(reflect.TypeFor[bson.DateTime](), bson.ValueEncoderFunc(
		func(_ bson.EncodeContext, writer bson.ValueWriter, value reflect.Value) error {
			return writer.WriteDateTime(int64(value.Interface().(bson.DateTime)) + int64(24*time.Hour/time.Millisecond))
		}))
	registry.RegisterTypeEncoder(reflect.TypeFor[bson.ObjectID](), bson.ValueEncoderFunc(
		func(_ bson.EncodeContext, writer bson.ValueWriter, value reflect.Value) error {
			id := value.Interface().(bson.ObjectID)
			id[len(id)-1]++

			return writer.WriteObjectID(id)
		}))

	return registry
}

// shiftedFixture returns the record [TestApplicationRegistryLive] stores; its ObjectIDs are equal but for other, which holds the
// value the others are stored as, so it is stored as an ObjectID encoded twice.
func shiftedFixture() shiftedRecord {
	at := bson.NewDateTimeFromTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	id := bson.ObjectID{11: 1}
	shifted := id
	shifted[len(shifted)-1]++

	return shiftedRecord{
		At:    at,
		Inner: shiftedInner{At: at, ID: id},
		Deep:  bson.D{{Key: "doc", Value: bson.D{{Key: "at", Value: at}}}},
		List:  []bson.DateTime{at},
		Docs:  []shiftedInner{{At: at, ID: id}},
		IDs:   []bson.ObjectID{id},
		Other: []bson.ObjectID{shifted},
	}
}

// TestApplicationRegistryLive stores a record through an application registry whose date and ObjectID encoders are not idempotent
// (a date is written one day later, an ObjectID with its last byte incremented) and checks that the evaluator, reading the record
// through the same registry, selects it for exactly the filters the server does, at every depth: the dates of a struct field, a
// nested document, an array and a document in an array are read as the server stores them, encoded once, and so are the ObjectIDs
// an expression compares — an array reached through a list of documents against a direct one, in, ordering, size() and the
// membership of an array field.
func TestApplicationRegistryLive(t *testing.T) {
	t.Parallel()

	registry := shiftingRegistry()
	record := shiftedFixture()

	coll := newCollection(t, "registry").Database().Collection(t.Name(), mongooptions.Collection().SetRegistry(registry))
	_, err := coll.InsertOne(t.Context(), bson.D{{Key: "v", Value: record}})
	require.NoError(t, err)

	policy := filtertest.MustPolicy(t, filter.WithAnyField(true))
	eval, err := evaluator.New(policy, evaluator.WithProfile(mongo.DefaultSchema()),
		evaluator.WithValueEncoding(mongo.ValueEncoding(mongo.WithValueEncodingRegistry(registry))))
	require.NoError(t, err)

	tr, err := mongo.NewFilterTranslator(policy)
	require.NoError(t, err)

	selected := 0

	for _, path := range []string{"v.at", "v.inner.at", "v.deep.doc.at", "v.list", "v.docs.at"} {
		for _, day := range []string{"2026-01-01", "2026-01-02", "2026-01-03"} {
			expr := path + ` == timestamp("` + day + `T00:00:00Z")`
			node := filtertest.MustParseFilter(t, expr)

			query, err := tr.TranslateD(node)
			require.NoError(t, err, expr)

			n, err := coll.CountDocuments(t.Context(), query)
			require.NoError(t, err, expr)

			got, err := eval.Evaluate(node, map[string]any{"v": record})
			require.NoError(t, err, expr)
			require.Equal(t, n == 1, got, expr)

			if got {
				selected++
			}
		}
	}

	require.Equal(t, 5, selected, "each date is selected by the day after it, once")

	for _, c := range []struct {
		expr        string
		arrayFields bool
		want        bool
	}{
		{expr: "v.docs.id == v.ids", want: true},
		{expr: "v.ids == v.docs.id", want: true},
		{expr: "v.docs.id != v.ids", want: false},
		{expr: "v.docs.id == v.other", want: false},
		{expr: "v.docs.at == v.list", want: true},
		{expr: "v.ids in [v.docs.id]", want: true},
		{expr: "v.other in [v.docs.id]", want: false},
		{expr: "v.docs.id in [v.other, v.ids]", want: true},
		{expr: "v.docs.id <= v.ids", want: true},
		{expr: "v.docs.id >= v.ids", want: true},
		{expr: "v.docs.id < v.ids", want: false},
		{expr: "v.docs.id.size() == v.ids.size()", want: true},
		{expr: "v.inner.id == v.inner.id", want: true},
		{expr: "v.docs.id == v.inner.id", arrayFields: true, want: true},
		{expr: "v.ids == v.inner.id", arrayFields: true, want: true},
		{expr: "v.other == v.inner.id", arrayFields: true, want: false},
		{expr: "v.inner.id in [v.docs.id, v.other]", arrayFields: true, want: true},
	} {
		rules := []filter.PolicyOption{filter.WithAnyField(true)}
		if c.arrayFields {
			rules = append(rules, filter.WithArrayFields("v.ids", "v.docs.id", "v.other"))
		}

		exprPolicy := filtertest.MustPolicy(t, rules...)
		exprEval, err := evaluator.New(exprPolicy, evaluator.WithProfile(mongo.DefaultSchema()),
			evaluator.WithValueEncoding(mongo.ValueEncoding(mongo.WithValueEncodingRegistry(registry))))
		require.NoError(t, err)

		exprTr, err := mongo.NewFilterTranslator(exprPolicy)
		require.NoError(t, err)

		node := filtertest.MustParseFilter(t, c.expr)

		query, err := exprTr.TranslateD(node)
		require.NoError(t, err, c.expr)

		n, err := coll.CountDocuments(t.Context(), query)
		require.NoError(t, err, c.expr)
		require.Equal(t, c.want, n == 1, "server: %s", c.expr)

		got, err := exprEval.Evaluate(node, map[string]any{"v": record})
		require.NoError(t, err, c.expr)
		require.Equal(t, c.want, got, "evaluator: %s", c.expr)
	}
}
