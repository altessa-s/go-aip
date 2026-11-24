// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//nolint:mnd,goconst // Fixed sizes, counts, page sizes, field names and order_by strings describe the conformance scenarios.
package listingtest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/page"
	"github.com/altessa-s/go-aip/pagination/pagetoken"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Subject is the gRPC method the conformance lists bind their tokens to.
const Subject = "/acme.test.v1.WidgetService/ListWidgets"

// Lister lists one page of widgets under subject.
type Lister func(ctx context.Context, subject string, req listing.Request) (page.Page[Widget], error)

// Factory returns the list of items under schema, served by the engine under
// test with codec signing its page tokens (nil: a single page). It is called
// once per scenario; a storage-backed engine stores items in a fresh
// collection.
type Factory func(t *testing.T, schema *listing.Schema, codec pagetoken.Signer, items []Widget) Lister

// Run runs the conformance suite: every engine pages every order, matches
// every filter, binds and refuses tokens, and rejects every invalid request
// exactly as the others do. Each scenario checks the engine against an
// expectation computed independently of the package.
func Run(t *testing.T, newList Factory, opts ...Option) {
	run(t, newList, true, newOptions(opts...))
}

// RunWithoutArrays runs the conformance suite on the same fixture without
// its repeated field. Array-specific cases are omitted; all other paging,
// filtering, token, validation and presence cases remain enabled.
func RunWithoutArrays(t *testing.T, newList Factory, opts ...Option) {
	run(t, newList, false, newOptions(opts...))
}

func run(t *testing.T, newList Factory, arrays bool, cfg *options) {
	t.Helper()

	scenarios := []struct {
		name string
		run  func(*testing.T, Factory, bool, *options)
	}{
		{"PagesEveryOrder", testPagesEveryOrder},
		{"IdentityOrders", testIdentityOrders},
		{"PagesFiltered", testPagesFiltered},
		{"MatchesContract", testMatchesContract},
		{"DefaultEqualities", testDefaultEqualities},
		{"Restriction", testRestriction},
		{"Cutoff", testCutoff},
		{"RestrictionTokenBinding", testRestrictionTokenBinding},
		{"OrdersTimestampsAtMillis", testOrdersTimestampsAtMillis},
		{"TokenBinding", testTokenBinding},
		{"RejectsRequests", testRejectsRequests},
		{"PageSize", testPageSize},
		{"WithoutCodec", testWithoutCodec},
		{"ParentRequired", testParentRequired},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			sc.run(t, newList, arrays, cfg)
		})
	}
}

func fixtureSchema(t testing.TB, arrays bool, cfg *options, opts ...listing.ProfileOption) *listing.Schema {
	t.Helper()

	opts = append(slices.Clone(cfg.schemaOptions), opts...)
	if arrays {
		return NewSchema(t, opts...)
	}

	return NewSchemaWithoutArrays(t, opts...)
}

// orderCases are the accepted orders the paging scenarios walk, with the
// keys the independent expectation sorts by.
var orderCases = []struct {
	orderBy string
	keys    []orderKey
}{
	{"", []orderKey{{"createTime", true}}},
	{"createTime", []orderKey{{"createTime", false}}},
	{"name", []orderKey{{"name", false}}},
	{"name desc", []orderKey{{"name", true}}},
	{"kind, priority desc", []orderKey{{"kind", false}, {"priority", true}}},
	{"kind desc, priority", []orderKey{{"kind", true}, {"priority", false}}},
	{"priority, createTime", []orderKey{{"priority", false}, {"createTime", false}}},
	{"priority desc, createTime desc", []orderKey{{"priority", true}, {"createTime", true}}},
	{"id", []orderKey{{"id", false}}},
	{"id desc", []orderKey{{"id", true}}},
}

