// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
)

type textCodec struct{}

func (textCodec) ValidateLiteral(value any) error {
	if _, ok := value.(string); !ok {
		return filter.ErrFieldTypeMismatch
	}

	return nil
}

func TestCustomKindAndContractRule(t *testing.T) {
	t.Parallel()

	const custom filter.FieldKind = 100

	p, err := filter.NewPolicy(filter.WithAllowedFields("name"), filter.WithFieldTypes(map[string]filter.FieldKind{"name": custom}), filter.WithValueCodecs(map[filter.FieldKind]filter.ValueCodec{custom: textCodec{}}))
	require.NoError(t, err)
	require.NoError(t, p.CheckLiteralKind("name", &filter.LiteralNode{Value: "x"}))
	require.ErrorIs(t, p.CheckLiteralKind("name", &filter.LiteralNode{Value: 1}), filter.ErrFieldTypeMismatch)
	p, err = filter.NewPolicy(filter.WithAnyField(true), filter.WithPortableContract(true), filter.WithContractRules(map[filter.ContractRule]bool{filter.PortableRegex: false}))
	require.NoError(t, err)

	n := &filter.CallNode{Op: filter.OpMatches, Target: &filter.IdentNode{Name: "name"}, Args: []filter.Node{&filter.LiteralNode{Value: "(?i)Иван"}}}
	require.NoError(t, filter.Validate(n, p))
}

func TestNeutralDecimalPrecision(t *testing.T) {
	t.Parallel()

	_, err := filter.DecimalLiteral(strings.Repeat("9", 65))
	require.NoError(t, err)
}

func TestIndependentFieldComparisonRule(t *testing.T) {
	t.Parallel()

	p, err := filter.NewPolicy(filter.WithAllowedFields("a", "b"), filter.WithPortableContract(true), filter.WithContractRules(map[filter.ContractRule]bool{filter.LiteralComparisons: false}))
	require.NoError(t, err)

	n := &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "a"}, Right: &filter.IdentNode{Name: "b"}}
	require.NoError(t, filter.Validate(n, p))
}
