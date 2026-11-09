// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
)

func TestValidateIdent(t *testing.T) {
	t.Parallel()

	t.Run("accepts", func(t *testing.T) {
		t.Parallel()

		for _, name := range []string{
			"name",
			"_name",
			"n4me",
			"created_at",
			"address.city",
			"user.profile.name",
			"A.B",
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				require.NoError(t, sqlbase.ValidateIdent(name))
			})
		}
	})

	// Most of these would have been neutralized by quoting alone. They
	// are refused anyway: the column position is the one part of a
	// generated clause no placeholder can cover, so it accepts plain
	// identifiers and nothing else.
	t.Run("rejects", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			label string
			name  string
		}{
			{"empty", ""},
			{"bare dot", "."},
			{"empty segment", "address..city"},
			{"leading dot", ".city"},
			{"trailing dot", "address."},
			{"leading digit", "1name"},
			{"whitespace", "na me"},
			{"call expression", "lower(name)"},
			{"subscript", "metadata['x']"},
			{"backtick break-out", "name` = '' OR 1 = 1"},
			{"double-quote break-out", `name" = '' OR 1 = 1`},
			{"comment", "name--"},
			{"non-ASCII", "naïve"},
		}

		for _, tt := range tests {
			t.Run(tt.label, func(t *testing.T) {
				t.Parallel()
				require.ErrorIs(t, sqlbase.ValidateIdent(tt.name), filter.ErrInvalidExpression)
			})
		}
	})
}

func TestQuoteWhole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		quote byte
		limit int
		want  string
	}{
		{"simple", "name", '`', 0, "`name`"},
		{"dots stay inside one identifier", "address.city", '`', 0, "`address.city`"},
		{"double quote", "name", '"', 0, `"name"`},
		{"at the limit", strings.Repeat("a", 255), '`', sqlbase.ClickHouseMaxIdentLen, "`" + strings.Repeat("a", 255) + "`"},
		{"any length without a limit", strings.Repeat("a", 300), '`', 0, "`" + strings.Repeat("a", 300) + "`"},
		{"any number of dots", "a.b.c.d", '`', 0, "`a.b.c.d`"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := sqlbase.QuoteWhole(tt.input, tt.quote, tt.limit)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}

	rejected := map[string]struct {
		input string
		limit int
	}{
		"invalid name":            {"lower(name)", 0},
		"one byte over the limit": {strings.Repeat("a", 256), sqlbase.ClickHouseMaxIdentLen},
		"dots count to the limit": {strings.Repeat("a", 30) + "." + strings.Repeat("b", 30), 60},
	}

	for name, tt := range rejected {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := sqlbase.QuoteWhole(tt.input, '`', tt.limit)
			require.ErrorIs(t, err, filter.ErrInvalidExpression)
			require.Empty(t, got)
		})
	}
}

func TestQuoteQualified(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		quote byte
		limit int
		want  string
	}{
		{"simple backtick", "name", '`', 0, "`name`"},
		{"simple double quote", "name", '"', 0, `"name"`},
		{"two segments", "address.city", '`', 0, "`address`.`city`"},
		{"every part at the limit", strings.Repeat("a", 63) + "." + strings.Repeat("b", 63), '"', sqlbase.PostgresMaxIdentLen,
			`"` + strings.Repeat("a", 63) + `"."` + strings.Repeat("b", 63) + `"`},
		{"MariaDB limit", strings.Repeat("a", 64), '`', sqlbase.MariaDBMaxIdentLen, "`" + strings.Repeat("a", 64) + "`"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := sqlbase.QuoteQualified(tt.input, tt.quote, tt.limit)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}

	// A PostgreSQL identifier past 63 bytes is truncated by the server, so `aaa...(63)_shadow` would read the column
	// `aaa...(63)`; a third part would name a schema-qualified column of another table.
	rejected := map[string]struct {
		input string
		limit int
	}{
		"invalid name":               {"address..city", 0},
		"truncated by PostgreSQL":    {strings.Repeat("a", 63) + "_shadow", sqlbase.PostgresMaxIdentLen},
		"table part over the limit":  {strings.Repeat("a", 64) + ".b", sqlbase.PostgresMaxIdentLen},
		"column part over the limit": {"a." + strings.Repeat("b", 65), sqlbase.MariaDBMaxIdentLen},
		"schema-qualified column":    {"user.profile.name", 0},
		"database-qualified column":  {"db.t.c", sqlbase.MariaDBMaxIdentLen},
		"four parts":                 {"a.b.c.d", sqlbase.PostgresMaxIdentLen},
	}

	for name, tt := range rejected {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := sqlbase.QuoteQualified(tt.input, '"', tt.limit)
			require.ErrorIs(t, err, filter.ErrInvalidExpression)
			require.Empty(t, got)
		})
	}
}
