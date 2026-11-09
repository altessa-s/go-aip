// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"encoding/json"
	"math"
	"math/big"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-atlas-core/types/optional"

	"github.com/altessa-s/go-aip/backend/mongo"
)

// openBudget never runs out; closedBudget is exhausted from the start.
type openBudget struct{}

func (openBudget) Charge(int) bool { return true }

type closedBudget struct{}

func (closedBudget) Charge(int) bool { return false }

// valueHook encodes itself as its string with a "!" suffix.
type valueHook struct{ s string }

func (v valueHook) MarshalBSONValue() (byte, []byte, error) {
	typ, data, err := bson.MarshalValue(v.s + "!")
	return byte(typ), data, err
}

// docHook encodes itself, through its pointer, as the document {n: n}.
type docHook struct{ n int32 }

func (d *docHook) MarshalBSON() ([]byte, error) {
	if d == nil {
		return bson.Marshal(bson.D{})
	}

	return bson.Marshal(bson.D{{Key: "n", Value: d.n}})
}

// textKey encodes itself as text, which makes a map keyed by it opaque.
type textKey int

func (k textKey) MarshalText() ([]byte, error) { return []byte(strconv.Itoa(int(k))), nil }

type encoded struct {
	Map      map[string]int
	IntMap   map[int]string
	Strings  []string
	Bytes    []byte
	Decimal  decimal.Decimal
	Vector   bson.Vector
	URL      *url.URL
	Ptr      *int
	NilPtr   *int
	Iface    any
	Hook     valueHook
	NilHook  *valueHook
	Opt      optional.Optional[int]
	NoOpt    optional.Optional[int]
	OptPtr   *optional.Optional[int]
	NilOpt   *optional.Optional[int]
	Array    [2]int
	NilSlice []int
	NilMap   map[string]int
	Bool     bool
	Float    float64
}

func TestValueEncoding_EncodeStruct(t *testing.T) {
	t.Parallel()

	n := 5
	opt := optional.Some(9)
	v := encoded{
		Map:     map[string]int{"a": 1},
		IntMap:  map[int]string{1: "x"},
		Strings: []string{"s"},
		Bytes:   []byte{1, 2},
		Decimal: decimal.RequireFromString("1.25"),
		Vector:  bson.NewVector([]int8{1, 2}),
		URL:     &url.URL{Scheme: "http", User: url.UserPassword("u", "p"), Host: "h"},
		Ptr:     &n,
		Iface:   "iface",
		Hook:    valueHook{"h"},
		Opt:     optional.Some(7),
		OptPtr:  &opt,
		Array:   [2]int{1, 2},
		Bool:    true,
		Float:   1.5,
	}

	got, ok := mongo.ValueEncoding().Encode(v, openBudget{}).(map[string]any)
	require.True(t, ok, "a struct encodes as a document")

	require.Equal(t, bson.D{{Key: "a", Value: int32(1)}}, got["map"])
	require.Equal(t, bson.D{{Key: "1", Value: "x"}}, got["intmap"])
	require.Equal(t, bson.A{"s"}, got["strings"])
	require.Equal(t, bson.Binary{Subtype: 0, Data: []byte{1, 2}}, got["bytes"])
	require.IsType(t, bson.Decimal128{}, got["decimal"])
	require.Equal(t, "1.25", got["decimal"].(bson.Decimal128).String())
	require.IsType(t, bson.Binary{}, got["vector"])
	require.Equal(t, "http://u:p@h", got["url"])
	require.Equal(t, int32(5), got["ptr"])
	require.Nil(t, got["nilptr"])
	require.Equal(t, "iface", got["iface"])
	require.Equal(t, "h!", got["hook"])
	require.Nil(t, got["nilhook"])
	require.Equal(t, int32(7), got["opt"])
	require.Nil(t, got["noopt"])
	require.Equal(t, int32(9), got["optptr"])
	require.Nil(t, got["nilopt"])
	require.Equal(t, bson.A{int32(1), int32(2)}, got["array"])
	require.Nil(t, got["nilslice"])
	require.Nil(t, got["nilmap"])
	require.Equal(t, true, got["bool"])
	require.InDelta(t, 1.5, got["float"], 0)
}

