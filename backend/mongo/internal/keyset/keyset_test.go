// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package keyset

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/altessa-s/go-atlas-core/types/optional"

	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

func TestKeys(t *testing.T) {
	t.Parallel()

	keys, err := sortKeys(bson.D{{Key: "rank", Value: -1}, {Key: "name", Value: int32(1)}}, "")
	require.NoError(t, err)
	require.Equal(t, []sortKey{{"rank", true}, {"name", false}, {"_id", false}}, keys, "_id tie-breaker appended")

	keys, err = sortKeys(bson.D{{Key: "_id", Value: int64(-1)}, {Key: "ignored", Value: 1}}, "")
	require.NoError(t, err)
	require.Equal(t, []sortKey{{"_id", true}}, keys, "nothing breaks a tie after _id")

	keys, err = sortKeys(nil, "")
	require.NoError(t, err)
	require.Equal(t, []sortKey{{"_id", false}}, keys)

	for name, sort := range map[string]bson.D{
		"direction_2":   {{Key: "a", Value: 2}},
		"text_score":    {{Key: "a", Value: bson.D{{Key: "$meta", Value: "textScore"}}}},
		"string_dir":    {{Key: "a", Value: "asc"}},
		"empty_field":   {{Key: "", Value: 1}},
		"operator":      {{Key: "$natural", Value: 1}},
		"repeated_name": {{Key: "a", Value: 1}, {Key: "a", Value: -1}},
	} {
		_, err := sortKeys(sort, "")
		require.ErrorIs(t, err, ErrSort, name)
	}
}

func TestPositionRoundTrip(t *testing.T) {
	t.Parallel()

	keys := []sortKey{{"meta.rank", true}, {"_id", false}}
	doc, err := bson.Marshal(bson.D{{Key: "_id", Value: "x1"}, {Key: "meta", Value: bson.D{{Key: "rank", Value: 7}}}})
	require.NoError(t, err)

	payload, err := encodePosition(doc, keys, time.Millisecond)
	require.NoError(t, err)
	values, err := decodePosition(payload, len(keys))
	require.NoError(t, err)
	require.Equal(t, int32(7), values[0].Int32())
	require.Equal(t, "x1", values[1].StringValue())

	_, err = decodePosition(payload, 3)
	require.ErrorIs(t, err, pagetoken.ErrInvalidToken, "a key count mismatch is a bad token")
	_, err = decodePosition([]byte("garbage"), 2)
	require.ErrorIs(t, err, pagetoken.ErrInvalidToken)

	for name, d := range map[string]bson.D{
		"missing": {{Key: "_id", Value: "x1"}},
		"null":    {{Key: "_id", Value: "x1"}, {Key: "meta", Value: bson.D{{Key: "rank", Value: nil}}}},
		"array":   {{Key: "_id", Value: "x1"}, {Key: "meta", Value: bson.D{{Key: "rank", Value: bson.A{1}}}}},
	} {
		raw, err := bson.Marshal(d)
		require.NoError(t, err)
		_, err = encodePosition(raw, keys, time.Millisecond)
		require.ErrorIs(t, err, ErrMissingKey, name)
	}
}

func TestWithSeek(t *testing.T) {
	t.Parallel()

	v := func(x any) bson.RawValue {
		typ, data, err := bson.MarshalValue(x)
		require.NoError(t, err)

		return bson.RawValue{Type: typ, Value: data}
	}
	keys := []sortKey{{"rank", true}, {"_id", false}}
	got := withSeek(mustFilter(t, bson.D{{Key: "group", Value: "a"}}), keys, []bson.RawValue{v(3), v("id9")})

	want := bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "group", Value: "a"}},
		bson.D{{Key: "$or", Value: bson.A{
			bson.D{{Key: "rank", Value: bson.D{{Key: "$lt", Value: v(3)}}}},
			bson.D{
				{Key: "rank", Value: bson.D{{Key: "$eq", Value: v(3)}}},
				{Key: "_id", Value: bson.D{{Key: "$gt", Value: v("id9")}}},
			},
		}}},
	}}}
	gotJSON, err := bson.MarshalExtJSON(got, true, false)
	require.NoError(t, err)
	wantJSON, err := bson.MarshalExtJSON(want, true, false)
	require.NoError(t, err)
	require.JSONEq(t, string(wantJSON), string(gotJSON))

	noFilter := withSeek(mustFilter(t, nil), keys[1:], []bson.RawValue{v("id9")})
	require.Equal(t, "$or", noFilter[0].Key)
	require.Len(t, noFilter, 1, "an empty filter adds no $and")
}

