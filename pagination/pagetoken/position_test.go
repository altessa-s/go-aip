// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package pagetoken_test

import (
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

type (
	namedString string
	namedInt    int16
	namedFloat  float32
	namedBool   bool
)

func mustSortValue(t testing.TB, v any) pagetoken.SortValue {
	t.Helper()

	sv, err := pagetoken.NewSortValue(v)
	require.NoError(t, err)

	return sv
}

func TestNewSortValue_StoredForm(t *testing.T) {
	t.Parallel()

	s := "x"
	n := 7
	at := time.Date(2026, 1, 2, 3, 4, 5, 999_999_999, time.FixedZone("UTC+3", 3*3600))

	tests := map[string]struct {
		in    any
		class pagetoken.SortClass
		same  any // a value that must compare equal
	}{
		"string":              {in: "x", class: pagetoken.SortClassString, same: "x"},
		"named string":        {in: namedString("x"), class: pagetoken.SortClassString, same: "x"},
		"pointer to string":   {in: &s, class: pagetoken.SortClassString, same: "x"},
		"int":                 {in: 7, class: pagetoken.SortClassNumber, same: int64(7)},
		"pointer to int":      {in: &n, class: pagetoken.SortClassNumber, same: int64(7)},
		"named int":           {in: namedInt(7), class: pagetoken.SortClassNumber, same: int64(7)},
		"uint32":              {in: uint32(7), class: pagetoken.SortClassNumber, same: int64(7)},
		"float32":             {in: namedFloat(1.5), class: pagetoken.SortClassNumber, same: 1.5},
		"int equals float":    {in: int64(2), class: pagetoken.SortClassNumber, same: 2.0},
		"decimal equals int":  {in: decimal.RequireFromString("2.00"), class: pagetoken.SortClassNumber, same: int64(2)},
		"bool":                {in: namedBool(true), class: pagetoken.SortClassBool, same: true},
		"time to millisecond": {in: at, class: pagetoken.SortClassTime, same: at.UTC().Truncate(time.Millisecond)},
		"pointer to time":     {in: &at, class: pagetoken.SortClassTime, same: at},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			v := mustSortValue(t, tc.in)
			assert.Equal(t, tc.class, v.Class())
			assert.Zero(t, v.Compare(mustSortValue(t, tc.same)))
		})
	}
}

func TestNewSortValue_Unsortable(t *testing.T) {
	t.Parallel()

	var nilString *string

	type namedTime time.Time

	for name, in := range map[string]any{
		"nil":           nil,
		"nil pointer":   nilString,
		"slice":         []string{"a"},
		"map":           map[string]any{},
		"struct":        struct{ A int }{},
		"named time":    namedTime(time.Now()),
		"huge uint64":   uint64(math.MaxUint64),
		"bytes":         []byte("a"),
		"nil interface": any(nil),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := pagetoken.NewSortValue(in)
			require.ErrorIs(t, err, pagetoken.ErrUnsortableValue)
		})
	}
}

func TestSortValue_CompareBSONOrder(t *testing.T) {
	t.Parallel()

	// Ascending MongoDB sort order: numbers (NaN first, exact across types), strings, bools, dates.
	ordered := []any{
		math.NaN(),
		math.Inf(-1),
		int64(math.MinInt64),
		decimal.RequireFromString("-1.5"),
		int64(-1),
		0.1,                               // the double 0.1 is above the decimal 0.1
		decimal.RequireFromString("0.11"), // and below 0.11
		int64(1) << 53,
		int64(1)<<53 + 1, // not rounded to a double
		math.Inf(1),
		"",
		"A",
		"a",
		"b",
		false,
		true,
		time.UnixMilli(-1),
		time.UnixMilli(0),
	}

	values := make([]pagetoken.SortValue, len(ordered))
	for i, v := range ordered {
		values[i] = mustSortValue(t, v)
	}

	for i := range values {
		assert.Zero(t, values[i].Compare(values[i]), "%v equals itself", ordered[i])

		for j := i + 1; j < len(values); j++ {
			assert.Equal(t, -1, values[i].Compare(values[j]), "%v < %v", ordered[i], ordered[j])
			assert.Equal(t, 1, values[j].Compare(values[i]), "%v > %v", ordered[j], ordered[i])
		}
	}

	assert.Equal(t, -1, pagetoken.SortValue{}.Compare(values[0]), "the zero value orders first")
	assert.Zero(t, mustSortValue(t, decimal.RequireFromString("0.1")).Compare(mustSortValue(t, decimal.RequireFromString("0.10"))))
	assert.Equal(t, 1, mustSortValue(t, int64(1)<<53+1).Compare(mustSortValue(t, float64(1<<53))), "an integer beyond 2^53 compares exactly")
	assert.Zero(t, mustSortValue(t, float64(1<<53)).Compare(mustSortValue(t, int64(1)<<53)))
	assert.Equal(t, 1, mustSortValue(t, 0.1).Compare(mustSortValue(t, decimal.RequireFromString("0.1"))))
}

