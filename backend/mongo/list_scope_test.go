// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/listingtest"
)

// TestRefusesAnUnpinnedScope checks that a scoped list refuses, before
// querying, a scope that does not pin its key to the parent: a store that
// omits or mistypes the pin must not read the items of every parent. The
// list has no collection — a refused scope never reaches it.
func TestRefusesAnUnpinnedScope(t *testing.T) {
	t.Parallel()
	l := newWidgets(t, listingtest.NewSchema(t, listing.WithParentRequired(true)), mongo.WithListScopeKeys("owner"), mongo.WithListParentKey("owner"))

	tests := []struct {
		name  string
		scope bson.D
	}{
		{"no scope", nil},
		{"other key", bson.D{{Key: "kind", Value: "owner-1"}}},
		{"another parent", bson.D{{Key: "owner", Value: "owner-0"}}},
		{"operator", bson.D{{Key: "owner", Value: bson.D{{Key: "$ne", Value: "x"}}}}},
		{"list", bson.D{{Key: "owner", Value: bson.A{"owner-1"}}}},
		{"null", bson.D{{Key: "owner", Value: nil}}},
		{"not a string", bson.D{{Key: "owner", Value: 1}}},
		{"map operator", bson.D{{Key: "owner", Value: map[string]any{"$ne": "x"}}}},
		{"typed nil", bson.D{{Key: "owner", Value: (*string)(nil)}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := l.List(t.Context(), listingtest.Subject, listing.Request{Parent: "owner-1"}, tc.scope)
			require.ErrorIs(t, err, mongo.ErrScope)
		})
	}
}

// TestRefusesAnUnpinnedMultiKeyScope checks the pin of every key of a
// multi-key scope by its BSON encoding: an ordinary map is an operator
// document and a typed nil pointer is null, so neither pins its key.
func TestRefusesAnUnpinnedMultiKeyScope(t *testing.T) {
	t.Parallel()
	l := newWidgets(t, listingtest.NewSchema(t), mongo.WithListScopeKeys("tenant", "owner"))

	tests := []struct {
		name  string
		scope bson.D
	}{
		{"map operator", bson.D{{Key: "tenant", Value: map[string]any{"$ne": "a"}}, {Key: "owner", Value: "owner-1"}}},
		{"typed nil", bson.D{{Key: "tenant", Value: (*string)(nil)}, {Key: "owner", Value: "owner-1"}}},
		{"struct", bson.D{{Key: "tenant", Value: struct{ In []string }{[]string{"a"}}}, {Key: "owner", Value: "owner-1"}}},
		{"slice", bson.D{{Key: "tenant", Value: []string{"a", "b"}}, {Key: "owner", Value: "owner-1"}}},
		{"missing key", bson.D{{Key: "owner", Value: "owner-1"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := l.List(t.Context(), listingtest.Subject, listing.Request{Parent: "owner-1"}, tc.scope)
			require.ErrorIs(t, err, mongo.ErrScope)
		})
	}
}
