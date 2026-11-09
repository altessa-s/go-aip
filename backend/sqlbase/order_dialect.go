// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"strings"

	"github.com/altessa-s/go-aip/orderby"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// CollationStyle is how an [OrderDialect] writes a COLLATE modifier.
type CollationStyle uint8

const (
	// CollationUnsupported rejects every collation.
	CollationUnsupported CollationStyle = iota
	// CollationIdentifier writes `col COLLATE "name" DIR`: the collation is a (possibly schema-qualified) identifier
	// quoted under the dialect's [Identifiers], before the direction (PostgreSQL).
	CollationIdentifier
	// CollationLiteral writes `col DIR [NULLS …] COLLATE 'locale'`: the collation is a string literal after the null
	// placement, so it must be a plain locale identifier (letters, digits, '_' and '-', starting with a letter, at most
	// 64 bytes) (ClickHouse).
	CollationLiteral
	// CollationBare writes `col COLLATE name DIR`: the collation is an unquoted collation name (letters, digits and
	// '_', starting with a letter, at most 64 bytes) (MariaDB/MySQL).
	CollationBare
)

// NullPlacement is how an [OrderDialect] expresses an explicit null placement.
type NullPlacement uint8

const (
	// NullsUnsupported rejects every explicit null placement.
	NullsUnsupported NullPlacement = iota
	// NullsClause writes NULLS FIRST / NULLS LAST after the direction.
	NullsClause
	// NullsSmallestEmulated orders null before every value natively (first ascending, last descending): a placement
	// equal to that native order renders as a plain key, and the opposite one adds a leading `col IS NULL` key
	// (`col IS NULL ASC, col ASC` for asc nulls last, `col IS NULL DESC, col DESC` for desc nulls first) (MariaDB/MySQL).
	NullsSmallestEmulated
)

// maxLocaleLen bounds a collation locale; ICU locale identifiers are far shorter.
const maxLocaleLen = 64

// OrderDialect describes how one SQL backend renders an ORDER BY body: identifier quoting and limits, and whether null
// placement and collation can be expressed. The built-in dialects are postgres.OrderDialect, mariadb.OrderDialect and
// clickhouse.OrderDialect of the backend packages; another database describes itself with the same fields. The zero
// value rejects every key. An OrderDialect is a [ColumnResolver] and a [KeyRenderer], so a custom resolver can
// delegate to it.
type OrderDialect struct {
	// Identifiers validates and quotes column references, and a CollationIdentifier collation.
	Identifiers Identifiers
	// Name labels the translation metrics of the dialect, as in orderby_<name>_translate_duration_seconds; empty
	// means "sql".
	Name string
	// Collation is how a COLLATE modifier is written.
	Collation CollationStyle
	// Nulls is how an explicit null placement is expressed.
	Nulls NullPlacement
	// Qualified reports that a dot in a column name may separate a table qualifier, so a dotted client key must be
	// declared ([QualifiedNamesDialect]).
	Qualified bool
}

// Table validates name as a plain column or qualified column reference of the
// dialect and quotes each part on its own.
func (d OrderDialect) Table(name string) (string, error) {
	return d.Identifiers.Column(name)
}

// QualifiesNames reports whether a dot in a column name may separate a table qualifier, as in PostgreSQL, MariaDB and
// ClickHouse (where it may also address a Nested or JSON subcolumn): then [TranslateOrder] refuses a dotted client key the
// policy does not declare ([orderby.TranslatorContext.DeclaresField]).
func (d OrderDialect) QualifiesNames() bool { return d.Qualified }

// RenderKey renders a complete key on an already resolved column: collation,
// direction and null placement. An attribute the dialect cannot express, or a
// collation name it cannot accept, fails with [orderby.ErrUnsupportedOrdering].
func (d OrderDialect) RenderKey(column string, key orderby.Key) (string, error) {
	prefix, nulls, err := d.nullsParts(column, key)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.Grow(len(prefix) + len(column) + len(key.Collation) + keySizeHint)
	b.WriteString(prefix)
	b.WriteString(column)

	if key.Collation != "" {
		switch d.Collation {
		case CollationIdentifier:
			name, err := d.Identifiers.Column(key.Collation)
			if err != nil {
				return "", coreerrs.Wrapf(orderby.ErrUnsupportedOrdering, "collation: %w", err)
			}

			b.WriteString(" COLLATE ")
			b.WriteString(name)
		case CollationBare:
			if !isCollationName(key.Collation) {
				return "", coreerrs.Wrapf(orderby.ErrUnsupportedOrdering, "collation %q is not a collation name", key.Collation)
			}

			b.WriteString(" COLLATE ")
			b.WriteString(key.Collation)
		case CollationLiteral:
			if !isLocale(key.Collation) {
				return "", coreerrs.Wrapf(orderby.ErrUnsupportedOrdering, "collation %q is not a locale", key.Collation)
			}
		default: // CollationUnsupported, or a style this version does not know
			return "", coreerrs.Wrap(orderby.ErrUnsupportedOrdering, "collation")
		}
	}

	if key.IsDescending() {
		b.WriteString(" DESC")
	} else {
		b.WriteString(" ASC")
	}

	b.WriteString(nulls)

	if key.Collation != "" && d.Collation == CollationLiteral {
		b.WriteString(" COLLATE '")
		b.WriteString(key.Collation)
		b.WriteByte('\'')
	}

	return b.String(), nil
}

// nullsParts returns what expresses the null placement of key: a leading
// emulation key, or a suffix after the direction.
func (d OrderDialect) nullsParts(column string, key orderby.Key) (prefix, suffix string, err error) {
	n := key.Nulls

	switch {
	case n == orderby.NullsDefault:
		return "", "", nil
	case n > orderby.NullsLast:
		return "", "", coreerrs.Wrap(orderby.ErrUnsupportedOrdering, "null placement")
	}

	switch d.Nulls {
	case NullsClause:
		if n == orderby.NullsFirst {
			return "", " NULLS FIRST", nil
		}

		return "", " NULLS LAST", nil
	case NullsSmallestEmulated:
		if n == orderby.NullsSmallest(key) {
			return "", "", nil
		}
		// `col IS NULL` is 0 for a value and 1 for null: ascending puts nulls last, descending first.
		if n == orderby.NullsLast {
			return column + " IS NULL ASC, ", "", nil
		}

		return column + " IS NULL DESC, ", "", nil
	default:
		return "", "", coreerrs.Wrap(orderby.ErrUnsupportedOrdering, "null placement")
	}
}

// isCollationName reports whether s is a plain MariaDB/MySQL collation name
// such as "utf8mb4_bin" or "utf8mb4_uca1400_ai_ci", safe to write unquoted.
func isCollationName(s string) bool {
	if s == "" || len(s) > maxLocaleLen {
		return false
	}

	for i := range len(s) {
		switch c := s[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case (c >= '0' && c <= '9') || c == '_':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}

	return true
}

// isLocale reports whether s is a plain locale identifier such as "en",
// "en_US" or "de-u-co-phonebk", safe to write inside a SQL string literal.
func isLocale(s string) bool {
	if s == "" || len(s) > maxLocaleLen {
		return false
	}

	for i := range len(s) {
		switch c := s[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case (c >= '0' && c <= '9') || c == '_' || c == '-':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}

	return true
}
