// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package mongo_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"

	"github.com/altessa-s/go-aip/apierror"
	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/backend/mongo/mongotest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/integration/docker/internal/dbtest"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
	"github.com/altessa-s/go-aip/pagination/page"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
)

// collections numbers the collections of the test binary.
var collections atomic.Int64

// newCollection returns a fresh collection of a fresh database.
func newCollection(t *testing.T, name string) *drivermongo.Collection {
	t.Helper()

	return dbtest.Mongo(t).Collection(fmt.Sprintf("%s_%d", name, collections.Add(1)))
}

// widgetCollection returns a fresh widgets collection holding ws in their stored form (listingtest.Widget's bson tags: an absent
// optional value omitted, a nil presence value null).
func widgetCollection(t *testing.T, ws []listingtest.Widget) *drivermongo.Collection {
	t.Helper()

	coll := newCollection(t, "widgets")
	if len(ws) == 0 {
		return coll
	}

	docs := make([]any, len(ws))
	for i, w := range ws {
		docs[i] = w
	}

	_, err := coll.InsertMany(t.Context(), docs)
	require.NoError(t, err)

	return coll
}

// widgetOptions are the engine options every widget list starts from.
func widgetOptions(opts ...mongo.ListEngineOption) []mongo.ListEngineOption {
	return append([]mongo.ListEngineOption{mongo.WithListFieldPaths(listingtest.FieldPaths())}, opts...)
}

// widgetList returns the list engine of a fresh collection holding ws, its indexes ensured.
func widgetList(t *testing.T, s *listing.Schema, codec pagetoken.Signer, ws []listingtest.Widget,
	opts ...mongo.ListEngineOption,
) (*mongo.ListEngine[listingtest.Widget], *drivermongo.Collection) {
	t.Helper()

	coll := widgetCollection(t, ws)

	l, err := mongo.NewListEngine[listingtest.Widget](coll, codec, s, widgetOptions(opts...)...)
	require.NoError(t, err)
	require.NoError(t, l.EnsureIndexes(t.Context()))

	return l, coll
}

// scoped lists l restricted to scope.
func scoped(l *mongo.ListEngine[listingtest.Widget], scope bson.D) listingtest.Lister {
	return func(ctx context.Context, subject string, req listing.Request) (page.Page[listingtest.Widget], error) {
		return l.List(ctx, subject, req, scope)
	}
}

// TestListEngineConformance runs the listing engine conformance suite against real MongoDB collections: every order and page
// size, every contract filter, token binding and request validation, executed by the server.
func TestListEngineConformance(t *testing.T) {
	t.Parallel()

	listingtest.Run(t, func(t *testing.T, s *listing.Schema, codec pagetoken.Signer, items []listingtest.Widget) listingtest.Lister {
		t.Helper()

		l, _ := widgetList(t, s, codec, items)

		return scoped(l, nil)
	})
}

// ids lists the _id of every document l selects for expr, ordered by id.
func ids(t *testing.T, l *mongo.ListEngine[bson.M], expr string) []string {
	t.Helper()

	p, err := l.List(t.Context(), listingtest.Subject, listing.Request{PageSize: 20, OrderBy: "id", Filter: expr}, nil)
	require.NoError(t, err)

	out := make([]string, len(p.Items))
	for i, item := range p.Items {
		out[i], _ = item["_id"].(string)
	}

	return out
}