func mustFilter(tb testing.TB, filter bson.D) bson.Raw {
	tb.Helper()

	raw, err := marshalFilter(filter, nil, nil)
	require.NoError(tb, err)

	return raw
}

func mustBindings(tb testing.TB, filter bson.D, keys []sortKey, subject string) pagetoken.Bindings {
	tb.Helper()
	b, err := newBindings(mustFilter(tb, filter), keys, subject)
	require.NoError(tb, err)

	return b
}

func TestBindings(t *testing.T) {
	t.Parallel()

	keys, err := sortKeys(bson.D{{Key: "rank", Value: -1}}, "")
	require.NoError(t, err)
	a := mustBindings(t, bson.D{{Key: "g", Value: "a"}}, keys, "s")
	b := mustBindings(t, bson.D{{Key: "g", Value: "b"}}, keys, "s")
	require.NotEqual(t, a.Filter, b.Filter)
	require.Equal(t, a.Sort, b.Sort)
	require.Equal(t, "s", a.Subject)

	none := mustBindings(t, nil, keys, "")
	empty := mustBindings(t, bson.D{}, keys, "")
	require.Equal(t, none.Filter, empty.Filter, "nil and empty filters match the same documents")
}

func TestFilterBindsTheExecutedBytes(t *testing.T) {
	t.Parallel()

	keys, err := sortKeys(nil, "")
	require.NoError(t, err)
	// An embedded-document literal is compared by MongoDB in its key order: the executed filter and the binding keep it.
	filter := bson.D{{Key: "meta", Value: bson.D{{Key: "b", Value: 1}, {Key: "a", Value: 2}}}}
	raw := mustFilter(t, filter)
	want, err := bson.Marshal(filter)
	require.NoError(t, err)
	require.Equal(t, bson.Raw(want), raw, "the filter is executed as written")
	elems, err := raw.Lookup("meta").Document().Elements()
	require.NoError(t, err)
	require.Equal(t, "b", elems[0].Key())
	require.Equal(t, "a", elems[1].Key())

	bound := mustBindings(t, filter, keys, "")
	fingerprint, err := bson.Marshal(bson.D{{Key: "f", Value: filter}})
	require.NoError(t, err)
	require.Equal(t, string(fingerprint), bound.Filter, "the binding is the executed bytes")

	reordered := mustBindings(t, bson.D{{Key: "meta", Value: bson.D{{Key: "a", Value: 2}, {Key: "b", Value: 1}}}}, keys, "")
	require.NotEqual(t, bound.Filter, reordered.Filter, "a reordered literal is another filter")
}

func TestFindPageRejectsTokenWithoutCodec(t *testing.T) {
	t.Parallel()

	codec, err := pagetoken.New(bytes.Repeat([]byte{1}, pagetoken.MinKeyLength))
	require.NoError(t, err)
	token, err := codec.Issue([]byte("x"), pagetoken.Bindings{})
	require.NoError(t, err)
	_, err = FindPage[bson.M](t.Context(), nil, nil, Query{After: token})
	require.ErrorIs(t, err, ErrTokensRequired)
	require.ErrorIs(t, err, pagetoken.ErrCodecRequired)
	require.Equal(t, pagetoken.FailureMalformed, pagetoken.Classify(err))
}

func TestBindingsKeepTypes(t *testing.T) {
	t.Parallel()

	keys, err := sortKeys(nil, "")
	require.NoError(t, err)
	a := mustBindings(t, bson.D{{Key: "rank", Value: bson.D{{Key: "$eq", Value: int32(1)}}}}, keys, "")
	b := mustBindings(t, bson.D{{Key: "rank", Value: bson.D{{Key: "$eq",
		Value: bson.D{{Key: "$numberInt", Value: "1"}}}}}}, keys, "")
	require.NotEqual(t, a.Filter, b.Filter, "an int32 and a $numberInt document are different filters")
}

func TestCheckProjection(t *testing.T) {
	t.Parallel()

	keys := []sortKey{{"meta.rank", false}, {"_id", false}}
	for _, p := range []bson.D{
		nil,
		{{Key: "meta.rank", Value: 1}, {Key: "name", Value: bson.D{{Key: "$literal", Value: 0}}}},
		{{Key: "meta", Value: true}},
		{{Key: "secret", Value: 0}},
		{{Key: "meta.rank", Value: int64(1)}, {Key: "_id", Value: 0.0}},
	} {
		require.NoError(t, checkProjection(p, keys), "%v", p)
	}

	for _, p := range []bson.D{
		{{Key: "meta.rank", Value: bson.D{{Key: "$literal", Value: 0}}}},
		{{Key: "meta", Value: "$other"}},
		{{Key: "meta.rank.x", Value: bson.D{{Key: "$slice", Value: 1}}}},
		{{Key: "meta.rank.$", Value: 1}},
		{{Key: "_id", Value: 2}},
	} {
		require.ErrorIs(t, checkProjection(p, keys), ErrProjection, "%v", p)
	}
}

