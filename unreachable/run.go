// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package unreachable

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// Source is one backend a multi-source read fans out to: a location, a shard,
// a cell or a parent of the collection read across collections (AIP-159).
type Source[T any] struct {
	// Name is the service-relative resource name reported in unreachable when
	// the source cannot be reached, scoped as precisely as the source is (a
	// zonal or a regional location, say).
	Name string
	// Required marks a source without which the request returns no data by
	// definition (the single parent a request is scoped to): when it is
	// unreachable [Run] fails with an *[Error] whatever
	// [WithPartialSuccess] says.
	Required bool
	// Fetch reads the source's items for the current page. It must honor ctx.
	// An error the classifier accepts (see [WithClassifier]) makes the source
	// unreachable; an *[Error] names the unreachable resources itself. The
	// items Fetch returns along with an unreachable error are kept.
	Fetch func(ctx context.Context) ([]T, error)
}

// Result is the merged outcome of [Run].
type Result[T any] struct {
	// Items are the items of every reachable source in source order, each
	// source's items in the order its Fetch returned them.
	Items []T
	// Unreachable holds the names for the unreachable response field, sorted
	// and bounded like [Set.Names].
	Unreachable []string
	// Truncated is true when Unreachable omits names over the limit.
	Truncated bool
}

// outcome is the result of one fetch.
type outcome[T any] struct {
	items []T
	names []string // unreachable names, nil when reachable
	err   error    // failure that fails the run, nil otherwise
}

// Run fetches every source concurrently, at most [WithConcurrency] at a time,
// each bounded by [WithSourceTimeout], and merges the results
// deterministically: items in source order, unreachable names sorted. It is
// the AIP-217 fan-out of one page of a multi-source read.
//
// A source whose fetch fails with an error the classifier accepts is
// unreachable and its name, or the names of the *[Error] it returned, goes to
// Result.Unreachable. Run fails, canceling the fetches still running, when:
//
//   - ctx ends: the error is the context's cause;
//   - a fetch fails with another error: a *[SourceError] (the failure of the
//     lowest-indexed such source when several fail);
//   - a [Source.Required] source is unreachable, or any source is with
//     [WithPartialSuccess](false): an *[Error] (UNAVAILABLE) listing the
//     unreachable names found before the run stopped.
//
// Sources are checked before any fetch: a nil Fetch, an invalid or duplicate
// Name is an error matching [ErrInvalidSource] or [ErrInvalidName].
func Run[T any](ctx context.Context, sources []Source[T], opts ...Option) (Result[T], error) {
	o := newOptions(opts...)

	if err := checkSources(sources); err != nil {
		return Result[T]{}, err
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	outcomes := make([]outcome[T], len(sources))

	limit := o.concurrency
	if limit <= 0 || limit > len(sources) {
		limit = max(len(sources), 1)
	}

	sem := make(chan struct{}, limit)

	var wg sync.WaitGroup

launch:
	for i := range sources {
		select {
		case sem <- struct{}{}:
		case <-runCtx.Done():
			break launch
		}

		wg.Go(func() {
			defer func() { <-sem }()

			outcomes[i] = fetch(runCtx, &sources[i], o)
			if outcomes[i].err != nil || (outcomes[i].names != nil && (!o.partialSuccess || sources[i].Required)) {
				cancel()
			}
		})
	}

	wg.Wait()

	if err := ctx.Err(); err != nil {
		return Result[T]{}, context.Cause(ctx)
	}

	return merge(sources, outcomes, o)
}

// checkSources validates the sources before any fetch.
func checkSources[T any](sources []Source[T]) error {
	seen := make(map[string]int, len(sources))

	for i := range sources {
		s := &sources[i]
		if err := ValidateName(s.Name); err != nil {
			return fmt.Errorf("%w: sources[%d]: %w", ErrInvalidSource, i, err)
		}

		if s.Fetch == nil {
			return fmt.Errorf("%w: sources[%d] %q has no fetch function", ErrInvalidSource, i, s.Name)
		}

		if j, dup := seen[s.Name]; dup {
			return fmt.Errorf("%w: sources[%d] repeats the name %q of sources[%d]", ErrInvalidSource, i, s.Name, j)
		}

		seen[s.Name] = i
	}

	return nil
}

// fetch runs one source and classifies its error.
func fetch[T any](ctx context.Context, s *Source[T], o *options) outcome[T] {
	if ctx.Err() != nil {
		return outcome[T]{} // the run stopped before this source started
	}

	fctx := ctx

	if o.sourceTimeout > 0 {
		var cancel context.CancelFunc

		fctx, cancel = context.WithTimeout(ctx, o.sourceTimeout)
		defer cancel()
	}

	items, err := s.Fetch(fctx)
	out := outcome[T]{items: items}

	switch {
	case err == nil:
		return out
	case ctx.Err() != nil && errors.Is(err, context.Canceled):
		return outcome[T]{} // canceled by the run, not by the source
	}

	names := []string{s.Name}
	if e, ok := errors.AsType[*Error](err); ok && len(e.Names) > 0 && validNames(e.Names) {
		names = e.Names
	} else if !o.isUnreachable(err) {
		out.err = &SourceError{Source: s.Name, Err: err}

		return out
	}

	out.names = names
	if s.Required {
		out.err = &Error{Names: names, Err: err}
	}

	return out
}

// validNames reports whether every name is a valid service-relative name.
func validNames(names []string) bool {
	for _, n := range names {
		if ValidateName(n) != nil {
			return false
		}
	}

	return true
}

// merge combines the outcomes in source order.
func merge[T any](sources []Source[T], outcomes []outcome[T], o *options) (Result[T], error) {
	set := &Set{names: make(map[string]struct{}), o: o}

	for i := range outcomes {
		if err := outcomes[i].err; err != nil {
			if e, ok := errors.AsType[*Error](err); ok && sources[i].Required {
				e.Names, e.Truncated = bound(slices.Compact(slices.Sorted(slices.Values(e.Names))), o)
			}

			return Result[T]{}, err
		}

		for _, n := range outcomes[i].names {
			set.names[n] = struct{}{}
		}
	}

	names, truncated := set.Names()
	if len(names) > 0 && !o.partialSuccess {
		return Result[T]{}, &Error{Names: names, Truncated: truncated}
	}

	var n int
	for i := range outcomes {
		n += len(outcomes[i].items)
	}

	items := make([]T, 0, n)
	for i := range outcomes {
		items = append(items, outcomes[i].items...)
	}

	return Result[T]{Items: items, Unreachable: names, Truncated: truncated}, nil
}
