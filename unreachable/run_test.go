// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package unreachable_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/unreachable"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func ok(items ...string) func(context.Context) ([]string, error) {
	return func(context.Context) ([]string, error) { return items, nil }
}

func fail(err error) func(context.Context) ([]string, error) {
	return func(context.Context) ([]string, error) { return nil, err }
}

// blockUntilDone waits for its context and returns its error.
func blockUntilDone(ctx context.Context) ([]string, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func loc(id string) string { return "projects/p/locations/" + id }

func TestRunMergesInSourceOrder(t *testing.T) {
	t.Parallel()

	sources := []unreachable.Source[string]{
		{Name: loc("c"), Fetch: func(context.Context) ([]string, error) {
			time.Sleep(5 * time.Millisecond)
			return []string{"c1", "c2"}, nil
		}},
		{Name: loc("a"), Fetch: ok("a1")},
		{Name: loc("b"), Fetch: ok()},
	}

	res, err := unreachable.Run(t.Context(), sources)
	require.NoError(t, err)
	assert.Equal(t, []string{"c1", "c2", "a1"}, res.Items)
	assert.Nil(t, res.Unreachable)
	assert.False(t, res.Truncated)
}

func TestRunPartialSuccess(t *testing.T) {
	t.Parallel()

	sources := []unreachable.Source[string]{
		{Name: loc("us-west1"), Fetch: fail(coreerrs.ErrUnavailable)},
		{Name: loc("us-east1"), Fetch: ok("e1")},
		{Name: loc("europe-west2"), Fetch: fail(fmt.Errorf("dial: %w", unreachable.ErrUnreachable))},
		{Name: loc("asia-east1"), Fetch: func(context.Context) ([]string, error) {
			return []string{"x1"}, unreachable.NewError(loc("asia-east1-a"), loc("asia-east1-a"))
		}},
	}

	res, err := unreachable.Run(t.Context(), sources)
	require.NoError(t, err)
	assert.Equal(t, []string{"e1", "x1"}, res.Items)
	assert.Equal(t, []string{loc("asia-east1-a"), loc("europe-west2"), loc("us-west1")}, res.Unreachable)
}

func TestRunWithoutPartialSuccessFails(t *testing.T) {
	t.Parallel()

	sources := []unreachable.Source[string]{
		{Name: loc("us-west1"), Fetch: fail(coreerrs.ErrUnavailable)},
		{Name: loc("us-east1"), Fetch: blockUntilDone},
	}

	_, err := unreachable.Run(t.Context(), sources, unreachable.WithPartialSuccess(false))
	require.ErrorIs(t, err, unreachable.ErrUnreachable)
	require.ErrorIs(t, err, coreerrs.ErrUnavailable)

	e, ok := errors.AsType[*unreachable.Error](err)
	require.True(t, ok)
	assert.Equal(t, []string{loc("us-west1")}, e.Names)
}

func TestRunWithoutPartialSuccessAllReachable(t *testing.T) {
	t.Parallel()

	sources := []unreachable.Source[string]{{Name: loc("a"), Fetch: ok("a1")}}

	res, err := unreachable.Run(t.Context(), sources, unreachable.WithPartialSuccess(false))
	require.NoError(t, err)
	assert.Equal(t, []string{"a1"}, res.Items)
}

func TestRunRequiredSource(t *testing.T) {
	t.Parallel()

	cause := fmt.Errorf("publisher: %w", coreerrs.ErrUnavailable)
	sources := []unreachable.Source[string]{
		{Name: "publishers/p1", Required: true, Fetch: fail(cause)},
	}

	_, err := unreachable.Run(t.Context(), sources)
	require.ErrorIs(t, err, unreachable.ErrUnreachable)
	require.ErrorIs(t, err, cause)

	e, ok := errors.AsType[*unreachable.Error](err)
	require.True(t, ok)
	assert.Equal(t, []string{"publishers/p1"}, e.Names)
}

func TestRunRequiredSourceWithNames(t *testing.T) {
	t.Parallel()

	sources := []unreachable.Source[string]{
		{Name: "publishers/p1", Required: true, Fetch: fail(unreachable.NewError("publishers/p1/shards/b", "publishers/p1/shards/a"))},
	}

	_, err := unreachable.Run(t.Context(), sources, unreachable.WithMaxNames(1))
	e, ok := errors.AsType[*unreachable.Error](err)
	require.True(t, ok)
	assert.Equal(t, []string{"publishers/p1/shards/a"}, e.Names)
	assert.True(t, e.Truncated)
}

func TestRunFatalError(t *testing.T) {
	t.Parallel()

	boom := errors.New("permission denied")
	sources := []unreachable.Source[string]{
		{Name: loc("a"), Fetch: blockUntilDone},
		{Name: loc("b"), Fetch: fail(boom)},
		{Name: loc("c"), Fetch: fail(coreerrs.ErrUnavailable)},
	}

	_, err := unreachable.Run(t.Context(), sources)
	require.ErrorIs(t, err, boom)

	se, ok := errors.AsType[*unreachable.SourceError](err)
	require.True(t, ok)
	assert.Equal(t, loc("b"), se.Source)
}

func TestRunInvalidNamesInErrorFallBackToSource(t *testing.T) {
	t.Parallel()

	sources := []unreachable.Source[string]{
		{Name: loc("a"), Fetch: fail(unreachable.NewError("bare-id"))},
	}

	res, err := unreachable.Run(t.Context(), sources)
	require.NoError(t, err)
	assert.Equal(t, []string{loc("a")}, res.Unreachable)
}

func TestRunClassifier(t *testing.T) {
	t.Parallel()

	flaky := errors.New("flaky backend")
	sources := []unreachable.Source[string]{
		{Name: loc("a"), Fetch: fail(flaky)},
		{Name: loc("b"), Fetch: fail(coreerrs.ErrUnavailable)},
	}

	res, err := unreachable.Run(t.Context(), sources, unreachable.WithClassifier(func(err error) bool {
		return errors.Is(err, flaky) || unreachable.IsUnreachable(err)
	}))
	require.NoError(t, err)
	assert.Equal(t, []string{loc("a"), loc("b")}, res.Unreachable)

	res, err = unreachable.Run(t.Context(), sources[1:], unreachable.WithClassifier(nil))
	require.NoError(t, err)
	assert.Equal(t, []string{loc("b")}, res.Unreachable)
}

func TestRunSourceTimeout(t *testing.T) {
	t.Parallel()

	sources := []unreachable.Source[string]{
		{Name: loc("slow"), Fetch: blockUntilDone},
		{Name: loc("fast"), Fetch: ok("f1")},
	}

	res, err := unreachable.Run(t.Context(), sources, unreachable.WithSourceTimeout(10*time.Millisecond))
	require.NoError(t, err)
	assert.Equal(t, []string{"f1"}, res.Items)
	assert.Equal(t, []string{loc("slow")}, res.Unreachable)
}

func TestRunContextCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancelCause(t.Context())
	cause := errors.New("client went away")
	started := make(chan struct{})

	sources := []unreachable.Source[string]{
		{Name: loc("a"), Fetch: func(ctx context.Context) ([]string, error) {
			close(started)
			return blockUntilDone(ctx)
		}},
		{Name: loc("b"), Fetch: ok("b1")},
	}

	go func() {
		<-started
		cancel(cause)
	}()

	_, err := unreachable.Run(ctx, sources, unreachable.WithConcurrency(1))
	require.ErrorIs(t, err, cause)
}

