// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package pagetoken

import (
	"errors"
	"fmt"

	"github.com/altessa-s/go-atlas-core/encoding/keys"
	"github.com/altessa-s/go-atlas-core/types/nilcheck"
)

// ErrCodecRequired is returned when a page token is presented to a list that
// has no [Codec] and so cannot verify it.
var ErrCodecRequired = errors.New("pagetoken: page token given without a codec")

// ErrNegativeOffset is returned by [Offset.Issue] for a negative offset.
var ErrNegativeOffset = errors.New("pagetoken: negative offset")

// NewBindings returns the bindings of a list request. subject names the list
// (for example its RPC) so tokens of different lists never cross; sort
// fingerprints the order; filter holds the request fields the token is bound
// to — never the page size, which may change between pages. Filter parts are
// length-prefixed ([keys.Compose]), so ("ab", "c") and ("a", "bc") differ.
func NewBindings(subject, sort string, filter ...string) Bindings {
	return Bindings{Subject: subject, Sort: sort, Filter: keys.Compose(filter...)}
}

// Signer signs and verifies token payloads; [Typed] and [Offset] build on it
// and [*Codec] implements it.
type Signer interface {
	Issue(payload []byte, b Bindings) (string, error)
	Resolve(token string, b Bindings) ([]byte, error)
}

// Typed issues and resolves tokens carrying a cursor of type C, encoded as
// JSON by default, or a configured PayloadCodec. The zero value and a Typed over a nil [Signer] issue no tokens, so a
// list without page tokens serves a single page. A Typed is a small value;
// build one per call or keep it, it is safe for concurrent use.
type Typed[C any] struct {
	codec   Signer
	payload PayloadCodec
}

// NewTyped returns a Typed signing with codec, which may be nil (including a
// typed nil such as a nil *Codec).
func NewTyped[C any](codec Signer, opts ...TypedOption) Typed[C] {
	if nilcheck.IsNil(codec) {
		return Typed[C]{}
	}

	return Typed[C]{codec: codec, payload: newTypedOptions(opts...).payloadCodec}
}

// Issue returns a token carrying cursor, bound to b. Without a codec it
// returns "" and no error.
func (t Typed[C]) Issue(cursor C, b Bindings) (string, error) {
	if t.codec == nil {
		return "", nil
	}

	payload, err := t.payload.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("pagetoken: marshal cursor: %w", err)
	}

	return t.codec.Issue(payload, b)
}

// Resolve verifies token against b and returns its cursor. An empty token is
// the first page: the zero cursor, first true and no error. A token that
// fails [Codec.Resolve] returns its error; a token presented without a codec
// returns [ErrCodecRequired]; a verified payload that does not decode into C
// returns [ErrInvalidToken]. Every such error is classified by [Classify].
func (t Typed[C]) Resolve(token string, b Bindings) (cursor C, first bool, err error) {
	var zero C
	if token == "" {
		return zero, true, nil
	}

	if t.codec == nil {
		return zero, false, ErrCodecRequired
	}

	payload, err := t.codec.Resolve(token, b)
	if err != nil {
		return zero, false, err
	}

	if err := t.payload.Unmarshal(payload, &cursor); err != nil {
		return zero, false, fmt.Errorf("%w: decode cursor: %w", ErrInvalidToken, err)
	}

	return cursor, false, nil
}

// Offset issues and resolves tokens carrying a non-negative offset, for
// small in-memory catalogs that are paged by position. Like [Typed], the
// zero value and an Offset over a nil codec issue no tokens.
type Offset struct {
	typed Typed[int]
}

// NewOffset returns an Offset signing with codec, which may be nil.
func NewOffset(codec Signer, opts ...TypedOption) Offset {
	return Offset{typed: NewTyped[int](codec, opts...)}
}

// Issue returns a token carrying offset, bound to b; "" without a codec. A
// negative offset is a caller bug and is refused with [ErrNegativeOffset]
// (not a token failure: [Classify] reports [FailureNone]).
func (o Offset) Issue(offset int, b Bindings) (string, error) {
	if offset < 0 {
		return "", fmt.Errorf("%w: %d", ErrNegativeOffset, offset)
	}

	return o.typed.Issue(offset, b)
}

// Resolve verifies token against b and returns its offset: 0 for an empty
// token (the first page). It fails like [Typed.Resolve], and with
// [ErrInvalidToken] for a negative offset. The caller still checks the offset
// against its own bounds.
func (o Offset) Resolve(token string, b Bindings) (int, error) {
	offset, _, err := o.typed.Resolve(token, b)
	if err != nil {
		return 0, err
	}

	if offset < 0 {
		return 0, ErrInvalidToken
	}

	return offset, nil
}

// Failure classifies a page-token error for a transport, independently of
// how the transport reports it.
type Failure int

// Failure classes returned by [Classify].
const (
	// FailureNone means the error is not a page-token failure.
	FailureNone Failure = iota
	// FailureMalformed covers a token that is not ours, was tampered with,
	// does not decode, was issued for another list, filter, sort or subject,
	// or was presented to a list without a codec.
	FailureMalformed
	// FailureExpired is a genuine token older than the codec TTL.
	FailureExpired
)

// String returns the reason a transport reports: "malformed", "expired", or
// "" for [FailureNone].
func (f Failure) String() string {
	switch f {
	case FailureMalformed:
		return "malformed"
	case FailureExpired:
		return "expired"
	default:
		return ""
	}
}

// Classify reports which page-token failure err is, matching the package
// errors with errors.Is anywhere in its chain. [ErrPayloadTooLarge] and
// [ErrWeakKey] are server-side failures, not token failures, and classify as
// [FailureNone] like nil and any other error.
func Classify(err error) Failure {
	switch {
	case err == nil:
		return FailureNone
	case errors.Is(err, ErrExpiredToken):
		return FailureExpired
	case errors.Is(err, ErrInvalidToken),
		errors.Is(err, ErrSortChanged),
		errors.Is(err, ErrFilterChanged),
		errors.Is(err, ErrSubjectMismatch),
		errors.Is(err, ErrCodecRequired):
		return FailureMalformed
	default:
		return FailureNone
	}
}
