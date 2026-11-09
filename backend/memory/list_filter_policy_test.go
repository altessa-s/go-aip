// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/memory"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
)

func TestFilterTimestampPrecision(t *testing.T) {
	t.Parallel()

	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "at", Kind: filter.FieldKindTimestamp}}, []string{"id"}, listing.NewProfile(listing.WithTimestampPrecision(time.Microsecond)))
	require.NoError(t, err)

	type item struct {
		id string
		at time.Time
	}

	l, err := memory.NewListEngine(nil, schema, func(v item) string { return v.id }, func(v item) map[string]any { return map[string]any{"id": v.id, "at": v.at} }, memory.WithListTimestampPrecision(time.Microsecond))
	require.NoError(t, err)

	at := time.Date(2026, 1, 1, 0, 0, 0, 123456000, time.UTC)

	items := []item{{"a", at}, {"b", at.Add(time.Microsecond)}}
	for _, tc := range []struct{ expr, id string }{{`at == timestamp("2026-01-01T00:00:00.123456Z")`, "a"}, {`at > timestamp("2026-01-01T00:00:00.123456Z")`, "b"}} {
		got, err := l.List(t.Context(), "list", listing.Request{Filter: tc.expr}, items)
		require.NoError(t, err)
		require.Len(t, got.Items, 1)
		require.Equal(t, tc.id, got.Items[0].id)
	}
}

func TestTokenErrorFormatter(t *testing.T) {
	t.Parallel()

	marker := errors.New("formatted")
	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, listing.NewProfile(listing.WithErrorFormatter(func(f, reason string, cause error) error {
		require.Equal(t, listing.FieldPageToken, f)
		return errors.Join(marker, cause)
	})))
	require.NoError(t, err)
	l, err := memory.NewListEngine(nil, schema, func(v string) string { return v }, func(v string) map[string]any { return map[string]any{"id": v} })
	require.NoError(t, err)
	_, err = l.List(t.Context(), "list", listing.Request{PageToken: "bad"}, nil)
	require.ErrorIs(t, err, marker)
}
