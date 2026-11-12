// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"

	pagetoken "github.com/altessa-s/go-aip/pagination/pagetoken"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// widgetSchema returns the conformance widget schema with opts appended.
func widgetSchema(tb testing.TB, opts ...listing.ProfileOption) *listing.Schema {
	tb.Helper()
	return listingtest.NewSchema(tb, opts...)
}

// fieldOf returns the field of the core/errors.FieldError err wraps.
func fieldOf(err error) string { return listingtest.FieldOf(err) }

func TestPageSizeDefaults(t *testing.T) {
	t.Parallel()
	require.Equal(t, 50, listing.DefaultPageSize)
	require.Equal(t, 500, listing.DefaultMaxPageSize)
}

// TestCompilePageSize checks the page-size options: an omitted size is the
// default, a larger one is clamped to the maximum, a negative one is
// invalid, and a non-positive option keeps the package default.
func TestCompilePageSize(t *testing.T) {
	t.Parallel()

	custom := []listing.ProfileOption{listing.WithDefaultPageSize(7), listing.WithMaxPageSize(20)}

	tests := []struct {
		name string
		opts []listing.ProfileOption
		size int32
		want int
	}{
		{name: "package default", want: listing.DefaultPageSize},
		{name: "requested", size: 1, want: 1},
		{name: "package maximum", size: listing.DefaultMaxPageSize + 1, want: listing.DefaultMaxPageSize},
		{name: "huge", size: 1 << 30, want: listing.DefaultMaxPageSize},
		{name: "custom default", opts: custom, want: 7},
		{name: "custom maximum", opts: custom, size: 30, want: 20},
		{name: "zero options keep defaults", opts: []listing.ProfileOption{listing.WithDefaultPageSize(0), listing.WithMaxPageSize(-1)}, size: 600, want: 500},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q, err := widgetSchema(t, tc.opts...).Compile(t.Context(), listing.Request{PageSize: tc.size})
			require.NoError(t, err)
			require.Equal(t, tc.want, q.Size)
		})
	}

	_, err := widgetSchema(t).Compile(t.Context(), listing.Request{PageSize: -1})
	require.Equal(t, pagetoken.SizeField, fieldOf(err))
}

// TestCompileOrdersFields checks the first invalid field in request order
// — pageSize, parent, orderBy, filter — is the one reported.
func TestCompileOrdersFields(t *testing.T) {
	t.Parallel()

	s := widgetSchema(t, listing.WithParentRequired(true))
	bad := listing.Request{PageSize: -1, OrderBy: "nope", Filter: "nope =="}
	_, err := s.Compile(t.Context(), bad)
	require.Equal(t, pagetoken.SizeField, fieldOf(err))

	bad.PageSize = 0
	_, err = s.Compile(t.Context(), bad)
	require.Equal(t, listing.FieldParent, fieldOf(err))

	bad.Parent = "p"
	_, err = s.Compile(t.Context(), bad)
	require.Equal(t, listing.FieldOrderBy, fieldOf(err))

	bad.OrderBy = ""
	_, err = s.Compile(t.Context(), bad)
	require.Equal(t, listing.FieldFilter, fieldOf(err))
}

// TestCompileQuery checks the compiled query: the default restriction joins
// the filter, timestamps take TimestampPrecision, and the prepared filter
// matches what the node says.
func TestCompileQuery(t *testing.T) {
	t.Parallel()

	s := widgetSchema(t, listing.WithDefaultEqualities(map[string]any{"archived": false}))
	q, err := s.Compile(t.Context(), listing.Request{})
	require.NoError(t, err)
	require.Equal(t, "createTime desc", q.Order.String())
	require.NotNil(t, q.Filter, "the default restriction applies to an empty filter")
	require.NotNil(t, q.Filter)

	q, err = s.Compile(t.Context(), listing.Request{Filter: `createTime == timestamp("2026-03-01T12:00:00.123999Z") && archived == true`})
	require.NoError(t, err)

	at := time.Date(2026, 3, 1, 12, 0, 0, 123_000_000, time.UTC)

	var literal time.Time

	filter.Walk(q.Filter, func(n filter.Node) bool {
		if value, ok := n.(*filter.LiteralNode); ok {
			if got, ok := value.Value.(time.Time); ok {
				literal = got
			}
		}

		return true
	})
	require.Equal(t, at, literal, "the literal is truncated to %v", listing.TimestampPrecision)

	q, err = widgetSchema(t).Compile(t.Context(), listing.Request{Filter: "  "})
	require.NoError(t, err)
	require.Nil(t, q.Filter, "a blank filter matches everything")
}

