// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filtertest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func TestAnyFieldPolicy(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		opts       []any
		allowsElse bool
	}{
		{"default", nil, true},
		{"allow-list", []any{filter.WithAllowedFields("name")}, false},
		{"untrusted", []any{filter.WithAllowedFields("name"), filter.WithUntrustedInput(true)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts := append([]any{filter.WithFieldMapping(map[string]string{"name": "stored_name"})}, tc.opts...)
			policy, binding := filtertest.AnyFieldPolicy(t, opts...)
			require.True(t, policy.IsFieldAllowed("name"))
			require.Equal(t, tc.allowsElse, policy.IsFieldAllowed("secret"))
			require.Equal(t, "name", policy.ApplyFieldMapping("name"))

			bound, err := filter.NewTranslatorContext(policy, binding...)
			require.NoError(t, err)
			require.Equal(t, "stored_name", bound.ApplyFieldMapping("name"))
			require.Equal(t, tc.allowsElse, bound.IsFieldAllowed("secret"))
		})
	}
}
