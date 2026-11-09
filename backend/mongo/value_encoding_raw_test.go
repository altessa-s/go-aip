// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"reflect"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/x/bsonx/bsoncore"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

// malformedRaws are raw documents whose lengths the driver trusts and slices out of range by.
func malformedRaws() map[string][]byte {
	return map[string][]byte{
		"zero length":         {0, 0, 0, 0},
		"length below header": {3, 0, 0, 0, 0},
		"length beyond bytes": {9, 0, 0, 0, 0},
		"negative length":     {0xff, 0xff, 0xff, 0xff, 0},
		"overflowing length":  {0xff, 0xff, 0xff, 0x7f, 0},
		"truncated element":   {9, 0, 0, 0, 0x10, 'a', 0, 1, 0},
		"bad nested document": {12, 0, 0, 0, 0x03, 'a', 0, 0, 0, 0, 0, 0},
		"bad nested string":   {13, 0, 0, 0, 0x02, 'a', 0, 0, 0, 0, 0, 0, 0},
	}
}

// rawDocHook encodes itself as the raw document it holds, the second time and later as bad.
type rawDocHook struct {
	good, bad []byte
	calls     *atomic.Int32
}

func (h rawDocHook) MarshalBSON() ([]byte, error) {
	if h.calls != nil && h.calls.Add(1) > 1 {
		return h.bad, nil
	}

	return h.good, nil
}

// rawValueHook encodes itself as a document of the raw bytes it holds.
type rawValueHook struct{ data []byte }

func (h rawValueHook) MarshalBSONValue() (byte, []byte, error) {
	return byte(bson.TypeEmbeddedDocument), h.data, nil
}

type rawHolder struct {
	Raw   bson.Raw          `bson:"raw"`
	Value bson.RawValue     `bson:"value"`
	Doc   bsoncore.Document `bson:"doc"`
}

// TestValueEncoding_MalformedRaw: review finding R10-F2 — raw BSON reached the driver's encoders unchecked: a bson.Raw of length
// zero passed the walk as a byte slice, and the driver, which trusts the lengths inside raw BSON, sliced out of range and
// panicked. Raw BSON — a bson.Raw, a bson.RawValue, a bsoncore.Document or Array, at the top level or nested, and what a value
// encoding itself returns — is checked before the driver reads it, and a value holding a malformed one is opaque.
func TestValueEncoding_MalformedRaw(t *testing.T) {
	t.Parallel()

	encodings := map[string]*mongo.BSONEncoding{
		"default":  mongo.ValueEncoding(),
		"registry": mongo.ValueEncoding(mongo.WithValueEncodingRegistry(mongo.NewRegistry())),
	}

	for name, raw := range malformedRaws() {
		values := map[string]any{
			"bson.Raw":            bson.Raw(raw),
			"pointer":             new(bson.Raw(raw)),
			"bson.RawValue":       bson.RawValue{Type: bson.TypeEmbeddedDocument, Value: raw},
			"array RawValue":      bson.RawValue{Type: bson.TypeArray, Value: raw},
			"bsoncore.Document":   bsoncore.Document(raw),
			"bsoncore.Array":      bsoncore.Array(raw),
			"struct field":        rawHolder{Raw: raw},
			"struct RawValue":     rawHolder{Value: bson.RawValue{Type: bson.TypeEmbeddedDocument, Value: raw}},
			"struct core":         rawHolder{Doc: raw},
			"in a bson.D":         bson.D{{Key: "r", Value: bson.Raw(raw)}},
			"in a typed slice":    []bson.Raw{raw},
			"in a typed map":      map[string]bson.Raw{"r": raw},
			"bson.Marshaler":      rawDocHook{good: raw},
			"bson.ValueMarshaler": rawValueHook{data: raw},
		}

		for kind, v := range values {
			for encName, enc := range encodings {
				label := name + "/" + kind + "/" + encName

				want := v
				if p, ok := v.(*bson.Raw); ok {
					want = *p // the pointer is followed to the opaque value
				}

				require.NotPanics(t, func() {
					// A document read without encoding it (a bson.D, a typed map) holds the raw value, opaque when it is read.
					if got := deepStored(enc, v); !reflect.DeepEqual(want, got) {
						require.True(t, holdsRaw(got), "%s: a value holding malformed raw BSON is opaque: %#v", label, got)
					}
				}, label)
			}
		}
	}
}

