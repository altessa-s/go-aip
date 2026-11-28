// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"context"
	"fmt"

	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/page"
)

// HydratedPage is a page of loaded rows and, when it was asked for, the bounded total of the matching rows.
type HydratedPage[R any] struct {
	page.Page[R]
	// Total is nil unless the read counted the matches.
	Total *Total
}

// ListHydrated lists a page of keys, loads its rows with load and, when count is set, counts the matching rows with
// [ListEngine.BoundedCount] and countOpts. The loader is required and must return exactly one row per key in the
// supplied order. An empty page skips the loader. Run the read in one snapshot, for example a read-only repeatable-read
// transaction the querier joins, so the rows, next page token and total describe the same state.
func ListHydrated[T comparable, R any](
	ctx context.Context,
	l *ListEngine[T],
	subject string,
	req listing.Request,
	scope Scope,
	load func(context.Context, []T) ([]R, error),
	count bool,
	countOpts ...CountOption,
) (HydratedPage[R], error) {
	if l == nil || load == nil {
		return HydratedPage[R]{}, invalid("list engine and loader are required")
	}

	var (
		o       *countOptions
		counter ListCountDialect
		err     error
	)

	if count {
		o = newCountOptions(countOpts...)

		counter, err = l.validateCountOptions(o)
		if err != nil {
			return HydratedPage[R]{}, err
		}
	}

	keys, err := l.List(ctx, subject, req, scope)
	if err != nil {
		return HydratedPage[R]{}, err
	}

	var result HydratedPage[R]

	result.NextPageToken = keys.NextPageToken
	if len(keys.Items) > 0 {
		if result.Items, err = load(ctx, keys.Items); err != nil {
			return HydratedPage[R]{}, err
		}

		if len(result.Items) != len(keys.Items) {
			return HydratedPage[R]{}, fmt.Errorf("%w: loader returned %d rows for %d keys", ErrCorruptRecord, len(result.Items), len(keys.Items))
		}
	}

	if count {
		total, err := l.boundedCount(ctx, req, scope, o, counter)
		if err != nil {
			return HydratedPage[R]{}, err
		}

		result.Total = &total
	}

	return result, nil
}