func testPagesEveryOrder(t *testing.T, newList Factory, arrays bool, cfg *options) {
	ws := MakeWidgets(97)
	l := newList(t, fixtureSchema(t, arrays, cfg), cfg.signerFactory(t), ws)

	for _, oc := range orderCases {
		for _, size := range []int32{1, 7, 50, 500} {
			t.Run(fmt.Sprintf("%s/size=%d", oc.orderBy, size), func(t *testing.T) {
				t.Parallel()
				got := Collect(t, l, listing.Request{PageSize: size, OrderBy: oc.orderBy})
				require.Equal(t, expectedOrder(ws, oc.keys), got)
			})
		}
	}
}

func testPagesFiltered(t *testing.T, newList Factory, arrays bool, cfg *options) {
	ws := MakeWidgets(97)
	l := newList(t, fixtureSchema(t, arrays, cfg), cfg.signerFactory(t), ws)
	tests := []struct {
		filter string
		keep   func(Widget) bool
	}{
		{`kind == "KIND_B"`, func(w Widget) bool { return w.Kind == "b" }},
		{`kind in ["KIND_A", "KIND_C"] && priority >= 2`, func(w Widget) bool { return w.Kind != "b" && w.Priority >= 2 }},
		{`kind != "KIND_A"`, func(w Widget) bool { return w.Kind != "a" }},
		{`name.startsWith("name-1")`, func(w Widget) bool { return strings.HasPrefix(w.Name, "name-1") }},
		{`name.contains("-0") && priority < 3`, func(w Widget) bool { return strings.Contains(w.Name, "-0") && w.Priority < 3 }},
		{
			`createTime >= timestamp("2026-03-01T12:10:00Z") && createTime < timestamp("2026-03-01T12:15:00Z")`,
			func(w Widget) bool {
				return !w.Created.Before(baseTime.Add(10*time.Minute)) && w.Created.Before(baseTime.Add(15*time.Minute))
			},
		},
		{`search("NOTE 1")`, func(w Widget) bool { return w.Note != nil && strings.Contains(*w.Note, "note 1") }},
		{`note != null`, func(w Widget) bool { return w.Note != nil && *w.Note != "" }},
		{`note > "note 5"`, func(w Widget) bool { return w.Note != nil && *w.Note > "note 5" }}, // absent notes never match
		{`size(note) == 7 && priority == 0`, func(w Widget) bool { return w.Note != nil && len(*w.Note) == 7 && w.Priority == 0 }},
		{`!(priority == 0) || name == "name-00"`, func(w Widget) bool { return w.Priority != 0 || w.Name == "name-00" }},
	}

	orders := []struct {
		orderBy string
		keys    []orderKey
	}{
		{"kind, priority desc", []orderKey{{"kind", false}, {"priority", true}}},
		{"kind desc, priority", []orderKey{{"kind", true}, {"priority", false}}},
		{"priority desc, createTime desc", []orderKey{{"priority", true}, {"createTime", true}}},
	}
	for _, tc := range tests {
		for _, oc := range orders {
			t.Run(tc.filter+"/"+oc.orderBy, func(t *testing.T) {
				t.Parallel()

				want := expectedOrder(matching(ws, tc.keep), oc.keys)
				require.GreaterOrEqual(t, len(want), 2, "the case needs several pages")

				for _, size := range []int32{1, 3} {
					got := Collect(t, l, listing.Request{PageSize: size, Filter: tc.filter, OrderBy: oc.orderBy})
					require.Equal(t, want, got, "page size %d", size)
				}
			})
		}
	}
}

// createdMinute is the minute of w's creation after baseTime.
func createdMinute(w Widget) int { return int(w.Created.Sub(baseTime) / time.Minute) }

// hasTag reports whether w carries tag.
func hasTag(w Widget, tag string) bool { return slices.Contains(w.Tags, tag) }

