// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory_test

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/memory"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/listing"
)

// labelKind is a struct, a document only an encoding reads.
type labelKind struct {
	Kind string
}

// labeled is an item whose label is a struct.
type labeled struct {
	ID    string
	Label labelKind
}

// labelEncoding stores a labelKind as the document {"kind": Kind}, as the MongoDB driver's encoding (go-atlas
// aip/filter/evaluator/adapters/mongo) stores it with a bson:"kind" tag.
type labelEncoding struct{}

func (labelEncoding) Stored(v any, _ evaluator.Budget) (any, bool) { return v, false }

func (labelEncoding) Encode(v any, _ evaluator.Budget) any {
	if l, ok := v.(labelKind); ok {
		return map[string]any{"kind": l.Kind}
	}

	return v
}

func (labelEncoding) EncodesItself(reflect.Type) bool { return false }

func (labelEncoding) Owns(reflect.Type) bool { return false }

// TestValueEncoding checks that a value whose stored form an encoding defines — a struct — is read through memory.WithListValueEncoding:
// with an encoding storing it as a document it is that document, without one it is opaque and no
// predicate on its fields holds.
func TestValueEncoding(t *testing.T) {
	t.Parallel()

	item := labeled{ID: "a"}
	item.Label.Kind = "tool"
	fields := []listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "label.kind", Kind: filter.FieldKindString}}
	values := func(l labeled) map[string]any { return map[string]any{"id": l.ID, "label": l.Label} }
	id := func(l labeled) string { return l.ID }
	req := listing.Request{Filter: `label.kind == "tool"`}

	for name, tc := range map[string]struct {
		opts []memory.ListEngineOption
		want int
	}{
		"document encoding": {opts: []memory.ListEngineOption{memory.WithListValueEncoding(labelEncoding{})}, want: 1},
		"no encoding":       {want: 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s, err := listing.NewSchema(fields, []string{"id"}, nil)
			require.NoError(t, err)
			l, err := memory.NewListEngine(nil, s, id, values, tc.opts...)
			require.NoError(t, err)
			p, err := l.List(t.Context(), "test", req, []labeled{item})
			require.NoError(t, err)
			require.Len(t, p.Items, tc.want)
		})
	}
}
