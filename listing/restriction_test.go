// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/softdelete"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func eq(field string, value any) *filter.BinaryOpNode {
	return &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: field}, Right: &filter.LiteralNode{Value: value}}
}

func canonical(t *testing.T, n filter.Node) string {
	t.Helper()

	c, err := filter.Canonical(n)
	require.NoError(t, err)

	return c
}

// TestCompileRestriction checks the restriction is ANDed after the client
// filter and the default equalities, also for a blank filter.
func TestCompileRestriction(t *testing.T) {
	t.Parallel()

	s := widgetSchema(t, listing.WithDefaultEqualities(map[string]any{"archived": false}))
	restriction := eq("name", "n1")

	tests := []struct {
		name   string
		filter string
		want   filter.Node
	}{
		{name: "blank filter", want: &filter.BinaryOpNode{Op: filter.OpAnd, Left: eq("archived", false), Right: eq("name", "n1")}},
		{name: "whitespace filter", filter: "  ", want: &filter.BinaryOpNode{Op: filter.OpAnd, Left: eq("archived", false), Right: eq("name", "n1")}},
		{
			name:   "client filter overriding the default",
			filter: `archived == true`,
			want:   &filter.BinaryOpNode{Op: filter.OpAnd, Left: eq("archived", true), Right: eq("name", "n1")},
		},
		{
			name:   "client filter on the restricted field narrows only",
			filter: `name == "n2"`,
			want: &filter.BinaryOpNode{
				Op:    filter.OpAnd,
				Left:  &filter.BinaryOpNode{Op: filter.OpAnd, Left: eq("name", "n2"), Right: eq("archived", false)},
				Right: eq("name", "n1"),
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			before := canonical(t, restriction)

			q, err := s.Compile(t.Context(), listing.Request{Filter: tc.filter, Restriction: restriction})
			require.NoError(t, err)
			assert.Equal(t, canonical(t, tc.want), canonical(t, q.Filter))
			assert.Equal(t, before, canonical(t, restriction), "the caller's restriction is not modified")
		})
	}
}

// TestCompileRestrictionWithoutDefaults checks a lone restriction is the
// whole filter and a nil one changes nothing.
func TestCompileRestrictionWithoutDefaults(t *testing.T) {
	t.Parallel()

	s := widgetSchema(t)

	q, err := s.Compile(t.Context(), listing.Request{Restriction: eq("archived", false)})
	require.NoError(t, err)
	assert.Equal(t, canonical(t, eq("archived", false)), canonical(t, q.Filter))

	var typedNil *filter.BinaryOpNode

	for _, r := range []filter.Node{nil, typedNil} {
		q, err = s.Compile(t.Context(), listing.Request{Restriction: r})
		require.NoError(t, err)
		assert.Nil(t, q.Filter)
	}
}

// TestCompileRestrictionTimestamp checks timestamp literals of the
// restriction are cut to the profile precision on a copy.
func TestCompileRestrictionTimestamp(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 3, 1, 12, 0, 0, 123456789, time.FixedZone("x", 3600))
	restriction := &filter.BinaryOpNode{Op: filter.OpLT, Left: &filter.IdentNode{Name: "createTime"}, Right: &filter.LiteralNode{Value: at}}

	q, err := widgetSchema(t).Compile(t.Context(), listing.Request{Restriction: restriction})
	require.NoError(t, err)

	got := q.Filter.(*filter.BinaryOpNode).Right.(*filter.LiteralNode).Value //nolint:forcetypeassert,errcheck // shape asserted by the test
	assert.Equal(t, at.UTC().Truncate(time.Millisecond), got)
	assert.Equal(t, at, restriction.Right.(*filter.LiteralNode).Value, "the caller's literal is untouched") //nolint:forcetypeassert,errcheck // built above
}

