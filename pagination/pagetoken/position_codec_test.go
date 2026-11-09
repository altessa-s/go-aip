// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package pagetoken_test

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/pagination/pagetoken"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
)

// bytesCodec demonstrates extending both the scalar model and wire representation.
type bytesCodec struct{}

func (bytesCodec) Name() string { return "bytes-v1" }
func (bytesCodec) Normalize(v any, _ time.Duration) (pagetoken.SortValue, error) {
	b, ok := v.([]byte)
	if !ok {
		return pagetoken.SortValue{}, pagetoken.ErrUnsortableValue
	}

	return pagetoken.OpaqueSortValue(bytes.Clone(b)), nil
}

func (bytesCodec) Compare(a, b pagetoken.SortValue) int {
	av, _ := a.Value()
	bv, _ := b.Value()

	return bytes.Compare(av.([]byte), bv.([]byte))
}

func (bytesCodec) Marshal(v any) ([]byte, error) {
	p := v.(pagetoken.Position)
	id, _ := p.ID.Value()

	return json.Marshal(id)
}

func (c bytesCodec) Unmarshal(data []byte, v any) error {
	var b []byte
	if err := json.Unmarshal(data, &b); err != nil {
		return err
	}

	id, err := c.Normalize(b, time.Millisecond)
	if err != nil {
		return err
	}

	*v.(*pagetoken.Position) = pagetoken.Position{ID: id}

	return nil
}

func TestOpaquePositionCodec(t *testing.T) {
	t.Parallel()

	codec := bytesCodec{}
	first, err := codec.Normalize([]byte{1}, time.Millisecond)
	require.NoError(t, err)
	second, err := codec.Normalize([]byte{2}, time.Millisecond)
	require.NoError(t, err)
	tokens := pagetoken.NewTyped[pagetoken.Position](pagetokentest.Codec(t), pagetoken.WithPayloadCodec(codec))
	positions := []pagetoken.Position{{ID: first}, {ID: second}}
	pager := pagetoken.NewSlicePager(tokens, func(p pagetoken.Position) pagetoken.Position { return p }, pagetoken.ComparePositions(nil, false, codec))
	bindings := pagetoken.NewBindings("test", codec.Name())
	page, err := pager.Page(positions, 1, "", bindings)
	require.NoError(t, err)
	next, err := pager.Page(positions, 1, page.Next, bindings)
	require.NoError(t, err)
	require.Len(t, next.Items, 1)
	require.Zero(t, codec.Compare(second, next.Items[0].ID))

	_, err = json.Marshal(first)
	require.ErrorIs(t, err, pagetoken.ErrUnsortableValue)
}

func TestPositionTimestampResolutions(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 1, 1, 0, 0, 0, 123456789, time.UTC)

	for _, precision := range []time.Duration{time.Nanosecond, time.Microsecond, time.Millisecond, time.Second} {
		t.Run(precision.String(), func(t *testing.T) {
			t.Parallel()

			value, err := (pagetoken.ScalarPositionCodec{}).Normalize(&at, precision)
			require.NoError(t, err)

			raw, ok := value.Value()
			require.True(t, ok)
			require.Equal(t, at.Truncate(precision), raw)

			data, err := json.Marshal(value)
			require.NoError(t, err)

			var decoded pagetoken.SortValue
			require.NoError(t, json.Unmarshal(data, &decoded))
			require.Zero(t, value.Compare(decoded))
		})
	}
}

func TestPositionTimestampBeyondRFC3339(t *testing.T) {
	t.Parallel()

	at := time.Date(10000, 1, 1, 0, 0, 0, 123456789, time.UTC)
	value, err := (pagetoken.ScalarPositionCodec{}).Normalize(at, time.Nanosecond)
	require.NoError(t, err)
	data, err := json.Marshal(value)
	require.NoError(t, err)

	var decoded pagetoken.SortValue
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Zero(t, value.Compare(decoded))
}

func TestDecimalOrderingBeyondBSONRange(t *testing.T) {
	t.Parallel()
	huge := mustSortValue(t, decimal.New(1, math.MaxInt32))
	tiny := mustSortValue(t, decimal.New(1, math.MinInt32))
	require.Positive(t, huge.Compare(mustSortValue(t, math.MaxFloat64)))
	require.Negative(t, tiny.Compare(mustSortValue(t, math.SmallestNonzeroFloat64)))
	require.Positive(t, huge.Compare(tiny))

	for _, value := range []pagetoken.SortValue{huge, tiny} {
		data, err := json.Marshal(value)
		require.NoError(t, err)

		var decoded pagetoken.SortValue
		require.NoError(t, json.Unmarshal(data, &decoded))
		require.Zero(t, value.Compare(decoded))
	}
}