// semanticCases are filters whose result depends on the listing contract's
// rules for absent and zero values, explicit presence, arrays, timestamps,
// bools and the global call form.
var semanticCases = []struct {
	filter string
	keep   func(Widget) bool
}{
	// Absent values: == null matches them, != a value matches them, an
	// ordering comparison never does but its negation does, and a failing
	// predicate does not spoil the rest of the expression.
	{`note == null`, func(w Widget) bool { return w.Note == nil || *w.Note == "" }},
	{`note == ""`, func(w Widget) bool { return w.Note == nil || *w.Note == "" }},
	{`note < "n"`, func(w Widget) bool { return w.Note == nil || *w.Note < "n" }},
	{`flag == false`, func(w Widget) bool { return w.Flag == nil || !*w.Flag }},
	{`level == 0`, func(w Widget) bool { return w.Level == nil || *w.Level == 0 }},
	{`kind2 == "KIND_UNSPECIFIED"`, func(w Widget) bool { return w.Kind2 == nil || *w.Kind2 == "" }},
	{`kind2 == null`, func(w Widget) bool { return w.Kind2 == nil || *w.Kind2 == "" }},
	{`kind2 != null`, func(w Widget) bool { return w.Kind2 != nil && *w.Kind2 != "" }},
	{`note != "note 0"`, func(w Widget) bool { return w.Note == nil || *w.Note != "note 0" }},
	{`!(note > "note 5")`, func(w Widget) bool { return w.Note == nil || *w.Note <= "note 5" }},
	{`note > "note 5" || priority == 4`, func(w Widget) bool { return (w.Note != nil && *w.Note > "note 5") || w.Priority == 4 }},
	{`tags == null`, func(w Widget) bool { return len(w.Tags) == 0 }},
	// Arrays: membership per element.
	{`tags == "t1"`, func(w Widget) bool { return hasTag(w, "t1") }},
	{`"u2" in tags`, func(w Widget) bool { return hasTag(w, "u2") }},
	{`tags != "t1"`, func(w Widget) bool { return !hasTag(w, "t1") }},
	{`!(tags == "t1")`, func(w Widget) bool { return !hasTag(w, "t1") }},
	{`tags in ["t0", "u4"]`, func(w Widget) bool { return hasTag(w, "t0") || hasTag(w, "u4") }},
	{`tags.startsWith("u0")`, func(w Widget) bool { return hasTag(w, "u0") }},
	{`tags > "u3"`, func(w Widget) bool { return hasTag(w, "u4") }},
	{`tags == "t2" && tags == "u1"`, func(w Widget) bool { return hasTag(w, "t2") && hasTag(w, "u1") }},
	{`size(tags) == 2 && kind == "KIND_B"`, func(w Widget) bool { return len(w.Tags) == 2 && w.Kind == "b" }},
	// size() of an absent value is 0 (an empty string or list is stored
	// absent), for every operator and under negation.
	{`size(note) != 0`, func(w Widget) bool { return w.Note != nil && len(*w.Note) != 0 }},
	{`size(note) == 0`, func(w Widget) bool { return w.Note == nil || *w.Note == "" }},
	{`size(note) < 1`, func(w Widget) bool { return w.Note == nil || len(*w.Note) == 0 }},
	{`size(note) <= 0 || priority == 4`, func(w Widget) bool { return w.Note == nil || len(*w.Note) == 0 || w.Priority == 4 }},
	{`size(note) >= 0 && kind == "KIND_A"`, func(w Widget) bool { return w.Kind == "a" }},
	{`size(note) > 6`, func(w Widget) bool { return w.Note != nil && len(*w.Note) > 6 }},
	{`size(note) == 2 && note.startsWith("界")`, func(w Widget) bool { return w.Note != nil && *w.Note == "界é" }},
	{`!(size(note) > 6)`, func(w Widget) bool { return w.Note == nil || len(*w.Note) <= 6 }},
	{`size(tags) == 0`, func(w Widget) bool { return len(w.Tags) == 0 }},
	{`size(tags) != 2`, func(w Widget) bool { return len(w.Tags) != 2 }},
	{`!(size(tags) <= 1)`, func(w Widget) bool { return len(w.Tags) > 1 }},
	{`size(tags) < 1 && priority != 0`, func(w Widget) bool { return len(w.Tags) == 0 && w.Priority != 0 }},
	// Timestamps compare at listing.TimestampPrecision (milliseconds,
	// truncated): the stored .123456 is .123, a literal .1239 is .123 too.
	{`createTime == timestamp("2026-03-01T12:05:00.123Z")`, func(w Widget) bool { return createdMinute(w) == 5 }},
	{`createTime == timestamp("2026-03-01T12:05:00.123999Z")`, func(w Widget) bool { return createdMinute(w) == 5 }},
	{
		`createTime in [timestamp("2026-03-01T12:01:00.1234Z"), timestamp("2026-03-01T12:02:00.123Z")]`,
		func(w Widget) bool { m := createdMinute(w); return m == 1 || m == 2 },
	},
	{`createTime > timestamp("2026-03-01T12:10:00.1231Z")`, func(w Widget) bool { return createdMinute(w) > 10 }},
	{`createTime >= timestamp("2026-03-01T12:20:00.1239Z")`, func(w Widget) bool { return createdMinute(w) >= 20 }},
	{`createTime <= timestamp("2026-03-01T12:03:00.123456Z")`, func(w Widget) bool { return createdMinute(w) <= 3 }},
	// Bools order false before true, as MongoDB orders them.
	{`archived > false`, func(w Widget) bool { return w.Archived }},
	{`!(archived > false)`, func(w Widget) bool { return !w.Archived }},
	{`archived >= true`, func(w Widget) bool { return w.Archived }},
	{`archived < true`, func(w Widget) bool { return !w.Archived }},
	{`!(archived <= false)`, func(w Widget) bool { return w.Archived }},
	// The global form of a string function.
	{`startsWith(name, "name-1")`, func(w Widget) bool { return strings.HasPrefix(w.Name, "name-1") }},
	{`contains(name, "-0") && endsWith(name, "3")`, func(w Widget) bool {
		return strings.Contains(w.Name, "-0") && strings.HasSuffix(w.Name, "3")
	}},
	// Presence fields follow the default evaluator: null equals null and
	// differs from every value, is in no list and contains no string, so the
	// negation of those holds for null; an ordering comparison with null is
	// unknown, also under negation; null comparisons and size are two-valued.
	{`rating == 1`, func(w Widget) bool { return w.Rating != nil && *w.Rating == 1 }},
	{`rating != 1`, func(w Widget) bool { return w.Rating == nil || *w.Rating != 1 }},
	{`!(rating == 1)`, func(w Widget) bool { return w.Rating == nil || *w.Rating != 1 }},
	{`!(rating != 1)`, func(w Widget) bool { return w.Rating != nil && *w.Rating == 1 }},
	{`!(rating == 1 || priority == 4)`, func(w Widget) bool { return (w.Rating == nil || *w.Rating != 1) && w.Priority != 4 }},
	{`!!(rating > 1)`, func(w Widget) bool { return w.Rating != nil && *w.Rating > 1 }},
	{`!(rating > 1)`, func(w Widget) bool { return w.Rating != nil && *w.Rating <= 1 }},
	{`!(rating in [1, 2])`, func(w Widget) bool { return w.Rating == nil || (*w.Rating != 1 && *w.Rating != 2) }},
	{`rating == null`, func(w Widget) bool { return w.Rating == nil }},
	{`rating != null`, func(w Widget) bool { return w.Rating != nil }},
	{`!(rating == null)`, func(w Widget) bool { return w.Rating != nil }},
	{`!(rating != null)`, func(w Widget) bool { return w.Rating == nil }},
	{`rating == 0`, func(w Widget) bool { return w.Rating != nil && *w.Rating == 0 }},
	{`score == ""`, func(w Widget) bool { return w.Score != nil && *w.Score == "" }},
	{`!(score.startsWith("a"))`, func(w Widget) bool { return w.Score == nil || !strings.HasPrefix(*w.Score, "a") }},
	{`size(score) == 0`, func(w Widget) bool { return w.Score == nil || len(*w.Score) == 0 }},
	{`!(size(score) == 0)`, func(w Widget) bool { return w.Score != nil && len(*w.Score) != 0 }},
	{`!(size(score) > 0)`, func(w Widget) bool { return w.Score == nil || len(*w.Score) == 0 }},
	{`rating != 1 && priority == 4`, func(w Widget) bool { return (w.Rating == nil || *w.Rating != 1) && w.Priority == 4 }},
}