func TestValueEncoding_EncodeHooksAndSpecials(t *testing.T) {
	t.Parallel()

	enc := mongo.ValueEncoding()

	require.Equal(t, map[string]any{"n": int32(4)}, enc.Encode(&docHook{4}, openBudget{}))
	require.Equal(t, "x!", enc.Encode(valueHook{"x"}, openBudget{}))

	vec, ok := enc.Encode(bson.NewVector([]float32{1}), openBudget{}).(bson.Binary)
	require.True(t, ok)
	require.Equal(t, byte(bson.TypeBinaryVector), vec.Subtype)

	bits, err := bson.NewPackedBitVector([]byte{0xf0}, 4)
	require.NoError(t, err)

	_, ok = enc.Encode(bits, openBudget{}).(bson.Binary)
	require.True(t, ok)

	user := url.User("only")
	require.Equal(t, "//only@", enc.Encode(&url.URL{User: user}, openBudget{}))
}

func TestValueEncoding_EncodeReturnsUnencodableUnchanged(t *testing.T) {
	t.Parallel()

	enc := mongo.ValueEncoding()

	textKeyed := map[textKey]int{1: 2}
	require.Equal(t, textKeyed, enc.Encode(textKeyed, openBudget{}), "a key encoding itself as text is opaque")

	huge := decimal.New(1, 2000).Add(decimal.New(1, 0))
	require.Equal(t, huge, enc.Encode(huge, openBudget{}), "a coefficient beyond the bound is refused")

	ch := make(chan int)
	require.Equal(t, ch, enc.Encode(ch, openBudget{}), "a kind the driver refuses is kept")

	floatKeyed := map[float64]int{1: 1}
	require.Equal(t, floatKeyed, enc.Encode(floatKeyed, openBudget{}))

	type cyclic struct{ Next *cyclic }

	c := &cyclic{}
	c.Next = c
	require.Same(t, c, enc.Encode(c, openBudget{}), "a cycle exceeds the pointer and nesting bounds")

	v := encoded{Strings: []string{"a"}}
	require.Equal(t, v, enc.Encode(v, closedBudget{}), "an exhausted budget leaves the value as it is")
}

func TestValueEncoding_EncodeLargeDecimalWithinBound(t *testing.T) {
	t.Parallel()

	// A coefficient a Decimal128 cannot hold as is, but which divides down to one.
	d := decimal.NewFromBigInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(40), nil), -40)
	got := mongo.ValueEncoding().Encode(d, openBudget{})
	dec, ok := got.(decimal.Decimal)
	require.True(t, ok, "%T", got)
	require.True(t, dec.Equal(decimal.NewFromInt(1)))
}

