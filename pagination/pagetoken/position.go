// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package pagetoken

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// ErrUnsortableValue is returned by [NewSortValue] for a value that is not a sortable scalar: nil, a nil pointer, or a value of a
// type other than a number, a string, a bool or a time.
var ErrUnsortableValue = errors.New("pagetoken: value does not sort")

// SortClass is the comparison class used by the built-in scalar preset.
type SortClass uint8

// Sort classes of the built-in scalar preset.
const (
	// SortClassNone is the class of the zero SortValue, which orders before every other value.
	SortClassNone SortClass = iota
	// SortClassNumber holds integers, floats and decimals, which compare by their exact values.
	SortClassNumber
	// SortClassString holds strings, which compare byte-wise.
	SortClassString
	// SortClassBool holds bools: false orders before true.
	SortClassBool
	// SortClassTime holds normalized timestamps.
	SortClassTime
)

// String returns the class name.
func (c SortClass) String() string {
	switch c {
	case SortClassNumber:
		return "number"
	case SortClassString:
		return "string"
	case SortClassBool:
		return "bool"
	case SortClassTime:
		return "time"
	default:
		return "none"
	}
}

// SortValue holds an immutable typed scalar. NewSortValue builds the standard
// representation; PositionCodec can normalize, order and encode other scalars
// using OpaqueSortValue. JSON uses one type tag per scalar, preserving strings,
// integers, decimals, booleans, floats and timestamps without type inference.
// The zero SortValue holds no value, such as an SQL NULL key: it orders first
// and encodes as the empty object.
//
// A SortValue is a small inline value: building, copying and comparing a
// string, integer, float, bool or timestamp allocates nothing. Compare values
// with [SortValue.Compare], never with ==.
type SortValue struct {
	ref  any       // the opaque value, or the decimal.Decimal
	s    string    // the string
	t    time.Time // the timestamp
	bits uint64    // the int64, the float64 bits, or 1 for true
	kind sortKind
}

// sortKind tags the member a [SortValue] holds.
type sortKind uint8

// Kinds of SortValue members; the zero kind is the zero SortValue, which holds no value.
const (
	kindNone sortKind = iota
	kindOpaque
	kindDecimal
	kindString
	kindInt
	kindFloat
	kindBool
	kindTime
)

func stringSortValue(s string) SortValue { return SortValue{s: s, kind: kindString} }

func intSortValue(n int64) SortValue { return SortValue{bits: uint64(n), kind: kindInt} } //nolint:gosec // bit-preserving storage

func floatSortValue(f float64) SortValue {
	return SortValue{bits: math.Float64bits(f), kind: kindFloat}
}

func boolSortValue(b bool) SortValue {
	v := SortValue{kind: kindBool}
	if b {
		v.bits = 1
	}

	return v
}

// timeValue returns a timestamp held as is: callers normalize it first.
func timeValue(t time.Time) SortValue { return SortValue{t: t, kind: kindTime} }

func (v SortValue) int() int64 { return int64(v.bits) } //nolint:gosec // bit-preserving storage

func (v SortValue) float() float64 { return math.Float64frombits(v.bits) }

func (v SortValue) bool() bool { return v.bits != 0 }

func (v SortValue) decimal() decimal.Decimal {
	d, _ := v.ref.(decimal.Decimal) // kindDecimal holds one
	return d
}

// sortValueJSON is the JSON form of a [SortValue].
type sortValueJSON struct {
	D *string    `json:"d,omitempty"` // a decimal: plain digits, or coefficient "e" exponent beyond plainExponent
	S *string    `json:"s,omitempty"`
	N *int64     `json:"n,omitempty"`
	F *float64   `json:"f,omitempty"`
	B *bool      `json:"b,omitempty"`
	T *time.Time `json:"t,omitempty"`
	X *string    `json:"x,omitempty"` // a non-finite float: "NaN", "+Inf" or "-Inf"
	R *string    `json:"r,omitempty"` // a string that is not valid UTF-8, as unpadded URL-safe base64 of its bytes
	M *[2]int64  `json:"m,omitempty"` // epoch seconds and nanoseconds outside RFC 3339's years 0..9999
}

// timeSortValue applies the standard millisecond resolution without database bounds.
func timeSortValue(t time.Time) (SortValue, error) {
	return timeValue(t.UTC().Truncate(time.Millisecond)), nil
}

// rfc3339Year reports whether t's year is one RFC 3339 (and time.Time's JSON form) can write: 0 to 9999.
func rfc3339Year(t time.Time) bool {
	year := t.Year()
	return year >= 0 && year <= 9999
}