// TestProto3ZeroAndArrayStorage checks the proto3 storage contract on stored documents: an absent or null optional scalar reads
// as its zero value, an absent, null or empty array as the empty array.
func TestProto3ZeroAndArrayStorage(t *testing.T) {
	t.Parallel()

	coll := newCollection(t, "proto3_filters")
	docs := []any{
		bson.D{{Key: "_id", Value: "absent"}},
		bson.D{{Key: "_id", Value: "null"}, {Key: "note", Value: nil}, {Key: "tags", Value: nil}},
		bson.D{
			{Key: "_id", Value: "zero"}, {Key: "note", Value: ""}, {Key: "level", Value: int64(0)}, {Key: "flag", Value: false},
			{Key: "tags", Value: bson.A{}},
		},
		bson.D{
			{Key: "_id", Value: "value"}, {Key: "note", Value: "x"}, {Key: "level", Value: int64(1)}, {Key: "flag", Value: true},
			{Key: "tags", Value: bson.A{"t1"}},
		},
		bson.D{{Key: "_id", Value: "other"}, {Key: "tags", Value: bson.A{"t2"}}},
	}
	_, err := coll.InsertMany(t.Context(), docs)
	require.NoError(t, err)

	schema, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "note", Kind: filter.FieldKindString, Optional: true},
		{Name: "level", Kind: filter.FieldKindInt, Optional: true},
		{Name: "flag", Kind: filter.FieldKindBool, Optional: true},
		{Name: "tags", Kind: filter.FieldKindString, Array: true},
	}, []string{"id"}, nil)
	require.NoError(t, err)

	l, err := mongo.NewListEngine[bson.M](coll, nil, schema, mongo.WithListFieldPaths(map[string]string{"id": "_id"}))
	require.NoError(t, err)

	require.Equal(t, []string{"absent", "null", "other", "zero"}, ids(t, l, `note == ""`))
	require.Equal(t, []string{"value"}, ids(t, l, `note != ""`))
	require.Equal(t, []string{"absent", "null", "other", "zero"}, ids(t, l, `note == null`))
	require.Equal(t, []string{"absent", "null", "other", "zero"}, ids(t, l, `level == 0`))
	require.Equal(t, []string{"absent", "null", "other", "zero"}, ids(t, l, `flag == false`))
	require.Equal(t, []string{"absent", "null", "zero"}, ids(t, l, `tags == null`))
	require.Equal(t, []string{"other", "value"}, ids(t, l, `tags != null`))
	require.Equal(t, []string{"absent", "null", "other", "zero"}, ids(t, l, `tags != "t1"`))
}

// TestPresenceStorage checks a Presence field on stored documents: null and absent are one state, which differs from every value
// (so != and the negation of == hold for it), is in no list, and is unknown to an ordering comparison, also under negation.
func TestPresenceStorage(t *testing.T) {
	t.Parallel()

	coll := newCollection(t, "presence_filters")
	docs := []any{
		bson.D{{Key: "_id", Value: "absent"}},
		bson.D{{Key: "_id", Value: "null"}, {Key: "rating", Value: nil}},
		bson.D{{Key: "_id", Value: "zero"}, {Key: "rating", Value: int64(0)}},
		bson.D{{Key: "_id", Value: "one"}, {Key: "rating", Value: int64(1)}},
		bson.D{{Key: "_id", Value: "two"}, {Key: "rating", Value: int64(2)}},
	}
	_, err := coll.InsertMany(t.Context(), docs)
	require.NoError(t, err)

	schema, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "rating", Kind: filter.FieldKindInt, Presence: true},
	}, []string{"id"}, nil)
	require.NoError(t, err)

	l, err := mongo.NewListEngine[bson.M](coll, nil, schema, mongo.WithListFieldPaths(map[string]string{"id": "_id"}))
	require.NoError(t, err)

	require.Equal(t, []string{"absent", "null", "two", "zero"}, ids(t, l, `rating != 1`))
	require.Equal(t, []string{"absent", "null", "two", "zero"}, ids(t, l, `!(rating == 1)`))
	require.Equal(t, []string{"absent", "null", "zero"}, ids(t, l, `!(rating in [1, 2])`))
	require.Equal(t, []string{"one", "zero"}, ids(t, l, `!(rating > 1)`))
	require.Equal(t, []string{"two"}, ids(t, l, `rating > 1`))
	require.Equal(t, []string{"absent", "null"}, ids(t, l, `rating == null`))
	require.Equal(t, []string{"zero"}, ids(t, l, `rating == 0`))
}