func TestSortValue_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 1, 2, 3, 4, 5, 6_000_000, time.UTC)
	pos := pagetoken.Position{
		Keys: []pagetoken.SortValue{
			mustSortValue(t, decimal.RequireFromString("1.25")),
			mustSortValue(t, "s"),
			mustSortValue(t, int64(3)),
			mustSortValue(t, 2.5),
			mustSortValue(t, false),
			mustSortValue(t, at),
		},
		ID: mustSortValue(t, "id-1"),
	}
	data, err := json.Marshal(pos)
	require.NoError(t, err)
	assert.JSONEq(t,
		`{"k":[{"d":"1.25"},{"s":"s"},{"n":3},{"f":2.5},{"b":false},{"t":"2026-01-02T03:04:05.006Z"}],"i":{"s":"id-1"}}`, string(data))

	var back pagetoken.Position
	require.NoError(t, json.Unmarshal(data, &back))
	require.Len(t, back.Keys, len(pos.Keys))
	assert.Equal(t, pos.ID, back.ID)

	for i := range pos.Keys {
		assert.Zero(t, pos.Keys[i].Compare(back.Keys[i]))
		assert.Equal(t, pos.Keys[i].Class(), back.Keys[i].Class())
	}

	for _, bad := range []string{`{"s":"a","n":1}`, `[]`} {
		var v pagetoken.SortValue
		require.Error(t, json.Unmarshal([]byte(bad), &v), bad)
	}
}

func TestComparePositions(t *testing.T) {
	t.Parallel()

	pos := func(id string, keys ...any) pagetoken.Position {
		p := pagetoken.Position{ID: mustSortValue(t, id)}
		for _, k := range keys {
			p.Keys = append(p.Keys, mustSortValue(t, k))
		}

		return p
	}
	// name asc, score desc, id in the direction of the last key (desc).
	compare := pagetoken.ComparePositions([]bool{false, true}, true, pagetoken.ScalarPositionCodec{})
	items := []pagetoken.Position{
		pos("1", "b", int64(1)),
		pos("2", "a", int64(1)),
		pos("3", "a", int64(2)),
		pos("4", "a", int64(2)),
	}
	slices.SortFunc(items, compare)

	ids := make([]string, len(items))
	for i, p := range items {
		ids[i] = stringID(p.ID)
	}

	assert.Equal(t, []string{"4", "3", "2", "1"}, ids)

	// A position of another shape compares deterministically.
	assert.Equal(t, -1, compare(pos("9", "a"), pos("1", "a", int64(1))))
	assert.Equal(t, 1, compare(pos("1", "a", int64(1)), pos("9", "a")))
}