// testMatchesContract checks the semantic cases, alone and under a default
// restriction the filter overrides or not.
func testMatchesContract(t *testing.T, newList Factory, arrays bool, cfg *options) {
	ws := MakeWidgets(97)
	l := newList(t, fixtureSchema(t, arrays, cfg, listing.WithDefaultEqualities(map[string]any{"archived": false})), cfg.signerFactory(t), ws)
	keys := []orderKey{{"priority", false}, {"createTime", false}}

	for _, tc := range semanticCases {
		if !arrays && strings.Contains(tc.filter, "tags") {
			continue
		}

		for _, expr := range []string{tc.filter, "(" + tc.filter + ") && (archived == true || archived == false)"} {
			t.Run(expr, func(t *testing.T) {
				t.Parallel()

				keep := func(w Widget) bool { return tc.keep(w) && !w.Archived }
				if strings.Contains(expr, "archived") {
					keep = tc.keep
				}

				want := expectedOrder(matching(ws, keep), keys)
				require.NotEmpty(t, want, "the case proves nothing")
				require.Less(t, len(want), len(ws), "the case proves nothing")
				got := Collect(t, l, listing.Request{PageSize: 5, Filter: expr, OrderBy: "priority, createTime"})
				require.Equal(t, want, got)
			})
		}
	}
}

