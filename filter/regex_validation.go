// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

import (
	"regexp"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// ValidateRegex validates a regex pattern for length and correctness.
// It enforces the given maxLength and compiles the pattern with Go's RE2 engine
// to reject patterns that could be problematic. This function is intended to be
// used by both the evaluator and translators for consistent regex validation.
func ValidateRegex(pattern string, maxLength int) error {
	if len(pattern) > maxLength {
		return coreerrs.Wrapf(ErrInvalidRegex, "pattern length %d exceeds maximum %d", len(pattern), maxLength)
	}

	if _, err := regexp.Compile(pattern); err != nil {
		return coreerrs.Wrapf(ErrInvalidRegex, "%w", err)
	}

	return nil
}
