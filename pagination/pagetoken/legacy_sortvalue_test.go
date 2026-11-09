// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package pagetoken_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

// legacySortValue is the pointer-per-kind SortValue the inline representation replaced, kept verbatim as the wire-format oracle
// of the differential tests: both must write the same bytes and accept the same documents.
type legacySortValue struct {
	opaque any
	d      *decimal.Decimal
	s      *string
	n      *int64
	f      *float64
	b      *bool
	t      *time.Time
}

// legacySortValueJSON is the JSON form of a [legacySortValue].
type legacySortValueJSON struct {
	D *string    `json:"d,omitempty"` // a decimal: plain digits, or coefficient "e" exponent beyond legacyPlainExponent
	S *string    `json:"s,omitempty"`
	N *int64     `json:"n,omitempty"`
	F *float64   `json:"f,omitempty"`
	B *bool      `json:"b,omitempty"`
	T *time.Time `json:"t,omitempty"`
	X *string    `json:"x,omitempty"` // a non-finite float: "NaN", "+Inf" or "-Inf"
	R *string    `json:"r,omitempty"` // a string that is not valid UTF-8, as unpadded URL-safe base64 of its bytes
	M *[2]int64  `json:"m,omitempty"` // epoch seconds and nanoseconds outside RFC 3339's years 0..9999
}

// legacyFrom builds the legacy representation of a scalar of a built-in type, as the legacy NewSortValue stored it.
func legacyFrom(v any) legacySortValue {
	switch x := v.(type) {
	case string:
		return legacySortValue{s: &x}
	case int64:
		return legacySortValue{n: &x}
	case float64:
		return legacySortValue{f: &x}
	case bool:
		return legacySortValue{b: &x}
	case time.Time:
		stored := x.UTC().Truncate(time.Millisecond)
		return legacySortValue{t: &stored}
	case decimal.Decimal:
		d, _ := legacyDecimalSortValue(x)
		return d
	default:
		panic(fmt.Sprintf("legacyFrom: %T", v))
	}
}

// MarshalJSON encodes v as an object with its one member.
func (v legacySortValue) MarshalJSON() ([]byte, error) {
	if v.opaque != nil {
		return nil, fmt.Errorf("%w: opaque value requires a custom position codec", pagetoken.ErrUnsortableValue)
	}

	w := legacySortValueJSON{S: v.s, N: v.n, F: v.f, B: v.b, T: v.t}
	if v.d != nil {
		d := legacyDecimalText(*v.d)
		w.D = &d
	}

	if v.s != nil && !utf8.ValidString(*v.s) {
		raw := base64.RawURLEncoding.EncodeToString([]byte(*v.s))
		w.S, w.R = nil, &raw
	}

	if v.t != nil && (v.t.Year() < 0 || v.t.Year() > 9999) {
		epoch := [2]int64{v.t.Unix(), int64(v.t.Nanosecond())}
		w.T, w.M = nil, &epoch
	}

	if v.f != nil && (math.IsNaN(*v.f) || math.IsInf(*v.f, 0)) {
		name := legacyNonFiniteName(*v.f)
		w.F, w.X = nil, &name
	}

	return json.Marshal(w)
}

// legacyPlainExponent bounds the exponents a decimal is written out in plain digits ("1.25", as decimal.Decimal marshals); beyond it the
// text is coefficient "e" exponent ("1e6111"), so a token never expands a large exponent into its digits.
const legacyPlainExponent = 32

// legacyDecimalText returns the JSON text of a decimal: its plain digits for an exponent within legacyPlainExponent, else its scientific form.
func legacyDecimalText(d decimal.Decimal) string {
	if exp := d.Exponent(); exp >= -legacyPlainExponent && exp <= legacyPlainExponent {
		return d.String()
	}

	return d.Coefficient().String() + "e" + strconv.Itoa(int(d.Exponent()))
}

// legacyDecimalSortValue preserves decimal values without imposing a database range.
func legacyDecimalSortValue(d decimal.Decimal) (legacySortValue, error) {
	if d.IsZero() {
		d = decimal.Zero
	}

	return legacySortValue{d: &d}, nil
}

// legacyNonFiniteName returns the JSON spelling of a non-finite float.
func legacyNonFiniteName(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case f > 0:
		return "+Inf"
	default:
		return "-Inf"
	}
}

// legacyNonFiniteValue parses the JSON spelling of a non-finite float.
func legacyNonFiniteValue(name string) (float64, bool) {
	switch name {
	case "NaN":
		return math.NaN(), true
	case "+Inf":
		return math.Inf(1), true
	case "-Inf":
		return math.Inf(-1), true
	default:
		return 0, false
	}
}

// UnmarshalJSON decodes an object with exactly one member, as [legacySortValue.MarshalJSON] encodes it.
func (v *legacySortValue) UnmarshalJSON(data []byte) error {
	var w legacySortValueJSON
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}

	set := 0

	for _, isSet := range []bool{w.D != nil, w.S != nil, w.N != nil, w.F != nil, w.B != nil, w.T != nil, w.X != nil, w.R != nil, w.M != nil} {
		if isSet {
			set++
		}
	}

	if set != 1 {
		return fmt.Errorf("%w: a sort value holds %d values", pagetoken.ErrUnsortableValue, set)
	}

	if w.X != nil {
		f, ok := legacyNonFiniteValue(*w.X)
		if !ok {
			return fmt.Errorf("%w: a non-finite float spelled %q", pagetoken.ErrUnsortableValue, *w.X)
		}

		w.F = &f
	}

	if w.R != nil {
		raw, err := base64.RawURLEncoding.DecodeString(*w.R)
		if err != nil {
			return fmt.Errorf("%w: a malformed raw string", pagetoken.ErrUnsortableValue)
		}

		s := string(raw)
		w.S = &s
	}

	if w.M != nil {
		if w.M[1] < 0 || w.M[1] >= int64(time.Second) {
			return pagetoken.ErrUnsortableValue
		}

		at := time.Unix(w.M[0], w.M[1]).UTC()
		w.T = &at
	}

	out := legacySortValue{s: w.S, n: w.N, f: w.F, b: w.B, t: w.T}
	if w.D != nil {
		d, err := decimal.NewFromString(*w.D)
		if err != nil {
			return fmt.Errorf("%w: a malformed decimal", pagetoken.ErrUnsortableValue)
		}

		if out, err = legacyDecimalSortValue(d); err != nil {
			return err
		}
	}

	*v = out

	return nil
}