func TestValueEncoding_StoredBSONTypes(t *testing.T) {
	t.Parallel()

	enc := mongo.ValueEncoding()
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	tests := []struct {
		name string
		in   any
		want any
	}{
		{"nil A", bson.A(nil), nil},
		{"A", bson.A{1, "x"}, []any{1, "x"}},
		{"nil M", bson.M(nil), nil},
		{"M", bson.M{"a": 1}, map[string]any{"a": 1}},
		{"nil D", bson.D(nil), nil},
		{"D keeps the first of a repeated key", bson.D{{Key: "a", Value: 1}, {Key: "b", Value: 2}, {Key: "a", Value: 3}}, map[string]any{"a": 1, "b": 2}},
		{"DateTime", bson.NewDateTimeFromTime(ts), ts.Local()},
		{"Null", bson.Null{}, nil},
		{"generic Binary", bson.Binary{Data: []byte{1}}, []byte{1}},
		{"generic Binary without data", bson.Binary{}, []byte{}},
		{"UUID Binary", bson.Binary{Subtype: 4, Data: []byte{1}}, bson.Binary{Subtype: 4, Data: []byte{1}}},
		{"Symbol", bson.Symbol("s"), bson.Symbol("s")},
		{"ObjectID", bson.ObjectID{1}, bson.ObjectID{1}},
		{"MinKey", bson.MinKey{}, bson.MinKey{}},
		{"json.Number", json.Number("42"), int64(42)},
		{"json.Number float", json.Number("1.5"), 1.5},
		{"Optional", optional.Some("o"), "o"},
		{"elements slice", []bson.E{{Key: "k", Value: "v"}}, map[string]any{"k": "v"}},
		{"nil elements slice", []bson.E(nil), nil},
		{"elements array", [1]bson.E{{Key: "k", Value: 1}}, map[string]any{"k": 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, handled := enc.Stored(tc.in, openBudget{})
			require.True(t, handled)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestValueEncoding_StoredDecimal128(t *testing.T) {
	t.Parallel()

	enc := mongo.ValueEncoding()

	d, err := bson.ParseDecimal128("12.50")
	require.NoError(t, err)

	got, handled := enc.Stored(d, openBudget{})
	require.True(t, handled)
	require.True(t, got.(decimal.Decimal).Equal(decimal.RequireFromString("12.5")))

	nan, err := bson.ParseDecimal128("NaN")
	require.NoError(t, err)

	got, _ = enc.Stored(nan, openBudget{})
	require.True(t, math.IsNaN(got.(float64)))

	for in, sign := range map[string]int{"Infinity": 1, "-Infinity": -1} {
		inf, err := bson.ParseDecimal128(in)
		require.NoError(t, err)

		got, _ = enc.Stored(inf, openBudget{})
		require.True(t, math.IsInf(got.(float64), sign), in)
	}
}

func TestValueEncoding_StoredDriverTypes(t *testing.T) {
	t.Parallel()

	raw, err := bson.Marshal(bson.D{{Key: "x", Value: int32(1)}})
	require.NoError(t, err)

	got, handled := mongo.ValueEncoding().Stored(bson.Raw(raw), openBudget{})
	require.True(t, handled)
	require.Equal(t, map[string]any{"x": int32(1)}, got)
}

func TestValueEncoding_StoredBudgetAndUnhandled(t *testing.T) {
	t.Parallel()

	enc := mongo.ValueEncoding()

	d := bson.D{{Key: "a", Value: 1}}
	got, handled := enc.Stored(d, closedBudget{})
	require.True(t, handled)
	require.Equal(t, d, got, "an exhausted budget keeps the document opaque")

	elems := []bson.E{{Key: "a", Value: 1}}
	got, handled = enc.Stored(elems, closedBudget{})
	require.True(t, handled)
	require.Equal(t, elems, got)

	_, handled = enc.Stored("plain", openBudget{})
	require.False(t, handled)
	_, handled = enc.Stored(nil, openBudget{})
	require.False(t, handled)
	_, handled = enc.Stored(struct{}{}, openBudget{})
	require.False(t, handled)
}

func TestValueEncoding_TypeQueries(t *testing.T) {
	t.Parallel()

	enc := mongo.ValueEncoding()

	require.True(t, enc.EncodesItself(reflect.TypeFor[valueHook]()))
	require.True(t, enc.EncodesItself(reflect.TypeFor[*docHook]()))
	require.False(t, enc.EncodesItself(reflect.TypeFor[docHook]()))
	require.False(t, enc.EncodesItself(reflect.TypeFor[string]()))

	require.True(t, enc.Owns(reflect.TypeFor[bson.ObjectID]()))
	require.False(t, enc.Owns(reflect.TypeFor[time.Time]()))

	require.True(t, enc.DocumentElement(reflect.TypeFor[bson.E]()))
	require.False(t, enc.DocumentElement(reflect.TypeFor[string]()))

	s, ok := enc.StringValue(bson.Symbol("sym"))
	require.True(t, ok)
	require.Equal(t, "sym", s)

	_, ok = enc.StringValue("plain")
	require.False(t, ok)

	sub, data, ok := enc.BinaryValue(bson.Binary{Subtype: 5, Data: []byte{9}})
	require.True(t, ok)
	require.Equal(t, byte(5), sub)
	require.Equal(t, []byte{9}, data)

	_, _, ok = enc.BinaryValue([]byte{9})
	require.False(t, ok)
}

func TestValueEncoding_ScanLength(t *testing.T) {
	t.Parallel()

	enc := mongo.ValueEncoding()

	tests := []struct {
		in   any
		want int
		ok   bool
	}{
		{bson.Symbol("abc"), 3, true},
		{bson.JavaScript("f()"), 3, true},
		{bson.Binary{Data: []byte{1, 2}}, 2, true},
		{bson.Regex{Pattern: "ab", Options: "i"}, 3, true},
		{bson.DBPointer{DB: "db"}, 2, true},
		{bson.ObjectID{}, 0, false},
		{"plain", 0, false},
	}
	for _, tc := range tests {
		n, ok := enc.ScanLength(tc.in)
		require.Equal(t, tc.ok, ok, "%T", tc.in)
		require.Equal(t, tc.want, n, "%T", tc.in)
	}
}

func TestApplicationRegistry(t *testing.T) {
	t.Parallel()

	type applicationValue struct{ Text string }

	registry := bson.NewRegistry()
	registry.RegisterTypeEncoder(reflect.TypeFor[applicationValue](), bson.ValueEncoderFunc(
		func(_ bson.EncodeContext, writer bson.ValueWriter, value reflect.Value) error {
			return writer.WriteString("custom:" + value.Interface().(applicationValue).Text)
		}))
	encoding := mongo.ValueEncoding(mongo.WithValueEncodingRegistry(registry))
	value, handled := encoding.Stored(applicationValue{Text: "x"}, openBudget{})
	require.True(t, handled)
	require.Equal(t, "custom:x", value)
}

// expander encodes itself, through an application registry, as an array of n strings of 64 bytes.
type expander struct{ n int }

// countingBudget allows left units and counts the units charged.
type countingBudget struct{ left, charged int }

func (b *countingBudget) Charge(units int) bool {
	if units > b.left {
		b.left = 0
		return false
	}

	b.left -= units
	b.charged += units

	return true
}

func TestApplicationRegistryBoundsExpansion(t *testing.T) {
	t.Parallel()

	const elements = 1 << 20 // 64 MiB of BSON if written in full

	tests := []struct {
		name     string
		limit    int
		budget   int
		maxCalls int
	}{
		{name: "byte limit", limit: 4 << 10, budget: math.MaxInt, maxCalls: 4 << 10 / 64},
		{name: "budget", limit: mongo.DefaultMaxDocumentBytes, budget: 64, maxCalls: 64},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			text := strings.Repeat("x", 64)
			registry := bson.NewRegistry()
			registry.RegisterTypeEncoder(reflect.TypeFor[expander](), bson.ValueEncoderFunc(
				func(_ bson.EncodeContext, writer bson.ValueWriter, value reflect.Value) error {
					array, err := writer.WriteArray()
					if err != nil {
						return err
					}

					for range value.Interface().(expander).n {
						element, err := array.WriteArrayElement()
						if err != nil {
							return err
						}

						calls++

						if err := element.WriteString(text); err != nil {
							return err
						}
					}

					return array.WriteArrayEnd()
				}))

			encoding := mongo.ValueEncoding(mongo.WithValueEncodingRegistry(registry), mongo.WithValueEncodingMaxDocumentBytes(tc.limit))
			in := expander{n: elements}
			budget := &countingBudget{left: tc.budget}
			value, handled := encoding.Stored(in, budget)
			require.True(t, handled)
			require.Equal(t, in, value, "an encoding past the bound leaves the value opaque")
			require.LessOrEqual(t, calls, tc.maxCalls, "the encoder is stopped at the bound")
		})
	}
}
