// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package pagetoken_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

// wireValues covers every wire member of a sort value: plain and invalid UTF-8 strings, integers, finite and non-finite floats,
// bools, timestamps inside and outside RFC 3339's years, and plain and scientific decimals.
func wireValues() map[string]any {
	return map[string]any{
		"string":         "Grüße <a&b>",
		"emptyString":    "",
		"rawString":      "\xff\xfeok",
		"int":            int64(-42),
		"maxInt":         int64(math.MaxInt64),
		"float":          2.5,
		"negativeZero":   math.Copysign(0, -1),
		"nan":            math.NaN(),
		"posInf":         math.Inf(1),
		"negInf":         math.Inf(-1),
		"true":           true,
		"false":          false,
		"time":           time.Date(2026, 3, 4, 5, 6, 7, 891000000, time.UTC),
		"timeAfter9999":  time.Date(12026, 1, 1, 0, 0, 0, 0, time.UTC),
		"timeBefore0":    time.Date(-5, 1, 1, 0, 0, 0, 5000000, time.UTC),
		"decimal":        decimal.RequireFromString("-1.250"),
		"decimalZero":    decimal.RequireFromString("-0.000"),
		"decimalHuge":    decimal.New(7, 6111),
		"decimalTiny":    decimal.New(-3, -6176),
		"uint":           uint32(7),
		"namedString":    namedWire("named"),
		"pointerToFloat": new(float32(1.5)),
	}
}

type namedWire string

