// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/drivertest"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/xoptions"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"

	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"
)

// widgetOptions are the engine options every widget list starts from.
func widgetOptions(opts ...mongo.ListEngineOption) []mongo.ListEngineOption {
	return append([]mongo.ListEngineOption{mongo.WithListFieldPaths(listingtest.FieldPaths())}, opts...)
}

// newWidgets returns an unconnected widget list: enough for everything
// decided before I/O.
func newWidgets(tb testing.TB, s *listing.Schema, opts ...mongo.ListEngineOption) *mongo.ListEngine[listingtest.Widget] {
	tb.Helper()

	l, err := mongo.NewListEngine[listingtest.Widget](nil, nil, s, widgetOptions(opts...)...)
	require.NoError(tb, err)

	return l
}

func TestNewRejects(t *testing.T) {
	t.Parallel()

	str := filter.FieldKindString
	fields := []listing.Field{
		{Name: "id", Kind: str},
		{Name: "name", Kind: str},
		{Name: "owner", Kind: str},
		{Name: "rank", Kind: filter.FieldKindInt},
		{Name: "rating", Kind: filter.FieldKindInt, Presence: true},
	}
	idPath := mongo.WithListFieldPaths(map[string]string{"id": "_id"})
	sortable := listing.WithSortableFields("name", "rank", "rating")

	tests := []struct {
		name     string
		patterns []string
		schema   []listing.ProfileOption
		opts     []mongo.ListEngineOption
		want     string
	}{
		{name: "presence key", patterns: []string{"rating"}, schema: []listing.ProfileOption{sortable}, want: `field "rating" is a Presence field`},
		{name: "presence key after another", patterns: []string{"name", "rank, rating desc"}, schema: []listing.ProfileOption{sortable}, want: "Presence field"},
		{name: "page ceiling", patterns: []string{"name"}, schema: []listing.ProfileOption{listing.WithMaxPageSize(mongo.MaxListPageSize + 1)}, want: "largest page"},
		{name: "scope key on the id", patterns: []string{"name"}, opts: []mongo.ListEngineOption{mongo.WithListScopeKeys("_id"), mongo.WithListParentKey("_id")}, want: "scope key"},
		{name: "scope key not a path", patterns: []string{"name"}, opts: []mongo.ListEngineOption{mongo.WithListScopeKeys("a b"), mongo.WithListParentKey("a b")}, want: "scope key"},
		{name: "path of an undeclared field", patterns: []string{"name"}, opts: []mongo.ListEngineOption{mongo.WithListFieldPaths(map[string]string{"gone": "x"})}, want: "not declared"},
		{name: "bad path", patterns: []string{"name"}, opts: []mongo.ListEngineOption{mongo.WithListFieldPaths(map[string]string{"name": "a b"})}, want: `path "a b"`},
		{name: "sort path on the id", patterns: []string{"name"}, opts: []mongo.ListEngineOption{mongo.WithListSortPaths(map[string]string{"name": "_id"})}, want: "sort path"},
		{name: "sort path of an undeclared field", patterns: []string{"name"}, opts: []mongo.ListEngineOption{mongo.WithListSortPaths(map[string]string{"gone": "x"})}, want: "not declared"},
		{name: "id not last", patterns: []string{"id, name"}, opts: []mongo.ListEngineOption{idPath}, want: "only be the last key"},
		{
			name: "two keys on one path", patterns: []string{"name, rank"},
			opts: []mongo.ListEngineOption{mongo.WithListFieldPaths(map[string]string{"rank": "name"})}, want: "same path",
		},
		{
			name: "sort path repeats another key", patterns: []string{"rank, name"},
			opts: []mongo.ListEngineOption{mongo.WithListSortPaths(map[string]string{"rank": "name"})}, want: "same path",
		},
		{name: "pattern on a scope key", patterns: []string{"owner"}, opts: []mongo.ListEngineOption{mongo.WithListScopeKeys("owner"), mongo.WithListParentKey("owner")}, want: "scope key"},
		{
			name: "sort path on a scope key", patterns: []string{"rank"},
			opts: []mongo.ListEngineOption{mongo.WithListScopeKeys("owner"), mongo.WithListParentKey("owner"), mongo.WithListSortPaths(map[string]string{"rank": "owner"})}, want: "scope key",
		},
		{name: "index of an undeclared pattern", patterns: []string{"name"}, opts: []mongo.ListEngineOption{mongo.WithListIndexNames(map[string]string{"rank": "i"})}, want: "not a declared pattern"},
		{
			name: "pattern named twice", patterns: []string{"name"},
			opts: []mongo.ListEngineOption{mongo.WithListIndexNames(map[string]string{"name": "a", "name desc": "b"})}, want: "named twice",
		},
		{name: "empty index name", patterns: []string{"name"}, opts: []mongo.ListEngineOption{mongo.WithListIndexNames(map[string]string{"name": ""})}, want: "empty index name"},
		{
			name: "index name used twice", patterns: []string{"name", "rank"},
			opts: []mongo.ListEngineOption{mongo.WithListIndexNames(map[string]string{"name": "a", "rank": "a"})}, want: "used twice",
		},
		{
			name: "generated name taken", patterns: []string{"name", "rank"},
			opts: []mongo.ListEngineOption{mongo.WithListIndexNames(map[string]string{"rank": "name_1__id_1"})}, want: "used twice",
		},
		{
			name: "built-in index named", patterns: []string{"id"},
			opts: []mongo.ListEngineOption{idPath, mongo.WithListIndexNames(map[string]string{"id": "idx_id"})}, want: "built-in index",
		},
		{
			name: "built-in index name reused", patterns: []string{"name"},
			opts: []mongo.ListEngineOption{mongo.WithListIndexNames(map[string]string{"name": mongo.IDIndex})}, want: "reserved",
		},
		{
			name: "scoped id order on the built-in index", patterns: []string{"id"},
			opts: []mongo.ListEngineOption{idPath, mongo.WithListScopeKeys("owner"), mongo.WithListParentKey("owner"), mongo.WithListIndexNames(map[string]string{"id": mongo.IDIndex})}, want: "reserved",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s, err := listing.NewSchema(fields, tc.patterns, listing.NewProfile(tc.schema...))
			require.NoError(t, err)
			_, err = mongo.NewListEngine[listingtest.Widget](nil, nil, s, tc.opts...)
			require.ErrorIs(t, err, listing.ErrInvalidSchema)
			require.Contains(t, err.Error(), tc.want)
		})
	}

	_, err := mongo.NewListEngine[listingtest.Widget](nil, nil, nil)
	require.ErrorIs(t, err, listing.ErrInvalidSchema, "nil schema")
}