// testDefaultEqualities checks a default restriction applies until the
// filter refers to its field.
func testDefaultEqualities(t *testing.T, newList Factory, arrays bool, cfg *options) {
	ws := MakeWidgets(60)
	s := fixtureSchema(t, arrays, cfg, listing.WithDefaultEqualities(map[string]any{"archived": false, "kind": "KIND_A"}))
	l := newList(t, s, cfg.signerFactory(t), ws)

	tests := []struct {
		filter string
		keep   func(Widget) bool
	}{
		{"", func(w Widget) bool { return !w.Archived && w.Kind == "a" }},
		{`priority > 1`, func(w Widget) bool { return !w.Archived && w.Kind == "a" && w.Priority > 1 }},
		{`archived == true`, func(w Widget) bool { return w.Archived && w.Kind == "a" }},
		{`archived == true || archived == false`, func(w Widget) bool { return w.Kind == "a" }},
		{`kind != "KIND_A" && archived == false`, func(w Widget) bool { return !w.Archived && w.Kind != "a" }},
	}
	for _, tc := range tests {
		t.Run(tc.filter, func(t *testing.T) {
			t.Parallel()

			want := expectedOrder(matching(ws, tc.keep), []orderKey{{"name", false}})
			require.NotEmpty(t, want)
			require.Equal(t, want, Collect(t, l, listing.Request{PageSize: 7, Filter: tc.filter, OrderBy: "name"}))
		})
	}
}

// testOrdersTimestampsAtMillis pages widgets created 100µs apart: ten share
// each millisecond, so ordering and paging by createTime must tie them and
// break the ties by id.
func testOrdersTimestampsAtMillis(t *testing.T, newList Factory, arrays bool, cfg *options) {
	ws := MakeWidgets(30)
	for i := range ws {
		ws[i].Created = baseTime.Add(time.Duration(i) * 100 * time.Microsecond)
	}

	l := newList(t, fixtureSchema(t, arrays, cfg), cfg.signerFactory(t), ws)

	for _, desc := range []bool{false, true} {
		orderBy := "createTime"
		if desc {
			orderBy += " desc"
		}

		kept := matching(ws, func(w Widget) bool { return w.Created.Before(baseTime.Add(2 * time.Millisecond)) })
		for i := range kept {
			kept[i].Created = kept[i].Created.Truncate(listing.TimestampPrecision)
		}

		want := expectedOrder(kept, []orderKey{{"createTime", desc}})
		req := listing.Request{PageSize: 3, OrderBy: orderBy, Filter: `createTime < timestamp("2026-03-01T12:00:00.0021Z")`}
		require.Equal(t, want, Collect(t, l, req), orderBy)
	}
}

