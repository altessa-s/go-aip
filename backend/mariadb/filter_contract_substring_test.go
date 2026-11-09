// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// TestSchemaSubstring pins substring() in the contract translator: SUBSTRING behind a CHAR_LENGTH guard, NULL where the
// evaluator finds the substring unknown (a NULL presence column, a range beyond the end), in every operand position and
// under negation.
func TestSchemaSubstring(t *testing.T) {
	t.Parallel()

	const (
		tail  = "(CASE WHEN CHAR_LENGTH(`t`.`name`) >= 1 THEN SUBSTRING(`t`.`name`, 2) END)"
		first = "(CASE WHEN CHAR_LENGTH(`t`.`name`) >= 1 THEN SUBSTRING(`t`.`name`, 1, 1) END)"
	)

	tr := schemaTranslator(t, nil)
	for _, tc := range []struct {
		expr, sql string
		args      []any
	}{
		{`name.substring(0, 2) == "ab"`, "((CASE WHEN CHAR_LENGTH(`t`.`name`) >= 2 THEN SUBSTRING(`t`.`name`, 1, 2) END) = ?)", []any{"ab"}},
		{`!(name.substring(1) == "a")`, "(" + tail + " <> ?)", []any{"a"}},
		{`note.substring(1) < "b"`, "((CASE WHEN CHAR_LENGTH(`note`) >= 1 THEN SUBSTRING(`note`, 2) END) < ?)", []any{"b"}},
		{`note.substring(0) != name`, "(SUBSTRING(`note`, 1) <> `t`.`name`)", nil},
		{`name.substring(1) != note`, "(" + tail + " <> `note` OR (`note` IS NULL AND " + tail + " IS NOT NULL))", nil},
		{`name.substring(1, 3) in ["a", null]`, "((CASE WHEN CHAR_LENGTH(`t`.`name`) >= 3 THEN SUBSTRING(`t`.`name`, 2, 2) END) IN (?))", []any{"a"}},
		{`!(name.substring(1) in ["a", "b"])`, "(" + tail + " NOT IN (?, ?))", []any{"a", "b"}},
		{`name.substring(1) in []`, "FALSE", nil},
		{`!(name.substring(1) in [])`, "((" + tail + ") IS NOT NULL)", nil},
		{`!(name.substring(1) in [null])`, "((" + tail + ") IS NOT NULL)", nil},
		{`name.substring(1) == null`, "FALSE", nil},
		{`!(name.substring(1) == null)`, "((" + tail + ") IS NOT NULL)", nil},
		{`(name.substring(0, 1) == "a") == false`, "((CASE WHEN (" + first + " = ?) THEN TRUE WHEN (" + first + " <> ?) THEN FALSE END) = ?)", []any{"a", "a", false}},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			sql, args, err := tr.Translate(filtertest.MustParseFilter(t, tc.expr))
			require.NoError(t, err)
			require.Equal(t, tc.sql, sql)
			require.Equal(t, tc.args, args)
		})
	}
}

// TestSchemaSubstringRejected rejects substring() outside a comparison operand, of anything but a text column, and with
// bounds that are not int literals in order.
func TestSchemaSubstringRejected(t *testing.T) {
	t.Parallel()

	tr := schemaTranslator(t, nil)
	for _, tc := range []struct {
		expr string
		want error
	}{
		{`tags.substring(1) == "a"`, filter.ErrUnsupportedOperation},
		{`jp.substring(1) == "a"`, filter.ErrUnsupportedOperation},
		{`n.substring(1) == "a"`, filter.ErrFieldTypeMismatch},
		{`"ab".substring(1) == "b"`, filter.ErrUnsupportedOperation},
		{`name.substring(0, n) == "a"`, filter.ErrUnsupportedOperation},
		{`name.substring(2, 1) == "a"`, filter.ErrInvalidExpression},
		{`name.substring(-1) == "a"`, filter.ErrInvalidExpression},
		{`name.substring(0, 1.5) == "a"`, filter.ErrInvalidExpression},
		{`name.substring(0, 1, 2) == "a"`, filter.ErrInvalidExpression},
		{`name.substring(0) == 1`, filter.ErrFieldTypeMismatch},
		{`size(name.substring(1)) == 1`, filter.ErrUnsupportedOperation},
		{`name.substring(1).contains("a")`, filter.ErrUnsupportedOperation},
		{`name.contains(name.substring(1))`, filter.ErrUnsupportedOperation},
		{`name.substring(1) in tags`, filter.ErrUnsupportedOperation},
		{`tags == name.substring(1)`, filter.ErrUnsupportedOperation},
		{`name in [name.substring(1)]`, filter.ErrUnsupportedOperation},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			_, _, err := tr.Translate(filtertest.MustParseFilter(t, tc.expr))
			require.ErrorIs(t, err, tc.want)
		})
	}

	portable := schemaTranslator(t, []filter.PolicyOption{filter.WithPortableContract(true),
		filter.WithFieldTypes(map[string]filter.FieldKind{"name": filter.FieldKindString})})
	_, _, err := portable.Translate(filtertest.MustParseFilter(t, `name.substring(1) == "a"`))
	require.ErrorIs(t, err, filter.ErrUnsupportedOperation)
}