// sortString renders a bson.D sort as "path:dir,...".
func sortString(d bson.D) string {
	parts := make([]string, len(d))
	for i, e := range d {
		parts[i] = fmt.Sprintf("%s:%v", e.Key, e.Value)
	}

	return strings.Join(parts, ",")
}

func TestSortAndIndex(t *testing.T) {
	t.Parallel()

	s := listingtest.NewSchema(t)
	named := newWidgets(t, s, mongo.WithListIndexNames(map[string]string{"createTime desc": "idx_create_time", "kind DESC, priority": "idx_kind"}))
	scoped := newWidgets(t, s, mongo.WithListScopeKeys("owner"), mongo.WithListParentKey("owner"))

	tests := []struct {
		orderBy string
		list    *mongo.ListEngine[listingtest.Widget]
		sort    string
		index   string
	}{
		{"", named, "created_at:-1,_id:-1", "idx_create_time"},
		{"createTime", named, "created_at:1,_id:1", "idx_create_time"},
		{"name DESC", named, "name:-1,_id:-1", "name_1__id_1"},
		{"kind, priority desc", named, "kind:1,priority:-1,_id:-1", "idx_kind"},
		{"kind desc, priority", named, "kind:-1,priority:1,_id:1", "idx_kind"},
		{"priority desc, createTime desc", named, "priority:-1,created_at:-1,_id:-1", "priority_1_created_at_1__id_1"},
		{"id", named, "_id:1", mongo.IDIndex},
		{"id desc", named, "_id:-1", mongo.IDIndex},
		{"name", scoped, "name:1,_id:1", "owner_1_name_1__id_1"},
		{"id desc", scoped, "_id:-1", "owner_1__id_1"},
	}
	for _, tc := range tests {
		o, err := s.Order(t.Context(), tc.orderBy)
		require.NoError(t, err)
		require.Equal(t, tc.sort, sortString(tc.list.Sort(o)), "sort of %q", tc.orderBy)
		require.Equal(t, tc.index, tc.list.Index(o), "index of %q", tc.orderBy)
	}
}