// testTokenBinding checks a token is valid only for the request that issued
// it: another filter, order, parent or subject rejects it; a changed page
// size or another spelling of the same filter or order does not.
func testTokenBinding(t *testing.T, newList Factory, arrays bool, cfg *options) {
	l := newList(t, fixtureSchema(t, arrays, cfg), cfg.signerFactory(t), MakeWidgets(30))
	first := listing.Request{PageSize: 5, Filter: `priority > 0 && name.startsWith("name")`, OrderBy: "name", Parent: "p1"}
	p, err := l(t.Context(), Subject, first)
	require.NoError(t, err)
	require.NotEmpty(t, p.NextPageToken)

	next := first
	next.PageToken = p.NextPageToken

	tests := []struct {
		name    string
		mutate  func(*listing.Request)
		subject string
		ok      bool
	}{
		{name: "same request", mutate: func(*listing.Request) {}, ok: true},
		{name: "other page size", mutate: func(r *listing.Request) { r.PageSize = 9 }, ok: true},
		{name: "equivalent order spelling", mutate: func(r *listing.Request) { r.OrderBy = " name ASC" }, ok: true},
		{name: "filter spelling", mutate: func(r *listing.Request) { r.Filter = `priority>0&&name.startsWith("name")` }, ok: true},
		{name: "global call form", mutate: func(r *listing.Request) { r.Filter = `priority > 0 && startsWith(name, "name")` }, ok: true},
		{name: "other filter", mutate: func(r *listing.Request) { r.Filter = `priority > 1 && name.startsWith("name")` }},
		{name: "filter removed", mutate: func(r *listing.Request) { r.Filter = "" }},
		{name: "other order", mutate: func(r *listing.Request) { r.OrderBy = "name desc" }},
		{name: "default order", mutate: func(r *listing.Request) { r.OrderBy = "" }},
		{name: "other parent", mutate: func(r *listing.Request) { r.Parent = "p2" }},
		{name: "other subject", mutate: func(*listing.Request) {}, subject: "/acme.test.v1.WidgetService/ListGadgets"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := next
			tc.mutate(&req)

			subject := Subject
			if tc.subject != "" {
				subject = tc.subject
			}

			_, err := l(t.Context(), subject, req)
			if tc.ok {
				require.NoError(t, err)
				return
			}

			require.Equal(t, pagetoken.TokenField, FieldOf(err), "error %v", err)
		})
	}
}

