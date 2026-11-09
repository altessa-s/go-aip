// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package keyset

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-atlas-core/types/optional"
)

// TestValidateFilter_RejectsDangerousOperators is the regression guard:
// every operator in [dangerousFilterOperators] must be blocked at any
// depth of the filter — including nested inside $expr, $or, $and, and
// $nor arrays.
func TestValidateFilter_RejectsDangerousOperators(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		filter bson.M
		op     string
	}{
		{
			name:   "top-level $where",
			filter: bson.M{"$where": "this.x == this.y"},
			op:     "$where",
		},
		{
			name:   "top-level $function",
			filter: bson.M{"$function": bson.M{"body": "function() {}", "lang": "js"}},
			op:     "$function",
		},
		{
			name:   "top-level $accumulator",
			filter: bson.M{"$accumulator": bson.M{}},
			op:     "$accumulator",
		},
		{
			name: "nested in $expr via bson.M",
			filter: bson.M{
				"$expr": bson.M{"$function": bson.M{"body": "function() {}", "lang": "js"}},
			},
			op: "$function",
		},
		{
			name: "nested in $or via []bson.M",
			filter: bson.M{
				"$or": []bson.M{
					{"a": 1},
					{"$where": "true"},
				},
			},
			op: "$where",
		},
		{
			name: "nested in $or via []any",
			filter: bson.M{
				"$or": []any{
					bson.M{"a": 1},
					bson.M{"$where": "true"},
				},
			},
			op: "$where",
		},
		{
			name: "nested in $and via bson.A",
			filter: bson.M{
				"$and": bson.A{
					bson.M{"a": 1},
					bson.M{"$accumulator": bson.M{}},
				},
			},
			op: "$accumulator",
		},
		{
			name: "deeply nested via bson.D",
			filter: bson.M{
				"$expr": bson.D{{Key: "$function", Value: bson.M{}}},
			},
			op: "$function",
		},
		{
			name: "typed array of bson.D",
			filter: bson.M{
				"$or": []bson.D{{{Key: "$where", Value: "true"}}},
			},
			op: "$where",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateFilter(t.Context(), tc.filter, nil)
			require.ErrorIs(t, err, ErrDangerousOperator,
				"validateFilter must surface ErrDangerousOperator")
			require.Contains(t, err.Error(), tc.op,
				"error must name the offending operator so logs locate the attacker payload")
		})
	}
}

// TestValidateFilter_AllowsLegitimateOperators confirms the deny-list is
// narrow: common comparison / logical / projection operators must pass
// untouched. Blocking them would break every real query.
func TestValidateFilter_AllowsLegitimateOperators(t *testing.T) {
	t.Parallel()

	cases := []bson.M{
		{"_id": "abc"},
		{"$or": []bson.M{{"a": 1}, {"b": 2}}},
		{"age": bson.M{"$gt": 18, "$lt": 65}},
		{"tags": bson.M{"$in": []string{"x", "y"}}},
		{"$and": bson.A{bson.M{"x": 1}, bson.M{"y": 2}}},
		{"$expr": bson.M{"$eq": []string{"$field1", "$field2"}}}, // $expr without nested $function — safe
		{"name": bson.M{"$regex": "^a", "$options": "i"}},        // regex is a perf hazard, not RCE; allowed
		{"score": bson.M{"$exists": true, "$ne": nil}},
	}

	for i, filter := range cases {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			t.Parallel()
			require.NoError(t, ValidateFilter(t.Context(), filter, nil),
				"filter %v must pass — only RCE-grade operators are blocked", filter)
		})
	}
}

// TestValidateFilter_EmptyAndNil pins the edge cases.
func TestValidateFilter_EmptyAndNil(t *testing.T) {
	t.Parallel()
	require.NoError(t, ValidateFilter(t.Context(), nil, nil))
	require.NoError(t, ValidateFilter(t.Context(), bson.M{}, nil))
}

// kind is a named string without its own BSON marshaling: a leaf.
type kind string

// docMarshaler is a type that marshals itself to a whole document.
type docMarshaler struct{}

func (docMarshaler) MarshalBSON() ([]byte, error) {
	return bson.Marshal(bson.D{{Key: "$where", Value: "1"}})
}

// rawOf marshals doc.
func rawOf(tb testing.TB, doc any) bson.Raw {
	tb.Helper()

	raw, err := bson.Marshal(doc)
	require.NoError(tb, err)

	return raw
}

