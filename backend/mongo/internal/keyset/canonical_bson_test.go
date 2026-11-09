// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package keyset

import (
	"bytes"
	"math/rand/v2"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// TestCanonicalFilterKeepsBSONTypes checks, for filters of every shape ValidateFilter admits, that the canonical filter encodes to
// the same BSON as the original: the same type at every position, documents compared without their key order, arrays in order.
func TestCanonicalFilterKeepsBSONTypes(t *testing.T) {
	t.Parallel()

	r := rand.New(rand.NewPCG(7, 11)) //nolint:gosec // a reproducible shape generator, not a secret

	for i := range 3000 {
		g := shapeGen{r: r}
		filter := bson.D{{Key: "f", Value: g.value(0)}, {Key: "g", Value: g.value(0)}}
		require.NoError(t, ValidateFilter(t.Context(), filter, nil), "shape %d is admitted: %#v", i, filter)

		requireSameBSON(t, filter, mustCanonical(t, filter), "shape %d: %#v", i, filter)
	}
}

// TestCanonicalFilterKeepsElementSlicesDocuments checks a []bson.E, which the driver encodes as a document, stays a document when
// a map below it is replaced: at the top of a value, in a list, in a list of lists, and as an operator document.
func TestCanonicalFilterKeepsElementSlicesDocuments(t *testing.T) {
	t.Parallel()

	nested := []bson.E{{Key: "nested", Value: bson.M{"a": 1, "b": 2}}}
	operators := []bson.E{{Key: "score", Value: bson.M{"$gte": 1, "$lt": 10}}}

	for name, filter := range map[string]bson.D{
		"value":              {{Key: "x", Value: nested}},
		"operator document":  {{Key: "$and", Value: bson.A{operators}}},
		"in an untyped list": {{Key: "$or", Value: []any{nested, operators}}},
		"in a list of lists": {{Key: "$or", Value: [][]bson.E{nested, operators}}},
		"as a list element":  {{Key: "$nor", Value: bson.A{bson.D{{Key: "y", Value: nested}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.NoError(t, ValidateFilter(t.Context(), filter, nil))

			canonical := mustCanonical(t, filter)
			requireSameBSON(t, filter, canonical)

			raw, err := bson.Marshal(canonical)
			require.NoError(t, err)

			again, err := bson.Marshal(mustCanonical(t, filter))
			require.NoError(t, err)
			require.Equal(t, raw, again, "the canonical filter encodes to the same bytes")
		})
	}

	raw, err := bson.Marshal(mustCanonical(t, bson.D{{Key: "$and", Value: bson.A{operators}}}))
	require.NoError(t, err)

	want, err := bson.Marshal(bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "score", Value: bson.D{{Key: "$gte", Value: 1}, {Key: "$lt", Value: 10}}}}}}})
	require.NoError(t, err)
	require.Equal(t, bson.Raw(want), bson.Raw(raw))
}

// requireSameBSON fails unless a and b encode to the same BSON, documents compared without their key order.
func requireSameBSON(tb testing.TB, a, b bson.D, msgAndArgs ...any) {
	tb.Helper()

	ra, err := bson.Marshal(a)
	require.NoError(tb, err, msgAndArgs...)

	rb, err := bson.Marshal(b)
	require.NoError(tb, err, msgAndArgs...)

	va := bson.RawValue{Type: bson.TypeEmbeddedDocument, Value: ra}
	vb := bson.RawValue{Type: bson.TypeEmbeddedDocument, Value: rb}
	require.True(tb, sameBSON(va, vb), append([]any{"%s\n!=\n%s"}, bson.Raw(ra), bson.Raw(rb))...)
}

