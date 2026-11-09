// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//nolint:mnd,goconst // Fixed moduli, sizes, field names and order_by patterns shape the widget fixture.
package listingtest

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"

	filterfuncs "github.com/altessa-s/go-aip/filter/functions"
)

// Widget is the conformance resource. Kind holds the stored enum value ("a",
// "b", "c"); Note and Tags are optional. The bson tags are its stored form
// for a document engine: [FieldPaths] maps the public names that differ.
type Widget struct {
	ID       string    `bson:"_id"`
	Name     string    `bson:"name"`
	Kind     string    `bson:"kind"`
	Priority int64     `bson:"priority"`
	Created  time.Time `bson:"created_at"`
	Note     *string   `bson:"note,omitempty"`
	Owner    string    `bson:"owner"`
	Tags     []string  `bson:"tags,omitempty"`
	Archived bool      `bson:"archived"`
	Flag     *bool     `bson:"flag,omitempty"`
	Level    *int64    `bson:"level,omitempty"`
	Kind2    *string   `bson:"kind2,omitempty"`
	Rating   *int64    `bson:"rating,omitempty"`
	Score    *string   `bson:"score"`
}

// KindEnum maps the proto enum names of Widget.Kind to the stored values.
func KindEnum() map[string]any { return map[string]any{"KIND_A": "a", "KIND_B": "b", "KIND_C": "c"} }

// OptionalKindEnum includes the proto3 zero enum value.
func OptionalKindEnum() map[string]any {
	return map[string]any{"KIND_UNSPECIFIED": "", "KIND_A": "a", "KIND_B": "b"}
}

// Fields are the filter fields of the widget list.
func Fields() []listing.Field {
	return []listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "name", Kind: filter.FieldKindString},
		{Name: "kind", Kind: filter.FieldKindString, Enum: KindEnum()},
		{Name: "priority", Kind: filter.FieldKindInt},
		{Name: "createTime", Kind: filter.FieldKindTimestamp},
		{Name: "note", Kind: filter.FieldKindString, Optional: true},
		{Name: "tags", Kind: filter.FieldKindString, Array: true, Optional: true},
		{Name: "archived", Kind: filter.FieldKindBool},
		{Name: "flag", Kind: filter.FieldKindBool, Optional: true},
		{Name: "level", Kind: filter.FieldKindInt, Optional: true},
		{Name: "kind2", Kind: filter.FieldKindString, Enum: OptionalKindEnum(), EnumZero: "KIND_UNSPECIFIED", Optional: true},
		{Name: "rating", Kind: filter.FieldKindInt, Presence: true},
		{Name: "score", Kind: filter.FieldKindString, Presence: true},
	}
}

// FieldPaths are the stored paths of the fields whose public name differs:
// the id is the document id.
func FieldPaths() map[string]string {
	return map[string]string{"id": "_id", "createTime": "created_at"}
}

// Patterns are the accepted orders of the widget list.
func Patterns() []string {
	return []string{"createTime desc", "name", "kind, priority desc", "priority, createTime", "id"}
}

// Functions are the custom filter functions of the widget list.
func Functions() map[string]filter.CustomFunction {
	return map[string]filter.CustomFunction{"search": filterfuncs.TextSearch([]string{"name", "note"})}
}

// NewSchema returns the widget schema with opts appended.
func NewSchema(tb testing.TB, opts ...listing.ProfileOption) *listing.Schema {
	tb.Helper()

	s, err := listing.NewSchema(Fields(), Patterns(), listing.NewProfile(append([]listing.ProfileOption{listing.WithFunctions(Functions())}, opts...)...))
	require.NoError(tb, err)

	return s
}

// NewSchemaWithoutArrays returns the standard fixture schema without its
// repeated tags field. It supports conformance runs on engines that reject
// array storage while keeping the remaining fixture fields unchanged.
func NewSchemaWithoutArrays(tb testing.TB, opts ...listing.ProfileOption) *listing.Schema {
	tb.Helper()

	fields := make([]listing.Field, 0, len(Fields()))
	for _, field := range Fields() {
		if !field.Array {
			fields = append(fields, field)
		}
	}

	s, err := listing.NewSchema(fields, Patterns(), listing.NewProfile(append([]listing.ProfileOption{listing.WithFunctions(Functions())}, opts...)...))
	require.NoError(tb, err)

	return s
}

