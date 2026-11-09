// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package batch_test

import (
	"context"
	"errors"
	"maps"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/batch"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// fakeTx is a transaction runner over a map store: writes go to a copy that
// replaces the store only when fn succeeds.
type fakeTx struct {
	store    map[string]int
	attempts int
	retries  int
}

type txKey struct{}

func (f *fakeTx) run(ctx context.Context, fn func(context.Context) error) error {
	for {
		f.attempts++

		staged := maps.Clone(f.store)

		if err := fn(context.WithValue(ctx, txKey{}, staged)); err != nil {
			return err
		}

		if f.attempts <= f.retries {
			continue // simulate a transient commit failure that the runner retries
		}

		f.store = staged

		return nil
	}
}

func put(ctx context.Context, _ int, item string) (int, error) {
	staged, ok := ctx.Value(txKey{}).(map[string]int)
	if !ok {
		return 0, errors.New("no transaction")
	}

	if item == "bad" {
		return 0, &coreerrs.FieldError{Field: "book.title", Reason: "is required"}
	}

	if _, exists := staged[item]; exists {
		return 0, coreerrs.ErrAlreadyExists
	}

	staged[item] = len(item)

	return len(item), nil
}

func TestAtomic(t *testing.T) {
	t.Parallel()

	t.Run("all succeed in order", func(t *testing.T) {
		t.Parallel()

		tx := &fakeTx{store: map[string]int{}, retries: 1}
		got, err := batch.Atomic(t.Context(), batch.FieldRequests, []string{"a", "bb", "ccc"}, tx.run, put)
		require.NoError(t, err)
		assert.Equal(t, []int{1, 2, 3}, got)
		assert.Equal(t, map[string]int{"a": 1, "bb": 2, "ccc": 3}, tx.store)
		assert.Equal(t, 2, tx.attempts)
	})

	t.Run("first failure rolls back", func(t *testing.T) {
		t.Parallel()

		tx := &fakeTx{store: map[string]int{"x": 1}}
		got, err := batch.Atomic(t.Context(), batch.FieldRequests, []string{"a", "x", "bad"}, tx.run, put)
		require.Nil(t, got)
		require.ErrorIs(t, err, coreerrs.ErrAlreadyExists)

		ie, ok := errors.AsType[*batch.ItemError](err)
		require.True(t, ok)
		assert.Equal(t, 1, ie.Index)
		assert.Equal(t, map[string]int{"x": 1}, tx.store)
	})

	t.Run("nested field error", func(t *testing.T) {
		t.Parallel()

		tx := &fakeTx{store: map[string]int{}}
		_, err := batch.Atomic(t.Context(), batch.FieldRequests, []string{"a", "bad"}, tx.run, put)
		requireFieldError(t, err, "requests[1].book.title", coreerrs.ErrInvalidArgument)
		assert.Empty(t, tx.store)
	})

	t.Run("empty batch", func(t *testing.T) {
		t.Parallel()

		tx := &fakeTx{store: map[string]int{}}
		got, err := batch.Atomic(t.Context(), batch.FieldRequests, nil, tx.run, put)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("canceled context", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		tx := &fakeTx{store: map[string]int{}}
		_, err := batch.Atomic(ctx, batch.FieldRequests, []string{"a"}, tx.run, put)
		require.ErrorIs(t, err, context.Canceled)
		assert.Empty(t, tx.store)
	})

	t.Run("no runner", func(t *testing.T) {
		t.Parallel()

		_, err := batch.Atomic(t.Context(), batch.FieldRequests, []string{"a"}, nil, put)
		require.ErrorIs(t, err, batch.ErrNoTransaction)
	})

	t.Run("runner skips the batch", func(t *testing.T) {
		t.Parallel()

		skip := func(context.Context, func(context.Context) error) error { return nil }
		_, err := batch.Atomic(t.Context(), batch.FieldRequests, []string{"a"}, skip, put)
		require.ErrorIs(t, err, batch.ErrNoTransaction)
	})
}

var errInvalid = errors.New("invalid")

// half fails every odd item.
func half(_ context.Context, i int, item string) (string, error) {
	if i%2 == 1 {
		return "", errInvalid
	}

	return item + "!", nil
}

func TestPartial(t *testing.T) {
	t.Parallel()

	items := []string{"a", "b", "c", "d", "e"}

	for _, n := range []int{0, 1, 3, 10} {
		res, err := batch.Partial(t.Context(), batch.FieldRequests, items, half, batch.WithConcurrency(n))
		require.NoError(t, err)
		assert.Equal(t, []string{"a!", "", "c!", "", "e!"}, res.Values)
		assert.Equal(t, map[int]error{1: errInvalid, 3: errInvalid}, res.Failed)
		assert.Equal(t, 3, res.SucceededCount())
		assert.True(t, res.Succeeded(0))
		assert.False(t, res.Succeeded(1))
		assert.False(t, res.Succeeded(-1))
		assert.False(t, res.Succeeded(5))
	}
}

func TestPartialOrderUnderConcurrency(t *testing.T) {
	t.Parallel()

	items := make([]int, 64)
	for i := range items {
		items[i] = i
	}

	var running, peak atomic.Int32

	square := func(_ context.Context, _ int, v int) (int, error) {
		cur := running.Add(1)

		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				break
			}
		}

		time.Sleep(time.Duration(64-v) * 10 * time.Microsecond) // later items finish first
		running.Add(-1)

		return v * v, nil
	}

	res, err := batch.Partial(t.Context(), batch.FieldRequests, items, square, batch.WithConcurrency(8))
	require.NoError(t, err)

	for i, v := range res.Values {
		assert.Equal(t, i*i, v)
	}

	assert.LessOrEqual(t, peak.Load(), int32(8))
	assert.Empty(t, res.Failed)
}