func testRejectsRequests(t *testing.T, newList Factory, arrays bool, cfg *options) {
	l := newList(t, fixtureSchema(t, arrays, cfg), cfg.signerFactory(t), MakeWidgets(10))

	tests := []struct {
		name  string
		req   listing.Request
		field string
		cause error
	}{
		{name: "negative page size", req: listing.Request{PageSize: -1}, field: pagetoken.SizeField},
		{name: "unsupported order", req: listing.Request{OrderBy: "priority"}, field: listing.FieldOrderBy},
		{name: "unparsable filter", req: listing.Request{Filter: "name =="}, field: listing.FieldFilter, cause: filter.ErrParseFailed},
		{name: "unknown field", req: listing.Request{Filter: `secret == "x"`}, field: listing.FieldFilter, cause: filter.ErrFieldNotAllowed},
		{name: "type mismatch", req: listing.Request{Filter: `priority == "high"`}, field: listing.FieldFilter, cause: filter.ErrFieldTypeMismatch},
		{name: "timestamp as string", req: listing.Request{Filter: `createTime > "2026-01-01"`}, field: listing.FieldFilter, cause: filter.ErrFieldTypeMismatch},
		{name: "matches not exposed", req: listing.Request{Filter: `name.matches("^n")`}, field: listing.FieldFilter, cause: filter.ErrFunctionNotAllowed},
		{
			name: "substring not exposed", req: listing.Request{Filter: `name.substring(1) == "a"`},
			field: listing.FieldFilter, cause: filter.ErrFunctionNotAllowed,
		},
		{name: "field to field", req: listing.Request{Filter: `name == note`}, field: listing.FieldFilter},
		{name: "empty in", req: listing.Request{Filter: `note in []`}, field: listing.FieldFilter, cause: filter.ErrInvalidExpression},
		{name: "null in", req: listing.Request{Filter: `note in [null, "a"]`}, field: listing.FieldFilter, cause: filter.ErrInvalidExpression},
		{name: "ordered null", req: listing.Request{Filter: `note < null`}, field: listing.FieldFilter, cause: filter.ErrInvalidExpression},
		{
			name: "null test on a required field", req: listing.Request{Filter: `priority == null`}, field: listing.FieldFilter,
			cause: filter.ErrUnsupportedOperation,
		},
		{
			name: "not-null test on a required field", req: listing.Request{Filter: `name != null`}, field: listing.FieldFilter,
			cause: filter.ErrUnsupportedOperation,
		},
		{name: "enum stored value", req: listing.Request{Filter: `kind == "a"`}, field: listing.FieldFilter},
		{name: "enum ordering", req: listing.Request{Filter: `kind > "KIND_A"`}, field: listing.FieldFilter},
		{name: "enum string function", req: listing.Request{Filter: `kind.contains("A")`}, field: listing.FieldFilter},
		{name: "enum number", req: listing.Request{Filter: `kind == 1`}, field: listing.FieldFilter},
		{name: "enum unknown in list", req: listing.Request{Filter: `kind in ["KIND_A", "KIND_Z"]`}, field: listing.FieldFilter},
		{name: "too deep", req: listing.Request{Filter: strings.Repeat("!(", 20) + "priority == 1" + strings.Repeat(")", 20)}, field: listing.FieldFilter},
		{name: "size in a list", req: listing.Request{Filter: `size(note) in [1, 2]`}, field: listing.FieldFilter, cause: filter.ErrUnsupportedOperation},
		{name: "size of a float", req: listing.Request{Filter: `size(note) < 1.5`}, field: listing.FieldFilter},
		{name: "size out of range", req: listing.Request{Filter: `size(tags) == 2147483648`}, field: listing.FieldFilter, cause: filter.ErrInvalidExpression},
		{name: "global call on a literal", req: listing.Request{Filter: `startsWith("n")`}, field: listing.FieldFilter, cause: filter.ErrInvalidExpression},
		{name: "malformed token", req: listing.Request{PageToken: "garbage"}, field: pagetoken.TokenField, cause: pagetoken.ErrInvalidToken},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := l(t.Context(), Subject, tc.req)
			require.Equal(t, tc.field, FieldOf(err), "error %v", err)

			if tc.cause != nil {
				require.ErrorIs(t, err, tc.cause)
			}
		})
	}
}

// testPageSize checks the page-size options: an omitted size is the
// default, a larger one is clamped to the maximum.
func testPageSize(t *testing.T, newList Factory, arrays bool, cfg *options) {
	ws := MakeWidgets(9)
	for _, codec := range []pagetoken.Signer{nil, cfg.signerFactory(t)} {
		l := newList(t, fixtureSchema(t, arrays, cfg, listing.WithDefaultPageSize(2), listing.WithMaxPageSize(4)), codec, ws)
		for size, want := range map[int32]int{0: 2, 3: 3, 100: 4} {
			p, err := l(t.Context(), Subject, listing.Request{PageSize: size, OrderBy: "name"})
			require.NoError(t, err)
			require.Len(t, p.Items, want, "pageSize %d", size)
			require.Equal(t, codec != nil, p.NextPageToken != "", "pageSize %d: next token %q", size, p.NextPageToken)
		}
	}
}

