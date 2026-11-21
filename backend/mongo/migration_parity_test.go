// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-atlas-core/encoding/keys"
	"github.com/altessa-s/go-atlas-core/time/civil"
	"github.com/altessa-s/go-atlas-core/types/optional"
	"github.com/altessa-s/go-mongox/bsonfilter"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

// TestMigrationCodecBytes checks the registry encodes dates, optionals and decimals to the bytes the pre-migration codecs wrote.
func TestMigrationCodecBytes(t *testing.T) {
	t.Parallel()

	type cloudDate struct {
		Year  int
		Month time.Month
		Day   int
	}

	reg := mongo.NewRegistry()
	require.NoError(t, mongo.RegisterDateType[cloudDate](reg))

	d := decimal.RequireFromString("12.50")
	doc := bson.D{
		{Key: "cloud", Value: cloudDate{2024, time.February, 29}},
		{Key: "cloudZero", Value: cloudDate{}},
		{Key: "atlas", Value: civil.Date{Year: 2024, Month: time.February, Day: 29}},
		{Key: "atlasZero", Value: civil.Date{}},
		{Key: "some", Value: optional.Some(d)},
		{Key: "none", Value: optional.None[int]()},
		{Key: "ptr", Value: &d},
		{Key: "null", Value: decimal.NullDecimal{}},
		{Key: "i32", Value: int32(42)},
		{Key: "i64", Value: int64(42)},
	}

	var got bytes.Buffer

	enc := bson.NewEncoder(bson.NewDocumentWriter(&got))
	enc.SetRegistry(reg)
	require.NoError(t, enc.Encode(doc))

	want, err := os.ReadFile("testdata/codecs.bson")
	require.NoError(t, err)
	require.Equal(t, want, got.Bytes())
}

func readMigrationJSON(tb testing.TB, path string, dest any) {
	tb.Helper()

	data, err := os.ReadFile(path)
	require.NoError(tb, err)
	require.NoError(tb, json.Unmarshal(data, dest))
}

// bindingCapture stops List after the engine has prepared the predicate, before any database I/O.
type bindingCapture struct {
	pagetoken.Signer
	bindings pagetoken.Bindings
}

var errBindingsCaptured = errors.New("captured token bindings")

func (c *bindingCapture) Resolve(_ string, bindings pagetoken.Bindings) ([]byte, error) {
	c.bindings = bindings
	return nil, errBindingsCaptured
}

func migrationBindings(tb testing.TB, scope bson.D) (pagetoken.Bindings, error) {
	tb.Helper()

	fields := []listing.Field{{Name: "rank", Kind: filter.FieldKindInt}, {Name: "seq", Kind: filter.FieldKindInt}}
	schema, err := listing.NewSchema(fields, []string{"rank desc, seq"}, listing.NewProfile())
	require.NoError(tb, err)

	capture := &bindingCapture{}
	engine, err := mongo.NewListEngine[bson.D](nil, capture, schema,
		mongo.WithListFieldPaths(map[string]string{"seq": "identity.seq"}), mongo.WithListTiebreakPath("identity.seq"))
	require.NoError(tb, err)

	_, err = engine.List(tb.Context(), "fixture-reader", listing.Request{PageToken: "capture"}, scope)
	if !errors.Is(err, errBindingsCaptured) {
		return pagetoken.Bindings{}, err
	}

	// Atlas fixtures use a plain keyset subject. Assert the additional List bindings
	// before comparing the common sort, filter and precision to the historical tokens.
	require.Equal(tb, keys.Compose("fixture-reader", "", schema.ContractID())+"/precision=1ms", capture.bindings.Subject)
	capture.bindings.Subject = "fixture-reader/precision=1ms"

	return capture.bindings, nil
}

// TestMigrationRawFilters checks scope validation admits the pinned raw filter bytes unchanged and refuses the pinned bad inputs with
// the same sentinels as before the migration.
func TestMigrationRawFilters(t *testing.T) {
	t.Parallel()

	var fixtures []struct {
		Name  string
		Hex   string `json:"bson_hex"`
		Error string
	}
	readMigrationJSON(t, "testdata/raw-filters.json", &fixtures)
	require.NotEmpty(t, fixtures)

	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			t.Parallel()

			raw, err := hex.DecodeString(f.Hex)
			require.NoError(t, err)
			// List accepts a bson.D scope. Place the raw filter in a document literal
			// so every byte, including its encoded length, reaches scope validation.
			b, err := migrationBindings(t, bson.D{{Key: "literal", Value: bson.Raw(raw)}})

			switch f.Error {
			case "":
				require.NoError(t, err)
				require.Equal(t, bson.Raw(raw), bson.Raw(b.Filter).Lookup("f", "literal").Document())
			case "ErrDangerousOperator":
				require.ErrorIs(t, err, mongo.ErrScope)
				require.ErrorIs(t, err, bsonfilter.ErrDangerousOperator)
			case "ErrValueType":
				require.ErrorIs(t, err, mongo.ErrScope)
				require.ErrorIs(t, err, bsonfilter.ErrValueType)
			case "ErrMalformedRaw":
				require.ErrorIs(t, err, mongo.ErrScope)
				require.ErrorIs(t, err, bsonfilter.ErrMalformedRaw)
			default:
				t.Fatalf("unknown fixture error %q", f.Error)
			}
		})
	}
}

// TestMigrationSignedTokens checks tokens signed before the migration resolve and reissue byte-identically, and a token bound to
// a differently ordered embedded document is refused as a changed filter.
func TestMigrationSignedTokens(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	codec, err := pagetoken.New(bytes.Repeat([]byte{0x42}, 32), pagetoken.WithClock(func() time.Time { return now }))
	require.NoError(t, err)
	canonical, err := migrationBindings(t, bson.D{{Key: "meta", Value: bson.M{"b": int64(2), "a": int32(1)}}})
	require.NoError(t, err)

	for _, order := range []string{"sorted", "reversed"} {
		t.Run(order, func(t *testing.T) {
			t.Parallel()

			var f struct {
				Filter  string `json:"filter_hex"`
				Payload string `json:"payload_hex"`
				Sort    string `json:"sort_binding"`
				Subject string `json:"subject_binding"`
				Token   string
			}
			readMigrationJSON(t, "testdata/tokens-"+order+".json", &f)

			meta := bson.D{{Key: "a", Value: int32(1)}, {Key: "b", Value: int64(2)}}
			if order == "reversed" {
				meta[0], meta[1] = meta[1], meta[0]
			}

			b, err := migrationBindings(t, bson.D{{Key: "meta", Value: meta}})
			require.NoError(t, err)
			require.Equal(t, f.Sort, b.Sort)
			require.Equal(t, f.Subject, b.Subject)
			require.Equal(t, f.Filter, hex.EncodeToString(bson.Raw(b.Filter).Lookup("f").Document()))
			payload, err := codec.Resolve(f.Token, b)
			require.NoError(t, err)
			require.Equal(t, f.Payload, hex.EncodeToString(payload))
			reissued, err := codec.Issue(payload, b)
			require.NoError(t, err)
			require.Equal(t, f.Token, reissued)

			got, err := codec.Resolve(f.Token, canonical)
			if order == "reversed" {
				require.ErrorIs(t, err, pagetoken.ErrFilterChanged)
				return
			}

			require.NoError(t, err)
			require.Equal(t, payload, got)
		})
	}
}
