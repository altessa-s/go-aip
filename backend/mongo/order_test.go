// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
)

// mustOrderTranslator builds a translator and fails the test on any
// construction error. Keeps the success-path tests free of error-wiring
// noise; tests that exercise construction failures call NewTranslator
// directly.
func mustOrderTranslator(tb testing.TB, policy *orderby.Policy, opts ...orderby.TranslatorOption) *mongo.OrderTranslator {
	tb.Helper()

	tr, err := mongo.NewOrderTranslator(policy, mongo.WithOrderOptions(opts...))
	require.NoError(tb, err)

	return tr
}

func TestOrderTranslate_Multi(t *testing.T) {
	t.Parallel()
	ob := orderbytest.MustParseOrderBy(t, "create_time desc, slug")

	got, err := mustOrderTranslator(t, orderbytest.MustPolicy(t)).Translate(ob)
	require.NoError(t, err)
	require.Equal(t,
		bson.D{
			{Key: "create_time", Value: int32(-1)},
			{Key: "slug", Value: int32(1)},
		},
		got,
	)
}

func TestOrderTranslate_Empty(t *testing.T) {
	t.Parallel()
	got, err := mustOrderTranslator(t, orderbytest.MustPolicy(t)).Translate(orderby.Spec{})
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestOrderTranslate_FieldMapping(t *testing.T) {
	t.Parallel()
	ob := orderbytest.MustParseOrderBy(t, "createdAt desc")

	got, err := mustOrderTranslator(t, orderbytest.MustPolicy(t), orderby.WithFieldMapping(map[string]string{
		"createdAt": "created_at",
	})).Translate(ob)
	require.NoError(t, err)
	require.Equal(t,
		bson.D{{Key: "created_at", Value: int32(-1)}},
		got,
	)
}

func TestOrderTranslate_AllowedFields(t *testing.T) {
	t.Parallel()
	ob := orderbytest.MustParseOrderBy(t, "createdAt desc, secret")

	_, err := mustOrderTranslator(t, orderbytest.MustPolicy(t, orderby.WithAllowedFields("createdAt"))).Translate(ob)
	require.ErrorIs(t, err, orderby.ErrFieldNotAllowed)
}

// TestNewOrderTranslator_UntrustedInput_RequiresAllowlist locks the contract
// that an untrusted order cannot reach a translator without an allow-list:
// the policy refuses the configuration at construction, and a translator
// refuses to be built without a policy.
func TestNewOrderTranslator_UntrustedInput_RequiresAllowlist(t *testing.T) {
	t.Parallel()

	_, err := orderby.NewPolicy(orderby.WithUntrustedInput(true))
	require.ErrorIs(t, err, orderby.ErrAllowlistRequired)

	_, err = mongo.NewOrderTranslator(nil)
	require.ErrorIs(t, err, orderby.ErrPolicyRequired)

	_, err = mongo.NewOrderTranslator(orderbytest.MustPolicy(t,
		orderby.WithUntrustedInput(true),
		orderby.WithAllowedFields("createdAt"),
	))
	require.NoError(t, err)
}

// TestNewPolicy_UntrustedInput_EmptyAllowlist guards the
// misconfiguration path where WithAllowedFields was called with no
// arguments (or with an empty slice). The check has to happen at
// construction so the bug surfaces during boot, not on the first
// untrusted query.
func TestNewPolicy_UntrustedInput_EmptyAllowlist(t *testing.T) {
	t.Parallel()

	_, err := orderby.NewPolicy(
		orderby.WithUntrustedInput(true),
		orderby.WithAllowedFields(),
	)
	require.ErrorIs(t, err, orderby.ErrAllowlistRequired)

	var empty []string

	_, err = orderby.NewPolicy(
		orderby.WithUntrustedInput(true),
		orderby.WithAllowedFields(empty...),
	)
	require.ErrorIs(t, err, orderby.ErrAllowlistRequired)
}

func TestOrderTranslate_NestedField(t *testing.T) {
	t.Parallel()
	ob := orderbytest.MustParseOrderBy(t, "address.city asc")

	got, err := mustOrderTranslator(t, orderbytest.MustPolicy(t, orderby.WithAllowedFields("address.city"))).Translate(ob)
	require.NoError(t, err)
	require.Equal(t,
		bson.D{{Key: "address.city", Value: int32(1)}},
		got,
	)
}

// TestOrderTranslate_AllowedFieldsWildcard covers the `prefix.*` form of
// WithAllowedFields. The options are translator-agnostic, so exercising
// them through the Mongo translator is enough.
func TestOrderTranslate_AllowedFieldsWildcard(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		expr    string
		allow   []string
		wantErr error
		want    bson.D
	}{
		{
			name:  "subtree allowed via wildcard",
			expr:  "address.city desc",
			allow: []string{"address.*"},
			want:  bson.D{{Key: "address.city", Value: int32(-1)}},
		},
		{
			name:  "deep subtree allowed via wildcard",
			expr:  "address.zip.code asc",
			allow: []string{"address.*"},
			want:  bson.D{{Key: "address.zip.code", Value: int32(1)}},
		},
		{
			name:    "bare prefix rejected — wildcard does not cover it",
			expr:    "address asc",
			allow:   []string{"address.*"},
			wantErr: orderby.ErrFieldNotAllowed,
		},
		{
			name:    "sibling not covered",
			expr:    "billing.city asc",
			allow:   []string{"address.*"},
			wantErr: orderby.ErrFieldNotAllowed,
		},
		{
			name:  "bare star matches anything",
			expr:  "any.thing.here asc",
			allow: []string{"*"},
			want:  bson.D{{Key: "any.thing.here", Value: int32(1)}},
		},
		{
			name:  "mix of exact and wildcard",
			expr:  "createdAt desc, address.city asc",
			allow: []string{"createdAt", "address.*"},
			want: bson.D{
				{Key: "createdAt", Value: int32(-1)},
				{Key: "address.city", Value: int32(1)},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ob := orderbytest.MustParseOrderBy(t, tt.expr)

			got, err := mustOrderTranslator(t, orderbytest.MustPolicy(t, orderby.WithAllowedFields(tt.allow...))).Translate(ob)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestOrderTranslate_FieldPrefixMapping covers WithFieldPrefixMapping: longest
// prefix wins, exact mapping still overrides prefix, malformed entries
// (no trailing dot) are silently dropped.
func TestOrderTranslate_FieldPrefixMapping(t *testing.T) {
	t.Parallel()

	t.Run("rewrites leading segment", func(t *testing.T) {
		t.Parallel()
		ob := orderbytest.MustParseOrderBy(t, "address.city desc")
		got, err := mustOrderTranslator(t, orderbytest.MustPolicy(t), orderby.WithFieldPrefixMapping(map[string]string{"address.": "addr."})).Translate(ob)
		require.NoError(t, err)
		require.Equal(t, bson.D{{Key: "addr.city", Value: int32(-1)}}, got)
	})

	t.Run("rewrites deep path", func(t *testing.T) {
		t.Parallel()
		ob := orderbytest.MustParseOrderBy(t, "address.zip.code asc")
		got, err := mustOrderTranslator(t, orderbytest.MustPolicy(t), orderby.WithFieldPrefixMapping(map[string]string{"address.": "addr."})).Translate(ob)
		require.NoError(t, err)
		require.Equal(t, bson.D{{Key: "addr.zip.code", Value: int32(1)}}, got)
	})

	t.Run("longest prefix wins", func(t *testing.T) {
		t.Parallel()
		ob := orderbytest.MustParseOrderBy(t, "user.profile.name desc")
		got, err := mustOrderTranslator(t, orderbytest.MustPolicy(t), orderby.WithFieldPrefixMapping(map[string]string{
			"user.":         "users.",
			"user.profile.": "users.prof.",
		})).Translate(ob)
		require.NoError(t, err)
		require.Equal(t, bson.D{{Key: "users.prof.name", Value: int32(-1)}}, got)
	})

	t.Run("exact mapping overrides prefix", func(t *testing.T) {
		t.Parallel()
		ob := orderbytest.MustParseOrderBy(t, "address.city desc")
		got, err := mustOrderTranslator(t, orderbytest.MustPolicy(t), orderby.WithFieldMapping(map[string]string{"address.city": "addrCity"}), orderby.WithFieldPrefixMapping(map[string]string{"address.": "addr."})).Translate(ob)
		require.NoError(t, err)
		require.Equal(t, bson.D{{Key: "addrCity", Value: int32(-1)}}, got)
	})

	t.Run("entries without trailing dot are silently ignored", func(t *testing.T) {
		t.Parallel()
		ob := orderbytest.MustParseOrderBy(t, "address.city asc")
		got, err := mustOrderTranslator(t, orderbytest.MustPolicy(t), orderby.WithFieldPrefixMapping(map[string]string{"addr": "ad"})).Translate(ob)
		require.NoError(t, err)
		require.Equal(t, bson.D{{Key: "address.city", Value: int32(1)}}, got)
	})

	t.Run("non-matching path passes through unchanged", func(t *testing.T) {
		t.Parallel()
		ob := orderbytest.MustParseOrderBy(t, "billing.city desc")
		got, err := mustOrderTranslator(t, orderbytest.MustPolicy(t), orderby.WithFieldPrefixMapping(map[string]string{"address.": "addr."})).Translate(ob)
		require.NoError(t, err)
		require.Equal(t, bson.D{{Key: "billing.city", Value: int32(-1)}}, got)
	})
}
