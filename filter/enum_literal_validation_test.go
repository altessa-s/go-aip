// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/parser"
)

func TestCustomEnumLiteralValidation(t *testing.T) {
	t.Parallel()

	type status uint8

	for _, value := range []any{status(1), struct{ Code int }{1}} {
		p, err := parser.New(parser.WithEnumNames(map[string]map[string]any{"state": {"ACTIVE": value}}))
		require.NoError(t, err)
		policy, err := filter.NewPolicy(filter.WithAllowedFields("state"))
		require.NoError(t, err)

		for _, expression := range []string{`state == "ACTIVE"`, `state in ["ACTIVE"]`} {
			node, err := p.Parse(t.Context(), expression)
			require.NoError(t, err)
			require.NoError(t, filter.Validate(node, policy))
		}
	}
}