// TestScoped pages a child list: the scope pins the parent, the token is bound to it.
func TestScoped(t *testing.T) {
	t.Parallel()

	ws := listingtest.MakeWidgets(60)
	s := listingtest.NewSchema(t, listing.WithParentRequired(true))
	l, _ := widgetList(t, s, pagetokentest.Codec(t), ws, mongo.WithListScopeKeys("owner"), mongo.WithListParentKey("owner"))

	for _, owner := range []string{"owner-0", "owner-1"} {
		var want []listingtest.Widget

		for _, w := range ws {
			if w.Owner == owner {
				want = append(want, w)
			}
		}

		got := listingtest.Collect(t, scoped(l, bson.D{{Key: "owner", Value: owner}}), listing.Request{PageSize: 4, OrderBy: "id", Parent: owner})
		require.ElementsMatch(t, listingtest.IDs(want), got)
		require.IsIncreasing(t, got)
	}

	_, err := l.List(t.Context(), listingtest.Subject, listing.Request{}, nil)
	require.Equal(t, listing.FieldParent, listingtest.FieldOf(err))

	scope := bson.D{{Key: "owner", Value: "owner-0"}}
	p, err := l.List(t.Context(), listingtest.Subject, listing.Request{PageSize: 4, Parent: "owner-0"}, scope)
	require.NoError(t, err)

	// The token under another parent, correctly scoped: the parent binds it.
	_, err = l.List(t.Context(), listingtest.Subject, listing.Request{PageSize: 4, Parent: "owner-1", PageToken: p.NextPageToken},
		bson.D{{Key: "owner", Value: "owner-1"}})
	require.Equal(t, pagetoken.TokenField, listingtest.FieldOf(err))

	// A store that pins the scope to another value than the parent fails before querying instead of reading another parent's
	// items.
	_, err = l.List(t.Context(), listingtest.Subject, listing.Request{PageSize: 4, Parent: "owner-1"}, scope)
	require.ErrorIs(t, err, mongo.ErrScope)
}

// TestTokenBindsTheScope checks a token is bound to the effective filter, scope included.
func TestTokenBindsTheScope(t *testing.T) {
	t.Parallel()

	l, _ := widgetList(t, listingtest.NewSchema(t), pagetokentest.Codec(t), listingtest.MakeWidgets(30))
	req := listing.Request{PageSize: 5, OrderBy: "name"}

	p, err := l.List(t.Context(), listingtest.Subject, req, nil)
	require.NoError(t, err)

	req.PageToken = p.NextPageToken
	_, err = l.List(t.Context(), listingtest.Subject, req, bson.D{{Key: "owner", Value: "owner-0"}})
	require.Equal(t, pagetoken.TokenField, listingtest.FieldOf(err))
}

// TestMapScopePages pages a scope of multi-entry maps, nested: the driver encodes a map in a varying order, so every page must
// re-encode the scope to the bytes its token is bound to. Each run reads every page and the same items.
func TestMapScopePages(t *testing.T) {
	t.Parallel()

	ws := listingtest.MakeWidgets(60)
	l, _ := widgetList(t, listingtest.NewSchema(t), pagetokentest.Codec(t), ws, mongo.WithListScopeKeys("owner"))

	var want []listingtest.Widget

	for _, w := range ws {
		if w.Owner == "owner-1" && w.Priority >= 1 && w.Priority < 4 && w.Kind != "zzz" {
			want = append(want, w)
		}
	}

	for run := range 5 {
		scope := bson.D{
			{Key: "owner", Value: "owner-1"},
			{Key: "priority", Value: bson.M{"$gte": 1, "$lt": 4, "$ne": -1, "$exists": true}},
			{Key: "$or", Value: bson.A{
				bson.M{"kind": bson.M{"$ne": "zzz", "$exists": true, "$type": "string"}, "archived": false, "owner": "owner-1"},
				map[string]any{"archived": true, "kind": map[string]any{"$in": bson.A{"a", "b", "c"}, "$nin": bson.A{"zzz"}}},
			}},
		}

		got := listingtest.Collect(t, scoped(l, scope), listing.Request{PageSize: 3, OrderBy: "id"})
		require.ElementsMatch(t, listingtest.IDs(want), got, "run %d", run)
		require.IsIncreasing(t, got, "run %d", run)
	}
}