func TestIndexes(t *testing.T) {
	t.Parallel()

	s := listingtest.NewSchema(t)

	tests := []struct {
		name string
		list *mongo.ListEngine[listingtest.Widget]
		want []string
	}{
		{
			name: "generated names", list: newWidgets(t, s),
			want: []string{ // the id-only order uses the built-in _id_ index
				"created_at_-1__id_-1=created_at:-1,_id:-1", "name_1__id_1=name:1,_id:1",
				"kind_1_priority_-1__id_-1=kind:1,priority:-1,_id:-1", "priority_1_created_at_1__id_1=priority:1,created_at:1,_id:1",
			},
		},
		{
			name: "scoped with a given name", list: newWidgets(t, s, mongo.WithListScopeKeys("owner"), mongo.WithListParentKey("owner"), mongo.WithListIndexNames(map[string]string{"name": "idx_name"})),
			want: []string{
				"owner_1_created_at_-1__id_-1=owner:1,created_at:-1,_id:-1", "idx_name=owner:1,name:1,_id:1",
				"owner_1_kind_1_priority_-1__id_-1=owner:1,kind:1,priority:-1,_id:-1",
				"owner_1_priority_1_created_at_1__id_1=owner:1,priority:1,created_at:1,_id:1", "owner_1__id_1=owner:1,_id:1",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got []string

			for _, m := range tc.list.Indexes() {
				keys, ok := m.Keys.(bson.D)
				require.True(t, ok, "index keys are %T, want bson.D", m.Keys)

				var io mongooptions.IndexOptions
				for _, set := range m.Options.Opts {
					require.NoError(t, set(&io))
				}

				require.NotNil(t, io.Name)
				got = append(got, *io.Name+"="+sortString(keys))
			}

			require.Equal(t, tc.want, got)
		})
	}
}

// TestListWithoutCollection checks an unconnected list reports a failure
// found before any I/O (here a computed projection of a sort field) instead
// of panicking on the missing collection, and refuses an invalid request
// before querying.
func TestListWithoutCollection(t *testing.T) {
	t.Parallel()

	l := newWidgets(t, listingtest.NewSchema(t), mongo.WithListProjection(bson.D{{Key: "created_at", Value: "$name"}}))
	_, err := l.List(t.Context(), listingtest.Subject, listing.Request{}, nil)
	require.Error(t, err)
	require.Empty(t, listingtest.FieldOf(err))

	_, err = l.List(t.Context(), listingtest.Subject, listing.Request{Filter: `size(note) < 1.5`}, nil)
	require.Equal(t, listing.FieldFilter, listingtest.FieldOf(err))
	require.Contains(t, err.Error(), "integer")

	_, err = l.List(t.Context(), listingtest.Subject, listing.Request{Filter: `size(tags) == 2147483648`}, nil)
	require.Equal(t, listing.FieldFilter, listingtest.FieldOf(err))
	require.ErrorIs(t, err, filter.ErrInvalidExpression)
}

func TestConfiguredTiebreakPath(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{{Name: "name", Kind: filter.FieldKindString}}, []string{"name desc"}, nil)
	require.NoError(t, err)
	list, err := mongo.NewListEngine[any](nil, nil, schema, mongo.WithListTiebreakPath("identity.uuid"))
	require.NoError(t, err)
	require.Equal(t, bson.D{{Key: "name", Value: int32(-1)}, {Key: "identity.uuid", Value: int32(-1)}}, list.Sort(schema.Default()))
	require.Equal(t, "name_-1_identity.uuid_-1", list.Index(schema.Default()))
	require.Len(t, list.Indexes(), 1)
	reverse, err := schema.Order(t.Context(), "name")
	require.NoError(t, err)
	require.Equal(t, bson.D{{Key: "name", Value: int32(1)}, {Key: "identity.uuid", Value: int32(1)}}, list.Sort(reverse))

	_, err = mongo.NewListEngine[any](nil, nil, schema, mongo.WithListTiebreakPath("$bad"))
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
	_, err = mongo.NewListEngine[any](nil, nil, schema, mongo.WithListTiebreakPath("identity.uuid"), mongo.WithListScopeKeys("identity.uuid"))
	require.ErrorIs(t, err, listing.ErrInvalidSchema)
}

