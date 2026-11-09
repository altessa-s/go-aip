// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package meili_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/meili"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
)

func mustTranslator(tb testing.TB, policy *orderby.Policy, opts ...orderby.TranslatorOption) *meili.OrderTranslator {
	tb.Helper()

	tr, err := meili.NewOrderTranslator(policy, meili.WithOrderOptions(opts...))
	require.NoError(tb, err)

	return tr
}

func TestOrderTranslate_Multi(t *testing.T) {
	t.Parallel()
	ob := orderbytest.MustParseOrderBy(t, "createdAt desc, name")

	got, err := mustTranslator(t, orderbytest.MustPolicy(t)).Translate(ob)
	require.NoError(t, err)
	require.Equal(t, []string{"createdAt:desc", "name:asc"}, got)
}

func TestOrderTranslate_Empty(t *testing.T) {
	t.Parallel()
	got, err := mustTranslator(t, orderbytest.MustPolicy(t)).Translate(orderby.Spec{})
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestOrderTranslate_FieldMapping(t *testing.T) {
	t.Parallel()
	ob := orderbytest.MustParseOrderBy(t, "createdAt desc")

	got, err := mustTranslator(t, orderbytest.MustPolicy(t), orderby.WithFieldMapping(map[string]string{
		"createdAt": "created_at",
	})).Translate(ob)
	require.NoError(t, err)
	require.Equal(t, []string{"created_at:desc"}, got)
}

func TestOrderTranslate_AllowedFields(t *testing.T) {
	t.Parallel()
	ob := orderbytest.MustParseOrderBy(t, "createdAt desc, secret")

	_, err := mustTranslator(t, orderbytest.MustPolicy(t, orderby.WithAllowedFields("createdAt"))).Translate(ob)
	require.ErrorIs(t, err, orderby.ErrFieldNotAllowed)
}

// TestNewTranslator_RequiresPolicy locks the contract that a translator
// cannot be built without a policy, so untrusted input never reaches it
// unvalidated.
func TestNewOrderTranslator_RequiresPolicy(t *testing.T) {
	t.Parallel()

	_, err := meili.NewOrderTranslator(nil)
	require.ErrorIs(t, err, orderby.ErrPolicyRequired)
}

// TestTranslate_NestedField asserts that dotted field paths round-trip
// through the Meili translator unchanged — Meilisearch accepts nested
// attributes in sort instructions natively.
func TestOrderTranslate_NestedField(t *testing.T) {
	t.Parallel()
	ob := orderbytest.MustParseOrderBy(t, "address.city desc, address.zip.code asc")

	got, err := mustTranslator(t, orderbytest.MustPolicy(t, orderby.WithAllowedFields("address.*"))).Translate(ob)
	require.NoError(t, err)
	require.Equal(t, []string{"address.city:desc", "address.zip.code:asc"}, got)
}
