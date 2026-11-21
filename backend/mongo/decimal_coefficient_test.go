// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"math/big"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-atlas-core/types/optional"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
)

// coefficientFailFast is the most refusing an oversized coefficient may take: dividing it by ten digit by digit, as the driver
// does, takes far longer.
const coefficientFailFast = time.Second

// oneWithTrailingZeros returns 1 written as 10^digits × 10^-digits: it fits a Decimal128 once its trailing zeros fold, but its
// coefficient has digits+1 digits.
func oneWithTrailingZeros(digits int64) decimal.Decimal {
	return decimal.NewFromBigInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(digits), nil), -int32(digits))
}

// TestScopeRefusesOversizedDecimals checks a scope holding a decimal with an oversized coefficient fails before it is encoded,
// as a pin or beside one, in every position.
func TestScopeRefusesOversizedDecimals(t *testing.T) {
	t.Parallel()

	l := newWidgets(t, listingtest.NewSchema(t), mongo.WithListScopeKeys("owner"))
	huge := oneWithTrailingZeros(200_000)

	for name, v := range map[string]any{
		"decimal":      huge,
		"null decimal": decimal.NullDecimal{Decimal: huge, Valid: true},
		"optional":     optional.Some(huge),
		"slice":        []decimal.Decimal{huge},
		"operator":     bson.D{{Key: "$in", Value: []decimal.NullDecimal{{Decimal: huge, Valid: true}}}},
	} {
		for key, scope := range map[string]bson.D{
			"pin":    {{Key: "owner", Value: v}},
			"beside": {{Key: "owner", Value: "owner-1"}, {Key: "amount", Value: v}},
		} {
			t.Run(name+"/"+key, func(t *testing.T) {
				t.Parallel()

				start := time.Now()
				_, err := l.List(t.Context(), listingtest.Subject, listing.Request{}, scope)
				require.ErrorIs(t, err, mongo.ErrScope)
				requireQuick(t, time.Since(start), coefficientFailFast)
			})
		}
	}
}
