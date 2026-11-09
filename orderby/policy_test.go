// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package orderby_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/fieldpolicy"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
)

type allowAll struct{}

func (allowAll) Allows(string) bool { return true }

func TestNewPolicy(t *testing.T) {
	t.Parallel()

	_, err := orderby.NewPolicy(orderby.WithAllowedFields("name"), orderby.WithDefaultOrder(orderby.Key{Name: "secret"}))
	require.ErrorIs(t, err, orderby.ErrFieldNotAllowed)

	_, err = orderby.NewPolicy(orderby.WithDeniedFields("secret"), orderby.WithDefaultOrder(orderby.Key{Name: "secret.part"}))
	require.ErrorIs(t, err, orderby.ErrFieldNotAllowed)

	_, err = orderby.NewPolicy(orderby.WithDefaultOrder(orderby.Key{}))
	require.ErrorIs(t, err, orderby.ErrInvalidFieldPath)

	_, err = orderby.NewPolicy(orderby.WithDefaultOrder(orderby.Key{Name: "a", Direction: 7}))
	require.ErrorIs(t, err, orderby.ErrInvalidDirection)
}

// TestNewPolicyUntrustedAllowlist pins the untrusted-input guard: only an allow-list that admits some but not every field
// satisfies it, as in filter and projection. The zero AllowList and one holding "*" allow every field, and an opaque access
// policy needs a restrictive WithAllowedFields list beside it. A malformed allow-list entry fails in any mode.
func TestNewPolicyUntrustedAllowlist(t *testing.T) {
	t.Parallel()

	list := fieldpolicy.NewAllowList("name")

	tests := []struct {
		name string
		opts []orderby.PolicyOption
		want error
	}{
		{name: "no allow-list", want: orderby.ErrAllowlistRequired},
		{name: "empty allowed fields", opts: []orderby.PolicyOption{orderby.WithAllowedFields()}, want: orderby.ErrAllowlistRequired},
		{name: "star allowed fields", opts: []orderby.PolicyOption{orderby.WithAllowedFields("*")}, want: orderby.ErrAllowlistRequired},
		{name: "opaque access", opts: []orderby.PolicyOption{orderby.WithFieldAccess(allowAll{})}, want: orderby.ErrAllowlistRequired},
		{
			name: "zero allow-list access",
			opts: []orderby.PolicyOption{orderby.WithFieldAccess(fieldpolicy.AllowList{})},
			want: orderby.ErrAllowlistRequired,
		},
		{
			name: "nil allow-list pointer access",
			opts: []orderby.PolicyOption{orderby.WithFieldAccess((*fieldpolicy.AllowList)(nil))},
			want: orderby.ErrAllowlistRequired,
		},
		{
			name: "star access",
			opts: []orderby.PolicyOption{orderby.WithFieldAccess(fieldpolicy.NewAllowList("*"))},
			want: orderby.ErrAllowlistRequired,
		},
		{
			name: "two-segment stars access",
			opts: []orderby.PolicyOption{orderby.WithFieldAccess(fieldpolicy.NewAllowList("*.*"))},
			want: orderby.ErrAllowlistRequired,
		},
		{
			name: "stars allowed fields",
			opts: []orderby.PolicyOption{orderby.WithAllowedFields("name", "*.*.*")},
			want: orderby.ErrAllowlistRequired,
		},
		{name: "star then a name access", opts: []orderby.PolicyOption{orderby.WithFieldAccess(fieldpolicy.NewAllowList("*.x"))}},
		{
			name: "empty access",
			opts: []orderby.PolicyOption{orderby.WithFieldAccess(fieldpolicy.NewAllowList())},
			want: orderby.ErrAllowlistRequired,
		},
		{
			name: "malformed allowed field",
			opts: []orderby.PolicyOption{orderby.WithAllowedFields("name", "a..b")},
			want: fieldpolicy.ErrMalformedPattern,
		},
		{
			name: "malformed access entry",
			opts: []orderby.PolicyOption{orderby.WithAllowedFields("name"), orderby.WithFieldAccess(fieldpolicy.NewAllowList("a*"))},
			want: fieldpolicy.ErrMalformedPattern,
		},
		{name: "allowed fields", opts: []orderby.PolicyOption{orderby.WithAllowedFields("name", "address.*")}},
		{name: "allow-list access", opts: []orderby.PolicyOption{orderby.WithFieldAccess(list)}},
		{name: "allow-list pointer access", opts: []orderby.PolicyOption{orderby.WithFieldAccess(&list)}},
		{name: "opaque access with allowed fields", opts: []orderby.PolicyOption{orderby.WithAllowedFields("name"), orderby.WithFieldAccess(allowAll{})}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := orderby.NewPolicy(append([]orderby.PolicyOption{orderby.WithUntrustedInput(true)}, tc.opts...)...)
			if tc.want == nil {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, tc.want)
		})
	}

	// Trusted input keeps accepting an access policy alone, but never a malformed entry.
	_, err := orderby.NewPolicy(orderby.WithFieldAccess(allowAll{}))
	require.NoError(t, err)

	_, err = orderby.NewPolicy(orderby.WithAllowedFields("x."))
	require.ErrorIs(t, err, fieldpolicy.ErrMalformedPattern)
}