// goldenWireTokens are the page tokens the pointer-per-kind representation issued for wireValues, as {"k":[value],"i":value},
// under a fixed clock: the inline representation must issue the very same bytes.
var goldenWireTokens = map[string]string{
	"decimal":        "AQAAAZt22qgA-raU2UQwJfRpvT7KCnL-ZEb9SrfCX9ZTiefQb4BskPbGwonknpwFshRYYDh7c7yxJ3siayI6W3siZCI6Ii0xLjI1In1dLCJpIjp7ImQiOiItMS4yNSJ9fUq9LXAcoQ1aaO31j_Ok6cS3FmyNt08ksoxn-4tGekrb",
	"decimalHuge":    "AQAAAZt22qgA-raU2UQwJfRpvT7KCnL-ZEb9SrfCX9ZTiefQb4BskPbGwonknpwFshRYYDh7c7yxKXsiayI6W3siZCI6IjdlNjExMSJ9XSwiaSI6eyJkIjoiN2U2MTExIn19dKIGeh83WsCUsxXOPH6ATnyJ8eY2AM5zttHW_FgyhFo",
	"decimalTiny":    "AQAAAZt22qgA-raU2UQwJfRpvT7KCnL-ZEb9SrfCX9ZTiefQb4BskPbGwonknpwFshRYYDh7c7yxLXsiayI6W3siZCI6Ii0zZS02MTc2In1dLCJpIjp7ImQiOiItM2UtNjE3NiJ9faHZC52uvY-EVKR5XeozyjSwZsSXR9AdlB1JMQw23DoN",
	"decimalZero":    "AQAAAZt22qgA-raU2UQwJfRpvT7KCnL-ZEb9SrfCX9ZTiefQb4BskPbGwonknpwFshRYYDh7c7yxH3siayI6W3siZCI6IjAifV0sImkiOnsiZCI6IjAifX1CuFG9utdKD9aAV9BTbMH4VWZi1Y6daNVVOivlvXm7Ig",
	"emptyString":    "AQAAAZt22qgA-raU2UQwJfRpvT7KCnL-ZEb9SrfCX9ZTiefQb4BskPbGwonknpwFshRYYDh7c7yxHXsiayI6W3sicyI6IiJ9XSwiaSI6eyJzIjoiIn19Iqrvhf2zUsB1LY2esEf11eFU6oTWesehgQpqe6hhT34",
	"false":          "AQAAAZt22qgA-raU2UQwJfRpvT7KCnL-ZEb9SrfCX9ZTiefQb4BskPbGwonknpwFshRYYDh7c7yxI3siayI6W3siYiI6ZmFsc2V9XSwiaSI6eyJiIjpmYWxzZX196ERsfAX2Vi3Rxe-UeSRr5K5KUhQVbo00-XP2aea07iI",
	"float":          "AQAAAZt22qgA-raU2UQwJfRpvT7KCnL-ZEb9SrfCX9ZTiefQb4BskPbGwonknpwFshRYYDh7c7yxH3siayI6W3siZiI6Mi41fV0sImkiOnsiZiI6Mi41fX0tT470dLF-cx2OA4MwEPmKHsDLx018rwCsp8yWrx7IEQ",
	"int":            "AQAAAZt22qgA-raU2UQwJfRpvT7KCnL-ZEb9SrfCX9ZTiefQb4BskPbGwonknpwFshRYYDh7c7yxH3siayI6W3sibiI6LTQyfV0sImkiOnsibiI6LTQyfX1W6nmyBoTLztXgO5vBGs0J3dfFJKeFzDPV0rg-LaKEOQ",
	"maxInt":         "AQAAAZt22qgA-raU2UQwJfRpvT7KCnL-ZEb9SrfCX9ZTiefQb4BskPbGwonknpwFshRYYDh7c7yxP3siayI6W3sibiI6OTIyMzM3MjAzNjg1NDc3NTgwN31dLCJpIjp7Im4iOjkyMjMzNzIwMzY4NTQ3NzU4MDd9fUEOTqEw8Xl9ZHHf1y_V-e8P3Z-ejvsEHcog2-dH8wkx",
	"namedString":    "AQAAAZt22qgA-raU2UQwJfRpvT7KCnL-ZEb9SrfCX9ZTiefQb4BskPbGwonknpwFshRYYDh7c7yxJ3siayI6W3sicyI6Im5hbWVkIn1dLCJpIjp7InMiOiJuYW1lZCJ9feU_w4oHNOQpCs-ay01tD7JiZm37hDe6xD39DxkeCnL1",
	"nan":            "AQAAAZt22qgA-raU2UQwJfRpvT7KCnL-ZEb9SrfCX9ZTiefQb4BskPbGwonknpwFshRYYDh7c7yxI3siayI6W3sieCI6Ik5hTiJ9XSwiaSI6eyJ4IjoiTmFOIn19vg_NEOAapygPo8JlGwvXJF4bx28J2HD2pNZE2l1H53E",
	"negInf":         "AQAAAZt22qgA-raU2UQwJfRpvT7KCnL-ZEb9SrfCX9ZTiefQb4BskPbGwonknpwFshRYYDh7c7yxJXsiayI6W3sieCI6Ii1JbmYifV0sImkiOnsieCI6Ii1JbmYifX1jpsLMvBBv4B5CXZ2e_zsrk3jRZ8k3N-_GlBkx6krBxQ",
	"negativeZero":   "AQAAAZt22qgA-raU2UQwJfRpvT7KCnL-ZEb9SrfCX9ZTiefQb4BskPbGwonknpwFshRYYDh7c7yxHXsiayI6W3siZiI6LTB9XSwiaSI6eyJmIjotMH19wVafXYK16YyJzguoPr5ZRheAqJJJDcbeB6B1ePk4uSU",
	"pointerToFloat": "AQAAAZt22qgA-raU2UQwJfRpvT7KCnL-ZEb9SrfCX9ZTiefQb4BskPbGwonknpwFshRYYDh7c7yxH3siayI6W3siZiI6MS41fV0sImkiOnsiZiI6MS41fX2F28RhfKJRCLd1Jhu-0hO0R7HVy6LXnD3whC5lgvFY-A",
	"posInf":         "AQAAAZt22qgA-raU2UQwJfRpvT7KCnL-ZEb9SrfCX9ZTiefQb4BskPbGwonknpwFshRYYDh7c7yxJXsiayI6W3sieCI6IitJbmYifV0sImkiOnsieCI6IitJbmYifX2EdqIzfHaLwRr7pJCncKrPJCxnOHc9GrKYe6L08beK4w",
	"rawString":      "AQAAAZt22qgA-raU2UQwJfRpvT7KCnL-ZEb9SrfCX9ZTiefQb4BskPbGwonknpwFshRYYDh7c7yxKXsiayI6W3siciI6Il9fNXZhdyJ9XSwiaSI6eyJyIjoiX181dmF3In19cBfekGSFdcn4Z0Wft37eEEBrILGY-SComIJtmIa98Ug",
	"string":         "AQAAAZt22qgA-raU2UQwJfRpvT7KCnL-ZEb9SrfCX9ZTiefQb4BskPbGwonknpwFshRYYDh7c7yxVXsiayI6W3sicyI6Ikdyw7zDn2UgXHUwMDNjYVx1MDAyNmJcdTAwM2UifV0sImkiOnsicyI6Ikdyw7zDn2UgXHUwMDNjYVx1MDAyNmJcdTAwM2UifX1HYPMBR_6pk2eFYmHWHEsE0b8guyEkvZcdo_bGNKrMPQ",
	"time":           "AQAAAZt22qgA-raU2UQwJfRpvT7KCnL-ZEb9SrfCX9ZTiefQb4BskPbGwonknpwFshRYYDh7c7yxTXsiayI6W3sidCI6IjIwMjYtMDMtMDRUMDU6MDY6MDcuODkxWiJ9XSwiaSI6eyJ0IjoiMjAyNi0wMy0wNFQwNTowNjowNy44OTFaIn19B701DtXLVFXMjpuhe6DIgCAfVpZJFBgdmdmJadAXhwE",
	"timeAfter9999":  "AQAAAZt22qgA-raU2UQwJfRpvT7KCnL-ZEb9SrfCX9ZTiefQb4BskPbGwonknpwFshRYYDh7c7yxOXsiayI6W3sibSI6WzMxNzMzNjc0NTYwMCwwXX1dLCJpIjp7Im0iOlszMTczMzY3NDU2MDAsMF19fbUvO8CzxlRTRNLevOVl2S3rH-dm12DgpwxeB5_VyEnO",
	"timeBefore0":    "AQAAAZt22qgA-raU2UQwJfRpvT7KCnL-ZEb9SrfCX9ZTiefQb4BskPbGwonknpwFshRYYDh7c7yxRXsiayI6W3sibSI6Wy02MjMyNDk4NTYwMCw1MDAwMDAwXX1dLCJpIjp7Im0iOlstNjIzMjQ5ODU2MDAsNTAwMDAwMF19fTP7gQbBsoemEHYSSPo5sxxXT0KMm6GH4DY5aH_EspAY",
	"true":           "AQAAAZt22qgA-raU2UQwJfRpvT7KCnL-ZEb9SrfCX9ZTiefQb4BskPbGwonknpwFshRYYDh7c7yxIXsiayI6W3siYiI6dHJ1ZX1dLCJpIjp7ImIiOnRydWV9fYr10wl7lFqelz3_syAPkFUnBfllbUWdAGHO8-ek31-W",
	"uint":           "AQAAAZt22qgA-raU2UQwJfRpvT7KCnL-ZEb9SrfCX9ZTiefQb4BskPbGwonknpwFshRYYDh7c7yxG3siayI6W3sibiI6N31dLCJpIjp7Im4iOjd9fZ69dwuqADxzfVkgITJkolHs542MB9xS-ekWz8NToyu0",
}

