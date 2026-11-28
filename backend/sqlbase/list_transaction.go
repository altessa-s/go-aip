// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"context"
	"database/sql"
)

// ListTransaction coordinates list engines and other operations on one SQL transaction.
// Use one coordinator per transaction. It holds exclusive access through result-set closure and, for a timed count,
// through savepoint cleanup. Statements and transaction lifecycle calls made directly on the raw transaction
// outside the coordinator are outside its control.
type ListTransaction struct {
	tx   *sql.Tx
	busy chan struct{}
}

// NewListTransaction wraps tx for use with [WithListTransaction]. The caller retains ownership of tx and must finish
// it after all coordinated operations have returned.
func NewListTransaction(tx *sql.Tx) *ListTransaction {
	return &ListTransaction{tx: tx, busy: make(chan struct{}, 1)}
}

func resolveListTransaction(q Querier, t *ListTransaction) (Querier, *ListTransaction, error) {
	if t != nil {
		if t.tx == nil || t.busy == nil || (q != nil && q != t.tx) {
			return nil, nil, invalid("invalid or mismatched list transaction")
		}

		return t.tx, t, nil
	}

	if raw, ok := q.(*sql.Tx); ok && raw != nil {
		return raw, NewListTransaction(raw), nil
	}

	return q, nil, nil
}

// Run waits for exclusive access to the transaction or for ctx to end, then calls fn with the transaction.
// fn must use its own statement contexts, close any result sets before returning, and avoid calling engines
// or Run on the same coordinator while it holds access.
func (t *ListTransaction) Run(ctx context.Context, fn func(*sql.Tx) error) error {
	if t == nil || t.tx == nil || t.busy == nil || fn == nil {
		return invalid("invalid list transaction or operation")
	}

	if err := t.acquire(ctx); err != nil {
		return err
	}

	defer t.release()

	return fn(t.tx)
}

// acquire is a no-op for an engine operating without a transaction coordinator.
func (t *ListTransaction) acquire(ctx context.Context) error {
	if t == nil {
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case t.busy <- struct{}{}:
		if err := ctx.Err(); err != nil {
			t.release()
			return err
		}

		return nil
	}
}

func (t *ListTransaction) release() {
	if t != nil {
		<-t.busy
	}
}
