// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/altessa-s/go-aip/apierror"
	"github.com/altessa-s/go-aip/backend/mongo/internal/keyset"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

var (
	// ErrScope reports a scoped list queried without its scope keys pinned to the parent — the store would read the items of
	// every parent — or a scope holding a value a filter does not admit (see [ListEngine.List]). It is a server-side fault
	// (INTERNAL in an error mapper).
	ErrScope = errors.New("mongo: scope does not pin the parent")
	// ErrTimeout reports a query that exceeded its time cap ([WithListMaxTime]): a filter on unindexed fields scanned too much.
	// With a filter it travels inside a core/errors.FieldError on filter (narrow it); without one it is wrapped with
	// core/errors.ErrUnavailable (retry later).
	ErrTimeout = errors.New("mongo: query exceeded its time limit")
	// ErrCorruptRecord is the default category of a stored document that does not decode into the page type
	// ([WithListCorruptRecord]): a server-side fault, never a client error.
	ErrCorruptRecord = errors.New("mongo: stored record is invalid")
)

// mapError classifies a page read failure. The query context having ended (queryCtx done) reports that end alone, whatever
// FindPage returned — a cursor, decoder or token error included: the caller's context ending is its error and the time cap
// expiring is [ErrTimeout]. Otherwise a document that does not decode into the page type is the corrupt-record category whatever
// its decoder returned, a page-token failure is a field error on pageToken, a driver timeout is [ErrTimeout], and anything else is
// wrapped. [ErrTimeout] is on filter when one was given, else unavailable; a context end or a timeout hides err behind
// apierror.Classify, so no driver or decoder error in it reaches a client or an API error mapper (apierror.HiddenCause keeps it).
func (l *ListEngine[D]) mapError(ctx, queryCtx context.Context, err error, filtered bool) error {
	if l.opts.errorMapper != nil {
		return l.opts.errorMapper(ctx, err, filtered)
	}

	if queryCtx.Err() != nil {
		return l.timeoutError(ctx, err, filtered)
	}

	// A page type's decoder may return any error, a token or deadline sentinel included: it is still a stored record that does
	// not decode, so it is classified before the token and timeout causes it may also match.
	if _, ok := errors.AsType[*keyset.DecodeError](err); ok {
		return coreerrs.WrapOperation(classifyDecode(err, l.opts.corruptRecord), "list "+l.collName())
	}

	if failure := pagetoken.Classify(err); failure != pagetoken.FailureNone {
		return l.schema.FormatError(listing.FieldPageToken, failure.String(), err)
	}

	if errors.Is(err, context.DeadlineExceeded) || mongo.IsTimeout(err) {
		return l.timeoutError(ctx, err, filtered)
	}

	return coreerrs.WrapOperation(classifyDecode(err, l.opts.corruptRecord), "list "+l.collName())
}

// timeoutError reports the end of a query, hiding err: the caller's context ending is its error, and the time cap expiring (or a
// driver timeout) is [ErrTimeout] — on filter when the query was filtered, else unavailable.
func (l *ListEngine[D]) timeoutError(ctx context.Context, err error, filtered bool) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return apierror.Classify(ctxErr, err)
	}

	if filtered {
		return l.schema.FormatError(listing.FieldFilter, "the query took too long; narrow the filter", apierror.Classify(ErrTimeout, err))
	}

	return apierror.Classify(fmt.Errorf("%w: %w", coreerrs.ErrUnavailable, ErrTimeout), err)
}

// classifyDecode classifies a document decoding failure (a *keyset.DecodeError, which FindPage returns for any error decoding a
// document into the page type, a *bson.DecodeError or a bson.ValueDecoderError anywhere in the chain) with category through
// apierror.Classify: the result reads as and matches category alone and keeps the failure as its hidden cause
// (apierror.HiddenCause), so a decoder's page-token, deadline or field error never turns a stored record that does not decode
// into a client error. The whole failure is classified, whatever its chain already classifies: a decoder error joining a
// classified cause with a deadline or an API error must not leave that sibling to the mapper. Any other failure, or one that is
// itself classified by category, is returned unchanged.
func classifyDecode(err, category error) error {
	if _, ok := err.(apierror.Classified); ok && errors.Is(err, category) {
		return err
	}

	if _, ok := errors.AsType[*keyset.DecodeError](err); ok {
		return apierror.Classify(category, err)
	}

	if _, ok := errors.AsType[*bson.DecodeError](err); ok {
		return apierror.Classify(category, err)
	}

	if _, ok := errors.AsType[bson.ValueDecoderError](err); ok {
		return apierror.Classify(category, err)
	}

	return err
}

// collName names the collection in errors; an engine without a collection still reports.
func (l *ListEngine[D]) collName() string {
	if l.coll == nil {
		return "<no collection>"
	}

	return l.coll.Name()
}
