// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package batch_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/batch"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/batchtest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

type library map[string]*pb.Book

func (l library) fetch(calls *[][]string) batch.FetchFunc[string, *pb.Book] {
	return func(_ context.Context, keys []string) (map[string]*pb.Book, error) {
		*calls = append(*calls, keys)

		out := map[string]*pb.Book{}

		for _, k := range keys {
			if b, ok := l[k]; ok {
				out[k] = b
			}
		}

		return out, nil
	}
}

func TestGetKeys(t *testing.T) {
	t.Parallel()

	lib := library{"b/1": book("b/1"), "b/2": book("b/2")}

	t.Run("order and duplicates", func(t *testing.T) {
		t.Parallel()

		var calls [][]string

		got, err := batch.GetKeys(t.Context(), batch.FieldNames, []string{"b/2", "b/1", "b/2"}, lib.fetch(&calls))
		require.NoError(t, err)
		require.Len(t, got, 3)
		assert.Equal(t, []string{"b/2", "b/1", "b/2"}, []string{got[0].GetName(), got[1].GetName(), got[2].GetName()})
		assert.Equal(t, [][]string{{"b/2", "b/1"}}, calls)
	})

	t.Run("missing fails the batch", func(t *testing.T) {
		t.Parallel()

		var calls [][]string

		got, err := batch.GetKeys(t.Context(), batch.FieldNames, []string{"b/1", "b/9", "b/8"}, lib.fetch(&calls))
		require.Nil(t, got)
		require.ErrorIs(t, err, batch.ErrNotFound)
		require.ErrorIs(t, err, coreerrs.ErrNotFound)

		ie, ok := errors.AsType[*batch.ItemError](err)
		require.True(t, ok)
		assert.Equal(t, "names[1]", ie.Path())
	})

	t.Run("fetch error", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("boom")
		fail := func(context.Context, []string) (map[string]*pb.Book, error) { return nil, boom }

		_, err := batch.GetKeys(t.Context(), batch.FieldNames, []string{"b/1"}, fail)
		require.ErrorIs(t, err, boom)
	})
}

func TestGet(t *testing.T) {
	t.Parallel()

	lib := library{"b/1": book("b/1")}

	var calls [][]string

	reqs := []*pb.GetBookRequest{{Name: "b/1"}, {Name: "b/1"}}
	got, err := batch.Get(t.Context(), batch.FieldRequests, reqs, (*pb.GetBookRequest).GetName, lib.fetch(&calls))
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Equal(t, [][]string{{"b/1"}}, calls)

	_, err = batch.Get(t.Context(), batch.FieldRequests, []*pb.GetBookRequest{{Name: "b/1"}, {Name: "b/3"}},
		(*pb.GetBookRequest).GetName, lib.fetch(&calls))
	require.ErrorIs(t, err, coreerrs.ErrNotFound)
	assert.Contains(t, err.Error(), "requests[1]")
}