// rawValueOf marshals v as a value.
func rawValueOf(tb testing.TB, v any) bson.RawValue {
	tb.Helper()

	typ, data, err := bson.MarshalValue(v)
	require.NoError(tb, err)

	return bson.RawValue{Type: typ, Value: data}
}

// TestValidateFilterAdmitsTypes pins every type kind the walk admits, as a whole filter value and inside a document.
func TestValidateFilterAdmitsTypes(t *testing.T) {
	t.Parallel()

	oid := bson.NewObjectID()
	d128, err := bson.ParseDecimal128("1.5")
	require.NoError(t, err)

	for name, value := range map[string]any{
		"nil":              nil,
		"bool":             true,
		"int":              1,
		"int8":             int8(1),
		"int16":            int16(1),
		"int32":            int32(1),
		"int64":            int64(1),
		"uint":             uint(1),
		"uint8":            uint8(1),
		"uint16":           uint16(1),
		"uint32":           uint32(1),
		"uint64":           uint64(1),
		"float32":          float32(1),
		"float64":          1.5,
		"string":           "s",
		"named string":     kind("k"),
		"duration":         time.Second,
		"bytes":            []byte("b"),
		"time":             time.Now(),
		"datetime":         bson.NewDateTimeFromTime(time.Now()),
		"timestamp":        bson.Timestamp{T: 1, I: 1},
		"object id":        oid,
		"decimal128":       d128,
		"regex":            bson.Regex{Pattern: "^a", Options: "i"},
		"binary":           bson.Binary{Subtype: 0, Data: []byte{1}},
		"null":             bson.Null{},
		"undefined":        bson.Undefined{},
		"min key":          bson.MinKey{},
		"max key":          bson.MaxKey{},
		"decimal":          decimal.RequireFromString("1.5"),
		"null decimal":     decimal.NullDecimal{},
		"optional":         optional.Some(1),
		"absent optional":  optional.None[string](),
		"optional time":    optional.Some(time.Now()),
		"nested optional":  optional.Some(optional.Some("s")),
		"bson.D":           bson.D{{Key: "a", Value: 1}},
		"bson.E":           bson.E{Key: "a", Value: 1},
		"bson.M":           bson.M{"a": 1},
		"map[string]any":   map[string]any{"a": 1},
		"bson.A":           bson.A{1, "a"},
		"[]any":            []any{1, "a"},
		"[]bson.D":         []bson.D{{{Key: "a", Value: 1}}},
		"[]bson.M":         []bson.M{{"a": 1}},
		"[]string":         []string{"a"},
		"[]int64":          []int64{1},
		"[]time.Time":      []time.Time{time.Now()},
		"[][]any":          [][]any{{1}},
		"[]bson.A":         []bson.A{{1}},
		"nil slice":        []string(nil),
		"bson.Raw":         rawOf(t, bson.D{{Key: "a", Value: bson.A{1, bson.D{{Key: "b", Value: "c"}}}}}),
		"[]bson.Raw":       []bson.Raw{rawOf(t, bson.D{{Key: "a", Value: 1}})},
		"bson.RawArray":    bson.RawArray(rawOf(t, bson.D{{Key: "0", Value: 1}})),
		"raw scalar":       rawValueOf(t, "s"),
		"raw document":     rawValueOf(t, bson.D{{Key: "a", Value: 1}}),
		"raw regex":        rawValueOf(t, bson.Regex{Pattern: "a", Options: ""}),
		"raw binary":       rawValueOf(t, bson.Binary{Data: []byte{1, 2}}),
		"raw decimal":      rawValueOf(t, d128),
		"raw of all types": rawOf(t, bson.D{{Key: "n", Value: nil}, {Key: "t", Value: time.Now()}, {Key: "o", Value: oid}, {Key: "f", Value: 1.5}}),
	} {
		require.NoError(t, ValidateFilter(t.Context(), value, nil), name)
		require.NoError(t, ValidateFilter(t.Context(), bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "x", Value: value}}}}}, nil), name)
	}
}

