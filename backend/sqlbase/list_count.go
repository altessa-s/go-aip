// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"context"
	"errors"

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

	filtered := p.query.Filter != nil

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
