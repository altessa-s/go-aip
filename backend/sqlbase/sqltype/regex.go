// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqltype

import (
	"regexp"

	"github.com/altessa-s/go-aip/filter"
)

// CheckPortableRegex applies the [filter.PortableRegex] contract rule of the SQL schema translators: the pattern must be in
// the portable subset every backend reads alike ([filter.CheckPortableRegex]: QuoteMeta text, ^, $ and .*, with an optional
// ASCII-only (?i)), or it fails with [filter.ErrUnsupportedOperation]; a pattern RE2 cannot compile fails with
// [filter.ErrInvalidRegex]. It is the rule [filter.Validate] applies under a portable contract, so a pattern means the same
// whichever backend translates it.
func CheckPortableRegex(s string) error {
	if _, err := regexp.Compile(s); err != nil {
		return filter.ErrInvalidRegex
	}

	return filter.CheckPortableRegex(s)
}
