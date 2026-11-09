// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldpolicy_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/fieldpolicy"
)

// TestAllowList_EmptySegments reproduces SEC-1 (c): a wildcard prefix must not admit a field with an empty segment.
func TestAllowList_EmptySegments(t *testing.T) {
	t.Parallel()

	list := fieldpolicy.NewAllowList("a.*", "x.*.y", "exact")

	tests := []struct {
		field string
		want  bool
	}{
		{field: "a.b", want: true},
		{field: "a.b.c", want: true},
		{field: "x.k.y", want: true},
		{field: "exact", want: true},
		{field: "a."},
		{field: "a..b"},
		{field: "a.b."},
		{field: ".a.b"},
		{field: "x..y"},
		{field: ""},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, list.Allows(tt.field))
		})
	}

	require.True(t, fieldpolicy.AllowList{}.Allows("a..b"), "an unconfigured list allows every field")
	require.False(t, fieldpolicy.NewAllowList("*").Allows("a..b"), "a configured list refuses empty segments even with a lone star")
}

// TestAllowList_MalformedPatterns pins that a malformed entry matches nothing and is reported by Validate.
func TestAllowList_MalformedPatterns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		entry string
		probe string
	}{
		{entry: "", probe: ""},
		{entry: ".*", probe: ".b"},
		{entry: "a..*", probe: "a..b"},
		{entry: "a.", probe: "a."},
		{entry: ".a", probe: ".a"},
		{entry: "a..b", probe: "a..b"},
		{entry: "a*", probe: "ab"},
		{entry: "a.b*", probe: "a.bc"},
		{entry: "**", probe: "a"},
		{entry: "a.**", probe: "a.b"},
		{entry: "*a.b", probe: "xa.b"},
	}

	for _, tt := range tests {
		t.Run(tt.entry, func(t *testing.T) {
			t.Parallel()

			list := fieldpolicy.NewAllowList("ok", tt.entry)
			require.ErrorIs(t, list.Validate(), fieldpolicy.ErrMalformedPattern)
			require.False(t, list.Allows(tt.probe))
			require.True(t, list.Allows("ok"))
			require.False(t, list.MatchesAll())
		})
	}

	for _, entry := range []string{"*", "a", "a.*", "a.*.b", "*.b", "a.*.*"} {
		require.NoError(t, fieldpolicy.NewAllowList(entry).Validate(), entry)
	}

	require.NoError(t, fieldpolicy.AllowList{}.Validate())
}