// TestTimeout checks the time cap: with a filter the error points at it, without one it is unavailable; both wrap ErrTimeout.
func TestTimeout(t *testing.T) {
	t.Parallel()

	l, _ := widgetList(t, listingtest.NewSchema(t), nil, listingtest.MakeWidgets(20), mongo.WithListMaxTime(time.Nanosecond))

	_, err := l.List(t.Context(), listingtest.Subject, listing.Request{Filter: `priority > 1`}, nil)
	require.Equal(t, listing.FieldFilter, listingtest.FieldOf(err))
	require.ErrorIs(t, err, mongo.ErrTimeout)

	_, err = l.List(t.Context(), listingtest.Subject, listing.Request{}, nil)
	require.ErrorIs(t, err, coreerrs.ErrUnavailable)
	require.ErrorIs(t, err, mongo.ErrTimeout)
	require.Empty(t, listingtest.FieldOf(err))

	// The caller's own cancellation is not the cap.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = l.List(ctx, listingtest.Subject, listing.Request{}, nil)
	require.Error(t, err)
	require.NotErrorIs(t, err, mongo.ErrTimeout)
}

// TestIndexedSorts proves every accepted order (pattern and reversal) is an index scan without a blocking sort, unscoped and
// scoped, with generated and given index names.
func TestIndexedSorts(t *testing.T) {
	t.Parallel()

	ws := listingtest.MakeWidgets(40)
	s := listingtest.NewSchema(t)

	unscoped, coll := widgetList(t, s, nil, ws)
	mongotest.AssertIndexedSorts(t, coll, unscoped, nil)

	byOwner, coll := widgetList(t, s, nil, ws, mongo.WithListScopeKeys("owner"), mongo.WithListParentKey("owner"),
		mongo.WithListIndexNames(map[string]string{"name desc": "idx_name"}))
	mongotest.AssertIndexedSorts(t, coll, byOwner, bson.D{{Key: "owner", Value: "owner-1"}})

	// The helper detects a sort no index serves: the scoped index needs the owner pinned.
	byName, err := s.Order(t.Context(), "name")
	require.NoError(t, err)

	p := mongotest.Explain(t, coll, nil, byOwner.Sort(byName))
	require.False(t, p.Indexed(byOwner.Index(byName)), "unscoped sort on a scoped index reported as indexed: %s", p)
}

// TestEnsureIndexes checks an index already created under a given name is found again (EnsureIndexes is idempotent), the
// store's own indexes are created with the pattern indexes, and a conflicting index fails with the server's error instead of
// being dropped.
func TestEnsureIndexes(t *testing.T) {
	t.Parallel()

	names := mongo.WithListIndexNames(map[string]string{"createTime desc": "idx_create_time"})
	l, coll := widgetList(t, listingtest.NewSchema(t), nil, listingtest.MakeWidgets(3), names)

	own := drivermongo.IndexModel{Keys: bson.D{{Key: "owner", Value: 1}}}
	require.NoError(t, l.EnsureIndexes(t.Context(), own))
	require.NoError(t, l.EnsureIndexes(t.Context(), own))

	specs, err := coll.Indexes().ListSpecifications(t.Context())
	require.NoError(t, err)

	var got []string
	for _, spec := range specs {
		got = append(got, spec.Name)
	}

	require.ElementsMatch(t, []string{
		mongo.IDIndex, "idx_create_time", "name_1__id_1", "kind_1_priority_-1__id_-1", "priority_1_created_at_1__id_1", "owner_1",
	}, got)

	// The same keys under another name: renaming an index is a migration, never done silently.
	renamed, err := mongo.NewListEngine[listingtest.Widget](coll, nil, listingtest.NewSchema(t), widgetOptions(
		mongo.WithListIndexNames(map[string]string{"createTime desc": "idx_renamed"}))...)
	require.NoError(t, err)
	require.Error(t, renamed.EnsureIndexes(t.Context()))
}

