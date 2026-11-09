// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package crossbackend_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/memory"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
)

func policySchema(t *testing.T, opts ...listing.ProfileOption) *listing.Schema {
	t.Helper()

	s, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "name", Kind: filter.FieldKindString},
		{Name: "at", Kind: filter.FieldKindTimestamp},
	}, []string{"id"}, listing.NewProfile(opts...))
	require.NoError(t, err)

	return s
}

func TestProfileEmptyDeclarationsAndMemory(t *testing.T) {
	t.Parallel()

	p := listing.NewProfile(listing.WithEmptyFieldsAllowed(true), listing.WithEmptyOrderAllowed(true), listing.WithFilterOptions(filter.WithUntrustedInput(false), filter.WithAnyField(true)))
	for _, patterns := range [][]string{nil, {""}} {
		s, err := listing.NewSchema(nil, patterns, p)
		require.NoError(t, err)
		q, err := s.Compile(t.Context(), listing.Request{})
		require.NoError(t, err)
		require.Empty(t, q.Order.Keys())

		engine, err := memory.NewListEngine(nil, s, func(v string) string { return v }, func(v string) map[string]any { return nil })
		require.NoError(t, err)
		page, err := engine.List(t.Context(), "empty", listing.Request{}, []string{"b", "a"})
		require.NoError(t, err)
		require.Equal(t, []string{"a", "b"}, page.Items)
	}

	s := policySchema(t, listing.WithUnrestrictedOrder(true))
	engine, err := memory.NewListEngine(nil, s, func(v string) string { return v }, func(v string) map[string]any { return map[string]any{"id": v, "name": v} })
	require.NoError(t, err)
	page, err := engine.List(t.Context(), "test", listing.Request{OrderBy: "name desc"}, []string{"a", "b"})
	require.NoError(t, err)
	require.Equal(t, []string{"b", "a"}, page.Items)
}