// TestValidateFilterRejectsTypes pins the type kinds the walk refuses, at the top and nested in every container kind, with
// ErrValueType: structs, pointers, other maps, named slices, arrays, marshalers, channels, functions and complex numbers, and
// an optional.Optional of anything but a leaf.
func TestValidateFilterRejectsTypes(t *testing.T) {
	t.Parallel()

	type doc struct{ A int }

	type tags []string

	type namedMap map[string]any

	s := "s"
	raw := rawOf(t, bson.D{{Key: "a", Value: 1}})

	for name, value := range map[string]any{
		"struct":                 doc{A: 1},
		"empty struct":           struct{}{},
		"pointer to struct":      &doc{},
		"nil pointer":            (*doc)(nil),
		"pointer to string":      &s,
		"pointer to document":    &bson.D{},
		"map of strings":         map[string]string{"a": "b"},
		"map of int keys":        map[int]any{1: 1},
		"named map":              namedMap{"a": 1},
		"named slice":            tags{"a"},
		"slice of pointers":      []*int{nil},
		"slice of structs":       []doc{{}},
		"array":                  [2]int{1, 2},
		"channel":                make(chan int),
		"function":               func() {},
		"complex":                complex(1, 2),
		"uintptr":                uintptr(1),
		"value marshaler":        whereScope("1"),
		"marshaler":              docMarshaler{},
		"error":                  errors.New("e"),
		"bson.JavaScript":        bson.JavaScript("1"),
		"bson.CodeWithScope":     bson.CodeWithScope{Code: "1"},
		"bson.Symbol":            bson.Symbol("s"),
		"bson.DBPointer":         bson.DBPointer{DB: "d"},
		"optional document":      optional.Some(bson.M{"a": 1}),
		"optional raw":           optional.Some(raw),
		"optional struct":        optional.Some(doc{}),
		"optional pointer":       optional.Some(&s),
		"pointer to optional":    new(optional.Some(1)),
		"slice of optional docs": []optional.Optional[bson.M]{optional.Some(bson.M{})},
		"unregistered date":      civilDate{Year: 2026},
		"raw javascript":         rawValueOf(t, bson.JavaScript("1")),
		"raw symbol":             rawValueOf(t, bson.Symbol("s")),
		"raw code with scope":    rawValueOf(t, bson.CodeWithScope{Code: "1", Scope: bson.D{}}),
		"raw javascript element": rawOf(t, bson.D{{Key: "a", Value: bson.A{bson.JavaScript("1")}}}),
		"zero raw value":         bson.RawValue{},
	} {
		require.ErrorIs(t, ValidateFilter(t.Context(), value, nil), ErrValueType, name)

		for container, filter := range map[string]any{
			"bson.D":   bson.D{{Key: "x", Value: value}},
			"bson.E":   bson.E{Key: "x", Value: value},
			"bson.M":   bson.M{"x": value},
			"map":      map[string]any{"x": value},
			"bson.A":   bson.A{value},
			"[]any":    []any{value},
			"[]bson.D": []bson.D{{{Key: "x", Value: value}}},
			"[]bson.M": []bson.M{{"x": value}},
			"[][]any":  [][]any{{value}},
		} {
			require.ErrorIs(t, ValidateFilter(t.Context(), filter, nil), ErrValueType, "%s in %s", name, container)
		}
	}
}

// civilDate is a calendar-date type a registry may encode as a string.
type civilDate struct {
	Year  int
	Month time.Month
	Day   int
}

// TestValidateFilterAdmitsRegistryLeaves checks a type admitted for a registry is a leaf for that registry only, alone and as
// the value of an optional.Optional.
func TestValidateFilterAdmitsRegistryLeaves(t *testing.T) {
	t.Parallel()

	reg, other := bson.NewRegistry(), bson.NewRegistry()
	date := civilDate{Year: 2026, Month: time.November, Day: 9}

	require.ErrorIs(t, ValidateFilter(t.Context(), date, reg), ErrValueType, "not admitted yet")

	AdmitLeafType(reg, reflect.TypeFor[civilDate]())
	AdmitLeafType(nil, reflect.TypeFor[civilDate]())
	AdmitLeafType(reg, nil)

	for name, value := range map[string]any{
		"date":     date,
		"optional": optional.Some(date),
		"list":     []civilDate{date},
		"nested":   bson.D{{Key: "d", Value: bson.D{{Key: "$gte", Value: date}}}},
	} {
		require.NoError(t, ValidateFilter(t.Context(), value, reg), name)
		require.ErrorIs(t, ValidateFilter(t.Context(), value, other), ErrValueType, name)
		require.ErrorIs(t, ValidateFilter(t.Context(), value, nil), ErrValueType, name)
	}

	require.ErrorIs(t, ValidateFilter(t.Context(), &date, reg), ErrValueType, "a pointer stays refused")
}