func TestSlicePager_Positions(t *testing.T) {
	t.Parallel()

	codec, err := pagetoken.New([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)

	compare := pagetoken.ComparePositions([]bool{true}, true, pagetoken.ScalarPositionCodec{})

	items := make([]pagetoken.Position, 0, 5)
	for i, score := range []any{2.5, int64(3), decimal.RequireFromString("2.75"), int64(1), 2.5} {
		items = append(items, pagetoken.Position{Keys: []pagetoken.SortValue{mustSortValue(t, score)}, ID: mustSortValue(t, string(rune('a'+i)))})
	}

	slices.SortFunc(items, compare)
	pager := pagetoken.NewSlicePager(pagetoken.NewTyped[pagetoken.Position](codec), func(p pagetoken.Position) pagetoken.Position { return p }, compare)
	b := pagetoken.NewBindings("list", "score desc")

	var got []string

	token := ""
	for {
		page, err := pager.Page(items, 2, token, b)
		require.NoError(t, err)

		for _, p := range page.Items {
			got = append(got, stringID(p.ID))
		}

		if page.Next == "" {
			break
		}

		token = page.Next
	}

	assert.Equal(t, []string{"b", "c", "e", "a", "d"}, got)
}

// TestSortValue_NonFiniteFloats pins the token form of NaN and the infinities, which MongoDB stores and sorts but JSON numbers cannot
// hold: each round-trips, and a page boundary on each continues the paging.
func TestSortValue_NonFiniteFloats(t *testing.T) {
	t.Parallel()

	for spelling, f := range map[string]float64{"NaN": math.NaN(), "+Inf": math.Inf(1), "-Inf": math.Inf(-1)} {
		t.Run(spelling, func(t *testing.T) {
			t.Parallel()
			v := mustSortValue(t, f)
			data, err := json.Marshal(v)
			require.NoError(t, err)
			assert.JSONEq(t, `{"x":"`+spelling+`"}`, string(data))

			var back pagetoken.SortValue
			require.NoError(t, json.Unmarshal(data, &back))
			assert.Zero(t, v.Compare(back))
			assert.Equal(t, pagetoken.SortClassNumber, back.Class())
		})
	}

	finite, err := json.Marshal(mustSortValue(t, 2.5))
	require.NoError(t, err)
	assert.JSONEq(t, `{"f":2.5}`, string(finite), "a finite float keeps its number form")

	var v pagetoken.SortValue
	require.ErrorIs(t, json.Unmarshal([]byte(`{"x":"Infinity"}`), &v), pagetoken.ErrUnsortableValue)
	require.Error(t, json.Unmarshal([]byte(`{"x":"NaN","f":1}`), &v))

	codec, err := pagetoken.New([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)

	compare := pagetoken.ComparePositions([]bool{false}, false, pagetoken.ScalarPositionCodec{})

	items := []pagetoken.Position{}
	for i, score := range []any{math.Inf(1), int64(1), math.NaN(), math.Inf(-1), 2.5} {
		items = append(items, pagetoken.Position{Keys: []pagetoken.SortValue{mustSortValue(t, score)}, ID: mustSortValue(t, string(rune('a'+i)))})
	}

	slices.SortFunc(items, compare)
	pager := pagetoken.NewSlicePager(pagetoken.NewTyped[pagetoken.Position](codec), func(p pagetoken.Position) pagetoken.Position { return p }, compare)
	b := pagetoken.NewBindings("list", "score")

	var got []string

	token := ""
	for {
		page, err := pager.Page(items, 1, token, b)
		require.NoError(t, err)

		for _, p := range page.Items {
			got = append(got, stringID(p.ID))
		}

		if page.Next == "" {
			break
		}

		token = page.Next
	}

	assert.Equal(t, []string{"c", "d", "b", "e", "a"}, got)
}

// TestNewSortValue_Cycles pins that a value whose pointers or interfaces form a cycle fails instead of looping.
func TestNewSortValue_Cycles(t *testing.T) {
	t.Parallel()

	var self any

	self = &self

	var a, b any

	a, b = &b, &a

	type node struct{ next *node }

	ring := &node{}
	ring.next = ring

	deep := any("x")
	for range 40 {
		prev := deep
		deep = &prev
	}

	for name, in := range map[string]any{"self-referencing interface": self, "two-node cycle": a, "deep chain": deep} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := pagetoken.NewSortValue(in)
			require.ErrorIs(t, err, pagetoken.ErrUnsortableValue)
		})
	}

	_, err := pagetoken.NewSortValue(ring)
	require.ErrorIs(t, err, pagetoken.ErrUnsortableValue, "a struct is not sortable")
}

