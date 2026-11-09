// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/projection"
)

func TestOrderPolicyUnrestrictedAndEmpty(t *testing.T) {
	t.Parallel()

	s := policySchema(t, listing.WithUnrestrictedOrder(true))
	for _, field := range []string{"id", "name", "at"} {
		require.True(t, s.OrderPolicy().IsFieldAllowed(field), field)
	}

	empty, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, nil,
		listing.NewProfile(listing.WithEmptyOrderAllowed(true)))
	require.NoError(t, err)
	require.False(t, empty.OrderPolicy().IsFieldAllowed("id"))

	got, err := empty.OrderPolicy().Validate(orderby.Spec{})
	require.NoError(t, err)
	require.True(t, got.IsEmpty())
}

func TestProjectionOptionsDenyHiddenFields(t *testing.T) {
	t.Parallel()

	s, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "secret", Kind: filter.FieldKindString, Hidden: true},
	}, []string{"id"}, nil)
	require.NoError(t, err)

	p, err := projection.NewPolicy(append(s.ProjectionOptions(), projection.WithUntrustedInput(true),
		projection.WithAllowedFields("id", "secret", "title"), projection.WithView(projection.ViewBasic, "id"))...)
	require.NoError(t, err)
	require.Equal(t, []string{"secret"}, p.DeniedFields())

	all, err := p.Validate(projection.Spec{})
	require.NoError(t, err)
	require.Equal(t, []string{"id", "title"}, all.Paths(), "an omitted mask returns every allowed field but the hidden one")

	_, err = p.Validate(projection.Spec{Paths: []string{"secret"}})
	require.ErrorIs(t, err, projection.ErrFieldNotAllowed)

	f, err := p.Validate(projection.Spec{Paths: []string{"id", "title"}})
	require.NoError(t, err)
	require.Equal(t, []string{"id", "title"}, f.Paths())
}