// TestCompileRestrictionInvalid checks a refused restriction is a server
// fault, never a field error, and is reported after the earlier fields.
func TestCompileRestrictionInvalid(t *testing.T) {
	t.Parallel()

	s := widgetSchema(t)

	tests := []struct {
		name  string
		node  filter.Node
		cause error
	}{
		{name: "undeclared field", node: eq("tenant", "t1"), cause: filter.ErrFieldNotAllowed},
		{name: "wrong kind", node: eq("priority", "high"), cause: filter.ErrFieldTypeMismatch},
		{name: "null test on a required field", node: eq("name", nil), cause: filter.ErrUnsupportedOperation},
		{name: "enum name instead of the stored value", node: eq("kind", "KIND_A"), cause: filter.ErrEnumValueNotAllowed},
		{name: "nil child", node: &filter.BinaryOpNode{Op: filter.OpAnd, Left: eq("archived", false)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := s.Compile(t.Context(), listing.Request{Filter: `name == "x"`, Restriction: tc.node})
			require.ErrorIs(t, err, listing.ErrInvalidRestriction)
			require.ErrorIs(t, err, listing.ErrInvalidSchema)

			if tc.cause != nil {
				require.ErrorIs(t, err, tc.cause)
			}

			_, isField := errors.AsType[*coreerrs.FieldError](err)
			assert.False(t, isField, "a server restriction is not blamed on a request field")
		})
	}

	_, err := s.Compile(t.Context(), listing.Request{OrderBy: "nope", Restriction: eq("tenant", "t1")})
	assert.Equal(t, listing.FieldOrderBy, fieldOf(err), "earlier fields are reported first")

	_, err = s.Compile(t.Context(), listing.Request{Filter: "nope ==", Restriction: eq("tenant", "t1")})
	require.ErrorIs(t, err, listing.ErrInvalidRestriction, "the restriction is checked before the client filter")
}

// TestCompileRestrictionLimits checks a combined filter over the limits is a
// field error on filter, while each part alone is within them.
func TestCompileRestrictionLimits(t *testing.T) {
	t.Parallel()

	s := widgetSchema(t, listing.WithMaxFilterOperations(7))

	_, err := s.Compile(t.Context(), listing.Request{Filter: `priority > 1 && archived == false`})
	require.NoError(t, err)

	_, err = s.Compile(t.Context(), listing.Request{Restriction: eq("name", "n1")})
	require.NoError(t, err)

	_, err = s.Compile(t.Context(), listing.Request{Filter: `priority > 1 && archived == false`, Restriction: eq("name", "n1")})
	require.Equal(t, listing.FieldFilter, fieldOf(err))
	require.ErrorIs(t, err, filter.ErrMaxOperationsExceeded)
	require.NotErrorIs(t, err, listing.ErrInvalidRestriction)
}

// TestCompileRestrictionCanonical checks the canonical filter differs with
// and without a restriction, so engines bind tokens to it.
func TestCompileRestrictionCanonical(t *testing.T) {
	t.Parallel()

	s := widgetSchema(t)
	req := listing.Request{Filter: `priority > 1`}

	plain, err := s.Compile(t.Context(), req)
	require.NoError(t, err)

	req.Restriction = eq("archived", false)

	restricted, err := s.Compile(t.Context(), req)
	require.NoError(t, err)
	assert.NotEqual(t, canonical(t, plain.Filter), canonical(t, restricted.Filter))
}

func TestCompileMatchNone(t *testing.T) {
	t.Parallel()

	s := widgetSchema(t, listing.WithServerOnlyFields("archived"))
	none := listing.MatchNone("archived")
	q, err := s.Compile(t.Context(), listing.Request{Restriction: none})
	require.NoError(t, err)
	require.Equal(t, canonical(t, none), canonical(t, q.Filter))
	require.NoError(t, filter.Validate(q.Filter, s.CombinedFilterPolicy()))
	require.ErrorIs(t, filter.Validate(q.Filter, s.FilterPolicy()), filter.ErrInvalidExpression)

	_, err = s.Compile(t.Context(), listing.Request{Filter: `name in []`, Restriction: none})
	require.ErrorIs(t, err, filter.ErrInvalidExpression)

	_, err = s.Compile(t.Context(), listing.Request{Filter: `unknown == "x"`, Restriction: none})
	require.Error(t, err)
	fe, ok := errors.AsType[*coreerrs.FieldError](err)
	require.True(t, ok)
	require.Equal(t, listing.FieldFilter, fe.Field)

	_, err = s.Compile(t.Context(), listing.Request{Restriction: listing.MatchNone("unknown")})
	require.ErrorIs(t, err, listing.ErrInvalidRestriction)

	_, err = s.Compile(t.Context(), listing.Request{Cutoff: none})
	require.ErrorIs(t, err, listing.ErrInvalidRestriction)
}