// TestSortValue_DecimalRange verifies that the neutral codec does not impose BSON limits.
func TestSortValue_DecimalRange(t *testing.T) {
	t.Parallel()

	for name, d := range map[string]decimal.Decimal{
		"huge exponent":     decimal.New(1, math.MaxInt32),
		"tiny exponent":     decimal.New(1, math.MinInt32),
		"beyond Decimal128": decimal.New(1, 6145),
		"35 digits":         decimal.RequireFromString("12345678901234567890123456789012345"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := pagetoken.NewSortValue(d)
			require.NoError(t, err)
			_, err = pagetoken.NewSortValue(&d)
			require.NoError(t, err)
		})
	}

	for _, bad := range []string{`{"d":"x"}`} {
		var v pagetoken.SortValue
		require.ErrorIs(t, json.Unmarshal([]byte(bad), &v), pagetoken.ErrUnsortableValue, bad)
	}

	for text, d := range map[string]decimal.Decimal{
		`{"d":"1e6111"}`:  decimal.New(1, 6111),
		`{"d":"7e-6176"}`: decimal.New(7, -6176),
		`{"d":"1.25"}`:    decimal.RequireFromString("1.25"),
	} {
		v := mustSortValue(t, d)
		data, err := json.Marshal(v)
		require.NoError(t, err)
		assert.JSONEq(t, text, string(data))

		var back pagetoken.SortValue
		require.NoError(t, json.Unmarshal(data, &back))
		assert.Zero(t, v.Compare(back), text)
	}

	assert.Equal(t, 1, mustSortValue(t, decimal.New(1, 6111)).Compare(mustSortValue(t, int64(math.MaxInt64))))
	assert.Equal(t, -1, mustSortValue(t, decimal.New(7, -6176)).Compare(mustSortValue(t, math.SmallestNonzeroFloat64)))
}

// TestSortValue_ZeroWithExtremeExponent pins that a zero decimal, which Decimal128 stores whatever its exponent, is kept in its
// stored form: comparing it with an integer, a float or another decimal, and writing it out, takes bounded work.
func TestSortValue_ZeroWithExtremeExponent(t *testing.T) {
	t.Parallel()

	values := map[string]pagetoken.SortValue{
		"max exponent": mustSortValue(t, decimal.New(0, math.MaxInt32)),
		"min exponent": mustSortValue(t, decimal.New(0, math.MinInt32)),
	}

	for _, text := range []string{`{"d":"0e2147483647"}`, `{"d":"0e-2147483648"}`, `{"d":"-0e2147483647"}`} {
		var v pagetoken.SortValue
		require.NoError(t, json.Unmarshal([]byte(text), &v), text)
		values[text] = v
	}

	big, err := pagetoken.NewSortValue(decimal.New(1, 6111))
	require.NoError(t, err)

	for name, v := range values {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Zero(t, v.Compare(mustSortValue(t, int64(0))))
			assert.Equal(t, -1, v.Compare(mustSortValue(t, int64(1))))
			assert.Equal(t, 1, v.Compare(mustSortValue(t, int64(-1))))
			assert.Zero(t, v.Compare(mustSortValue(t, 0.0)))
			assert.Equal(t, -1, v.Compare(mustSortValue(t, math.SmallestNonzeroFloat64)))
			assert.Zero(t, v.Compare(mustSortValue(t, decimal.Zero)))
			assert.Equal(t, -1, v.Compare(big))
			assert.Equal(t, 1, v.Compare(mustSortValue(t, decimal.New(-1, -6176))))
			data, err := json.Marshal(v)
			require.NoError(t, err)
			assert.JSONEq(t, `{"d":"0"}`, string(data))
		})
	}
}