// TestMultiKeyScopeAcceptsScalarPins checks that scalar pins of every key of a multi-key scope pass the scope check and page the
// pinned items.
func TestMultiKeyScopeAcceptsScalarPins(t *testing.T) {
	t.Parallel()

	ws := listingtest.MakeWidgets(20)
	l, _ := widgetList(t, listingtest.NewSchema(t), pagetokentest.Codec(t), ws, mongo.WithListScopeKeys("archived", "owner"))
	scope := bson.D{{Key: "archived", Value: false}, {Key: "owner", Value: "owner-1"}}

	p, err := l.List(t.Context(), listingtest.Subject, listing.Request{PageSize: 50, Parent: "owner-1"}, scope)
	require.NoError(t, err)

	want := 0

	for _, w := range ws {
		if !w.Archived && w.Owner == "owner-1" {
			want++
		}
	}

	require.NotZero(t, want)
	require.Len(t, p.Items, want)
}

// TestCorruptRecord checks that a stored document that does not decode into the page type matches the corrupt-record category
// — the default or the one given by WithListCorruptRecord — and keeps the BSON decode error as its hidden cause.
func TestCorruptRecord(t *testing.T) {
	t.Parallel()

	custom := errors.New("custom corrupt record")

	tests := []struct {
		name string
		opts []mongo.ListEngineOption
		want error
	}{
		{name: "default", want: mongo.ErrCorruptRecord},
		{name: "nil keeps the default", opts: []mongo.ListEngineOption{mongo.WithListCorruptRecord(nil)}, want: mongo.ErrCorruptRecord},
		{name: "custom", opts: []mongo.ListEngineOption{mongo.WithListCorruptRecord(custom)}, want: custom},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			l, coll := widgetList(t, listingtest.NewSchema(t), nil, listingtest.MakeWidgets(3), tc.opts...)
			// name holds a number: it does not decode into Widget.Name.
			bad := bson.D{
				{Key: "_id", Value: "w-bad"}, {Key: "name", Value: int32(42)}, {Key: "kind", Value: "a"},
				{Key: "priority", Value: int64(1)}, {Key: "created_at", Value: time.Now()}, {Key: "owner", Value: "owner-0"},
				{Key: "archived", Value: false},
			}
			_, err := coll.InsertOne(t.Context(), bad)
			require.NoError(t, err)

			_, err = l.List(t.Context(), listingtest.Subject, listing.Request{}, nil)
			require.ErrorIs(t, err, tc.want)
			require.True(t, isDecodeError(apierror.HiddenCause(err)), "want the BSON decode error as the hidden cause: %v", err)
		})
	}
}

// isDecodeError reports whether err carries a BSON decode failure.
func isDecodeError(err error) bool {
	if _, ok := errors.AsType[*bson.DecodeError](err); ok {
		return true
	}

	_, ok := errors.AsType[bson.ValueDecoderError](err)

	return ok
}

// errRejectedWidget is the plain error rejectingWidget's UnmarshalBSON returns.
var errRejectedWidget = errors.New("widget rejected")

// rejectingWidget decodes itself and rejects every document with a plain error, not a BSON decode error.
type rejectingWidget struct{}

func (*rejectingWidget) UnmarshalBSON([]byte) error { return errRejectedWidget }

// TestCorruptRecordFromPageType checks that a page type whose own UnmarshalBSON fails with an ordinary error matches the
// corrupt-record category too, its error kept as the hidden cause.
func TestCorruptRecordFromPageType(t *testing.T) {
	t.Parallel()

	custom := errors.New("custom corrupt record")
	coll := widgetCollection(t, listingtest.MakeWidgets(3))

	l, err := mongo.NewListEngine[rejectingWidget](coll, nil, listingtest.NewSchema(t), widgetOptions(mongo.WithListCorruptRecord(custom))...)
	require.NoError(t, err)

	_, err = l.List(t.Context(), listingtest.Subject, listing.Request{}, nil)
	require.ErrorIs(t, err, custom)
	require.ErrorIs(t, apierror.HiddenCause(err), errRejectedWidget)
}

