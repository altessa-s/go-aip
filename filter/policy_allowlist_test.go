// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/fieldpolicy"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

type opaqueAccess struct{}

func (opaqueAccess) Allows(field string) bool { return field != "passwordHash" }

// TestNewPolicy_FieldAccessAsAllowList reproduces SEC-4: a field access policy that allows every field is no allow-list,
// so it cannot satisfy WithUntrustedInput (nor stand in for WithAllowedFields on trusted input without WithAnyField).
func TestNewPolicy_FieldAccessAsAllowList(t *testing.T) {
	t.Parallel()

	zero := fieldpolicy.AllowList{}
	star := fieldpolicy.NewAllowList("*")
	restricted := fieldpolicy.NewAllowList("name", "labels.*")

	tests := []struct {
		name     string
		opts     []filter.PolicyOption
		wantErr  error
		allowAll bool
	}{
		{name: "untrusted zero allow-list", opts: []filter.PolicyOption{filter.WithUntrustedInput(true), filter.WithFieldAccess(zero)}, wantErr: filter.ErrAllowlistRequired},
		{name: "untrusted zero allow-list pointer", opts: []filter.PolicyOption{filter.WithUntrustedInput(true), filter.WithFieldAccess(&zero)}, wantErr: filter.ErrAllowlistRequired},
		{name: "untrusted star", opts: []filter.PolicyOption{filter.WithUntrustedInput(true), filter.WithFieldAccess(star)}, wantErr: filter.ErrAllowlistRequired},
		{name: "untrusted star with other entries", opts: []filter.PolicyOption{filter.WithUntrustedInput(true), filter.WithFieldAccess(fieldpolicy.NewAllowList("name", "*"))}, wantErr: filter.ErrAllowlistRequired},
		{name: "untrusted empty configured", opts: []filter.PolicyOption{filter.WithUntrustedInput(true), filter.WithFieldAccess(fieldpolicy.NewAllowList())}, wantErr: filter.ErrAllowlistRequired},
		{name: "untrusted star with any field", opts: []filter.PolicyOption{filter.WithUntrustedInput(true), filter.WithAnyField(true), filter.WithFieldAccess(star)}, wantErr: filter.ErrAllowlistRequired},
		{name: "untrusted opaque access alone", opts: []filter.PolicyOption{filter.WithUntrustedInput(true), filter.WithFieldAccess(opaqueAccess{})}, wantErr: filter.ErrAllowlistRequired},
		// NEW-4: an entry of stars only admits every path of its depth.
		{name: "untrusted two-segment stars", opts: []filter.PolicyOption{filter.WithUntrustedInput(true), filter.WithFieldAccess(fieldpolicy.NewAllowList("*.*"))}, wantErr: filter.ErrAllowlistRequired},
		{name: "untrusted three-segment stars", opts: []filter.PolicyOption{filter.WithUntrustedInput(true), filter.WithFieldAccess(fieldpolicy.NewAllowList("name", "*.*.*"))}, wantErr: filter.ErrAllowlistRequired},
		{name: "untrusted star then a name", opts: []filter.PolicyOption{filter.WithUntrustedInput(true), filter.WithFieldAccess(fieldpolicy.NewAllowList("*.x"))}},
		{name: "star allowed field", opts: []filter.PolicyOption{filter.WithUntrustedInput(true), filter.WithAllowedFields("*")}, wantErr: filter.ErrInvalidOptions},
		{name: "wildcard allowed field", opts: []filter.PolicyOption{filter.WithAllowedFields("name", "labels.*")}, wantErr: filter.ErrInvalidOptions},
		{name: "untrusted restricted allow-list", opts: []filter.PolicyOption{filter.WithUntrustedInput(true), filter.WithFieldAccess(restricted)}},
		{name: "untrusted restricted allow-list pointer", opts: []filter.PolicyOption{filter.WithUntrustedInput(true), filter.WithFieldAccess(&restricted)}},
		{name: "untrusted star with allowed fields", opts: []filter.PolicyOption{filter.WithUntrustedInput(true), filter.WithFieldAccess(star), filter.WithAllowedFields("name")}},
		{name: "untrusted opaque access with allowed fields", opts: []filter.PolicyOption{filter.WithUntrustedInput(true), filter.WithFieldAccess(opaqueAccess{}), filter.WithAllowedFields("name")}},
		{name: "trusted zero allow-list", opts: []filter.PolicyOption{filter.WithFieldAccess(zero)}, wantErr: filter.ErrAllowlistRequired},
		{name: "trusted star", opts: []filter.PolicyOption{filter.WithFieldAccess(star)}, wantErr: filter.ErrAllowlistRequired},
		{name: "trusted star with any field", opts: []filter.PolicyOption{filter.WithAnyField(true), filter.WithFieldAccess(star)}, allowAll: true},
		{name: "trusted opaque access", opts: []filter.PolicyOption{filter.WithFieldAccess(opaqueAccess{})}},
		{name: "malformed allow-list entry", opts: []filter.PolicyOption{filter.WithAllowedFields("name"), filter.WithFieldAccess(fieldpolicy.NewAllowList("name", "a..b"))}, wantErr: filter.ErrInvalidOptions},
		{name: "malformed star entry", opts: []filter.PolicyOption{filter.WithAllowedFields("name"), filter.WithFieldAccess(fieldpolicy.NewAllowList("a*"))}, wantErr: fieldpolicy.ErrMalformedPattern},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			policy, err := filter.NewPolicy(tt.opts...)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Nil(t, policy)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.allowAll, policy.IsFieldAllowed("passwordHash"))
		})
	}
}

// TestPolicy_DeclaresField separates fields the service named from fields a wildcard admits.
func TestPolicy_DeclaresField(t *testing.T) {
	t.Parallel()

	policy := filtertest.MustPolicy(t,
		filter.WithAllowedFields("name"),
		filter.WithFieldTypes(map[string]filter.FieldKind{"age": filter.FieldKindInt}),
		filter.WithFieldAccess(fieldpolicy.NewAllowList("name", "age", "labels.*", "createdAt")),
	)
	bound, err := filter.NewTranslatorContext(policy, filter.WithFieldMapping(map[string]string{"createdAt": "created_at"}))
	require.NoError(t, err)

	tests := []struct {
		field string
		want  bool
	}{
		{field: "name", want: true},
		{field: "age", want: true},
		{field: "createdAt", want: true},
		{field: "labels.env"},
		{field: "a.b.c"},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, bound.DeclaresField(tt.field))
		})
	}

	require.False(t, policy.DeclaresField("createdAt"), "the mapping belongs to the bound copy only")
}
