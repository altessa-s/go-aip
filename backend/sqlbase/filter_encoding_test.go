// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

func encodeDecimal(v any) (any, error) {
	if d, ok := v.(decimal.Decimal); ok {
		return d.String(), nil
	}

	return v, nil
}

func TestCustomValueEncoder(t *testing.T) {
	t.Parallel()

	tr, err := sqlbase.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAllowedFields("price"), filter.WithFieldTypes(map[string]filter.FieldKind{"price": filter.FieldKindDecimal})), failing{},
		sqlbase.WithFilterLiteralEncoder(encodeDecimal))
	require.NoError(t, err)

	n := &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: "price"}, Right: &filter.LiteralNode{Value: decimal.NewFromInt(42)}}
	sql, args, err := tr.Translate(n)
	require.NoError(t, err)
	require.Equal(t, `"price" = ?`, sql)
	require.Equal(t, []any{"42"}, args)
}

func renderPredicate(n filter.Node, value sqlbase.ValueFunc) (string, bool, error) {
	if c, ok := n.(*filter.CallNode); ok && c.Op == filter.Operator(250) {
		arg, err := value("opaque")
		return "custom(" + arg + ")", true, err
	}

	return "", false, nil
}

func TestCustomPredicateRenderer(t *testing.T) {
	t.Parallel()

	tr, err := sqlbase.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAllowedFields("name")), failing{},
		sqlbase.WithFilterPredicateRenderer(renderPredicate))
	require.NoError(t, err)
	out, args, err := tr.Translate(&filter.CallNode{Op: filter.Operator(250), Target: &filter.IdentNode{Name: "name"}})
	require.NoError(t, err)
	require.Equal(t, "custom(?)", out)
	require.Equal(t, []any{"opaque"}, args)
}

// TestOptionalDialectExtensions: a dialect without the array, decimal or substring extension rejects those operations
// with the shared sentinels instead of rendering scalar SQL.
func TestOptionalDialectExtensions(t *testing.T) {
	t.Parallel()

	_, err := sqlbase.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true),
		filter.WithFieldTypes(map[string]filter.FieldKind{"price": filter.FieldKindDecimal})), failing{})
	require.ErrorIs(t, err, filter.ErrUnsupportedType)

	tr, err := sqlbase.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithArrayFields("tags")), failing{})
	require.NoError(t, err)

	for _, expr := range []string{`tags == "a"`, `"a" in tags`, `tags.size() == 1`, `tags.contains("a")`, `name.substring(0, 1) == "a"`} {
		_, _, err = tr.Translate(filtertest.MustParseFilter(t, expr))
		require.ErrorIs(t, err, filter.ErrUnsupportedOperation, expr)
	}
}

// TestValueEncoderInline: the encoder also runs before inline rendering.
func TestValueEncoderInline(t *testing.T) {
	t.Parallel()

	var seen []any

	tr, err := sqlbase.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithAnyField(true)), failing{},
		sqlbase.WithFilterLiteralEncoder(func(v any) (any, error) { seen = append(seen, v); return v, nil }))
	require.NoError(t, err)

	_, err = tr.TranslateInline(filtertest.MustParseFilter(t, `name == "a"`))
	require.NoError(t, err)
	require.Equal(t, []any{"a"}, seen)
}