func TestCompileCutoff(t *testing.T) {
	t.Parallel()
	s := widgetSchema(t)
	at := time.Date(2026, 3, 1, 0, 0, 0, 123456789, time.UTC)
	req := listing.Request{Restriction: eq("archived", false), Filter: "priority > 0"}
	plain, err := s.Compile(t.Context(), req)
	require.NoError(t, err)
	assert.Nil(t, plain.Cutoff())

	literal := &filter.LiteralNode{Value: at}
	req.Cutoff = &filter.BinaryOpNode{Op: filter.OpGT, Left: &filter.IdentNode{Name: "createTime"}, Right: literal}
	q, err := s.Compile(t.Context(), req)
	require.NoError(t, err)
	assert.Equal(t, canonical(t, plain.Filter), canonical(t, q.Filter))
	assert.Equal(t, at, literal.Value)
	literal.Value = nil
	want := &filter.BinaryOpNode{Op: filter.OpGT, Left: &filter.IdentNode{Name: "createTime"}, Right: &filter.LiteralNode{Value: at.Truncate(time.Millisecond)}}
	assert.Equal(t, canonical(t, want), canonical(t, q.Cutoff()))

	req.Cutoff = eq("kind", "KIND_A")
	_, err = s.Compile(t.Context(), req)
	require.ErrorIs(t, err, listing.ErrInvalidRestriction)
	require.ErrorIs(t, err, filter.ErrEnumValueNotAllowed)

	req = listing.Request{Filter: "priority > 1 && archived == false", Cutoff: eq("name", "n1")}
	_, err = widgetSchema(t, listing.WithMaxFilterOperations(7)).Compile(t.Context(), req)
	require.ErrorIs(t, err, listing.ErrInvalidRestriction)
	require.ErrorIs(t, err, filter.ErrMaxOperationsExceeded)
	assert.Empty(t, fieldOf(err))
}

func TestCompileServerOnlyFields(t *testing.T) {
	t.Parallel()

	fields := []listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "deleteTime", Kind: filter.FieldKindTimestamp, Presence: true},
		{Name: "purgeTime", Kind: filter.FieldKindTimestamp, Presence: true},
	}
	names := []string{"deleteTime", "purgeTime"}
	opt := listing.WithServerOnlyFields(names...)
	names[0] = "misspelled"

	expansions := listing.WithFunctions(map[string]filter.CustomFunction{
		"live": func([]filter.Node) (filter.Node, error) { return eq("deleteTime", nil), nil },
	})
	s := listing.MustSchema(fields, []string{"id"}, listing.NewProfile(opt, expansions))
	public := listing.MustSchema(fields, []string{"id"}, listing.NewProfile(expansions))
	denied := listing.MustSchema(fields, []string{"id"}, listing.NewProfile(expansions,
		listing.WithFilterOptions(filter.WithAllowedFields("id"))))

	for _, expr := range []string{`deleteTime == null`, `id == "a" || purgeTime != null`, `live()`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			req := listing.Request{Filter: expr}
			_, err := public.Compile(t.Context(), req)
			require.NoError(t, err)

			_, want := denied.Compile(t.Context(), req)
			require.ErrorIs(t, want, filter.ErrFieldNotAllowed)

			_, got := s.Compile(t.Context(), req)
			require.ErrorIs(t, got, filter.ErrFieldNotAllowed)

			wantField, ok := errors.AsType[*coreerrs.FieldError](want)
			require.True(t, ok)

			gotField, ok := errors.AsType[*coreerrs.FieldError](got)
			require.True(t, ok)
			require.Equal(t, listing.FieldFilter, gotField.Field)
			require.Equal(t, wantField.Reason, gotField.Reason)
		})
	}

	policy := softdelete.New()
	restriction := policy.Restriction(false)
	cutoff := policy.Cutoff(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	q, err := s.Compile(t.Context(), listing.Request{Restriction: restriction, Cutoff: cutoff})
	require.NoError(t, err)
	require.Equal(t, canonical(t, restriction), canonical(t, q.Filter))
	require.Equal(t, canonical(t, cutoff), canonical(t, q.Cutoff()))

	_, err = s.Compile(t.Context(), listing.Request{Restriction: eq("deleteTime", "wrong kind")})
	require.ErrorIs(t, err, listing.ErrInvalidRestriction)
	require.ErrorIs(t, err, filter.ErrFieldTypeMismatch)

	_, err = listing.NewSchema(fields, []string{"id"}, listing.NewProfile(listing.WithServerOnlyFields("missing")))
	require.ErrorIs(t, err, listing.ErrInvalidSchema)

	limited := listing.MustSchema(fields, []string{"id"}, listing.NewProfile(opt, listing.WithMaxFilterOperations(1)))
	_, err = limited.Compile(t.Context(), listing.Request{Filter: `deleteTime == null`})
	require.ErrorIs(t, err, filter.ErrMaxOperationsExceeded)
}
