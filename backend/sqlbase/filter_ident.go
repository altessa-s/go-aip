// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"strings"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// ValidateIdent rejects any column name that is not a dot-separated
// sequence of plain SQL identifiers ([A-Za-z_][A-Za-z0-9_]*).
//
// The check is deliberately stricter than quoting alone requires. The
// column position is the one part of a generated clause that no
// placeholder can cover, so it must not be reachable by anything a
// [filter.WithFieldMapping] entry could smuggle in — an expression, a
// subscript, a quote, a comment.
func ValidateIdent(name string) error {
	if name == "" {
		return coreerrs.Wrap(filter.ErrInvalidExpression, "empty column name")
	}

	for segment := range strings.SplitSeq(name, ".") {
		if !isIdentSegment(segment) {
			return coreerrs.Wrapf(filter.ErrInvalidExpression, "%q is not a valid SQL column name", name)
		}
	}

	return nil
}

// Identifier length limits of the supported backends, in bytes. Identifiers that pass [ValidateIdent] are ASCII, so bytes
// and characters agree.
const (
	// PostgresMaxIdentLen is NAMEDATALEN-1: PostgreSQL silently truncates a longer identifier, so a name past the limit
	// would read a different column.
	PostgresMaxIdentLen = 63
	// MariaDBMaxIdentLen is the MariaDB and MySQL limit on column, table and collation names.
	MariaDBMaxIdentLen = 64
	// ClickHouseMaxIdentLen bounds a ClickHouse column name, which also names the column's files on disk.
	ClickHouseMaxIdentLen = 255
)

// maxQualifiedParts is the most parts [QuoteQualified] accepts: a column qualified by its table, never by a schema or
// database as well.
const maxQualifiedParts = 2

// QuoteWhole validates name and wraps it in quote as a single identifier, dots included. This is the ClickHouse policy:
// a Nested column is literally stored under the name "address.city". A name longer than maxLen bytes (0 means no limit)
// is rejected with [filter.ErrInvalidExpression].
func QuoteWhole(name string, quote byte, maxLen int) (string, error) {
	if err := ValidateIdent(name); err != nil {
		return "", err
	}

	if maxLen > 0 && len(name) > maxLen {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "SQL column name %q exceeds %d bytes", name, maxLen)
	}

	var b strings.Builder
	b.Grow(len(name) + 2) //nolint:mnd // opening + closing quote.
	b.WriteByte(quote)
	b.WriteString(name)
	b.WriteByte(quote)

	return b.String(), nil
}

// QuoteQualified validates name and quotes each dot-separated segment on its own, producing the standard SQL qualified
// form ("table"."column"). This is the MariaDB and PostgreSQL policy. A name of more than two parts (a schema- or
// database-qualified column) or with a part longer than maxLen bytes (0 means no limit) is rejected with
// [filter.ErrInvalidExpression]: PostgreSQL would silently truncate the part and read another column.
func QuoteQualified(name string, quote byte, maxLen int) (string, error) {
	if err := ValidateIdent(name); err != nil {
		return "", err
	}

	if strings.Count(name, ".") >= maxQualifiedParts {
		return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "SQL column name %q has more than %d parts", name, maxQualifiedParts)
	}

	for segment := range strings.SplitSeq(name, ".") {
		if maxLen > 0 && len(segment) > maxLen {
			return "", coreerrs.Wrapf(filter.ErrInvalidExpression, "SQL column name %q exceeds %d bytes", name, maxLen)
		}
	}

	var b strings.Builder
	b.Grow(len(name) + 2*(strings.Count(name, ".")+1)) //nolint:mnd // two quotes per segment.

	for i, segment := range strings.Split(name, ".") {
		if i > 0 {
			b.WriteByte('.')
		}

		b.WriteByte(quote)
		b.WriteString(segment)
		b.WriteByte(quote)
	}

	return b.String(), nil
}

// isIdentSegment reports whether s matches [A-Za-z_][A-Za-z0-9_]*.
//
// Quoting characters need no escaping downstream precisely because they
// cannot survive this check: a name containing the dialect's quote byte
// is rejected before it reaches a quoter.
func isIdentSegment(s string) bool {
	if s == "" {
		return false
	}

	for i := range len(s) {
		switch c := s[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		case c >= '0' && c <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}

	return true
}