// pageAll pages items one by one through a signed SlicePager and returns the ids in page order; every boundary is issued and
// resolved through a token.
func pageAll(t *testing.T, items []pagetoken.Position, compare func(a, b pagetoken.Position) int) []string {
	t.Helper()

	codec, err := pagetoken.New([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	slices.SortFunc(items, compare)
	pager := pagetoken.NewSlicePager(pagetoken.NewTyped[pagetoken.Position](codec), func(p pagetoken.Position) pagetoken.Position { return p }, compare)

	var got []string

	token := ""
	for range len(items) + 1 {
		page, err := pager.Page(items, 1, token, pagetoken.NewBindings("list", "k"))
		require.NoError(t, err)

		for _, p := range page.Items {
			got = append(got, stringID(p.ID))
		}

		if page.Next == "" {
			return got
		}

		token = page.Next
	}

	t.Fatal("paging did not end")

	return nil
}

// TestSortValue_TimesOutsideRFC3339 pins dates MongoDB stores (any int64 milliseconds) but RFC 3339 cannot write: they round-trip
// as milliseconds and page across token boundaries; in-range dates keep their RFC 3339 form; a time beyond int64 milliseconds is
// refused.
func TestSortValue_TimesOutsideRFC3339(t *testing.T) {
	t.Parallel()

	times := []time.Time{
		time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(-1, 12, 31, 23, 59, 59, 999_000_000, time.UTC),
		time.UnixMilli(math.MaxInt64),
		time.UnixMilli(math.MinInt64),
		time.Date(9999, 12, 31, 23, 59, 59, 999_000_000, time.UTC),
		time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}

	items := make([]pagetoken.Position, 0, len(times))
	for i, at := range times {
		v := mustSortValue(t, at)
		data, err := json.Marshal(v)
		require.NoError(t, err)

		if y := at.Year(); y >= 0 && y <= 9999 {
			assert.Contains(t, string(data), `"t":`, "an in-range date keeps its RFC 3339 form")
		} else {
			assert.JSONEq(t, `{"m":[`+strconv.FormatInt(at.Unix(), 10)+`,`+strconv.Itoa(at.Nanosecond())+`]}`, string(data))
		}

		var back pagetoken.SortValue
		require.NoError(t, json.Unmarshal(data, &back))
		assert.Zero(t, v.Compare(back), at.String())
		items = append(items, pagetoken.Position{Keys: []pagetoken.SortValue{v}, ID: mustSortValue(t, strconv.Itoa(i))})
	}

	assert.Equal(t, []string{"3", "1", "5", "6", "4", "0", "2"}, pageAll(t, items, pagetoken.ComparePositions([]bool{false}, false, pagetoken.ScalarPositionCodec{})))

	for _, at := range []time.Time{time.Date(300_000_000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(-300_000_000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		_, err := pagetoken.NewSortValue(at)
		require.NoError(t, err, at.String())
	}
}

// TestPosition_InvalidUTF8 pins strings and ids that are not valid UTF-8, which MongoDB stores byte for byte but a JSON string
// would alter (to U+FFFD): they round-trip exactly and page across token boundaries in byte order.
func TestPosition_InvalidUTF8(t *testing.T) {
	t.Parallel()

	keys := []string{"\xff", "a\xc3", "a�", "\xef\xbf\xbd", "a", ""}

	items := make([]pagetoken.Position, 0, len(keys)+1)
	for i, s := range keys {
		v := mustSortValue(t, s)
		data, err := json.Marshal(v)
		require.NoError(t, err)

		var back pagetoken.SortValue
		require.NoError(t, json.Unmarshal(data, &back))
		assert.Zero(t, v.Compare(back), "%q", s)
		items = append(items, pagetoken.Position{Keys: []pagetoken.SortValue{v}, ID: mustSortValue(t, "id"+strconv.Itoa(i))})
	}
	// Two items with equal keys, told apart only by ids that are not valid UTF-8.
	items = append(items, pagetoken.Position{Keys: []pagetoken.SortValue{mustSortValue(t, "a")}, ID: mustSortValue(t, "\xfe")})
	items[4].ID = mustSortValue(t, "\xff")
	got := pageAll(t, items, pagetoken.ComparePositions([]bool{false}, false, pagetoken.ScalarPositionCodec{}))
	assert.Equal(t, []string{"id5", "\xfe", "\xff", "id1", "id2", "id3", "id0"}, got)

	for _, bad := range []string{`{"k":[],"i":"a","j":"YQ"}`, `{"k":[]}`, `{"k":[],"j":"*"}`} {
		var p pagetoken.Position
		require.Error(t, json.Unmarshal([]byte(bad), &p), bad)
	}

	var v pagetoken.SortValue
	require.ErrorIs(t, json.Unmarshal([]byte(`{"r":"*"}`), &v), pagetoken.ErrUnsortableValue)
}

func stringID(v pagetoken.SortValue) string { x, _ := v.Value(); return x.(string) }
