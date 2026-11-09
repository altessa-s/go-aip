// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidColumnName reports a column reference that is not a dot-separated sequence of plain identifiers within the
// limits of an [Identifiers] grammar.
var ErrInvalidColumnName = errors.New("sqlbase: invalid column name")

// Identifiers is the column-reference grammar of a SQL dialect: how a storage name such as "created_at" or
// "t.created_at" is validated and quoted when it is spliced into an ORDER BY or SELECT list. Every dot-separated part
// must be a plain identifier ([A-Za-z_][A-Za-z0-9_]*) of at most MaxIdentLen bytes, with at most MaxParts parts (zero
// means any); each part is quoted on its own with Quote. The built-in dialects use
// Identifiers{Quote: '"', MaxIdentLen: PostgresMaxIdentLen, MaxParts: 2} (PostgreSQL),
// Identifiers{Quote: '`', MaxIdentLen: MariaDBMaxIdentLen, MaxParts: 2} (MariaDB) and
// Identifiers{Quote: '`', MaxIdentLen: ClickHouseMaxIdentLen} (ClickHouse, whose Nested and JSON subcolumns are addressed
// part by part). The zero value rejects every name.
type Identifiers struct {
	// Quote is the identifier quote character.
	Quote byte
	// MaxIdentLen is the longest identifier part, in bytes.
	MaxIdentLen int
	// MaxParts is the largest number of dot-separated parts; zero means any.
	MaxParts int
}

// Column validates name under the grammar and quotes each part on its own. Anything else — an expression, a subscript,
// a quote, a part over MaxIdentLen, more than MaxParts parts — is rejected with [ErrInvalidColumnName], as is every name
// under an Identifiers without a quote character.
func (i Identifiers) Column(name string) (string, error) {
	if i.Quote == 0 {
		return "", fmt.Errorf("%w: no quoting policy", ErrInvalidColumnName)
	}

	parts := 0

	for part := range strings.SplitSeq(name, ".") {
		parts++

		switch {
		case !isIdentSegment(part):
			return "", fmt.Errorf("%w: %q", ErrInvalidColumnName, name)
		case len(part) > i.MaxIdentLen:
			return "", fmt.Errorf("%w: %q exceeds %d characters", ErrInvalidColumnName, part, i.MaxIdentLen)
		case i.MaxParts > 0 && parts > i.MaxParts:
			return "", fmt.Errorf("%w: %q has more than %d parts", ErrInvalidColumnName, name, i.MaxParts)
		}
	}

	var b strings.Builder
	b.Grow(len(name) + 2*parts) //nolint:mnd // two quotes per part

	for part := range strings.SplitSeq(name, ".") {
		if b.Len() > 0 {
			b.WriteByte('.')
		}

		b.WriteByte(i.Quote)
		b.WriteString(part)
		b.WriteByte(i.Quote)
	}

	return b.String(), nil
}