func TestSortValueGoldenTokens(t *testing.T) {
	t.Parallel()

	codec, err := pagetoken.New(key, pagetoken.WithClock(fixedClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))))
	require.NoError(t, err)

	tokens := pagetoken.NewTyped[pagetoken.Position](codec)

	for name, raw := range wireValues() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v, err := pagetoken.NewSortValue(raw)
			require.NoError(t, err)

			pos := pagetoken.Position{Keys: []pagetoken.SortValue{v}, ID: v}

			token, err := tokens.Issue(pos, bindings)
			require.NoError(t, err)
			require.Equal(t, goldenWireTokens[name], token)

			got, first, err := tokens.Resolve(token, bindings)
			require.NoError(t, err)
			require.False(t, first)
			require.Zero(t, got.Keys[0].Compare(v))
			require.Zero(t, got.ID.Compare(v))

			again, err := tokens.Issue(got, bindings)
			require.NoError(t, err)
			require.Equal(t, token, again)
		})
	}
}

// TestSortValueValid checks Valid agrees with Value: only the zero SortValue, which an opaque nil also is, holds no value.
func TestSortValueValid(t *testing.T) {
	t.Parallel()

	values := map[string]pagetoken.SortValue{
		"zero":       {},
		"opaqueNil":  pagetoken.OpaqueSortValue(nil),
		"opaque":     pagetoken.OpaqueSortValue([]byte{0}),
		"emptyText":  benchSortValue(""),
		"zeroInt":    benchSortValue(int64(0)),
		"falseBool":  benchSortValue(false),
		"zeroTime":   benchSortValue(time.Time{}),
		"zeroNumber": benchSortValue(decimal.Zero),
	}

	for name, v := range values {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, want := v.Value()
			require.Equal(t, want, v.Valid())
			require.Equal(t, name != "zero" && name != "opaqueNil", v.Valid())
		})
	}
}