// maxSortValueHops bounds the pointers and interfaces [NewSortValue] follows, so a self-referencing or cyclic value fails instead of
// looping: the driver stores no value that deep.
const maxSortValueHops = 32

// Types a struct sort value may have.
var (
	timeType    = reflect.TypeFor[time.Time]()
	decimalType = reflect.TypeFor[decimal.Decimal]()
)

// NewSortValue returns a standard scalar. It follows up to 32 pointers or
// interfaces and unwraps named primitive types. Unsigned integers must fit
// int64. Timestamps default to UTC milliseconds; PositionCodec.Normalize
// accepts an explicit resolution. Decimal values are not limited to Decimal128.
// Unsupported values fail with ErrUnsortableValue.
func NewSortValue(v any) (SortValue, error) {
	switch x := v.(type) {
	case nil:
		return SortValue{}, fmt.Errorf("%w: no value", ErrUnsortableValue)
	case string:
		return stringSortValue(x), nil
	case int64:
		return intSortValue(x), nil
	case float64:
		return floatSortValue(x), nil
	case bool:
		return boolSortValue(x), nil
	case decimal.Decimal:
		return decimalSortValue(x)
	case time.Time:
		return timeSortValue(x)
	}

	rv := reflect.ValueOf(v)
	for hops := 0; rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface; hops++ {
		if rv.IsNil() {
			return SortValue{}, fmt.Errorf("%w: no value", ErrUnsortableValue)
		}

		if hops == maxSortValueHops {
			return SortValue{}, fmt.Errorf("%w: more than %d pointers or interfaces", ErrUnsortableValue, maxSortValueHops)
		}

		rv = rv.Elem()
	}

	switch rv.Kind() {
	case reflect.String:
		return stringSortValue(rv.String()), nil
	case reflect.Bool:
		return boolSortValue(rv.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return intSortValue(rv.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		u := rv.Uint()
		if u > math.MaxInt64 {
			return SortValue{}, fmt.Errorf("%w: integer above the int64 range", ErrUnsortableValue)
		}

		return intSortValue(int64(u)), nil
	case reflect.Float32, reflect.Float64:
		return floatSortValue(rv.Float()), nil
	case reflect.Struct:
		if t := rv.Type(); t == timeType || t == decimalType {
			return NewSortValue(rv.Interface())
		}
	default:
	}

	return SortValue{}, fmt.Errorf("%w: a value of type %T", ErrUnsortableValue, v)
}

// kindClasses maps each member kind to its comparison class.
var kindClasses = [...]SortClass{
	kindNone: SortClassNone, kindOpaque: SortClassNone, kindDecimal: SortClassNumber, kindString: SortClassString,
	kindInt: SortClassNumber, kindFloat: SortClassNumber, kindBool: SortClassBool, kindTime: SortClassTime,
}

// Class returns the built-in comparison class of v, or SortClassNone for an opaque value.
func (v SortValue) Class() SortClass { return kindClasses[v.kind] }

// Valid reports whether v holds a value: it is false only for the zero SortValue. Unlike [SortValue.Value] it does not box the
// value.
func (v SortValue) Valid() bool { return v.kind != kindNone }

// Value returns the value held by v in its storage-normalized Go form. The
// boolean is false for the zero SortValue.
func (v SortValue) Value() (any, bool) {
	switch v.kind {
	case kindOpaque, kindDecimal:
		return v.ref, true
	case kindString:
		return v.s, true
	case kindInt:
		return v.int(), true
	case kindFloat:
		return v.float(), true
	case kindBool:
		return v.bool(), true
	case kindTime:
		return v.t, true
	default:
		return nil, false
	}
}

// Compare returns -1, 0 or +1 as v orders before, with or after w in the built-in scalar order: by class ([SortClass]), then within
// the class. Numbers compare by their exact values across integers, floats and decimals (a float is not rounded to a decimal nor an
// integer to a float); a NaN orders before every other number and equals another NaN. Strings compare byte-wise, false orders before
// true, and times compare at their normalized precision.
func (v SortValue) Compare(w SortValue) int {
	if v.kind == w.kind { // the common case: no class lookup and no mixed-number comparison
		switch v.kind {
		case kindString:
			return strings.Compare(v.s, w.s)
		case kindInt:
			return cmp.Compare(v.int(), w.int())
		case kindFloat:
			return cmp.Compare(v.float(), w.float()) // NaN first, as MongoDB sorts it
		case kindBool:
			return cmp.Compare(v.bits, w.bits)
		case kindTime:
			return v.t.Compare(w.t)
		default:
		}
	}

	cv, cw := v.Class(), w.Class()
	if cv != cw {
		return cmp.Compare(cv, cw)
	}

	switch cv {
	case SortClassNumber:
		return compareSortNumbers(v, w)
	case SortClassString:
		return strings.Compare(v.s, w.s)
	case SortClassBool:
		return cmp.Compare(v.bits, w.bits)
	case SortClassTime:
		return v.t.Compare(w.t)
	default:
		return 0
	}
}

// compareSortNumbers compares two numbers exactly.
func compareSortNumbers(v, w SortValue) int {
	switch {
	case v.kind == kindInt && w.kind == kindInt:
		return cmp.Compare(v.int(), w.int())
	case v.kind == kindFloat && w.kind == kindFloat:
		return cmp.Compare(v.float(), w.float()) // NaN first, as MongoDB sorts it
	case v.kind == kindDecimal && w.kind == kindDecimal:
		vd, wd := v.decimal(), w.decimal()
		if c := cmp.Compare(vd.Sign(), wd.Sign()); c != 0 {
			return c
		}

		if c := cmp.Compare(decimalMagnitude(vd), decimalMagnitude(wd)); c != 0 {
			return vd.Sign() * c
		}

		return vd.Cmp(wd)
	}

	vNaN, wNaN := v.kind == kindFloat && math.IsNaN(v.float()), w.kind == kindFloat && math.IsNaN(w.float())
	switch {
	case vNaN || wNaN:
		return cmp.Compare(boolRank(!vNaN), boolRank(!wNaN))
	case v.kind == kindFloat && math.IsInf(v.float(), 0):
		return int(math.Copysign(1, v.float()))
	case w.kind == kindFloat && math.IsInf(w.float(), 0):
		return -int(math.Copysign(1, w.float()))
	case v.kind == kindInt && w.kind == kindFloat && exactFloat(v.int()):
		return cmp.Compare(float64(v.int()), w.float())
	case v.kind == kindFloat && w.kind == kindInt && exactFloat(w.int()):
		return cmp.Compare(v.float(), float64(w.int()))
	}
	// A decimal outside the finite float/int magnitude range can be ordered
	// without expanding its exponent into a potentially enormous rational.
	if v.kind == kindDecimal {
		if c, ok := compareExtremeDecimal(v.decimal(), w); ok {
			return c
		}
	}

	if w.kind == kindDecimal {
		if c, ok := compareExtremeDecimal(w.decimal(), v); ok {
			return -c
		}
	}

	return v.rat().Cmp(w.rat())
}

func decimalMagnitude(d decimal.Decimal) int64 {
	return int64(len(d.Coefficient().Abs(d.Coefficient()).String())) + int64(d.Exponent())
}

func compareExtremeDecimal(d decimal.Decimal, other SortValue) (int, bool) {
	sign := 0
	if other.kind == kindInt {
		sign = cmp.Compare(other.int(), int64(0))
	} else {
		sign = cmp.Compare(other.float(), float64(0))
	}

	if c := cmp.Compare(d.Sign(), sign); c != 0 {
		return c, true
	}

	if sign == 0 {
		return 0, true
	}

	magnitude := decimalMagnitude(d)

	const maxFloatDecimalMagnitude = 309
	if magnitude > maxFloatDecimalMagnitude {
		return sign, true
	}

	if magnitude < -324 {
		return -sign, true
	}

	return 0, false
}

// exactFloat reports whether n converts to a float64 exactly.
func exactFloat(n int64) bool {
	const exact = 1 << 53
	return n >= -exact && n <= exact
}

// rat returns the exact value of a finite number.
func (v SortValue) rat() *big.Rat {
	switch v.kind {
	case kindInt:
		return new(big.Rat).SetInt64(v.int())
	case kindFloat:
		return new(big.Rat).SetFloat64(v.float())
	default:
		return v.decimal().Rat()
	}
}

// boolRank orders false before true.
func boolRank(b bool) int {
	if b {
		return 1
	}

	return 0
}

// plainExponent bounds the exponents a decimal is written out in plain digits ("1.25", as decimal.Decimal marshals); beyond it the
// text is coefficient "e" exponent ("1e6111"), so a token never expands a large exponent into its digits.
const plainExponent = 32

// decimalText returns the JSON text of a decimal: its plain digits for an exponent within plainExponent, else its scientific form.
func decimalText(d decimal.Decimal) string {
	if exp := d.Exponent(); exp >= -plainExponent && exp <= plainExponent {
		return d.String()
	}

	return d.Coefficient().String() + "e" + strconv.Itoa(int(d.Exponent()))
}

// decimalSortValue preserves decimal values without imposing a database range.
func decimalSortValue(d decimal.Decimal) (SortValue, error) {
	if d.IsZero() {
		d = decimal.Zero
	}

	return SortValue{ref: d, kind: kindDecimal}, nil
}

// nonFiniteName returns the JSON spelling of a non-finite float.
func nonFiniteName(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case f > 0:
		return "+Inf"
	default:
		return "-Inf"
	}
}

// nonFiniteValue parses the JSON spelling of a non-finite float.
func nonFiniteValue(name string) (float64, bool) {
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

// emptyObject reports whether data is a JSON object without members.
func emptyObject(data []byte) bool {
	var members map[string]json.RawMessage

	return json.Unmarshal(data, &members) == nil && members != nil && len(members) == 0
}

// UnmarshalJSON decodes an object with exactly one member, as [SortValue.MarshalJSON] encodes it, or the empty object
// of the zero SortValue.
func (v *SortValue) UnmarshalJSON(data []byte) error {
	var w sortValueJSON
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}

	set := 0

	for _, isSet := range [...]bool{w.D != nil, w.S != nil, w.N != nil, w.F != nil, w.B != nil, w.T != nil, w.X != nil, w.R != nil, w.M != nil} {
		if isSet {
			set++
		}
	}

	if set == 0 && emptyObject(data) {
		*v = SortValue{}

		return nil
	}

	if set != 1 {
		return fmt.Errorf("%w: a sort value holds %d values", ErrUnsortableValue, set)
	}

	switch {
	case w.D != nil:
		d, err := decimal.NewFromString(*w.D)
		if err != nil {
			return fmt.Errorf("%w: a malformed decimal", ErrUnsortableValue)
		}

		out, err := decimalSortValue(d)
		if err != nil {
			return err
		}

		*v = out
	case w.S != nil:
		*v = stringSortValue(*w.S)
	case w.R != nil:
		raw, err := base64.RawURLEncoding.DecodeString(*w.R)
		if err != nil {
			return fmt.Errorf("%w: a malformed raw string", ErrUnsortableValue)
		}

		*v = stringSortValue(string(raw))
	case w.N != nil:
		*v = intSortValue(*w.N)
	case w.F != nil:
		*v = floatSortValue(*w.F)
	case w.X != nil:
		f, ok := nonFiniteValue(*w.X)
		if !ok {
			return fmt.Errorf("%w: a non-finite float spelled %q", ErrUnsortableValue, *w.X)
		}

		*v = floatSortValue(f)
	case w.B != nil:
		*v = boolSortValue(*w.B)
	case w.T != nil:
		*v = timeValue(*w.T)
	default: // w.M
		if w.M[1] < 0 || w.M[1] >= int64(time.Second) {
			return ErrUnsortableValue
		}

		*v = timeValue(time.Unix(w.M[0], w.M[1]).UTC())
	}

	return nil
}

// Position holds the sort keys and a typed unique tie-breaker. It encodes as
// {"k": [values], "i": id}; page a slice by it with NewSlicePager and ComparePositions.
type Position struct {
	// Keys are the values of the sort keys, in key order.
	Keys []SortValue `json:"k"`
	// ID is the unique tie-breaker, with the same scalar representation as Keys.
	ID SortValue `json:"i"`
}

// UnmarshalJSON decodes a position with a required typed identity.
func (p *Position) UnmarshalJSON(data []byte) error {
	type wire Position

	var v wire
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}

	if !v.ID.Valid() {
		return fmt.Errorf("%w: missing identity", ErrUnsortableValue)
	}

	*p = Position(v)

	return nil
}

// ComparePositions returns the comparison of positions under a sort whose keys are descending where desc says so, followed by the
// id tie-breaker, descending when idDesc is set. comparator defines scalar order.
// Positions with another number of keys still compare
// deterministically: the common keys first, then the shorter position first, then the id.
func ComparePositions(desc []bool, idDesc bool, comparator ValueComparator) func(a, b Position) int {
	desc = append([]bool(nil), desc...)

	return func(a, b Position) int {
		for i := range min(len(a.Keys), len(b.Keys), len(desc)) {
			if c := comparator.Compare(a.Keys[i], b.Keys[i]); c != 0 {
				if desc[i] {
					return -c
				}

				return c
			}
		}

		if c := cmp.Compare(len(a.Keys), len(b.Keys)); c != 0 {
			return c
		}

		c := comparator.Compare(a.ID, b.ID)
		if idDesc {
			return -c
		}

		return c
	}
}
