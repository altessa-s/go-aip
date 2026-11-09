// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"bytes"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-atlas-core/types/optional"
)

func BenchmarkOptionalEncode(b *testing.B) {
	var out bytes.Buffer

	enc := bson.NewEncoder(bson.NewDocumentWriter(&out))
	enc.SetRegistry(testRegistry())

	value := struct {
		Value optional.Optional[int] `bson:"value"`
	}{optional.Some(42)}

	for b.Loop() {
		out.Reset()
		require.NoError(b, enc.Encode(value))
	}
}

func BenchmarkDecimalRoundTrip(b *testing.B) {
	in := struct {
		Amount decimal.Decimal `bson:"amount"`
	}{Amount: decimal.RequireFromString("12345.6789")}

	b.ReportAllocs()

	for b.Loop() {
		raw, err := marshal(in)
		if err != nil {
			b.Fatal(err)
		}

		var out struct {
			Amount decimal.Decimal `bson:"amount"`
		}
		if err := unmarshal(raw, &out); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCivilDateRoundTrip(b *testing.B) {
	in := struct {
		Day civilDate `bson:"day"`
	}{Day: civilDate{Year: 2026, Month: 10, Day: 9}}

	b.ReportAllocs()

	for b.Loop() {
		raw, err := marshal(in)
		if err != nil {
			b.Fatal(err)
		}

		var out struct {
			Day civilDate `bson:"day"`
		}
		if err := unmarshal(raw, &out); err != nil {
			b.Fatal(err)
		}
	}
}