// Values is the filter projection of a widget for an in-memory engine. It
// hands the stored Go values over as they are (a *string note, a []string
// of tags): the engine reads them the way the MongoDB driver stores them.
func Values(w Widget) map[string]any {
	return map[string]any{
		"id": w.ID, "name": w.Name, "kind": w.Kind, "priority": w.Priority, "createTime": w.Created,
		"note": w.Note, "tags": w.Tags, "archived": w.Archived, "flag": w.Flag, "level": w.Level, "kind2": w.Kind2,
		"rating": w.Rating, "score": w.Score,
	}
}

// ID returns the id of a widget.
func ID(w Widget) string { return w.ID }

// baseTime is the creation time of the first widget.
var baseTime = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// createdFraction is the sub-second part of every creation time: an engine keeps
// milliseconds (.123, listing.TimestampPrecision), so it must compare and
// page on the truncated value.
const createdFraction = 123456 * time.Microsecond

// MakeWidgets returns n widgets with repeating names, kinds, priorities and
// creation times, so every multi-key order has ties broken by later keys
// and by the id. Ids are fixed-width so their byte order is known. Times
// carry microseconds below listing.TimestampPrecision; owners alternate
// between "owner-0" and "owner-1".
func MakeWidgets(n int) []Widget {
	out := make([]Widget, n)

	kinds := []string{"a", "b", "c"}
	for i := range n {
		w := Widget{
			ID:       fmt.Sprintf("w-%04d", (i*7919)%n), // ids not in insertion order
			Name:     fmt.Sprintf("name-%02d", i%17),
			Kind:     kinds[i%3],
			Priority: int64(i % 5),
			Created:  baseTime.Add(time.Duration(i%23)*time.Minute + createdFraction),
			Owner:    fmt.Sprintf("owner-%d", i%2),
		}
		if i%4 == 0 {
			w.Note = new(fmt.Sprintf("note %d", i))
		}

		if i%13 == 0 {
			w.Note = new("")
		}

		if i == 13 {
			w.Note = new("note 0")
		}

		if i == 17 {
			w.Note = new("界é")
		}

		if i == 18 {
			w.Note = new("alpha")
		}

		if i%4 != 0 {
			w.Flag = new(i%7 == 0)
		}

		if i%4 != 0 {
			w.Level = new(int64(i % 9))
		}

		if i%3 != 0 {
			value := []string{"a", "b"}[i%2]
			w.Kind2 = new(value)
		} else if i == 0 {
			w.Kind2 = new("")
		}

		if i%3 != 0 {
			w.Tags = []string{fmt.Sprintf("t%d", i%4), fmt.Sprintf("u%d", i%5)}
		}

		if i%5 != 0 {
			w.Rating = new(int64((i / 5) % 3))
		}

		if i%4 != 0 {
			value := []string{"", "apple", "berry"}[(i/4)%3]
			w.Score = new(value)
		}

		w.Archived = i%6 == 0
		out[i] = w
	}

	return out
}

// orderKey is one key of an expected order.
type orderKey struct {
	field string
	desc  bool
}

// expectedOrder returns the ids of ws sorted by keys with the id
// tie-breaker in the last key's direction — an implementation independent
// of the package's.
func expectedOrder(ws []Widget, keys []orderKey) []string {
	sorted := slices.Clone(ws)
	slices.SortFunc(sorted, func(a, b Widget) int {
		for _, k := range keys {
			c := compareWidget(a, b, k.field)
			if k.desc {
				c = -c
			}

			if c != 0 {
				return c
			}
		}

		c := strings.Compare(a.ID, b.ID)
		if keys[len(keys)-1].desc {
			c = -c
		}

		return c
	})

	return IDs(sorted)
}

func compareWidget(a, b Widget, field string) int {
	switch field {
	case "id":
		return strings.Compare(a.ID, b.ID)
	case "name":
		return strings.Compare(a.Name, b.Name)
	case "kind":
		return strings.Compare(a.Kind, b.Kind)
	case "priority":
		return cmp.Compare(a.Priority, b.Priority)
	case "createTime":
		return a.Created.Truncate(listing.TimestampPrecision).Compare(b.Created.Truncate(listing.TimestampPrecision))
	default:
		panic("unknown field " + field)
	}
}

// IDs returns the ids of ws in order.
func IDs(ws []Widget) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.ID
	}

	return out
}

// matching returns the widgets keep accepts.
func matching(ws []Widget, keep func(Widget) bool) []Widget {
	var out []Widget

	for _, w := range ws {
		if keep(w) {
			out = append(out, w)
		}
	}

	return out
}
