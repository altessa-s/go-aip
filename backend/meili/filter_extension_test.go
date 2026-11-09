// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package meili

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/metrics"
)

func TestFilterTranslator_ValidateAST(t *testing.T) {
	t.Parallel()

	trans := mustTranslator(t, WithFilterCollector(metrics.Noop()))
	require.NoError(t, trans.ValidateAST(filtertest.MustParseFilter(t, `name == "a"`)))
	require.ErrorIs(t, trans.ValidateAST(filtertest.MustParseFilter(t, `name.endsWith("a")`)), filter.ErrUnsupportedOperation)
}

func TestFilterTranslator_PredicateRenderer(t *testing.T) {
	t.Parallel()

	errRender := errors.New("render failed")
	renderer := func(n filter.Node) (string, bool, error) {
		ident, ok := n.(*filter.IdentNode)
		if !ok {
			return "", false, nil
		}

		switch ident.Name {
		case "vip":
			return `tier = "gold"`, true, nil
		case "broken":
			return "", true, errRender
		default:
			return "", false, nil
		}
	}

	trans := mustTranslator(t, WithFilterPredicateRenderer(renderer), filter.WithAllowedFields("vip", "broken", "active", "name"))

	runCases(t, trans, []translateCase{
		{"handled", `vip`, `tier = "gold"`, nil},
		{"handled negated", `!vip`, `NOT (tier = "gold")`, nil},
		{"unhandled", `active && name == "a"`, `(active = true) AND (name = "a")`, nil},
		{"unhandled negated", `!active`, `active = false`, nil},
		{"handled under de morgan", `!(vip && name == "a")`, `(NOT (tier = "gold")) OR (name != "a")`, nil},
		{"failing", `broken`, "", errRender},
		{"failing negated", `!broken`, "", errRender},
		{"policy first", `other`, "", filter.ErrFieldNotAllowed},
	})
}

func TestFilterTranslator_LiteralKinds(t *testing.T) {
	t.Parallel()

	trans := mustTranslator(t, filter.WithArrayFields("tags"))

	runCases(t, trans, []translateCase{
		{"unsigned", `n == 7u`, `n = 7`, nil},
		{"float", `n == 1.25`, `n = 1.25`, nil},
		{"false", `flag == false`, `flag = false`, nil},
		{"bytes", `data == b"ab"`, `data = "ab"`, nil},
		{"unsigned size operand", `tags.size() == 0u`, `(tags NOT EXISTS OR tags IS NULL OR tags IS EMPTY)`, nil},
		{"mirrored >= size", `1 >= tags.size()`, "", filter.ErrUnsupportedOperation},
		{"mirrored <= size", `1 <= tags.size()`, `(tags EXISTS AND tags IS NOT NULL AND tags IS NOT EMPTY)`, nil},
		{"mirrored != size", `0 != tags.size()`, `(tags EXISTS AND tags IS NOT NULL AND tags IS NOT EMPTY)`, nil},
		{"literal predicate", `"a" || name == "b"`, "", filter.ErrInvalidExpression},
	})
}

func TestFilterTranslator_StringFunctionArguments(t *testing.T) {
	t.Parallel()

	trans := mustTranslator(t, WithFilterContainsFilter(true))

	runCases(t, trans, []translateCase{
		{"quoted", `name.contains("a\"b")`, `name CONTAINS "a\"b"`, nil},
		{"non-string argument", `name.startsWith(1)`, "", filter.ErrInvalidExpression},
		{"field argument", `name.contains(other)`, "", filter.ErrUnsupportedOperation},
		{"negated", `!name.startsWith("a")`, `NOT (name STARTS WITH "a")`, nil},
	})
}

func TestFilterTranslator_EncoderErrorsAndTypes(t *testing.T) {
	t.Parallel()

	errEncode := errors.New("encode failed")

	type opaque struct{}

	encoder := func(v any) (any, error) {
		switch v {
		case "fail":
			return nil, errEncode
		case "opaque":
			return opaque{}, nil
		default:
			return v, nil
		}
	}

	trans := mustTranslator(t, WithFilterLiteralEncoder(encoder))

	runCases(t, trans, []translateCase{
		{"comparison error", `name == "fail"`, "", errEncode},
		{"in error", `name in ["fail"]`, "", errEncode},
		{"unprintable", `name == "opaque"`, "", filter.ErrUnsupportedType},
		{"unprintable in list", `name in ["opaque"]`, "", filter.ErrUnsupportedType},
	})
}
