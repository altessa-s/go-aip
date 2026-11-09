// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package orderby_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/orderby"
)

func TestSpec_String(t *testing.T) {
	t.Parallel()

	p, err := orderby.NewParser(orderby.WithNoCache(true))
	require.NoError(t, err)

	tests := []struct{ in, want string }{
		{"", ""},
		{"name", "name"},
		{"name asc", "name"},
		{"createTime desc", "createTime desc"},
		{" createTime   DESC ,name asc,address.city", "createTime desc, name, address.city"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			spec, err := p.Parse(t.Context(), tt.in)
			require.NoError(t, err)
			require.Equal(t, tt.want, spec.String())

			again, err := p.Parse(t.Context(), spec.String())
			require.NoError(t, err)
			require.Equal(t, spec, again, "the canonical form parses back to the same spec")
		})
	}
}

func TestSpec_Reverse(t *testing.T) {
	t.Parallel()

	spec := orderby.Spec{Keys: []orderby.Key{
		{Name: "createTime", Direction: orderby.DirectionDescending},
		{Name: "name"},
	}}
	reversed := spec.Reverse()
	require.Equal(t, "createTime, name desc", reversed.String())
	require.Equal(t, "createTime desc, name", spec.String(), "the receiver is not modified")
	require.Equal(t, spec, reversed.Reverse())
	require.True(t, orderby.Spec{}.Reverse().IsEmpty())
}
