// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func TestFilterOptionSnapshot(t *testing.T) {
	t.Parallel()

	binding := []filter.TranslatorOption{filter.WithFieldMapping(map[string]string{"active": "on"})}
	option := evaluator.WithFilterOptions(binding...)
	binding[0] = filter.WithFieldMapping(map[string]string{"active": "secret"})
	eval, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAllowedFields("active")), option)
	require.NoError(t, err)

	node := &filter.IdentNode{Name: "active"}
	prepared, err := eval.Prepare(node)
	require.NoError(t, err)

	node.Name = "secret"
	matched, err := prepared.Evaluate(map[string]any{"on": true, "secret": false})
	require.NoError(t, err)
	require.True(t, matched)
	require.ErrorIs(t, eval.Validate(node), filter.ErrFieldNotAllowed)
}

func TestNewRequiresPolicy(t *testing.T) {
	t.Parallel()

	_, err := evaluator.New(nil)
	require.ErrorIs(t, err, filter.ErrPolicyRequired)
}
