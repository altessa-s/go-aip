// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
)

func TestDefaultDecimalPolicy(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"one", "rejected"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			schema, err := listing.NewSchema([]listing.Field{{Name: "cost", Kind: filter.FieldKindDecimal}}, []string{"cost"}, listing.NewProfile(
				listing.WithDefaultEqualities(map[string]any{"cost": value}),
				listing.WithFilterOptions(filter.WithDecimalParser(func(n filter.Node, v any) (decimal.Decimal, error) {
					if v == "one" {
						return decimal.NewFromInt(1), nil
					}

					if v == "rejected" {
						return decimal.Decimal{}, filter.ErrFieldTypeMismatch
					}

					return filter.DecimalOperand(n, v)
				}))))
			if value == "rejected" {
				require.ErrorIs(t, err, listing.ErrInvalidSchema)
				return
			}

			require.NoError(t, err)
			_, err = schema.Compile(t.Context(), listing.Request{})
			require.NoError(t, err)
		})
	}
}

type defaultStringCodec struct{}

func (defaultStringCodec) ValidateLiteral(v any) error {
	if _, ok := v.(string); !ok {
		return filter.ErrFieldTypeMismatch
	}

	return nil
}

func TestDefaultCustomKind(t *testing.T) {
	t.Parallel()

	const kind filter.FieldKind = 100

	schema, err := listing.NewSchema([]listing.Field{{Name: "value", Kind: kind}}, []string{"value"}, listing.NewProfile(listing.WithSortFieldValidator(nil), listing.WithDefaultEqualities(map[string]any{"value": "custom"}), listing.WithFilterOptions(filter.WithValueCodecs(map[filter.FieldKind]filter.ValueCodec{kind: defaultStringCodec{}}))))
	require.NoError(t, err)
	_, err = schema.Compile(t.Context(), listing.Request{})
	require.NoError(t, err)
}

func TestDefaultConverterValidation(t *testing.T) {
	t.Parallel()

	_, err := listing.NewSchema([]listing.Field{{Name: "value", Kind: filter.FieldKindString}}, []string{"value"}, listing.NewProfile(listing.WithDefaultEqualities(map[string]any{"value": "input"}), listing.WithDefaultLiteral(func(listing.Field, any) (any, error) { return 42, nil })))
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
	require.ErrorIs(t, err, filter.ErrFieldTypeMismatch)
}