// testWithoutCodec checks a list without a codec serves a single page and
// rejects a token.
func testWithoutCodec(t *testing.T, newList Factory, arrays bool, cfg *options) {
	ws := MakeWidgets(20)
	l := newList(t, fixtureSchema(t, arrays, cfg), nil, ws)
	p, err := l(t.Context(), Subject, listing.Request{PageSize: 5})
	require.NoError(t, err)
	require.Len(t, p.Items, 5)
	require.Empty(t, p.NextPageToken)

	tokened := newList(t, fixtureSchema(t, arrays, cfg), cfg.signerFactory(t), ws)
	first, err := tokened(t.Context(), Subject, listing.Request{PageSize: 5})
	require.NoError(t, err)
	_, err = l(t.Context(), Subject, listing.Request{PageSize: 5, PageToken: first.NextPageToken})
	require.Equal(t, pagetoken.TokenField, FieldOf(err), "error %v", err)
}

func testParentRequired(t *testing.T, newList Factory, arrays bool, cfg *options) {
	l := newList(t, fixtureSchema(t, arrays, cfg, listing.WithParentRequired(true)), cfg.signerFactory(t), MakeWidgets(5))
	_, err := l(t.Context(), Subject, listing.Request{Parent: "  "})
	require.Equal(t, listing.FieldParent, FieldOf(err), "error %v", err)
	_, err = l(t.Context(), Subject, listing.Request{Parent: "p"})
	require.NoError(t, err)
}

// Collect pages through req with l until the last page and returns the ids
// in order; it fails when a page exceeds the requested size or the walk does
// not end.
func Collect(tb testing.TB, l Lister, req listing.Request) []string {
	tb.Helper()

	var got []string

	for range 1000 {
		p, err := l(tb.Context(), Subject, req)
		require.NoError(tb, err, "List(%+v)", req)

		if req.PageSize > 0 {
			require.LessOrEqual(tb, len(p.Items), int(req.PageSize))
		}

		got = append(got, IDs(p.Items)...)
		if p.NextPageToken == "" {
			return got
		}

		req.PageToken = p.NextPageToken
	}

	tb.Fatal("paging did not end")

	return nil
}

// FieldOf returns the field of the core/errors.FieldError err wraps, or ""
// when it wraps none.
func FieldOf(err error) string {
	if fe, ok := errors.AsType[*coreerrs.FieldError](err); ok {
		return fe.Field
	}

	return ""
}

// testIdentityOrders exercises the shared empty-order and explicit identity contract on every engine.
func testIdentityOrders(t *testing.T, newList Factory, arrays bool, cfg *options) {
	fields := Fields()
	if !arrays {
		fields = slices.DeleteFunc(fields, func(f listing.Field) bool { return f.Array })
	}

	for _, patterns := range [][]string{nil, {"name, id desc"}} {
		t.Run(fmt.Sprint(patterns), func(t *testing.T) {
			t.Parallel()

			opts := append(slices.Clone(cfg.schemaOptions), listing.WithEmptyOrderAllowed(true))
			schema, err := listing.NewSchema(fields, patterns, listing.NewProfile(opts...))
			require.NoError(t, err)

			ws := MakeWidgets(31)
			list := newList(t, schema, cfg.signerFactory(t), ws)

			keys := []orderKey{{"id", false}}
			if len(patterns) > 0 {
				keys = []orderKey{{"name", false}, {"id", true}}
			}

			got := Collect(t, list, listing.Request{PageSize: 3})
			require.Equal(t, expectedOrder(ws, keys), got)
		})
	}
}