// TestValidateFilterShapes checks the deny-list is found in raw BSON at every depth — a raw document, a raw array, a raw value,
// raw BSON inside a list or a slice of raw documents — and that a nesting beyond the limit fails closed.
func TestValidateFilterShapes(t *testing.T) {
	t.Parallel()

	where := bson.D{{Key: "$where", Value: "1"}}
	raw := rawOf(t, bson.D{{Key: "a", Value: bson.A{bson.D{{Key: "b", Value: where}}}}})

	for name, filter := range map[string]any{
		"raw document":         raw,
		"raw value":            rawValueOf(t, bson.A{where}),
		"raw array":            bson.RawArray(rawOf(t, bson.D{{Key: "0", Value: where}})),
		"slice of raw":         []bson.Raw{rawOf(t, bson.D{{Key: "ok", Value: 1}}), raw},
		"raw in a list":        bson.A{bson.D{{Key: "x", Value: raw}}},
		"raw value in a slice": []bson.RawValue{rawValueOf(t, where)},
		"element":              bson.E{Key: "$where", Value: 1},
	} {
		require.ErrorIs(t, ValidateFilter(t.Context(), filter, nil), ErrDangerousOperator, name)
	}

	deep := any("x")
	for range maxFilterNesting + 1 {
		deep = []any{deep}
	}

	require.ErrorIs(t, ValidateFilter(t.Context(), deep, nil), errFilterTooDeep)

	deepRaw := bson.D{{Key: "x", Value: 1}}
	for range maxFilterNesting + 1 {
		deepRaw = bson.D{{Key: "x", Value: deepRaw}}
	}

	require.ErrorIs(t, validateRawFilterDocument(rawOf(t, deepRaw)), errFilterTooDeep)
}

// TestValidateFilterRejectsMalformedRaw checks raw BSON that is not well formed fails instead of being read past its bounds.
func TestValidateFilterRejectsMalformedRaw(t *testing.T) {
	t.Parallel()

	for name, value := range map[string]any{
		"short":                   bson.Raw{1, 2},
		"length mismatch":         bson.Raw{6, 0, 0, 0, 0},
		"no terminator":           bson.Raw{5, 0, 0, 0, 1},
		"unterminated key":        bson.Raw{7, 0, 0, 0, 0x10, 'a', 0},
		"value past the end":      bson.Raw{9, 0, 0, 0, 0x10, 'a', 0, 1, 0},
		"negative string length":  bson.Raw{15, 0, 0, 0, 0x02, 'a', 0, 0xff, 0xff, 0xff, 0xff, 'x', 0, 0, 0},
		"huge binary length":      bson.Raw{16, 0, 0, 0, 0x05, 'a', 0, 0xff, 0xff, 0xff, 0x7f, 0, 'x', 0, 0, 0},
		"unterminated string":     bson.Raw{14, 0, 0, 0, 0x02, 'a', 0, 2, 0, 0, 0, 'x', 'y', 0},
		"nested length too small": bson.Raw{12, 0, 0, 0, 0x03, 'a', 0, 1, 0, 0, 0, 0},
		"unterminated regex":      bson.Raw{9, 0, 0, 0, 0x0b, 'a', 0, 'x', 0},
		"raw value short scalar":  bson.RawValue{Type: bson.TypeInt64, Value: []byte{1}},
		"raw value long scalar":   bson.RawValue{Type: bson.TypeInt32, Value: []byte{1, 0, 0, 0, 0}},
	} {
		require.ErrorIs(t, ValidateFilter(t.Context(), value, nil), errMalformedRaw, name)
	}
}

// TestValidateFilterRejectsCycles checks a value that holds itself through a list or a map fails at the nesting limit instead of
// recursing without bound.
func TestValidateFilterRejectsCycles(t *testing.T) {
	t.Parallel()

	arr := bson.A{nil}
	arr[0] = arr

	list := []any{nil}
	list[0] = list

	doc := bson.M{}
	doc["self"] = doc

	m := map[string]any{}
	m["self"] = bson.A{m}

	d := bson.D{{Key: "self"}}
	d[0].Value = d

	for name, value := range map[string]any{"bson.A": arr, "[]any": list, "bson.M": doc, "map": m, "bson.D": d} {
		start := time.Now()

		require.ErrorIs(t, ValidateFilter(t.Context(), bson.D{{Key: "x", Value: value}}, nil), errFilterTooDeep, name)
		requireQuick(t, time.Since(start), fastFail, name)
	}
}
