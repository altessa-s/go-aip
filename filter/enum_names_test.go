// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

// enumNames declares an integer-stored and a string-stored enum.
func enumNames() map[string]map[string]any {
	return map[string]map[string]any{
		"state": {"STATE_ACTIVE": 1, "STATE_PAUSED": int32(2), "STATE_DONE": uint8(3)},
		"kind":  {"KIND_TENDER": "tender", "KIND_LOT": "lot"},
	}
}

func mustEnumParser(tb testing.TB, opts ...filterparser.Option) *filterparser.Parser {
	tb.Helper()

	p, err := filterparser.New(append([]filterparser.Option{filterparser.WithEnumNames(enumNames())}, opts...)...)
	require.NoError(tb, err)

	return p
}

func TestWithEnumNames_RewritesNamesToStoredValues(t *testing.T) {
	t.Parallel()

	plain, err := filterparser.New()
	require.NoError(t, err)

	tests := []struct{ expr, want string }{
		{`state == "STATE_ACTIVE"`, `state == 1`},
		{`state != "STATE_DONE"`, `state != 3`},
		{`"STATE_PAUSED" == state`, `2 == state`},
		{`state in ["STATE_ACTIVE", "STATE_PAUSED"]`, `state in [1, 2]`},
		{`kind == "KIND_LOT" && state == null`, `kind == "lot" && state == null`},
		{`"KIND_TENDER" in kind`, `"tender" in kind`},
		{`!(kind in ["KIND_TENDER"]) || state != null`, `!(kind in ["tender"]) || state != null`},
		{`name == "STATE_ACTIVE"`, `name == "STATE_ACTIVE"`},
		// size() measures an enum array (or string) without comparing its values.
		{`size(kind) == 3 && kind == "KIND_LOT"`, `kind.size() == 3 && kind == "lot"`},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			t.Parallel()
			p := mustEnumParser(t)
			got, err := p.Parse(t.Context(), tt.expr)
			require.NoError(t, err)
			want, err := plain.Parse(t.Context(), tt.want)
			require.NoError(t, err)
			require.Equal(t, want, got)

			again, err := p.Parse(t.Context(), tt.expr) // served from the cache, already rewritten
			require.NoError(t, err)
			require.Equal(t, want, again)
		})
	}
}

func TestWithEnumNames_Rejections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, expr string
		want       error
	}{
		{"ordering", `state > "STATE_ACTIVE"`, filter.ErrUnsupportedOperation},
		{"mirrored ordering", `"STATE_ACTIVE" <= state`, filter.ErrUnsupportedOperation},
		{"string function", `kind.startsWith("KIND")`, filter.ErrUnsupportedOperation},
		{"global string function", `contains(kind, "KIND")`, filter.ErrUnsupportedOperation},
		{"stored value", `state == 1`, filter.ErrFieldTypeMismatch},
		{"stored value in a list", `kind in ["KIND_LOT", 2]`, filter.ErrFieldTypeMismatch},
		{"unknown name", `state == "STATE_SECRET_123"`, filter.ErrEnumValueNotAllowed},
		{"unknown name in a list", `kind in ["KIND_LOT", "lot"]`, filter.ErrEnumValueNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := mustEnumParser(t).Parse(t.Context(), tt.expr)
			require.ErrorIs(t, err, tt.want)
			require.NotContains(t, err.Error(), "SECRET", "the literal is never echoed")
		})
	}
}

func TestWithEnumNames_EvaluatesAgainstStoredValues(t *testing.T) {
	t.Parallel()

	eval, err := newEvaluator(withPolicy(filter.WithFieldTypes(map[string]filter.FieldKind{
		"state": filter.FieldKindInt, "kind": filter.FieldKindString,
	})))
	require.NoError(t, err)
	p := mustEnumParser(t)
	data := map[string]any{"state": int64(2), "kind": "lot"}

	for expr, want := range map[string]bool{
		`state == "STATE_PAUSED"`:                     true,
		`state in ["STATE_ACTIVE", "STATE_DONE"]`:     false,
		`kind != "KIND_TENDER" && kind == "KIND_LOT"`: true,
	} {
		node, err := p.Parse(t.Context(), expr)
		require.NoError(t, err)
		got, err := eval.Evaluate(node, data)
		require.NoError(t, err, expr)
		require.Equal(t, want, got, expr)
	}
}

func TestWithEnumNames_InvalidDeclarations(t *testing.T) {
	t.Parallel()

	for name, names := range map[string]map[string]map[string]any{
		"empty vocabulary": {"state": {}},
	} {
		_, err := filterparser.New(filterparser.WithEnumNames(names))
		require.ErrorIs(t, err, filter.ErrInvalidOptions, name)
	}
}