// celsius is a filter value only a custom registry can encode.
type celsius struct{ deg int32 }

func TestFilterUsesTheQueryRegistryAndOptions(t *testing.T) {
	t.Parallel()

	reg := bson.NewRegistry()
	reg.RegisterTypeEncoder(reflect.TypeFor[celsius](), bson.ValueEncoderFunc(
		func(_ bson.EncodeContext, vw bson.ValueWriter, v reflect.Value) error {
			return vw.WriteInt32(v.Interface().(celsius).deg) //nolint:forcetypeassert // registered for celsius only
		}))
	raw, err := marshalFilter(bson.D{{Key: "temp", Value: celsius{deg: 21}}}, reg, nil)
	require.NoError(t, err)
	require.Equal(t, int32(21), raw.Lookup("temp").Int32(), "the custom codec of the query registry encodes the filter")

	type bounds struct {
		Min int64 `bson:"min"`
	}

	raw, err = marshalFilter(bson.D{{Key: "range", Value: bounds{Min: 3}}}, nil, &options.BSONOptions{IntMinSize: true})
	require.NoError(t, err)
	require.Equal(t, bson.TypeInt32, raw.Lookup("range", "min").Type, "the collection's encoder settings apply")
}

func TestConfiguredIdentityStopsSort(t *testing.T) {
	t.Parallel()

	keys, err := sortKeys(bson.D{{Key: "rank", Value: -1}, {Key: "identity.uuid", Value: -1}}, "identity.uuid")
	require.NoError(t, err)
	require.Equal(t, []sortKey{{field: "rank", desc: true}, {field: "identity.uuid", desc: true}}, keys)
	keys, err = sortKeys(nil, "identity.uuid")
	require.NoError(t, err)
	require.Equal(t, []sortKey{{field: "identity.uuid"}}, keys)

	_, err = sortKeys(nil, "$bad")
	require.ErrorIs(t, err, ErrSort)
}

func TestCustomIdentityCursorWithoutMongoID(t *testing.T) {
	t.Parallel()

	keys, err := sortKeys(nil, "identity.sequence")
	require.NoError(t, err)
	document, err := bson.Marshal(bson.D{{Key: "identity", Value: bson.D{{Key: "sequence", Value: int64(42)}}}})
	require.NoError(t, err)
	payload, err := encodePosition(document, keys, time.Millisecond)
	require.NoError(t, err)
	values, err := decodePosition(payload, len(keys))
	require.NoError(t, err)
	require.Equal(t, int64(42), values[0].Int64())
	seek := withSeek(mustFilter(t, nil), keys, values)
	require.Equal(t, bson.D{{Key: "$or", Value: bson.A{bson.D{{Key: "identity.sequence", Value: bson.D{{Key: "$gt", Value: values[0]}}}}}}}, seek)
	require.ErrorIs(t, checkProjection(bson.D{{Key: "identity", Value: bson.D{{Key: "$literal", Value: 0}}}}, keys), ErrProjection)
}

func TestCursorTimestampPrecision(t *testing.T) {
	t.Parallel()

	keys, err := sortKeys(bson.D{{Key: "at", Value: 1}}, "sequence")
	require.NoError(t, err)

	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	document, err := bson.Marshal(bson.D{{Key: "at", Value: at}, {Key: "sequence", Value: int64(42)}})
	require.NoError(t, err)
	payload, err := encodePosition(document, keys, time.Second)
	require.NoError(t, err)
	values, err := decodePosition(payload, len(keys))
	require.NoError(t, err)
	require.Equal(t, at.UnixMilli(), values[0].DateTime())
	require.Equal(t, int64(42), values[1].Int64())

	document, err = bson.Marshal(bson.D{{Key: "at", Value: at.Add(time.Millisecond)}, {Key: "sequence", Value: int64(42)}})
	require.NoError(t, err)
	_, err = encodePosition(document, keys, time.Second)
	require.ErrorIs(t, err, ErrMissingKey)
}