// TestZeroSortValueWire checks a key without a value, such as an SQL NULL, round-trips through a position as the empty
// object, while a member without a value stays invalid.
func TestZeroSortValueWire(t *testing.T) {
	t.Parallel()

	name, err := pagetoken.NewSortValue("a")
	require.NoError(t, err)
	id, err := pagetoken.NewSortValue(int64(7))
	require.NoError(t, err)

	data, err := json.Marshal(pagetoken.Position{Keys: []pagetoken.SortValue{{}, name}, ID: id})
	require.NoError(t, err)
	require.JSONEq(t, `{"k":[{},{"s":"a"}],"i":{"n":7}}`, string(data))

	var got pagetoken.Position
	require.NoError(t, json.Unmarshal(data, &got))
	require.Len(t, got.Keys, 2)
	require.False(t, got.Keys[0].Valid())
	require.Zero(t, got.Keys[1].Compare(name))
	require.Equal(t, -1, got.Keys[0].Compare(name), "the zero value orders first")

	for _, invalid := range []string{`{"s":null}`, `{"x":null,"s":null}`, `null`, `[]`} {
		var v pagetoken.SortValue
		require.Error(t, json.Unmarshal([]byte(invalid), &v), invalid)
	}

	require.ErrorIs(t, json.Unmarshal([]byte(`{"k":[],"i":{}}`), &got), pagetoken.ErrUnsortableValue, "the identity holds a value")
}

// FuzzSortValueWireMatchesLegacy decodes arbitrary documents with both representations: they must accept and reject the same
// documents with the same errors, and re-encode an accepted one to the same bytes, which decode again to an equal value.
func FuzzSortValueWireMatchesLegacy(f *testing.F) {
	for _, raw := range wireValues() {
		v, err := pagetoken.NewSortValue(raw)
		require.NoError(f, err)

		data, err := json.Marshal(v)
		require.NoError(f, err)
		f.Add(data)
	}

	for _, seed := range []string{`{}`, `{"s":null}`, `{"s":"a","n":1}`, `{"x":"NaN"}`, `{"x":"nan"}`, `{"r":"!!"}`, `{"m":[1,-1]}`,
		`{"m":[-62135596801,0]}`, `{"t":"2026-01-01T00:00:00+03:00"}`, `{"d":"1e-6200"}`, `{"d":"x"}`, `{"n":1.5}`, `{"b":true}`, `null`, `[]`} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var (
			legacy  legacySortValue
			current pagetoken.SortValue
		)

		legacyErr := json.Unmarshal(data, &legacy)
		currentErr := json.Unmarshal(data, &current)

		if currentErr == nil && !current.Valid() {
			// The empty object is the zero SortValue, which the legacy form refused.
			require.Error(t, legacyErr)

			got, err := json.Marshal(current)
			require.NoError(t, err)
			require.JSONEq(t, `{}`, string(got))

			return
		}

		require.Equal(t, legacyErr == nil, currentErr == nil, "legacy: %v, current: %v", legacyErr, currentErr)

		if currentErr != nil {
			// encoding/json names the decoded type; only the oracle's type name differs.
			want := strings.NewReplacer("pagetoken_test.legacySortValueJSON", "pagetoken.sortValueJSON",
				"legacySortValueJSON", "sortValueJSON").Replace(legacyErr.Error())
			require.Equal(t, want, currentErr.Error())

			return
		}

		want, legacyErr := json.Marshal(legacy)
		got, currentErr := json.Marshal(current)
		require.Equal(t, legacyErr == nil, currentErr == nil)

		if currentErr != nil {
			return
		}

		require.Equal(t, string(want), string(got))

		var again pagetoken.SortValue
		require.NoError(t, json.Unmarshal(got, &again))
		require.Zero(t, again.Compare(current))
		require.Equal(t, current.Valid(), again.Valid())
	})
}

// FuzzSortValueEncodingMatchesLegacy encodes scalars of every built-in class with both representations: the bytes must agree and
// decode back to an equal value.
func FuzzSortValueEncodingMatchesLegacy(f *testing.F) {
	f.Add("a", int64(1), 1.5, true, int64(0), int64(0), int64(125), int32(-2))
	f.Add("\xff", int64(math.MinInt64), math.Inf(-1), false, int64(-62135596801), int64(999999999), int64(-7), int32(6111))
	f.Add("<&> ", int64(math.MaxInt64), math.NaN(), false, int64(253402300800), int64(1), int64(0), int32(-6176))

	f.Fuzz(func(t *testing.T, s string, n int64, fl float64, b bool, sec, nsec, coef int64, exp int32) {
		at := time.Unix(sec, nsec)

		for _, raw := range []any{s, n, fl, b, at, decimal.New(coef, exp)} {
			current, err := pagetoken.NewSortValue(raw)
			require.NoError(t, err)
			require.True(t, current.Valid())

			got, currentErr := json.Marshal(current)
			want, legacyErr := json.Marshal(legacyFrom(raw))
			require.Equal(t, legacyErr == nil, currentErr == nil, "legacy: %v, current: %v", legacyErr, currentErr)

			if currentErr != nil {
				continue
			}

			require.Equal(t, string(want), string(got))

			var back pagetoken.SortValue
			require.NoError(t, json.Unmarshal(got, &back))
			require.Zero(t, back.Compare(current))
		}
	})
}
