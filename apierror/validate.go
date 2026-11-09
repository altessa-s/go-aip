// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package apierror

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"google.golang.org/genproto/googleapis/rpc/code"
)

// AIP-193 limits of ErrorInfo.
const (
	// MaxReasonLength is the longest ErrorInfo reason.
	MaxReasonLength = 63
	// MaxMetadataKeyLength is the longest ErrorInfo metadata key.
	MaxMetadataKeyLength = 64
)

// ErrInvalidError reports an [Error] or a [Rule] that breaks AIP-193: see
// [New] and [NewMapper].
var ErrInvalidError = errors.New("apierror: invalid error")

// ValidReason reports whether reason is an AIP-193 ErrorInfo reason: at most
// 63 characters matching [A-Z][A-Z0-9_]+[A-Z0-9].
func ValidReason(reason string) bool {
	if len(reason) < 3 || len(reason) > MaxReasonLength || !isUpper(reason[0]) || reason[len(reason)-1] == '_' {
		return false
	}

	for i := 1; i < len(reason); i++ {
		if c := reason[i]; !isUpper(c) && !isDigit(c) && c != '_' {
			return false
		}
	}

	return true
}

// ValidMetadataKey reports whether key is an AIP-193 ErrorInfo metadata key:
// at most 64 characters matching [a-z][a-zA-Z0-9-_]+ (lowerCamelCase).
func ValidMetadataKey(key string) bool {
	if len(key) < 2 || len(key) > MaxMetadataKeyLength || !isLower(key[0]) {
		return false
	}

	for i := 1; i < len(key); i++ {
		if c := key[i]; !isLower(c) && !isUpper(c) && !isDigit(c) && c != '-' && c != '_' {
			return false
		}
	}

	return true
}

// validCode reports whether c is an error code of google.rpc.Code.
func validCode(c code.Code) bool {
	return c > code.Code_OK && c <= code.Code_UNAUTHENTICATED
}

// validate checks e against AIP-193.
func (e *Error) validate() error {
	switch {
	case !validCode(e.code):
		return fmt.Errorf("%w: code %d is not an error code of google.rpc.Code", ErrInvalidError, e.code)
	case e.message == "" || !utf8.ValidString(e.message):
		return fmt.Errorf("%w: empty message or not UTF-8", ErrInvalidError)
	case e.info == nil:
		return fmt.Errorf("%w: missing ErrorInfo", ErrInvalidError)
	case !ValidReason(e.info.GetReason()):
		return fmt.Errorf("%w: reason %q is not UPPER_SNAKE_CASE of at most %d characters", ErrInvalidError,
			e.info.GetReason(), MaxReasonLength)
	}

	for k := range e.info.GetMetadata() {
		if !ValidMetadataKey(k) {
			return fmt.Errorf("%w: metadata key %q is not lowerCamelCase of at most %d characters", ErrInvalidError,
				k, MaxMetadataKeyLength)
		}
	}

	for i, v := range e.badRequest.GetFieldViolations() {
		if v == nil || v.GetField() == "" {
			return fmt.Errorf("%w: field violation %d has no field", ErrInvalidError, i)
		}

		if v.GetReason() != "" && !ValidReason(v.GetReason()) {
			return fmt.Errorf("%w: field violation reason %q is not UPPER_SNAKE_CASE", ErrInvalidError, v.GetReason())
		}

		if lm := v.GetLocalizedMessage(); lm != nil && (lm.GetLocale() == "" || lm.GetMessage() == "") {
			return fmt.Errorf("%w: localized message of field violation %d needs a locale and a message", ErrInvalidError, i)
		}
	}

	if e.localized != nil && (e.localized.GetLocale() == "" || e.localized.GetMessage() == "") {
		return fmt.Errorf("%w: localized message needs a locale and a message", ErrInvalidError)
	}

	if e.retry != nil && e.retry.GetRetryDelay().AsDuration() <= 0 {
		return fmt.Errorf("%w: retry delay must be positive", ErrInvalidError)
	}

	if err := validateItems(e); err != nil {
		return err
	}

	// A detail that does not marshal (a string that is not UTF-8, say) would
	// be lost on the wire.
	for _, d := range e.details(&renderOptions{includeDebugInfo: true}) {
		if _, err := pack(d); err != nil {
			return fmt.Errorf("%w: detail %s: %w", ErrInvalidError, detailKey(d), err)
		}
	}

	return nil
}

// validateItems checks the repeated entries of the other details.
func validateItems(e *Error) error {
	for _, l := range e.help.GetLinks() {
		if l.GetUrl() == "" {
			return fmt.Errorf("%w: help link without a URL", ErrInvalidError)
		}
	}

	for _, v := range e.quota.GetViolations() {
		if v == nil {
			return fmt.Errorf("%w: nil quota violation", ErrInvalidError)
		}
	}

	for _, v := range e.precondition.GetViolations() {
		if v == nil {
			return fmt.Errorf("%w: nil precondition violation", ErrInvalidError)
		}
	}

	return nil
}

func isUpper(c byte) bool { return c >= 'A' && c <= 'Z' }
func isLower(c byte) bool { return c >= 'a' && c <= 'z' }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
