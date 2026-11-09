// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldpolicy_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/fieldpolicy"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/projection"
)

type publicFields struct{}

func (publicFields) Allows(name string) bool { return name == "name" }

func TestAsAllowList(t *testing.T) {
	t.Parallel()

	list := fieldpolicy.NewAllowList("name")
	for _, tc := range []struct {
		name       string
		access     fieldpolicy.Access
		recognized bool
		restricts  bool
	}{
		{"value", list, true, true},
		{"pointer", &list, true, true},
		{"zero", fieldpolicy.AllowList{}, true, false},
		{"nil pointer", (*fieldpolicy.AllowList)(nil), true, false},
		{"nil interface", nil, false, false},
		{"opaque", publicFields{}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := fieldpolicy.AsAllowList(tc.access)
			require.Equal(t, tc.recognized, ok)
			require.Equal(t, tc.restricts, got.Restricts())
			require.True(t, got.Allows("name"))
			require.Equal(t, !tc.restricts, got.Allows("secret"))
		})
	}
}

func TestSharedAccess(t *testing.T) {
	t.Parallel()

	policy := publicFields{}
	f, err := filter.NewPolicy(filter.WithFieldAccess(policy))
	require.NoError(t, err)
	o, err := orderby.NewPolicy(orderby.WithFieldAccess(policy))
	require.NoError(t, err)
	pp, err := projection.NewPolicy(projection.WithFieldAccess(policy), projection.WithAllowedFields("name"))
	require.NoError(t, err)
	p, err := projection.NewTranslatorContext(pp)
	require.NoError(t, err)
	require.False(t, f.IsFieldAllowed("secret"))
	require.False(t, f.Field("secret").Allowed())
	require.True(t, f.Field("name").Allowed())
	require.False(t, o.IsFieldAllowed("secret"))

	_, err = p.Resolve(projection.Spec{Paths: []string{"secret"}})
	require.ErrorIs(t, err, projection.ErrFieldNotAllowed)
}

func TestEnumNormalization(t *testing.T) {
	t.Parallel()

	for _, v := range []any{int8(2), uint(2), uint64(2), int64(2)} {
		n, ok := fieldpolicy.IntegerValue(v)
		require.True(t, ok)
		require.Equal(t, int64(2), n)
	}

	_, ok := fieldpolicy.IntegerValue(^uint64(0))
	require.False(t, ok)
}