// sameBSON reports whether a and b are the same BSON value: the same type, documents holding the same keys with the same values
// in any order, arrays the same values in order, other values the same bytes.
func sameBSON(a, b bson.RawValue) bool {
	if a.Type != b.Type {
		return false
	}

	switch a.Type { //nolint:exhaustive // every other type compares by its bytes
	case bson.TypeEmbeddedDocument:
		ea, errA := bson.Raw(a.Value).Elements()
		eb, errB := bson.Raw(b.Value).Elements()

		if errA != nil || errB != nil || len(ea) != len(eb) {
			return false
		}

		byKey := make(map[string]bson.RawValue, len(eb))
		for _, e := range eb {
			byKey[e.Key()] = e.Value()
		}

		for _, e := range ea {
			v, ok := byKey[e.Key()]
			if !ok || !sameBSON(e.Value(), v) {
				return false
			}
		}

		return true
	case bson.TypeArray:
		va, errA := bson.RawArray(a.Value).Values()
		vb, errB := bson.RawArray(b.Value).Values()

		if errA != nil || errB != nil || len(va) != len(vb) {
			return false
		}

		for i := range va {
			if !sameBSON(va[i], vb[i]) {
				return false
			}
		}

		return true
	default:
		return bytes.Equal(a.Value, b.Value)
	}
}

// shapeGen generates filter values of every shape ValidateFilter admits, with distinct keys in each document and maps shared by
// several references.
type shapeGen struct {
	r *rand.Rand
}

// maxShapeDepth bounds the nesting of a generated value.
const maxShapeDepth = 4

// value returns a random admitted value depth levels deep.
func (g shapeGen) value(depth int) any {
	if depth >= maxShapeDepth || g.r.IntN(4) == 0 {
		return g.leaf()
	}

	switch g.r.IntN(17) {
	case 0:
		return g.d(depth)
	case 1:
		return []bson.E(g.d(depth))
	case 2:
		return bson.M(g.m(depth))
	case 3:
		return g.m(depth)
	case 4:
		return bson.A(g.list(depth))
	case 5:
		return g.list(depth)
	case 6:
		return []bson.D{g.d(depth), g.d(depth)}
	case 7:
		return []bson.M{g.m(depth), g.m(depth)}
	case 8:
		return []map[string]any{g.m(depth), g.m(depth)}
	case 9:
		return [][]any{g.list(depth), g.list(depth)}
	case 10:
		return [][]bson.E{g.d(depth), g.d(depth)}
	case 11:
		return []bson.A{g.list(depth)}
	case 12:
		return bson.E{Key: "e", Value: g.value(depth + 1)}
	case 13:
		raw, err := bson.Marshal(bson.M{"r": int32(g.r.IntN(9))})
		if err != nil {
			panic(err)
		}

		return bson.Raw(raw)
	case 14:
		m := g.m(depth)

		return []any{m, bson.A{m}, bson.M(m)} // one map shared, also under another type
	case 15:
		m := bson.M(g.m(depth))

		return []bson.M{m, m}
	default:
		return []string{"x", "y"}
	}
}

// leaf returns a random scalar.
func (g shapeGen) leaf() any {
	switch g.r.IntN(7) {
	case 0:
		return int32(g.r.IntN(100))
	case 1:
		return int64(g.r.IntN(100))
	case 2:
		return g.r.Float64()
	case 3:
		return "s" + strconv.Itoa(g.r.IntN(100))
	case 4:
		return g.r.IntN(2) == 0
	case 5:
		return nil
	default:
		return []int32{1, 2}
	}
}

// keys returns up to four distinct keys.
func (g shapeGen) keys() []string {
	all := []string{"a", "b", "$gt", "$lt", "c", "$in"}
	g.r.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })

	return all[:g.r.IntN(5)]
}

// d returns a bson.D of distinct keys.
func (g shapeGen) d(depth int) bson.D {
	keys := g.keys()
	d := make(bson.D, 0, len(keys))

	for _, k := range keys {
		d = append(d, bson.E{Key: k, Value: g.value(depth + 1)})
	}

	return d
}

// m returns a map of distinct keys.
func (g shapeGen) m(depth int) map[string]any {
	keys := g.keys()
	m := make(map[string]any, len(keys))

	for _, k := range keys {
		m[k] = g.value(depth + 1)
	}

	return m
}

// list returns a []any of up to three values.
func (g shapeGen) list(depth int) []any {
	out := make([]any, g.r.IntN(4))
	for i := range out {
		out[i] = g.value(depth + 1)
	}

	return out
}
