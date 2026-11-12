// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package etag

import (
	"math"

	coreetag "github.com/altessa-s/go-atlas-core/encoding/etag"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Reasons of the [coreerrs.FieldError] a [Precondition] returns for the
// etag field.
const (
	// ReasonRequired is the reason of a missing etag that [WithRequired]
	// demands.
	ReasonRequired = "required"
	// ReasonInvalid is the reason of an etag that is not an entity tag, or
	// not a tag the service issues.
	ReasonInvalid = "invalid value"
)

// Precondition is the AIP-154 freshness check of an Update, Delete or
// custom method. Build it with [NewPrecondition]; the zero value uses the
// defaults. A Precondition is immutable and safe for concurrent use.
//
// An empty sent etag carries no precondition unless [WithRequired](true).
// A sent etag is parsed like [Parse] and must match the current tag under
// the configured [Comparison] ([CompareExact] by default), else the request
// fails with [ErrMismatch], a core/errors.ErrConflict that the API reports
// as ABORTED. A malformed or missing required etag is a
// [coreerrs.FieldError] (INVALID_ARGUMENT) on [WithField].
type Precondition struct {
	o *options
}

// NewPrecondition returns a Precondition configured by opts.
func NewPrecondition(opts ...Option) Precondition {
	return Precondition{o: newOptions(opts...)}
}

func (p Precondition) opts() *options {
	if p.o == nil {
		return defaultOptions()
	}

	return p.o
}

// Parse decodes the etag a request sends. An empty sent is the zero coreetag.Tag (no
// precondition) unless the etag is required; a malformed one is a
// [coreerrs.FieldError] whose chain holds [ErrMissing] or [ErrInvalid].
func (p Precondition) Parse(sent string) (coreetag.Tag, error) {
	o := p.opts()
	if sent == "" {
		if o.required {
			return coreetag.Tag{}, &coreerrs.FieldError{Field: o.field, Reason: ReasonRequired, Err: ErrMissing}
		}

		return coreetag.Tag{}, nil
	}

	t, err := o.parse(sent)
	if err != nil {
		return coreetag.Tag{}, &coreerrs.FieldError{Field: o.field, Reason: ReasonInvalid, Err: err}
	}

	return t, nil
}

// Match checks a parsed etag against the current tag of the resource. A zero
// expected (no etag sent) accepts any resource. The zero current tag stands
// for a resource that does not exist (or has no etag): every sent etag
// fails it with [ErrMismatch].
func (p Precondition) Match(expected, current coreetag.Tag) error {
	if expected.IsZero() {
		return nil
	}

	if !p.opts().compare(expected, current) {
		return ErrMismatch
	}

	return nil
}

// Check parses sent and matches it against current: [Precondition.Parse]
// followed by [Precondition.Match].
func (p Precondition) Check(sent string, current coreetag.Tag) error {
	t, err := p.Parse(sent)
	if err != nil {
		return err
	}

	return p.Match(t, current)
}

// ParseRevision decodes the etag of a revision-guarded resource, one
// [FromRevision] issues. It returns ok == false for an absent optional etag,
// and fails like [Precondition.Parse] for a missing required or malformed
// etag, including a tag that is not a canonical weak revision, W/"0" without
// [WithSingleton] (every stored revision is positive) and MaxInt64 (the next
// revision would overflow). The caller compares the revision with the
// stored one, typically atomically in the store, and reports a stale one as
// [ErrMismatch].
func (p Precondition) ParseRevision(sent string) (rev int64, ok bool, err error) {
	t, err := p.Parse(sent)
	if err != nil || t.IsZero() {
		return 0, false, err
	}

	o := p.opts()

	rev, err = Revision[int64](t)
	if err == nil && (rev == math.MaxInt64 || (rev == 0 && !o.singleton)) {
		err = ErrNotRevision
	}

	if err != nil {
		return 0, false, &coreerrs.FieldError{Field: o.field, Reason: ReasonInvalid, Err: err}
	}

	return rev, true, nil
}

// compare matches the sent tag against the current one under the configured
// comparison; the zero tag matches nothing.
func (o *options) compare(sent, current coreetag.Tag) bool {
	switch o.comparison {
	case CompareStrong:
		return sent.StrongMatch(current)
	case CompareWeak:
		return sent.WeakMatch(current)
	default:
		return !current.IsZero() && sent == current
	}
}