func TestNewRejectsDynamicOrderProfiles(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		patterns []string
		options  []listing.ProfileOption
	}{
		{[]string{"name"}, []listing.ProfileOption{listing.WithUnrestrictedOrder(true)}},
	} {
		schema, err := listing.NewSchema([]listing.Field{{Name: "name", Kind: filter.FieldKindString}}, tc.patterns, listing.NewProfile(tc.options...))
		require.NoError(t, err)
		_, err = mongo.NewListEngine[listingtest.Widget](nil, nil, schema)
		require.ErrorIs(t, err, listing.ErrInvalidSchema)
	}
}

func TestEmptyOrderUsesIdentity(t *testing.T) {
	t.Parallel()

	s, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, nil, listing.NewProfile(listing.WithEmptyOrderAllowed(true)))
	require.NoError(t, err)
	l, err := mongo.NewListEngine[struct{}](nil, nil, s, mongo.WithListScopeKeys("tenant"))
	require.NoError(t, err)
	require.NotEmpty(t, l.Indexes())
}

// TestBindingsOfATranslatedFilterAreStable checks the MongoDB translator renders the same filter to the same BSON bytes on every
// call, so the filter a page token is bound to binds again on the next page.
func TestBindingsOfATranslatedFilterAreStable(t *testing.T) {
	t.Parallel()

	schema, err := mongo.NewSchema(mongo.WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{"note": filter.FieldKindString}))
	require.NoError(t, err)

	tr, err := mongo.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAnyField(true)), schema)
	require.NoError(t, err)

	node := filtertest.MustParseFilter(t, `(rank > 3 || tags.size() <= 2) && note.startsWith("a") && !(kind in ["x", "y"])`)
	first, err := tr.TranslateD(node)
	require.NoError(t, err)

	want, err := bson.Marshal(first)
	require.NoError(t, err)

	for range 20 {
		again, err := tr.TranslateD(node)
		require.NoError(t, err)

		got, err := bson.Marshal(again)
		require.NoError(t, err)
		require.Equal(t, want, got, "the same filter binds the same token")
	}
}

// TestEnsureIndexesReturnsServerConflicts checks EnsureIndexes returns either createIndexes conflict without attempting to drop or
// recreate the index.
func TestEnsureIndexesReturnsServerConflicts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		code int
	}{
		{"IndexOptionsConflict", 85},
		{"IndexKeySpecsConflict", 86},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reply := bson.D{{Key: "ok", Value: 0}, {Key: "code", Value: tc.code}, {Key: "errmsg", Value: tc.name}}

			var commands []string

			opts := mongooptions.Client().SetMonitor(&event.CommandMonitor{
				Started: func(_ context.Context, ev *event.CommandStartedEvent) {
					commands = append(commands, ev.CommandName)
				},
			})
			require.NoError(t, xoptions.SetInternalClientOptions(opts, "deployment", drivertest.NewMockDeployment(reply)))

			client, err := drivermongo.Connect(opts)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.Disconnect(context.Background())) })

			coll := client.Database("test").Collection("widgets")
			engine, err := mongo.NewListEngine[listingtest.Widget](coll, nil, listingtest.NewSchema(t), widgetOptions()...)
			require.NoError(t, err)

			conflict, ok := errors.AsType[drivermongo.ServerError](engine.EnsureIndexes(t.Context()))
			require.True(t, ok)
			require.True(t, conflict.HasErrorCode(tc.code))
			require.Equal(t, []string{"createIndexes"}, commands)
		})
	}
}
