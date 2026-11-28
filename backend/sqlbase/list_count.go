// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"context"
	"errors"
	"math"

	"github.com/altessa-s/go-aip/listing"

	corecontext "github.com/altessa-s/go-atlas-core/context"
)

// Count returns the exact number of matching rows in scope before pagination.
// It applies List's request validation and ignores PageToken. To read the same
// snapshot as List, use the same repeatable-read transaction and the same request, including its cutoff instant.
func (l *ListEngine[T]) Count(ctx context.Context, req listing.Request, scope Scope) (int64, error) {
	p, err := l.prepare(ctx, req, scope)
	if err != nil {
		return 0, err
	}

	where, args, err := l.buildWhere(p.filterSQL, p.args, p.pins, nil, nil)
	if err != nil {
		return 0, err
	}

	query := "SELECT COUNT(*) FROM " + l.from + " AS " + l.quote(l.opts.tableAlias) + " WHERE " + where

	qctx, cancel := corecontext.WithMaxTimeout(ctx, l.opts.maxTime)
	defer cancel()

	if err = l.tx.acquire(qctx); err != nil {
		return 0, l.mapError(ctx, qctx, err, p.query.Filter != nil)
	}

	defer l.tx.release()

	return l.count(ctx, qctx, query, args, p.query.Filter != nil)
}

// Total is the size of a collection from [ListEngine.BoundedCount]. Count is exact when Exact is set; otherwise it is
// at least the counted rows and, when the dialect estimates counts, at least the planner's estimate.
type Total struct {
	Count int64
	Exact bool
}

// BoundedCount returns the number of matching rows in scope before pagination, exact up to the count limit
// ([WithCountLimit], [DefaultCountLimit] by default). It counts at most limit+1 rows; a larger collection, or a count
// cut short by [WithCountProbeTime], reports the larger of the counted rows and the estimate of a [ListCountDialect]
// with Exact false. It validates the request as [ListEngine.Count] does, ignores PageToken and reads the snapshot of
// the querier, as Count does.
func (l *ListEngine[T]) BoundedCount(ctx context.Context, req listing.Request, scope Scope, opts ...CountOption) (Total, error) {
	o := newCountOptions(opts...)

	counter, err := l.validateCountOptions(o)
	if err != nil {
		return Total{}, err
	}

	return l.boundedCount(ctx, req, scope, o, counter)
}

func (l *ListEngine[T]) validateCountOptions(o *countOptions) (ListCountDialect, error) {
	if o.limit < 0 || o.limit == math.MaxInt {
		return nil, invalid("count limit %d", o.limit)
	}

	if o.probeTime < 0 {
		return nil, invalid("negative count probe time")
	}

	counter, _ := l.opts.dialect.(ListCountDialect)
	if o.probeTime > 0 && counter == nil {
		return nil, invalid("the dialect cannot bound a count probe")
	}

	return counter, nil
}

func (l *ListEngine[T]) boundedCount(ctx context.Context, req listing.Request, scope Scope, o *countOptions, counter ListCountDialect) (Total, error) {
	p, err := l.prepare(ctx, req, scope)
	if err != nil {
		return Total{}, err
	}

	where, args, err := l.buildWhere(p.filterSQL, p.args, p.pins, nil, nil)
	if err != nil {
		return Total{}, err
	}

	rows := "SELECT 1 FROM " + l.from + " AS " + l.quote(l.opts.tableAlias) + " WHERE " + where
	probe := "SELECT COUNT(*) FROM (" + l.opts.dialect.LimitSelect(rows, o.limit+1) + ") AS " + l.quote("_count")

	qctx, cancel := corecontext.WithMaxTimeout(ctx, l.opts.maxTime)
	defer cancel()

	if err = l.tx.acquire(qctx); err != nil {
		return Total{}, l.mapError(ctx, qctx, err, p.query.Filter != nil)
	}

	defer l.tx.release()

	filtered := p.query.Filter != nil

	var (
		n    int64
		done = true
	)

	if o.probeTime > 0 {
		n, done, err = counter.ProbeCount(qctx, l.q, probe, args, o.probeTime)
		if err != nil {
			return Total{}, l.mapError(ctx, qctx, err, filtered)
		}
	} else if n, err = l.count(ctx, qctx, probe, args, filtered); err != nil {
		return Total{}, err
	}

	if done && n <= int64(o.limit) {
		return Total{Count: n, Exact: true}, nil
	}

	if counter != nil {
		estimate, err := counter.EstimateCount(qctx, l.q, rows, args)
		if err != nil {
			return Total{}, l.mapError(ctx, qctx, err, filtered)
		}

		n = max(n, estimate)
	}

	return Total{Count: n}, nil
}

// count runs query, which returns one non-negative count.
func (l *ListEngine[T]) count(ctx, qctx context.Context, query string, args []any, filtered bool) (int64, error) {
	rows, err := l.q.QueryContext(qctx, query, args...)
	if err != nil {
		return 0, l.mapError(ctx, qctx, err, filtered)
	}

	defer func() { _ = rows.Close() }()

	var (
		total int64
		seen  bool
	)

	for rows.Next() {
		if seen {
			return 0, l.scanError(ctx, qctx, errors.New("multiple SQL count rows"), filtered)
		}

		if err := rows.Scan(&total); err != nil {
			return 0, l.scanError(ctx, qctx, err, filtered)
		}

		seen = true
	}

	if err := rows.Err(); err != nil {
		return 0, l.mapError(ctx, qctx, err, filtered)
	}

	if !seen || total < 0 {
		return 0, l.scanError(ctx, qctx, errors.New("missing or negative SQL count"), filtered)
	}

	return total, nil
}