// TestIntegerIdentityAndCoarsePrecision pages on a custom integer identity with a one-second cursor precision.
func TestIntegerIdentityAndCoarsePrecision(t *testing.T) {
	t.Parallel()

	type item struct {
		ID int64     `bson:"sequence"`
		At time.Time `bson:"at"`
	}

	coll := newCollection(t, "typed_cursor")
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := coll.InsertMany(t.Context(), []any{item{2, at}, item{10, at}, item{20, at}})
	require.NoError(t, err)

	schema, err := listing.NewSchema([]listing.Field{{Name: "at", Kind: filter.FieldKindTimestamp}}, []string{"at"}, nil)
	require.NoError(t, err)

	list, err := mongo.NewListEngine[item](coll, pagetokentest.Codec(t), schema, mongo.WithListTiebreakPath("sequence"),
		mongo.WithListTimestampPrecision(time.Second))
	require.NoError(t, err)
	require.NoError(t, list.EnsureIndexes(t.Context()))

	for _, order := range []string{"at", "at desc"} {
		var (
			got   []int64
			token string
		)

		for range 3 {
			p, err := list.List(t.Context(), "typed-cursor", listing.Request{OrderBy: order, PageSize: 1, PageToken: token}, nil)
			require.NoError(t, err)
			require.Len(t, p.Items, 1)

			got = append(got, p.Items[0].ID)
			token = p.NextPageToken
		}

		require.Empty(t, token)

		want := []int64{2, 10, 20}
		if order == "at desc" {
			want = []int64{20, 10, 2}
		}

		require.Equal(t, want, got)
	}
}

// TestDecimalDefaultEquality checks a decimal default equality matches a stored Decimal128 and not an integer.
func TestDecimalDefaultEquality(t *testing.T) {
	t.Parallel()

	coll := newCollection(t, "decimal_defaults")
	value, err := bson.ParseDecimal128("1.25")
	require.NoError(t, err)

	_, err = coll.InsertMany(t.Context(), []any{bson.M{"_id": "a", "cost": value}, bson.M{"_id": "b", "cost": int64(2)}})
	require.NoError(t, err)

	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "cost", Kind: filter.FieldKindDecimal}},
		[]string{"id"}, listing.NewProfile(listing.WithDefaultEqualities(map[string]any{"cost": "1.25"})))
	require.NoError(t, err)

	list, err := mongo.NewListEngine[bson.M](coll, nil, schema, mongo.WithListFieldPaths(map[string]string{"id": "_id"}))
	require.NoError(t, err)

	result, err := list.List(t.Context(), "decimal", listing.Request{}, nil)
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.Equal(t, "a", result.Items[0]["_id"])
}

// TestListingQueryDeadline observes the context the driver receives: the engine's time cap is the only deadline it adds, and
// the caller's shorter one wins.
func TestListingQueryDeadline(t *testing.T) {
	t.Parallel()

	db := dbtest.Mongo(t)

	for _, tc := range []struct {
		name    string
		options []mongo.ListEngineOption
		want    time.Duration
	}{
		{"default", nil, mongo.DefaultMaxTime},
		{"raised", []mongo.ListEngineOption{mongo.WithListMaxTime(20 * time.Second)}, 20 * time.Second},
		{"disabled", []mongo.ListEngineOption{mongo.WithListMaxTime(0)}, 30 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			observed := make(chan time.Time, 1)
			monitor := &event.CommandMonitor{Started: func(ctx context.Context, e *event.CommandStartedEvent) {
				if e.CommandName == "find" {
					deadline, _ := ctx.Deadline()
					observed <- deadline
				}
			}}

			client, err := drivermongo.Connect(dbtest.MongoClientOptions(t).SetMonitor(monitor))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.Disconnect(context.WithoutCancel(t.Context()))) })

			schema := listing.MustSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
			engine, err := mongo.NewListEngine[bson.M](client.Database(db.Name()).Collection("deadline"), nil, schema,
				append(tc.options, mongo.WithListFieldPaths(map[string]string{"id": "_id"}))...)
			require.NoError(t, err)

			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()

			started := time.Now()
			_, err = engine.List(ctx, "deadline", listing.Request{}, nil)
			require.NoError(t, err)

			select {
			case deadline := <-observed:
				require.WithinDuration(t, started.Add(tc.want), deadline, time.Second)
			default:
				t.Fatal("find command was not observed")
			}
		})
	}
}
