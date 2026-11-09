// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"bytes"
	"math/rand/v2"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

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

// TestApplicationRegistry_NestedEncodedOnce: review finding R9-F1 — a value encoded through an application registry was decoded
// and only its outer document converted to its stored form; the values inside kept their BSON types, and the evaluator read each
// of them through the registry again, so an encoder that is not idempotent applied twice to every nested value (a date encoded
// one day later read two days later). The values inside an encoded value are read as the registry stored them, at every depth:
// a struct field, a nested document, an array element and a document in an array.
func TestApplicationRegistry_NestedEncodedOnce(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := bson.NewDateTimeFromTime(day)
	id := bson.ObjectID{11: 1}
	record := shiftedRecord{
		At:    at,
		Inner: shiftedInner{At: at, ID: id},
		Deep:  bson.D{{Key: "doc", Value: bson.D{{Key: "at", Value: at}}}},
		List:  []bson.DateTime{at},
		Docs:  []shiftedInner{{At: at, ID: id}},
		IDs:   []bson.ObjectID{id},
	}
	encoding := mongo.ValueEncoding(mongo.WithValueEncodingRegistry(shiftingRegistry()))

	eval, err := newEvaluator(filtereval.WithValueEncoding(encoding), filtereval.WithProfile(mongo.DefaultSchema()))
	require.NoError(t, err)

	const (
		once  = `timestamp("2026-01-02T00:00:00Z")`
		twice = `timestamp("2026-01-03T00:00:00Z")`
	)

	for _, path := range []string{"v.at", "v.inner.at", "v.deep.doc.at", "v.list", "v.docs.at"} {
		for literal, want := range map[string]bool{once: true, twice: false} {
			expr := path + " == " + literal

			got, err := eval.Evaluate(filtertest.MustParseFilter(t, expr), map[string]any{"v": record})
			require.NoError(t, err, expr)
			require.Equal(t, want, got, expr)
		}
	}

	stored, ok := encoding.Encode(record, openBudget{}).(map[string]any)
	require.True(t, ok)

	shifted := id
	shifted[len(shifted)-1]++
	read := func(v any) any { return filtereval.StoredValue(v, encoding, openBudget{}) }

	inner, ok := read(stored["inner"]).(map[string]any)
	require.True(t, ok)
	require.Equal(t, shifted, read(inner["id"]), "a struct field")
	require.Equal(t, shifted, read(read(inner["id"])), "a stored form read again")

	ids, ok := read(stored["ids"]).([]any)
	require.True(t, ok)
	require.Equal(t, shifted, read(ids[0]), "an array element")

	docs, ok := read(stored["docs"]).([]any)
	require.True(t, ok)
	doc, ok := read(docs[0]).(map[string]any)
	require.True(t, ok)
	require.Equal(t, shifted, read(doc["id"]), "a document in an array")
	when, ok := read(doc["at"]).(time.Time)
	require.True(t, ok)
	require.True(t, when.Equal(day.Add(24*time.Hour)), "%v", when)
}

// TestApplicationRegistry_StoredFormNeverReencoded: review finding R10-F1 — the value of a BSON type the driver keeps (an
// ObjectID) that the registry had encoded was read through the registry again wherever it was read a second time: an ObjectID
// reached through a list of documents was collected by the path resolution and encoded again by the array comparison, and
// `v.docs.id == v.ids` was false although the arrays MongoDB stores are equal. Such a value is now its own stored form, which
// Stored returns as it is, on every path: path resolution, list collection, array comparison and ordering, in, size and the
// membership of an array field.
func TestApplicationRegistry_StoredFormNeverReencoded(t *testing.T) {
	t.Parallel()

	at := bson.NewDateTimeFromTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	id := bson.ObjectID{11: 1}
	shifted := id
	shifted[len(shifted)-1]++

	record := shiftedRecord{
		Inner: shiftedInner{At: at, ID: id},
		List:  []bson.DateTime{at},
		Docs:  []shiftedInner{{At: at, ID: id}},
		IDs:   []bson.ObjectID{id},
		Other: []bson.ObjectID{shifted}, // stored as id encoded twice
	}
	encoding := mongo.ValueEncoding(mongo.WithValueEncodingRegistry(shiftingRegistry()))

	for _, c := range storedFormCases() {
		var opts []any
		if c.arrayFields {
			opts = append(opts, filter.WithArrayFields("v.ids", "v.docs.id", "v.other"))
		}

		eval, err := newEvaluator(append(opts, filtereval.WithValueEncoding(encoding), filtereval.WithProfile(mongo.DefaultSchema()))...)
		require.NoError(t, err)

		got, err := eval.Evaluate(filtertest.MustParseFilter(t, c.expr), map[string]any{"v": record})
		require.NoError(t, err, c.expr)
		require.Equal(t, c.want, got, c.expr)
	}
}

