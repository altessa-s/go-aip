// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package pagetoken

import coreerrs "github.com/altessa-s/go-atlas-core/errors"

// Request fields of a paged list, by their AIP-158 JSON names: a page-token
// failure is a field violation on TokenField ([TokenFieldError]), a negative
// page size one on SizeField ([ResolvePageSize]).
const (
	TokenField = "pageToken"
	SizeField  = "pageSize"
)

// FieldError maps a page-token failure to a field violation on field (for
// example [TokenField]): for an error [Classify] recognizes it returns a
// [*coreerrs.FieldError] whose Reason is the failure class ("malformed" or
// "expired") and whose Err is err, so errors.Is still matches both the token
// error and [coreerrs.ErrInvalidArgument]. Any other error, including a
// server-side failure such as [ErrWeakKey], is returned unchanged, and nil
// yields nil.
func FieldError(err error, field string) error {
	f := Classify(err)
	if f == FailureNone {
		return err
	}

	return &coreerrs.FieldError{Field: field, Reason: f.String(), Err: err}
}

// TokenFieldError is [FieldError] on [TokenField], the page-token field of
// an AIP-158 list request.
func TokenFieldError(err error) error {
	return FieldError(err, TokenField)
}
