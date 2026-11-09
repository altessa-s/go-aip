// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
)

// ErrInvalidTableName reports a table name that is not a plain SQL identifier, optionally qualified by one schema name,
// or whose parts exceed [PortableMaxIdentLen].
var ErrInvalidTableName = errors.New("sqlbase: invalid table name")

// PortableMaxIdentLen is the shorter of PostgreSQL's (63) and MariaDB's (64) identifier limits, in bytes. Table, index
// and listing column names stay within it in every dialect: PostgreSQL silently truncates a longer name, so two distinct
// configured names could address the same table or index, and MariaDB rejects it.
const PortableMaxIdentLen = PostgresMaxIdentLen

// Style is the identifier quoting and placeholder form of a SQL dialect. A dialect constructs its own value, for
// example Style{Quote: '"', Numbered: true} for PostgreSQL and Style{Quote: '`'} for MariaDB and ClickHouse; the zero
// value quotes with a NUL byte and is not useful.
type Style struct {
	// Quote is the identifier quote character.
	Quote byte
	// Numbered selects $1, $2 … placeholders instead of ?.
	Numbered bool
}

// QuoteIdent quotes any identifier, doubling the quote character inside it.
func (s Style) QuoteIdent(name string) string {
	q := string(s.Quote)
	return q + strings.ReplaceAll(name, q, q+q) + q
}

// Table validates and quotes a possibly schema-qualified table name: a plain identifier ([A-Za-z_][A-Za-z0-9_]*),
// optionally qualified by one schema name. Table and column names are the only SQL fragments that cannot be bound as
// parameters, so they are restricted to this shape and then quoted. Each part must fit [PortableMaxIdentLen]; a name of
// any other shape is rejected with [ErrInvalidTableName].
//
// It sits on the per-request path of the SQL translators (one call per selected or sorted column), so it validates by
// hand and allocates only the result.
func (s Style) Table(name string) (string, error) {
	schema, table, qualified := strings.Cut(name, ".")
	if !isIdentSegment(schema) || (qualified && !isIdentSegment(table)) {
		return "", fmt.Errorf("%w: %q", ErrInvalidTableName, name)
	}

	for _, p := range [...]string{schema, table} {
		if len(p) > PortableMaxIdentLen {
			return "", fmt.Errorf("%w: %q exceeds %d characters", ErrInvalidTableName, p, PortableMaxIdentLen)
		}
	}

	var b strings.Builder
	b.Grow(len(name) + 4) //nolint:mnd // two quotes per part, at most two parts
	b.WriteByte(s.Quote)
	b.WriteString(schema)
	b.WriteByte(s.Quote)

	if qualified {
		b.WriteByte('.')
		b.WriteByte(s.Quote)
		b.WriteString(table)
		b.WriteByte(s.Quote)
	}

	return b.String(), nil
}

// IndexName derives a quoted index name from an unqualified table name, so storages over different tables never collide
// on index names. A name that would exceed [PortableMaxIdentLen] keeps a readable prefix and ends in an FNV-32a hash of
// the full table name, so truncation cannot make two tables' indexes collide. If the suffix itself exceeds the available
// space, the hash covers the full derived name and the readable prefix includes the table and suffix.
func (s Style) IndexName(table, suffix string) string {
	name := table + "_" + suffix + "_idx"
	if len(name) > PortableMaxIdentLen {
		h := fnv.New32a()
		_, _ = h.Write([]byte(table))

		tail := fmt.Sprintf("_%08x_%s_idx", h.Sum32(), suffix)
		if len(tail) > PortableMaxIdentLen {
			// Preserve existing index names for ordinary suffixes. When the suffix itself is too long, hash the full
			// name before truncating.
			h.Reset()
			_, _ = h.Write([]byte(name))
			tail = fmt.Sprintf("_%08x_idx", h.Sum32())
			name = name[:PortableMaxIdentLen-len(tail)] + tail
		} else {
			name = table[:PortableMaxIdentLen-len(tail)] + tail
		}
	}

	return string(s.Quote) + name + string(s.Quote)
}

// Bind rewrites the ? placeholders of a statement into the style's form, numbering from start; a positional style
// returns the query unchanged. Only statements authored by the storages pass through Bind, and they never contain a ?
// inside a literal.
func (s Style) Bind(query string, start int) string {
	if !s.Numbered {
		return query
	}

	var b strings.Builder
	b.Grow(len(query) + 16) //nolint:mnd // room for multi-digit placeholders

	n := start

	for i := range len(query) {
		if query[i] == '?' {
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			n++

			continue
		}

		b.WriteByte(query[i])
	}

	return b.String()
}

// IsPortableIdent reports whether name is a plain identifier ([A-Za-z_][A-Za-z0-9_]*) of at most [PortableMaxIdentLen]
// bytes, the unquoted identifier grammar of the listing engine's built-in dialects ([ListSyntax.ValidIdentifier]).
func IsPortableIdent(name string) bool {
	return len(name) <= PortableMaxIdentLen && isIdentSegment(name)
}

// Unqualified returns the table part of a possibly schema-qualified name.
func Unqualified(name string) string {
	if _, table, ok := strings.CutLast(name, "."); ok {
		return table
	}

	return name
}

// Qualifier returns the schema part of a schema-qualified name, or "" for an unqualified one.
func Qualifier(name string) string {
	if schema, _, ok := strings.CutLast(name, "."); ok {
		return schema
	}

	return ""
}