// TestValueEncoding_MalformedRawEvaluated evaluates filters over data holding malformed raw BSON: the evaluation returns, the
// values are opaque, and a well-formed raw document beside them still reads.
func TestValueEncoding_MalformedRawEvaluated(t *testing.T) {
	t.Parallel()

	good, err := bson.Marshal(bson.D{{Key: "n", Value: int32(1)}})
	require.NoError(t, err)

	for _, enc := range []*mongo.BSONEncoding{
		mongo.ValueEncoding(),
		mongo.ValueEncoding(mongo.WithValueEncodingRegistry(mongo.NewRegistry())),
	} {
		eval, err := newEvaluator(filtereval.WithValueEncoding(enc), filtereval.WithProfile(mongo.DefaultSchema()))
		require.NoError(t, err)

		for name, raw := range malformedRaws() {
			data := map[string]any{
				"bad":  bson.Raw(raw),
				"held": rawHolder{Raw: raw, Value: bson.RawValue{Type: bson.TypeEmbeddedDocument, Value: raw}},
				"good": bson.Raw(good),
			}

			for expr, want := range map[string]bool{
				"bad.n == 1":      false,
				"held.raw.n == 1": false,
				"bad != 1":        true,
				"good.n == 1":     true,
			} {
				require.NotPanics(t, func() {
					got, err := eval.Evaluate(filtertest.MustParseFilter(t, expr), data)
					require.NoError(t, err, name+": "+expr)
					require.Equal(t, want, got, name+": "+expr)
				}, name+": "+expr)
			}
		}
	}
}

// TestValueEncoding_HookOutputChecked: a value encoding itself is called again when the driver encodes it, and may return
// malformed raw BSON the second time; the encoding refuses it before the driver copies it ([NewRegistry]), and a panic of the
// driver an application registry runs into makes the value opaque.
func TestValueEncoding_HookOutputChecked(t *testing.T) {
	t.Parallel()

	good, err := bson.Marshal(bson.D{{Key: "n", Value: int32(1)}})
	require.NoError(t, err)

	for name, enc := range map[string]*mongo.BSONEncoding{
		"default":  mongo.ValueEncoding(),
		"registry": mongo.ValueEncoding(mongo.WithValueEncodingRegistry(mongo.NewRegistry())),
	} {
		for rawName, raw := range malformedRaws() {
			v := rawDocHook{good: good, bad: raw, calls: new(atomic.Int32)}

			require.NotPanics(t, func() {
				got := filtereval.StoredValue(v, enc, openBudget{})
				require.Equal(t, v, got, "%s/%s", name, rawName)
			}, name+"/"+rawName)
		}
	}
}

// panickingEncoder is an application encoder that panics, as a driver encoder reading malformed raw BSON does.
func panickingEncoder(bson.EncodeContext, bson.ValueWriter, reflect.Value) error {
	panic("encoder panic")
}

type panicky struct{ N int }

// TestValueEncoding_DriverPanicRecovered: a panic inside the driver's encoding through an application registry makes the value
// opaque instead of crashing the evaluation.
func TestValueEncoding_DriverPanicRecovered(t *testing.T) {
	t.Parallel()

	registry := bson.NewRegistry()
	registry.RegisterTypeEncoder(reflect.TypeFor[panicky](), bson.ValueEncoderFunc(panickingEncoder))

	enc := mongo.ValueEncoding(mongo.WithValueEncodingRegistry(registry))
	v := panicky{N: 1}

	require.NotPanics(t, func() {
		require.Equal(t, v, enc.Encode(v, openBudget{}))
	})

	eval, err := newEvaluator(filtereval.WithValueEncoding(enc), filtereval.WithProfile(mongo.DefaultSchema()))
	require.NoError(t, err)

	got, err := eval.Evaluate(filtertest.MustParseFilter(t, "v.n == 1"), map[string]any{"v": v})
	require.NoError(t, err)
	require.False(t, got)
}

// holdsRaw reports whether v, a stored form read at every depth, holds a raw BSON value of the driver, left opaque.
func holdsRaw(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		for _, value := range x {
			if holdsRaw(value) {
				return true
			}
		}

		return false
	case []any:
		return slices.ContainsFunc(x, holdsRaw)
	case bson.Raw, *bson.Raw, bson.RawValue, bsoncore.Document, bsoncore.Array:
		return true
	default:
		return false
	}
}