func TestRunConcurrencyBound(t *testing.T) {
	t.Parallel()

	var running, peak atomic.Int32

	fetch := func(context.Context) ([]string, error) {
		n := running.Add(1)

		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}

		time.Sleep(2 * time.Millisecond)
		running.Add(-1)

		return []string{"x"}, nil
	}

	sources := make([]unreachable.Source[string], 12)
	for i := range sources {
		sources[i] = unreachable.Source[string]{Name: loc(fmt.Sprint(i)), Fetch: fetch}
	}

	res, err := unreachable.Run(t.Context(), sources, unreachable.WithConcurrency(3))
	require.NoError(t, err)
	assert.Len(t, res.Items, 12)
	assert.LessOrEqual(t, peak.Load(), int32(3))

	res, err = unreachable.Run(t.Context(), sources, unreachable.WithConcurrency(0))
	require.NoError(t, err)
	assert.Len(t, res.Items, 12)
}

func TestRunNoSources(t *testing.T) {
	t.Parallel()

	res, err := unreachable.Run[string](t.Context(), nil)
	require.NoError(t, err)
	assert.Empty(t, res.Items)
	assert.Nil(t, res.Unreachable)
}

func TestRunInvalidSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		sources []unreachable.Source[string]
		want    error
	}{
		{name: "invalid name", sources: []unreachable.Source[string]{{Name: "a", Fetch: ok()}}, want: unreachable.ErrInvalidName},
		{name: "nil fetch", sources: []unreachable.Source[string]{{Name: loc("a")}}, want: unreachable.ErrInvalidSource},
		{name: "duplicate", sources: []unreachable.Source[string]{
			{Name: loc("a"), Fetch: ok()}, {Name: loc("a"), Fetch: ok()},
		}, want: unreachable.ErrInvalidSource},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := unreachable.Run(t.Context(), tt.sources)
			require.ErrorIs(t, err, tt.want)
			require.ErrorIs(t, err, unreachable.ErrInvalidSource)
		})
	}
}
