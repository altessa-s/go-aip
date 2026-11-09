// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
	"github.com/altessa-s/go-aip/orderby"

	filterfuncs "github.com/altessa-s/go-aip/filter/functions"
)

func TestNewSchemaRejects(t *testing.T) {
	t.Parallel()

	str := filter.FieldKindString
	base := []listing.Field{
		{Name: "name", Kind: str},
		{Name: "tags", Kind: str, Array: true},
		{Name: "note", Kind: str, Optional: true},
		{Name: "blob", Kind: filter.FieldKindBytes},
		{Name: "price", Kind: filter.FieldKindDecimal},
		{Name: "id", Kind: str},
		{Name: "owner", Kind: str},
	}
	ok := []string{"name"}

	tests := []struct {
		name     string
		fields   []listing.Field
		patterns []string
		opts     []listing.ProfileOption
		want     string
	}{
		{name: "no fields", patterns: ok, want: "no fields"},
		{name: "no patterns", fields: base, want: "no sort patterns"},
		{name: "unnamed field", fields: []listing.Field{{Kind: str}}, patterns: ok, want: "field name"},
		{name: "bad field name", fields: []listing.Field{{Name: "a b", Kind: str}}, patterns: ok, want: "field name"},
		{
			name: "duplicate field", fields: []listing.Field{{Name: "name", Kind: str}, {Name: "name", Kind: str}},
			patterns: ok, want: "declared twice",
		},
		{name: "no kind", fields: []listing.Field{{Name: "name"}}, patterns: ok, want: "kind is required"},
		{
			name: "enum value type", fields: []listing.Field{{Name: "name", Kind: str, Enum: map[string]any{"X": 1}}},
			patterns: ok, want: "want string",
		},
		{name: "empty enum", fields: []listing.Field{{Name: "name", Kind: str, Enum: map[string]any{}}}, patterns: ok, want: "empty enum"},
		{name: "undeclared sort field", fields: base, patterns: []string{"other"}, want: "not declared"},
		{name: "array sort field", fields: base, patterns: []string{"tags"}, want: "is an array"},
		{name: "optional sort field", fields: base, patterns: []string{"note desc"}, want: "is optional"},
		{name: "bytes sort field", fields: base, patterns: []string{"blob"}, want: "does not sort"},
		{name: "duplicate pattern", fields: base, patterns: []string{"name", "name asc"}, want: "repeats the order"},
		{name: "reversal declared", fields: base, patterns: []string{"owner, name desc", "owner desc, name"}, want: "repeats the order"},
		{name: "unparsable pattern", fields: base, patterns: []string{"name sideways"}, want: "pattern"},
		{name: "empty pattern", fields: base, patterns: []string{" "}, want: "empty order"},
		{name: "repeated pattern key", fields: base, patterns: []string{"name, name desc"}, want: "more than once"},
		{name: "pattern clause", fields: base, patterns: []string{"name desc extra"}, want: "clause"},
		{
			name: "default order undeclared", fields: base, patterns: ok,
			opts: []listing.ProfileOption{listing.WithDefaultOrderBy("owner")}, want: "default order",
		},
		{
			name: "default equality undeclared", fields: base, patterns: ok,
			opts: []listing.ProfileOption{listing.WithDefaultEqualities(map[string]any{"gone": false})}, want: "not declared",
		},
		{
			name: "default equality on array", fields: base, patterns: ok,
			opts: []listing.ProfileOption{listing.WithDefaultEqualities(map[string]any{"tags": "x"})}, want: "is an array",
		},
		{
			name: "default equality kind", fields: base, patterns: ok,
			opts: []listing.ProfileOption{listing.WithDefaultEqualities(map[string]any{"name": 1})}, want: "does not match kind",
		},
		{
			name: "default page above the maximum", fields: base, patterns: ok,
			opts: []listing.ProfileOption{listing.WithDefaultPageSize(60), listing.WithMaxPageSize(50)}, want: "default page size",
		},
		{
			name: "custom function shadows built-in", fields: base, patterns: ok,
			opts: []listing.ProfileOption{listing.WithFunctions(map[string]filter.CustomFunction{"contains": filterfuncs.TextSearch([]string{"name"})})},
			want: "filter parser",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := listing.NewSchema(tc.fields, tc.patterns, listing.NewProfile(tc.opts...))
			require.ErrorIs(t, err, listing.ErrInvalidSchema)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestPresenceFields(t *testing.T) {
	t.Parallel()

	fields := []listing.Field{{Name: "name", Kind: filter.FieldKindString}, {Name: "rating", Kind: filter.FieldKindInt, Presence: true}}
	schema, err := listing.NewSchema(fields, []string{"name"}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"rating"}, schema.PresenceFields())

	_, err = listing.NewSchema(fields, []string{"rating"}, nil)
	require.ErrorIs(t, err, listing.ErrInvalidSchema)

	for _, invalid := range []listing.Field{
		{Name: "x", Kind: filter.FieldKindInt, Presence: true, Optional: true},
		{Name: "x", Kind: filter.FieldKindInt, Presence: true, Array: true},
	} {
		_, err = listing.NewSchema([]listing.Field{{Name: "name", Kind: filter.FieldKindString}, invalid}, []string{"name"}, nil)
		require.ErrorIs(t, err, listing.ErrInvalidSchema)
	}
}

// TestMustSchemaReturnsTheSchema checks that MustSchema builds the schema
// NewSchema builds from the same declaration.
func TestMustSchemaReturnsTheSchema(t *testing.T) {
	t.Parallel()

	opts := []listing.ProfileOption{listing.WithFunctions(listingtest.Functions()), listing.WithMaxPageSize(100)}
	want, err := listing.NewSchema(listingtest.Fields(), listingtest.Patterns(), listing.NewProfile(opts...))
	require.NoError(t, err)

	got := listing.MustSchema(listingtest.Fields(), listingtest.Patterns(), listing.NewProfile(opts...))
	require.NotNil(t, got)
	require.Equal(t, want.Fields(), got.Fields())
	require.Equal(t, want.OptionalFields(), got.OptionalFields())
	require.Equal(t, want.Default(), got.Default())
	require.Equal(t, want.Orders(), got.Orders())
	require.Equal(t, want.MaxPageSize(), got.MaxPageSize())
}

// TestMustSchemaPanics checks that MustSchema panics with an error that
// names it and wraps the NewSchema error.
func TestMustSchemaPanics(t *testing.T) {
	t.Parallel()

	fields := []listing.Field{{Name: "name", Kind: filter.FieldKindString}}
	_, want := listing.NewSchema(fields, []string{"gone"}, nil)
	require.ErrorIs(t, want, listing.ErrInvalidSchema)

	var recovered any

	func() {
		defer func() { recovered = recover() }()

		listing.MustSchema(fields, []string{"gone"}, nil)
	}()

	err, ok := recovered.(error)
	require.True(t, ok, "panic value %T is not an error", recovered)
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
	require.Equal(t, "listing.MustSchema: "+want.Error(), err.Error())
}

func TestOrderAccepts(t *testing.T) {
	t.Parallel()

	s := widgetSchema(t)

	tests := []struct {
		orderBy   string
		canonical string
		keys      []listing.OrderKey
		reversed  bool
	}{
		{"", "createTime desc", []listing.OrderKey{{Field: "createTime", Desc: true}}, false},
		{"createTime desc", "createTime desc", []listing.OrderKey{{Field: "createTime", Desc: true}}, false},
		{"createTime", "createTime", []listing.OrderKey{{Field: "createTime"}}, true},
		{"createTime asc", "createTime", []listing.OrderKey{{Field: "createTime"}}, true},
		{"  name  ", "name", []listing.OrderKey{{Field: "name"}}, false},
		{"name DESC", "name desc", []listing.OrderKey{{Field: "name", Desc: true}}, true},
		{"kind, priority desc", "kind, priority desc", []listing.OrderKey{{Field: "kind"}, {Field: "priority", Desc: true}}, false},
		{"kind desc,priority", "kind desc, priority", []listing.OrderKey{{Field: "kind", Desc: true}, {Field: "priority"}}, true},
		{"id desc", "id desc", []listing.OrderKey{{Field: "id", Desc: true}}, true},
	}
	for _, tc := range tests {
		t.Run(tc.orderBy, func(t *testing.T) {
			t.Parallel()
			o, err := s.Order(t.Context(), tc.orderBy)
			require.NoError(t, err)
			require.Equal(t, tc.canonical, o.String())
			require.Equal(t, tc.keys, o.Keys())
			require.Equal(t, tc.reversed, o.Reversed())
		})
	}
}

func TestOrderRejects(t *testing.T) {
	t.Parallel()

	s := widgetSchema(t)

	tests := []struct {
		orderBy string
		cause   error
	}{
		{"priority", nil},                  // a prefix of a pattern is not a pattern
		{"kind", nil},                      // likewise
		{"kind, priority", nil},            // mixed directions not declared
		{"kind desc, priority desc", nil},  // likewise
		{"priority desc, createTime", nil}, // neither the pattern nor its reversal
		{"createTime desc, name", nil},     // extra key
		{"name, id", nil},                  // the id tie-breaker is implicit
		{"note", nil},                      // optional field
		{"unknown", nil},                   // undeclared field
		{"name sideways", orderby.ErrInvalidDirection},
		{"name, name desc", orderby.ErrDuplicateKey},
		{"name,,id", orderby.ErrEmptyClause},
		{"1name", orderby.ErrInvalidFieldPath},
	}
	for _, tc := range tests {
		t.Run(tc.orderBy, func(t *testing.T) {
			t.Parallel()
			_, err := s.Order(t.Context(), tc.orderBy)
			require.Equal(t, listing.FieldOrderBy, fieldOf(err), "error %v", err)

			if tc.cause != nil {
				require.ErrorIs(t, err, tc.cause)
				return
			}

			require.Contains(t, err.Error(), "name desc", "the accepted orders are listed")
		})
	}
}

func TestDefaultOrder(t *testing.T) {
	t.Parallel()
	require.Equal(t, "createTime desc", widgetSchema(t).Default().String(), "the first pattern")
	require.Equal(t, "name desc", widgetSchema(t, listing.WithDefaultOrderBy("name desc")).Default().String(), "the option's (a reversal)")
}

func TestOrdersListsPatternsAndReversals(t *testing.T) {
	t.Parallel()

	var got []string
	for _, o := range widgetSchema(t).Orders() {
		got = append(got, o.String())
	}

	require.Equal(t, []string{
		"createTime desc", "createTime", "name", "name desc", "kind, priority desc", "kind desc, priority",
		"priority, createTime", "priority desc, createTime desc", "id", "id desc",
	}, got)
}

func TestFieldAccessors(t *testing.T) {
	t.Parallel()

	s := widgetSchema(t, listing.WithMaxPageSize(80))
	require.Equal(t, []string{"archived", "createTime", "flag", "id", "kind", "kind2", "level", "name", "note", "priority", "rating", "score", "tags"}, s.Fields())
	f, ok := s.Field("tags")
	require.True(t, ok)
	require.True(t, f.Array)

	_, ok = s.Field("owner")
	require.False(t, ok)
	require.Equal(t, 80, s.MaxPageSize())
}

func TestOptionalEnumRequiresZeroValue(t *testing.T) {
	t.Parallel()

	base := []listing.Field{{Name: "id", Kind: filter.FieldKindString}, {
		Name: "state", Kind: filter.FieldKindString, Optional: true, Enum: map[string]any{"STATE_UNSPECIFIED": "", "STATE_ON": "on"},
	}}
	_, err := listing.NewSchema(base, []string{"id"}, nil)
	require.ErrorIs(t, err, listing.ErrInvalidSchema)

	base[1].EnumZero = "STATE_ON"
	_, err = listing.NewSchema(base, []string{"id"}, nil)
	require.ErrorIs(t, err, listing.ErrInvalidSchema)

	base[1].EnumZero = "STATE_UNSPECIFIED"
	s, err := listing.NewSchema(base, []string{"id"}, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]filter.FieldKind{"state": filter.FieldKindString}, s.ZeroFields())
}

func TestEnumZeroRequiresOptionalField(t *testing.T) {
	t.Parallel()

	_, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "kind", Kind: filter.FieldKindString, Enum: map[string]any{"KIND_A": "a"}, EnumZero: "KIND_A"},
	}, []string{"id"}, nil)
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
}