// storedFormCase is a filter of [TestApplicationRegistry_StoredFormNeverReencoded] and whether MongoDB selects its record; with
// arrayFields the arrays are declared by filter.WithArrayFields, so a scalar compared with one matches when the array holds it.
type storedFormCase struct {
	expr        string
	arrayFields bool
	want        bool
}

// storedFormCases are the filters of [TestApplicationRegistry_StoredFormNeverReencoded].
func storedFormCases() []storedFormCase {
	return []storedFormCase{
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
	}
}

// TestApplicationRegistry_StoredFormIsPublic: review findings R10-A1 and R10-A2 — the stored form of a value the registry encoded
// held a private wrapper around the values of the BSON types the driver keeps, which leaked through Lookup, StoredValue and Encode,
// failed `in` in the default evaluator, and encoded as an empty document when Stored passed a map[string]any holding one through
// the registry again. The stored form holds the driver's own values, and Stored leaves a base form unhandled.
func TestApplicationRegistry_StoredFormIsPublic(t *testing.T) {
	t.Parallel()

	id := bson.ObjectID{11: 1}
	shifted := id
	shifted[len(shifted)-1]++

	record := shiftedRecord{Inner: shiftedInner{ID: id}, IDs: []bson.ObjectID{id}}
	data := map[string]any{"id": shifted, "v": record}
	encoding := mongo.ValueEncoding(mongo.WithValueEncodingRegistry(shiftingRegistry()))

	eval, err := newEvaluator(filtereval.WithValueEncoding(encoding), filter.WithArrayFields("v.ids"))
	require.NoError(t, err)

	got, err := eval.Evaluate(filtertest.MustParseFilter(t, "id in v.ids"), data)
	require.NoError(t, err)
	require.True(t, got, "the default evaluator's in")

	profiled, err := newEvaluator(filtereval.WithValueEncoding(encoding), filtereval.WithProfile(mongo.DefaultSchema()))
	require.NoError(t, err)

	inner, present, err := profiled.Lookup(data, "v.inner")
	require.NoError(t, err)
	require.True(t, present)

	doc, ok := inner.(map[string]any)
	require.True(t, ok)
	require.Equal(t, shifted, doc["id"])
	requireDriverValues(t, inner)

	ids, _, err := profiled.Lookup(data, "v.ids")
	require.NoError(t, err)
	require.Equal(t, []any{shifted}, ids)

	stored := filtereval.StoredValue(record, encoding, nil)
	requireDriverValues(t, stored)
	require.Equal(t, stored, encoding.Encode(record, openBudget{}))

	again, handled := encoding.Stored(stored, openBudget{})
	require.False(t, handled, "a base form is not handled")
	require.Equal(t, stored, again)
}

// requireDriverValues fails unless v, a stored form, holds no value of a type of this module at any depth.
func requireDriverValues(tb testing.TB, v any) {
	tb.Helper()

	switch x := v.(type) {
	case map[string]any:
		for _, value := range x {
			requireDriverValues(tb, value)
		}
	case []any:
		for _, value := range x {
			requireDriverValues(tb, value)
		}
	default:
		if t := reflect.TypeOf(v); t != nil {
			require.NotContains(tb, t.PkgPath(), "go-aip", "a private type in a stored form: %T", v)
		}
	}
}