// TestSizeTakesAnInteger checks that size() compares only with an integer
// literal: any other operand (a float, an unsigned or a string literal, null,
// a field) is a filter error naming the integer contract, also under
// negation, so no comparison escapes the absent-is-0 rule.
func TestSizeTakesAnInteger(t *testing.T) {
	t.Parallel()

	s := widgetSchema(t)
	for _, expr := range []string{
		`size(note) < 1.5`,
		`!(size(note) < 1.5)`,
		`size(note) == 1.0`,
		`size(tags) >= 2.0 || priority == 1`,
		`size(note) == 1u`,
		`size(note) != "1"`,
		`size(note) == null`,
		`size(note) > priority`,
		`size(note) == true`,
		`1 < size(note)`,
		`size(note) == size(tags)`,
	} {
		_, err := s.Compile(t.Context(), listing.Request{Filter: expr})
		require.Equal(t, listing.FieldFilter, fieldOf(err), "%q: %v", expr, err)
		require.Contains(t, err.Error(), "integer", expr)
	}
}

func TestSizeRejectsUnsupportedFieldKinds(t *testing.T) {
	t.Parallel()

	s, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "blob", Kind: filter.FieldKindBytes},
		{Name: "count", Kind: filter.FieldKindInt},
		{Name: "enabled", Kind: filter.FieldKindBool},
		{Name: "created", Kind: filter.FieldKindTimestamp},
		{Name: "price", Kind: filter.FieldKindDecimal},
	}, []string{"id"}, nil)
	require.NoError(t, err)

	for _, name := range []string{"blob", "count", "enabled", "created", "price"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := s.Compile(t.Context(), listing.Request{Filter: "size(" + name + ") == 0"})
			require.Equal(t, listing.FieldFilter, fieldOf(err))
			require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
		})
	}
}

// TestSizeOperandRange checks size() operands outside MongoDB's supported
// range are rejected by the shared list contract.
func TestSizeOperandRange(t *testing.T) {
	t.Parallel()

	s := widgetSchema(t)
	for _, expr := range []string{
		`size(tags) == 2147483648`, `size(tags) != -1`, `!(size(note) == -1)`, `size(note) > 2147483648`,
	} {
		_, err := s.Compile(t.Context(), listing.Request{Filter: expr})
		require.Equal(t, listing.FieldFilter, fieldOf(err), "%q: %v", expr, err)
		require.ErrorIs(t, err, filter.ErrInvalidExpression, expr)
	}
}

func TestSchemaStorageFacts(t *testing.T) {
	t.Parallel()

	s := widgetSchema(t)
	require.Equal(t, []string{"flag", "kind2", "level", "note", "tags"}, s.OptionalFields())
	require.Equal(t, filter.FieldKindString, s.ZeroFields()["note"])
}

// TestGlobalCallForm checks the malformed global string calls are filter
// errors naming the accepted forms.
func TestGlobalCallForm(t *testing.T) {
	t.Parallel()

	s := widgetSchema(t)
	for _, expr := range []string{`startsWith("name-1")`, `contains("x", name)`, `endsWith(name)`} {
		_, err := s.Compile(t.Context(), listing.Request{Filter: expr})
		require.Equal(t, listing.FieldFilter, fieldOf(err), "%q: %v", expr, err)
		require.ErrorIs(t, err, filter.ErrInvalidExpression)
		require.Contains(t, err.Error(), "name.", "the error shows the method form")
	}
}

// TestFilterErrorsHideLiterals checks a refused filter does not echo its
// literals (a filter may carry personal data) into the reason.
func TestFilterErrorsHideLiterals(t *testing.T) {
	t.Parallel()

	_, err := widgetSchema(t).Compile(t.Context(), listing.Request{Filter: `name == "7707083893" &&`})
	fe, ok := errors.AsType[*coreerrs.FieldError](err)
	require.True(t, ok, "error %v", err)
	require.Equal(t, listing.FieldFilter, fe.Field)
	require.NotContains(t, fe.Reason, "7707083893")
}
