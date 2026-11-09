// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func TestNewTranslatorContext(t *testing.T) {
	t.Parallel()

	_, err := filter.NewTranslatorContext(nil)
	require.ErrorIs(t, err, filter.ErrPolicyRequired)

	shared := filtertest.MustPolicy(t, filter.WithAllowedFields("name", "age"), filter.WithUntrustedInput(true))
	users, err := filter.NewTranslatorContext(shared, filter.WithFieldMapping(map[string]string{"name": "user_name"}))
	require.NoError(t, err)
	people, err := filter.NewTranslatorContext(shared, filter.WithFieldMapping(map[string]string{"name": "full_name"}))
	require.NoError(t, err)

	require.Equal(t, "user_name", users.ApplyFieldMapping("name"))
	require.Equal(t, "full_name", people.ApplyFieldMapping("name"))
	require.Equal(t, "name", shared.ApplyFieldMapping("name"), "binding never changes the shared policy")

	for _, p := range []*filter.Policy{shared, users, people} {
		require.True(t, p.IsFieldAllowed("name"))
		require.False(t, p.IsFieldAllowed("user_name"), "the allow-list keeps naming the API fields")
		require.Equal(t, shared.MaxDepth(), p.MaxDepth())
	}
}

// TestUntrustedPolicyNeedsAllowlist: a translator takes a policy, and a policy for untrusted input cannot exist without an
// allow-list or a field access policy, so no translator can translate untrusted input over arbitrary fields.
func TestUntrustedPolicyNeedsAllowlist(t *testing.T) {
	t.Parallel()

	_, err := filter.NewPolicy(filter.WithUntrustedInput(true))
	require.ErrorIs(t, err, filter.ErrAllowlistRequired)
	_, err = filter.NewPolicy(filter.WithUntrustedInput(true), filter.WithAnyField(true))
	require.ErrorIs(t, err, filter.ErrAllowlistRequired)
}
