// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"context"

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
// [ListEngine.BoundedCount] and countOpts. load gets the keys in page order and is not called for an empty page. Run
// the read in one snapshot, for example a read-only repeatable-read transaction the querier joins, so that the rows,
// the next page token and the total describe the same state.
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
	}

	if count {
		total, err := l.BoundedCount(ctx, req, scope, countOpts...)
		if err != nil {
			return HydratedPage[R]{}, err
		}

		result.Total = &total
	}

	return result, nil
}
