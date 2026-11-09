// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package batch

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// TxRunner runs fn in one transaction: it commits when fn returns nil and
// rolls back otherwise, returning fn's error. It may call fn again to retry
// the transaction; [Atomic] starts every attempt afresh.
type TxRunner func(ctx context.Context, fn func(ctx context.Context) error) error

// ItemFunc runs the singular method on the item at index i of a batch and
// returns its result.
type ItemFunc[T, R any] func(ctx context.Context, i int, item T) (R, error)

// Atomic runs a batch all or nothing (AIP-231 to AIP-235: a synchronous batch
// method must be atomic): inTx runs do on every item in request order inside
// one transaction. The first item error ends the transaction, which rolls
// back, and is returned as the *[ItemError] of its index (on field, the JSON
// name of the repeated field) with no results. On success the results are in
// request order.
//
// The items run one after the other: a transaction is rarely safe for
// concurrent use. do must write through the context it receives, which
// carries the transaction. A nil inTx, or one that returns nil without
// running the batch, is [ErrNoTransaction].
func Atomic[T, R any](ctx context.Context, field string, items []T, inTx TxRunner, do ItemFunc[T, R]) ([]R, error) {
	if inTx == nil {
		return nil, ErrNoTransaction
	}

	var results []R

	err := inTx(ctx, func(ctx context.Context) error {
		attempt := make([]R, len(items))

		for i, item := range items {
			if err := ctx.Err(); err != nil {
				return err
			}

			r, err := do(ctx, i, item)
			if err != nil {
				return NewItemError(field, i, err)
			}

			attempt[i] = r
		}

		results = attempt

		return nil
	})
	if err != nil {
		return nil, err
	}

	if results == nil {
		return nil, fmt.Errorf("%w: the runner returned without running the batch", ErrNoTransaction)
	}

	return results, nil
}

// Result is the outcome of a partial-success batch ([Partial]).
type Result[R any] struct {
	// Values holds the result of each request at its index, in request order;
	// the zero value where the request did not succeed.
	Values []R
	// Failed maps the index of each failed request to its error as the
	// singular method returned it: the failed_requests of the operation
	// metadata (AIP-233 to AIP-235), once each error is converted to a
	// google.rpc.Status.
	Failed map[int]error

	succeeded []bool
}

// Succeeded reports whether the request at index i succeeded. A request that
// neither succeeded nor failed ([Result.Failed]) was not run, or was
// interrupted, because the batch stopped.
func (r *Result[R]) Succeeded(i int) bool {
	return i >= 0 && i < len(r.succeeded) && r.succeeded[i]
}

// SucceededCount returns the number of requests that succeeded.
func (r *Result[R]) SucceededCount() int {
	n := 0

	for _, ok := range r.succeeded {
		if ok {
			n++
		}
	}

	return n
}

// Partial runs a batch that allows partial success: do runs on every item,
// and an item that fails is recorded in [Result.Failed] while the others go
// on. AIP-233 (Create), AIP-234 (Update) and AIP-235 (Delete) allow it only
// for a method that returns a long-running operation, opted in with
// return_partial_success when the method was atomic before; a synchronous
// method and every Batch Get (AIP-231) must use [Atomic] or [Get].
//
// Up to [WithConcurrency] items run at once; the results stay in request
// order whatever the order of completion. An item error that [WithFatal]
// matches (a transient failure, which must not be reported as a failed
// request) stops the batch: the items not yet started are not run, the
// running ones see their context canceled, and the error that stopped the
// batch is returned as its *[ItemError] along with the Result so far. The
// cancellation of ctx stops the batch the same way and returns ctx's error.
// When every request failed the error is [ErrAllFailed], again with the
// Result.
func Partial[T, R any](ctx context.Context, field string, items []T, do ItemFunc[T, R], opts ...RunOption) (*Result[R], error) {
	o := newRunOptions(opts...)
	res := &Result[R]{Values: make([]R, len(items)), Failed: map[int]error{}, succeeded: make([]bool, len(items))}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make([]error, len(items))

	var (
		next  atomic.Int64
		mu    sync.Mutex
		fatal error
		wg    sync.WaitGroup
	)

	worker := func() {
		for {
			i := int(next.Add(1) - 1)
			if i >= len(items) || runCtx.Err() != nil {
				return
			}

			r, err := do(runCtx, i, items[i])

			switch {
			case err == nil:
				res.Values[i], res.succeeded[i] = r, true
			case runCtx.Err() != nil:
				// Failed after the batch stopped, likely because of it:
				// neither succeeded nor failed.
			case o.fatal != nil && o.fatal(err):
				mu.Lock()
				if fatal == nil {
					fatal = NewItemError(field, i, err)
				}
				mu.Unlock()
				cancel()
			default:
				errs[i] = err
			}
		}
	}

	for range min(o.concurrency, len(items)) {
		wg.Go(worker)
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			res.Failed[i] = err
		}
	}

	switch {
	case fatal != nil:
		return res, fatal
	case ctx.Err() != nil:
		return res, ctx.Err()
	case len(items) > 0 && len(res.Failed) == len(items):
		return res, ErrAllFailed
	default:
		return res, nil
	}
}
