// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/redis"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
)

func mustOrderTranslator(tb testing.TB, policy *orderby.Policy, opts ...orderby.TranslatorOption) *redis.OrderTranslator {
	tb.Helper()

	tr, err := redis.NewOrderTranslator(policy, redis.WithOrderOptions(opts...))
	require.NoError(tb, err)

	return tr
}

func TestOrderTranslate_SingleKey(t *testing.T) {
	t.Parallel()
	ob := orderbytest.MustParseOrderBy(t, "createdAt desc")

	got, err := mustOrderTranslator(t, orderbytest.MustPolicy(t)).Translate(ob)
	require.NoError(t, err)
	require.Equal(t, redis.SortBy{Field: "createdAt", Descending: true}, got)
}

func TestOrderTranslate_DefaultAscending(t *testing.T) {
	t.Parallel()
	ob := orderbytest.MustParseOrderBy(t, "createdAt")

	got, err := mustOrderTranslator(t, orderbytest.MustPolicy(t)).Translate(ob)
	require.NoError(t, err)
	require.Equal(t, redis.SortBy{Field: "createdAt", Descending: false}, got)
}

func TestOrderTranslate_Empty(t *testing.T) {
	t.Parallel()
	got, err := mustOrderTranslator(t, orderbytest.MustPolicy(t)).Translate(orderby.Spec{})
	require.NoError(t, err)
	require.Equal(t, redis.SortBy{}, got)
}

func TestOrderTranslate_FieldMapping(t *testing.T) {
	t.Parallel()
	ob := orderbytest.MustParseOrderBy(t, "createdAt desc")

	got, err := mustOrderTranslator(t, orderbytest.MustPolicy(t), orderby.WithFieldMapping(map[string]string{
		"createdAt": "created_at",
	})).Translate(ob)
	require.NoError(t, err)
	require.Equal(t, redis.SortBy{Field: "created_at", Descending: true}, got)
}

func TestOrderTranslate_TooManyKeys(t *testing.T) {
	t.Parallel()
	ob := orderbytest.MustParseOrderBy(t, "a, b desc")

	_, err := mustOrderTranslator(t, orderbytest.MustPolicy(t)).Translate(ob)
	require.ErrorIs(t, err, orderby.ErrTooManySortKeys)
}

func TestOrderTranslate_AllowedFields(t *testing.T) {
	t.Parallel()
	ob := orderbytest.MustParseOrderBy(t, "secret desc")

	_, err := mustOrderTranslator(t, orderbytest.MustPolicy(t, orderby.WithAllowedFields("createdAt"))).Translate(ob)
	require.ErrorIs(t, err, orderby.ErrFieldNotAllowed)
}

// TestNewTranslator_RequiresPolicy locks the contract that a translator
// cannot be built without a policy, so untrusted input never reaches it
// unvalidated.
func TestOrderNewTranslator_RequiresPolicy(t *testing.T) {
	t.Parallel()

	_, err := redis.NewOrderTranslator(nil)
	require.ErrorIs(t, err, orderby.ErrPolicyRequired)
}

// TestTranslate_NestedField asserts a dotted field path round-trips
// unchanged through the RediSearch translator. RediSearch accepts a
// nested attribute name in SORTBY as long as the underlying index
// declares the JSONPath, which is the caller's concern.
func TestOrderTranslate_NestedField(t *testing.T) {
	t.Parallel()
	ob := orderbytest.MustParseOrderBy(t, "address.city desc")

	got, err := mustOrderTranslator(t, orderbytest.MustPolicy(t, orderby.WithAllowedFields("address.*"))).Translate(ob)
	require.NoError(t, err)
	require.Equal(t, redis.SortBy{Field: "address.city", Descending: true}, got)
}
