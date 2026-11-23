// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/altessa-s/go-aip/apierror"
	"github.com/altessa-s/go-aip/listing"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

var (
	// ErrScope reports a scope that does not pin the parent of the list.
	ErrScope = errors.New("sqlbase: scope does not pin the parent")
	// ErrTimeout reports a query that exceeded its time limit.
	ErrTimeout = errors.New("sqlbase: query exceeded its time limit")
	// ErrCorruptRecord reports a stored row that does not scan into the page type, or whose sort values do not normalize: a
	// server-side fault, never a client error. It classifies the scan error through apierror.Classify: the failure matches this
	// sentinel alone (INTERNAL) whatever the scan error wraps, and apierror.HiddenCause returns the scan error.
	ErrCorruptRecord = errors.New("sqlbase: stored record is invalid")
	// ErrInvalidSchema is [listing.ErrInvalidSchema], re-exported so errors.Is matches either.
	ErrInvalidSchema = listing.ErrInvalidSchema
)

// mapError classifies a query failure e: the caller's context ending is its error and the time cap expiring (or a driver timeout)
// is [ErrTimeout] — on filter when one was given, else unavailable — each hiding e behind apierror.Classify, so no driver or
// scan error in it reaches a client or an API error mapper (apierror.HiddenCause keeps it for logs); anything else is wrapped.
func (l *ListEngine[T]) mapError(ctx, queryCtx context.Context, e error, filtered bool) error {
	if l.opts.errorMapper != nil {
		return l.opts.errorMapper(ctx, e, filtered)
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		return apierror.Classify(ctxErr, e)
	}

	netErr, isNetError := errors.AsType[net.Error](e)
	if errors.Is(queryCtx.Err(), context.DeadlineExceeded) || errors.Is(e, context.DeadlineExceeded) || (isNetError && netErr.Timeout()) {
		if filtered {
			return l.schema.FormatError(listing.FieldFilter, "the query took too long; narrow the filter", apierror.Classify(ErrTimeout, e))
		}

		return apierror.Classify(fmt.Errorf("%w: %w", coreerrs.ErrUnavailable, ErrTimeout), e)
	}

	return coreerrs.Wrap(e, "list SQL table")
}

// mapEndedError classifies a failure after the query ran — scanning its rows or issuing the next page token — while the query
// context has ended: the rows closed under the scan, or the request ended before its token was issued, so it is a timeout or a
// cancellation whatever the destination's Scan, the signer or the position codec returned. The default classification hides e
// ([mapError]); an error mapper receives the query context's error, which hides e behind apierror.Classify, so it never sees a
// FieldError, API error or sentinel e holds.
func (l *ListEngine[T]) mapEndedError(ctx, queryCtx context.Context, e error, filtered bool) error {
	if l.opts.errorMapper != nil {
		return l.opts.errorMapper(ctx, apierror.Classify(queryCtx.Err(), e), filtered)
	}

	return l.mapError(ctx, queryCtx, e, filtered)
}

// scanError classifies an unreadable stored value or aggregate. Rows closed by the query
// context ending report that end alone, even when a destination returns another error.
func (l *ListEngine[T]) scanError(ctx, queryCtx context.Context, err error, filtered bool) error {
	if queryCtx.Err() != nil {
		return l.mapEndedError(ctx, queryCtx, err, filtered)
	}

	return apierror.Classify(ErrCorruptRecord, err)
}