// TestApplicationRegistry_StoredFormIsFixed checks, for random documents encoded through a registry whose encoders are not
// idempotent, that every value of their stored form reads as itself (Stored(Stored(x)) == Stored(x)) and holds no private type,
// and that the stored form is what the driver writes through the registry and reads back.
func TestApplicationRegistry_StoredFormIsFixed(t *testing.T) {
	t.Parallel()

	registry := shiftingRegistry()
	encoding := mongo.ValueEncoding(mongo.WithValueEncodingRegistry(registry))
	rng := rand.New(rand.NewPCG(10, 1)) //nolint:gosec // a reproducible sequence of test values

	for range 200 {
		doc := randomDocument(rng, 0)

		stored := encoding.Encode(doc, openBudget{})
		requireDriverValues(t, stored)
		requireFixed(t, encoding, stored)

		var buf bytes.Buffer

		enc := bson.NewEncoder(bson.NewDocumentWriter(&buf))
		enc.SetRegistry(registry)
		require.NoError(t, enc.Encode(bson.D{{Key: "v", Value: doc}}))

		raw := buf.Bytes()

		var back bson.D
		require.NoError(t, bson.Unmarshal(raw, &back))

		// The stored document, read without the registry.
		require.Equal(t, deepStored(mongo.ValueEncoding(), back[0].Value), stored)
	}
}

// deepStored returns v in its stored form at every depth, read through enc.
func deepStored(enc filtereval.ValueEncoding, v any) any {
	switch x := filtereval.StoredValue(v, enc, nil).(type) {
	case map[string]any:
		doc := make(map[string]any, len(x))
		for key, value := range x {
			doc[key] = deepStored(enc, value)
		}

		return doc
	case []any:
		list := make([]any, len(x))
		for i, value := range x {
			list[i] = deepStored(enc, value)
		}

		return list
	default:
		return x
	}
}

// requireFixed fails unless every value of the stored form v reads as itself through enc.
func requireFixed(tb testing.TB, enc filtereval.ValueEncoding, v any) {
	tb.Helper()

	require.Equal(tb, v, filtereval.StoredValue(v, enc, openBudget{}))

	switch x := v.(type) {
	case map[string]any:
		for _, value := range x {
			requireFixed(tb, enc, value)
		}
	case []any:
		for _, value := range x {
			requireFixed(tb, enc, value)
		}
	}
}

// randomDocument returns a random document of the types the driver keeps, dates, strings, numbers and nested documents and
// arrays, depth levels down.
func randomDocument(rng *rand.Rand, depth int) bson.D {
	doc := bson.D{}
	for i := range rng.IntN(4) + 1 {
		doc = append(doc, bson.E{Key: string(rune('a' + i)), Value: randomValue(rng, depth+1)})
	}

	return doc
}

// randomValue returns a random value for [randomDocument].
func randomValue(rng *rand.Rand, depth int) any {
	kinds := 8
	if depth > 3 {
		kinds = 6 // no more containers
	}

	switch rng.IntN(kinds) {
	case 0:
		return bson.ObjectID{11: byte(rng.IntN(4))}
	case 1:
		return bson.NewDateTimeFromTime(time.Date(2026, 1, 1+rng.IntN(3), 0, 0, 0, 0, time.UTC))
	case 2:
		return bson.Symbol("s")
	case 3:
		return "text"
	case 4:
		return int32(rng.IntN(3))
	case 5:
		return bson.Binary{Subtype: 0x80, Data: []byte{byte(rng.IntN(3))}}
	case 6:
		return randomDocument(rng, depth)
	default:
		list := bson.A{}
		for range rng.IntN(3) {
			list = append(list, randomValue(rng, depth+1))
		}

		return list
	}
}
