// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/memory"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

type reverseCodec struct{ pagetoken.ScalarPositionCodec }

func (reverseCodec) Name() string                         { return "reverse-json-v1" }
func (reverseCodec) Compare(a, b pagetoken.SortValue) int { return -a.Compare(b) }
func (reverseCodec) Marshal(v any) ([]byte, error)        { return json.Marshal([]any{v}) }
func (reverseCodec) Unmarshal(b []byte, v any) error {
	var values []json.RawMessage
	if err := json.Unmarshal(b, &values); err != nil {
		return err
	}

	if len(values) != 1 {
		return pagetoken.ErrInvalidToken
	}

	return json.Unmarshal(values[0], v)
}

func TestPositionCodecIntegerIdentityAndPrecision(t *testing.T) {
	t.Parallel()

	type item struct {
		ID int64
		At time.Time
	}

	schema, err := listing.NewSchema([]listing.Field{
		{Name: "sequence", Kind: filter.FieldKindInt}, {Name: "at", Kind: filter.FieldKindTimestamp},
	}, []string{"at"}, nil)
	require.NoError(t, err)
	signer := pagetokentest.Codec(t)
	fields := func(it item) map[string]any { return map[string]any{"sequence": it.ID, "at": it.At} }
	list, err := memory.NewListEngine(signer, schema, func(it item) int64 { return 99 }, fields,
		memory.WithListTiebreakField("sequence"), memory.WithListTimestampPrecision(time.Second), memory.WithListPositionCodec(reverseCodec{}))
	require.NoError(t, err)

	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	items := []item{{1, at.Add(900 * time.Millisecond)}, {2, at}, {3, at.Add(time.Millisecond)}}

	var (
		ids   []int64
		token string
	)
	for range 3 {
		page, err := list.List(t.Context(), "positions", listing.Request{PageSize: 1, PageToken: token}, items)
		require.NoError(t, err)
		require.Len(t, page.Items, 1)
		ids = append(ids, page.Items[0].ID)
		token = page.NextPageToken
	}

	require.Empty(t, token)
	require.Equal(t, []int64{3, 2, 1}, ids)
	// A token cannot be resumed with a different ordering or precision.
	first, err := list.List(t.Context(), "positions", listing.Request{PageSize: 1}, items)
	require.NoError(t, err)
	other, err := memory.NewListEngine(signer, schema, func(it item) int64 { return it.ID }, fields)
	require.NoError(t, err)
	_, err = other.List(t.Context(), "positions", listing.Request{PageSize: 1, PageToken: first.NextPageToken}, items)
	require.ErrorIs(t, err, pagetoken.ErrFilterChanged)
}