func TestPartialAllFailed(t *testing.T) {
	t.Parallel()

	fail := func(context.Context, int, string) (string, error) { return "", coreerrs.ErrNotFound }

	res, err := batch.Partial(t.Context(), batch.FieldRequests, []string{"a", "b"}, fail)
	require.ErrorIs(t, err, batch.ErrAllFailed)
	assert.Len(t, res.Failed, 2)
	assert.Zero(t, res.SucceededCount())

	res, err = batch.Partial(t.Context(), batch.FieldRequests, []string{}, fail)
	require.NoError(t, err)
	assert.Empty(t, res.Values)
}

func TestPartialFatal(t *testing.T) {
	t.Parallel()

	t.Run("transient error stops the batch", func(t *testing.T) {
		t.Parallel()

		var ran atomic.Int32

		do := func(_ context.Context, i int, item string) (string, error) {
			ran.Add(1)

			switch i {
			case 1:
				return "", errInvalid
			case 2:
				return "", coreerrs.ErrUnavailable
			default:
				return item, nil
			}
		}

		res, err := batch.Partial(t.Context(), batch.FieldNames, []string{"a", "b", "c", "d", "e"}, do)
		require.ErrorIs(t, err, coreerrs.ErrUnavailable)

		ie, ok := errors.AsType[*batch.ItemError](err)
		require.True(t, ok)
		assert.Equal(t, "names[2]", ie.Path())
		assert.Equal(t, int32(3), ran.Load())
		assert.True(t, res.Succeeded(0))
		assert.Equal(t, map[int]error{1: errInvalid}, res.Failed)
		assert.False(t, res.Succeeded(3))
	})

	t.Run("custom classifier", func(t *testing.T) {
		t.Parallel()

		do := func(context.Context, int, string) (string, error) { return "", coreerrs.ErrUnavailable }

		res, err := batch.Partial(t.Context(), batch.FieldRequests, []string{"a", "b"}, do, batch.WithFatal(nil))
		require.ErrorIs(t, err, batch.ErrAllFailed)
		assert.Len(t, res.Failed, 2)
	})

	t.Run("in-flight items see the cancellation", func(t *testing.T) {
		t.Parallel()

		started := make(chan struct{})
		do := func(ctx context.Context, i int, _ string) (string, error) {
			if i == 0 {
				<-started
				return "", coreerrs.ErrUnavailable
			}

			close(started)
			<-ctx.Done()

			return "", ctx.Err()
		}

		res, err := batch.Partial(t.Context(), batch.FieldRequests, []string{"a", "b"}, do, batch.WithConcurrency(2))
		require.ErrorIs(t, err, coreerrs.ErrUnavailable)

		ie, ok := errors.AsType[*batch.ItemError](err)
		require.True(t, ok)
		assert.Equal(t, 0, ie.Index)
		assert.Empty(t, res.Failed)
		assert.False(t, res.Succeeded(1))
	})
}

func TestPartialCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	do := func(_ context.Context, i int, item string) (string, error) {
		if i == 1 {
			cancel()
		}

		return item, nil
	}

	res, err := batch.Partial(ctx, batch.FieldRequests, []string{"a", "b", "c"}, do)
	require.ErrorIs(t, err, context.Canceled)
	assert.True(t, res.Succeeded(0))
	assert.True(t, res.Succeeded(1))
	assert.False(t, res.Succeeded(2))
}

func TestDefaultFatal(t *testing.T) {
	t.Parallel()

	assert.True(t, batch.DefaultFatal(context.Canceled))
	assert.True(t, batch.DefaultFatal(context.DeadlineExceeded))
	assert.True(t, batch.DefaultFatal(coreerrs.ErrUnavailable))
	assert.False(t, batch.DefaultFatal(coreerrs.ErrNotFound))
}