func TestPolicyDeclaresField(t *testing.T) {
	t.Parallel()

	p := orderbytest.MustPolicy(t, orderby.WithUntrustedInput(true), orderby.WithAllowedFields("name", "address.city", "labels.*"))
	require.True(t, p.DeclaresField("address.city"))
	require.True(t, p.DeclaresField("name"))
	require.False(t, p.DeclaresField("labels.team"), "admitted only by a wildcard")
	require.False(t, p.DeclaresField("other"))

	ctx, err := orderby.NewTranslatorContext(p,
		orderby.WithFieldMapping(map[string]string{"labels.team": "t.team"}),
		orderby.WithFieldPrefixMapping(map[string]string{"labels": "l"}))
	require.NoError(t, err)
	require.True(t, ctx.DeclaresField("labels.team"), "an exact mapping entry declares the field")
	require.True(t, ctx.DeclaresField("address.city"))
	require.False(t, ctx.DeclaresField("labels.env"), "a prefix rule does not")
}

func TestPolicyValidate(t *testing.T) {
	t.Parallel()

	p := orderbytest.MustPolicy(t,
		orderby.WithUntrustedInput(true),
		orderby.WithAllowedFields("name", "address.*"),
		orderby.WithDeniedFields("address.secret"),
		orderby.WithDefaultOrder(orderby.Key{Name: "name", Direction: orderby.DirectionDescending}),
	)

	got, err := p.Validate(orderby.Spec{})
	require.NoError(t, err)
	require.Equal(t, []orderby.Key{{Name: "name", Direction: orderby.DirectionDescending}}, got.Keys)

	again, err := p.Validate(got)
	require.NoError(t, err)
	require.Equal(t, got, again)

	in := orderbytest.MustParseOrderBy(t, "address.city, name desc")
	got, err = p.Validate(in)
	require.NoError(t, err)
	require.Equal(t, in, got)

	got.Keys[0].Name = "mutated"
	require.Equal(t, "address.city", in.Keys[0].Name, "Validate must not alias the caller's keys")

	for _, expr := range []string{"secret", "address", "address.secret", "address.secret.part"} {
		_, err = p.Validate(orderbytest.MustParseOrderBy(t, expr))
		require.ErrorIs(t, err, orderby.ErrFieldNotAllowed, expr)
	}

	_, err = p.Validate(orderby.Spec{Keys: []orderby.Key{{Name: "name", Nulls: 9}}})
	require.ErrorIs(t, err, orderby.ErrUnsupportedOrdering)

	require.Equal(t, []string{"address.secret"}, p.DeniedFields())
}

// TestTranslatorRefusesUnvalidatedKeys feeds a hand-built Spec straight to a
// translator: the bound policy still authorizes it, and denied API fields
// stay denied through a mapping alias.
func TestTranslatorRefusesUnvalidatedKeys(t *testing.T) {
	t.Parallel()

	p := orderbytest.MustPolicy(t, orderby.WithUntrustedInput(true), orderby.WithAllowedFields("name", "alias"),
		orderby.WithDeniedFields("hash"))

	c, err := orderby.NewTranslatorContext(p, orderby.WithFieldMapping(map[string]string{"alias": "pw", "hash": "pw"}))
	require.NoError(t, err)
	require.Same(t, p, c.Policy())

	_, err = c.Resolve(orderby.Spec{Keys: []orderby.Key{{Name: "$natural"}}})
	require.ErrorIs(t, err, orderby.ErrFieldNotAllowed)

	_, err = c.Resolve(orderby.Spec{Keys: []orderby.Key{{Name: "alias"}}})
	require.ErrorIs(t, err, orderby.ErrFieldNotAllowed)

	_, err = c.Resolve(orderby.Spec{Keys: []orderby.Key{{Name: "name", Direction: 5}}})
	require.ErrorIs(t, err, orderby.ErrInvalidDirection)

	_, err = orderby.NewTranslatorContext(nil)
	require.ErrorIs(t, err, orderby.ErrPolicyRequired)
}

type badResolver struct{}

func (badResolver) ResolveKey(k orderby.Key) (orderby.Key, error) { k.Direction = 3; return k, nil }

func TestResolverAttributesChecked(t *testing.T) {
	t.Parallel()

	c, err := orderby.NewTranslatorContext(orderbytest.MustPolicy(t), orderby.WithKeyResolver(badResolver{}))
	require.NoError(t, err)
	_, err = c.Resolve(orderby.Spec{Keys: []orderby.Key{{Name: "a"}}})
	require.ErrorIs(t, err, orderby.ErrInvalidDirection)
}
