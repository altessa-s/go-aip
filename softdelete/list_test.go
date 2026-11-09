// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package softdelete_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/softdelete"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

func listPolicy(t *testing.T, deleteField string) *filter.Policy {
	t.Helper()

	policy, err := filter.NewPolicy(
		filter.WithAllowedFields("title", deleteField),
		filter.WithFieldTypes(map[string]filter.FieldKind{"title": filter.FieldKindString, deleteField: filter.FieldKindTimestamp}),
	)
	require.NoError(t, err)

	return policy
}

func TestRestrict(t *testing.T) {
	t.Parallel()

	parser, err := filterparser.New(filterparser.WithNoCache(true))
	require.NoError(t, err)

	deleted := map[string]any{"title": "Dune", "deleteTime": time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	live := map[string]any{"title": "Dune", "deleteTime": nil}
	other := map[string]any{"title": "Emma", "deleteTime": nil}

	tests := []struct {
		name        string
		expr        string
		showDeleted bool
		want        []bool // deleted, live, other
	}{
		{name: "empty filter hides deleted", want: []bool{false, true, true}},
		{name: "empty filter show deleted", showDeleted: true, want: []bool{true, true, true}},
		{name: "user filter hides deleted", expr: `title == "Dune"`, want: []bool{false, true, false}},
		{name: "user filter show deleted", expr: `title == "Dune"`, showDeleted: true, want: []bool{true, true, false}},
		{name: "disjunction stays grouped", expr: `title == "Dune" || title == "Emma"`, want: []bool{false, true, true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := softdelete.New()
			policy := listPolicy(t, softdelete.FilterFieldDeleteTime)

			var node filter.Node

			if tt.expr != "" {
				var err error

				node, err = parser.Parse(t.Context(), tt.expr)
				require.NoError(t, err)
			}

			before, err := filter.Canonical(node)
			require.NoError(t, err)

			restricted := p.Restrict(node, tt.showDeleted)
			require.NoError(t, filter.Validate(restricted, policy))

			after, err := filter.Canonical(node)
			require.NoError(t, err)
			assert.Equal(t, before, after, "the input AST is not modified")

			eval, err := filtereval.New(policy)
			require.NoError(t, err)

			for i, data := range []map[string]any{deleted, live, other} {
				got, err := eval.Evaluate(restricted, data)
				require.NoError(t, err)
				assert.Equal(t, tt.want[i], got, "item %d", i)
			}
		})
	}
}

func TestRestrictCustomFieldAndCanonicalForm(t *testing.T) {
	t.Parallel()

	p := softdelete.New(softdelete.WithFilterField("removedAt"))
	require.NoError(t, filter.Validate(p.Restrict(nil, false), listPolicy(t, "removedAt")))

	shown, err := filter.Canonical(p.Restrict(nil, true))
	require.NoError(t, err)

	hidden, err := filter.Canonical(p.Restrict(nil, false))
	require.NoError(t, err)
	assert.NotEqual(t, shown, hidden, "a page token bound to the filter tells show_deleted values apart")
	assert.Nil(t, p.Restrict(nil, true))
}

func TestRestrictUnderListingSchemaPolicy(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{
		{Name: "title", Kind: filter.FieldKindString},
		{Name: "deleteTime", Kind: filter.FieldKindTimestamp, Presence: true},
	}, []string{"title"}, nil)
	require.NoError(t, err)

	q, err := schema.Compile(t.Context(), listing.Request{Filter: `title == "Dune"`})
	require.NoError(t, err)

	p := softdelete.New()
	require.NoError(t, filter.Validate(p.Restrict(q.Filter, false), schema.FilterPolicy()))
	require.NoError(t, filter.Validate(p.Restrict(nil, false), schema.FilterPolicy()))
}

// TestRestrictionWithListing checks the show_deleted restriction under a
// listing schema: accepted on a nullable delete time, refused as a server
// fault on a required one.
func TestRestrictionWithListing(t *testing.T) {
	t.Parallel()

	p := softdelete.New()
	assert.Nil(t, p.Restriction(true))
	assert.NotSame(t, p.Restriction(false), p.Restriction(false), "a new node on every call")

	tests := []struct {
		field listing.Field
		ok    bool
	}{
		{field: listing.Field{Name: "deleteTime", Kind: filter.FieldKindTimestamp, Presence: true}, ok: true},
		{field: listing.Field{Name: "deleteTime", Kind: filter.FieldKindTimestamp, Optional: true}, ok: true},
		{field: listing.Field{Name: "deleteTime", Kind: filter.FieldKindTimestamp}},
	}
	for _, tc := range tests {
		schema, err := listing.NewSchema([]listing.Field{{Name: "title", Kind: filter.FieldKindString}, tc.field}, []string{"title"}, nil)
		require.NoError(t, err)

		for _, show := range []bool{false, true} {
			q, err := schema.Compile(t.Context(), listing.Request{Filter: `title == "Dune"`, Restriction: p.Restriction(show)})
			if !tc.ok && !show {
				require.ErrorIs(t, err, listing.ErrInvalidRestriction)
				continue
			}

			require.NoError(t, err)

			want, _ := filter.Canonical(&filter.BinaryOpNode{
				Op: filter.OpEqual, Left: &filter.IdentNode{Name: "title"}, Right: &filter.LiteralNode{Value: "Dune"},
			})
			if !show {
				want, _ = filter.Canonical(&filter.BinaryOpNode{
					Op: filter.OpAnd,
					Left: &filter.BinaryOpNode{
						Op: filter.OpEqual, Left: &filter.IdentNode{Name: "title"}, Right: &filter.LiteralNode{Value: "Dune"},
					},
					Right: p.Restriction(false),
				})
			}

			got, err := filter.Canonical(q.Filter)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		}
	}
}