// TestFindPageRefusesBeforeQuerying checks every failure FindPage decides before it sends a command (the collection is nil).
func TestFindPageRefusesBeforeQuerying(t *testing.T) {
	t.Parallel()

	codec, err := pagetoken.New(bytes.Repeat([]byte{1}, pagetoken.MinKeyLength))
	require.NoError(t, err)

	keys, err := sortKeys(nil, "")
	require.NoError(t, err)

	otherBindings, err := newBindings(mustFilter(t, bson.D{{Key: "g", Value: "other"}}), keys, "s/precision=1ms")
	require.NoError(t, err)

	foreign, err := codec.Issue([]byte("x"), otherBindings)
	require.NoError(t, err)

	bindings, err := newBindings(mustFilter(t, nil), keys, "s/precision=1ms")
	require.NoError(t, err)

	garbage, err := codec.Issue([]byte("not a position"), bindings)
	require.NoError(t, err)

	for name, tc := range map[string]struct {
		q    Query
		want error
	}{
		"dangerous filter":   {q: Query{Filter: bson.D{{Key: "$where", Value: "1"}}}, want: ErrDangerousOperator},
		"precision":          {q: Query{TimestampPrecision: time.Microsecond}, want: ErrMissingKey},
		"sort":               {q: Query{Sort: bson.D{{Key: "a", Value: 2}}}, want: ErrSort},
		"projection":         {q: Query{Projection: bson.D{{Key: "_id", Value: "$x"}}}, want: ErrProjection},
		"unencodable filter": {q: Query{Filter: bson.D{{Key: "f", Value: func() {}}}}, want: ErrValueType},
		"token of a filter":  {q: Query{After: foreign, Subject: "s"}, want: pagetoken.ErrFilterChanged},
		"malformed position": {q: Query{After: garbage, Subject: "s"}, want: pagetoken.ErrInvalidToken},
	} {
		_, err := FindPage[bson.M](t.Context(), nil, codec, tc.q)
		require.Error(t, err, name)

		if tc.want != nil {
			require.ErrorIs(t, err, tc.want, name)
		}
	}

	require.Equal(t, int32(1), sortDirection(false))
	require.Equal(t, int32(-1), sortDirection(true))
}

// whereScope is a named string that encodes itself as a $where document, which its Go value does not show.
type whereScope string

func (s whereScope) MarshalBSONValue() (byte, []byte, error) {
	typ, data, err := bson.MarshalValue(bson.D{{Key: "$where", Value: string(s)}})
	return byte(typ), data, err
}

// functionScope is a value a custom registry encodes as an array holding a $function expression.
type functionScope struct{}

func TestEncodedFilterRefusesDangerousOperators(t *testing.T) {
	t.Parallel()

	reg := bson.NewRegistry()
	reg.RegisterTypeEncoder(reflect.TypeFor[functionScope](), bson.ValueEncoderFunc(
		func(ec bson.EncodeContext, vw bson.ValueWriter, _ reflect.Value) error {
			expr := bson.A{bson.D{{Key: "$expr", Value: bson.D{{Key: "$function", Value: bson.D{{Key: "body", Value: "1"}}}}}}}

			enc, err := ec.LookupEncoder(reflect.TypeFor[bson.A]())
			if err != nil {
				return err
			}

			return enc.EncodeValue(ec, vw, reflect.ValueOf(expr))
		}))

	for name, q := range map[string]Query{
		"value marshaler": {Filter: bson.D{{Key: "$and", Value: bson.A{whereScope("sleep(1000)")}}}},
		"registry codec":  {Filter: bson.D{{Key: "$or", Value: functionScope{}}}, Registry: reg},
	} {
		require.ErrorIs(t, ValidateFilter(t.Context(), q.Filter, q.Registry), ErrValueType, "%s: a custom encoding is refused", name)

		_, err := FindPage[bson.M](t.Context(), nil, nil, q)
		require.ErrorIs(t, err, ErrValueType, name)

		// The encoded filter is checked on its own, as a second line.
		_, err = marshalFilter(q.Filter, q.Registry, q.BSONOptions)
		require.ErrorIs(t, err, ErrDangerousOperator, name)
	}
}

// TestEncodedFilterRefusesOptionalOperators checks the encoded filter is checked on its own: an optional.Optional a registry
// encodes as its value cannot hide an operator from it.
func TestEncodedFilterRefusesOptionalOperators(t *testing.T) {
	t.Parallel()

	reg := bson.NewRegistry()
	reg.RegisterTypeEncoder(reflect.TypeFor[optional.Optional[bson.M]](), bson.ValueEncoderFunc(
		func(ec bson.EncodeContext, vw bson.ValueWriter, v reflect.Value) error {
			inner, present := optional.GetReflect(v)
			if !present {
				return vw.WriteNull()
			}

			enc, err := ec.LookupEncoder(inner.Type())
			if err != nil {
				return err
			}

			return enc.EncodeValue(ec, vw, inner)
		}))

	_, err := marshalFilter(bson.D{{Key: "$and", Value: bson.A{optional.Some(bson.M{"$where": "sleep(1000)"})}}}, reg, nil)
	require.ErrorIs(t, err, ErrDangerousOperator)
}
