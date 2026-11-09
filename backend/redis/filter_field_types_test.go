// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// TestTranslator_WithFieldTypes is the cross-backend regression guard
// PR-45 review requested. See aip/backend/lua for the
// design rationale — the byte-identical CheckComparison call needs
// per-backend coverage so a future regression cannot delete it
// silently.
//
// RedisSearch already needs a schema (its own FieldType map for
// numeric/tag/text routing) — this test layers filter.WithFieldTypes
// on top to confirm the two schema layers don't conflict and the
// type-check still fires.
func TestFilterTranslator_WithFieldTypes(t *testing.T) {
	trans := mustTranslator(t, testSchema, filter.WithFieldTypes(map[string]filter.FieldKind{
		"status":    filter.FieldKindInt,
		"active":    filter.FieldKindBool,
		"name":      filter.FieldKindString,
		"priority":  filter.FieldKindFloat,
		"createdAt": filter.FieldKindTimestamp,
	}))

	t.Run("match", func(t *testing.T) {
		for _, expr := range []string{
			`status == 1`,
			`active == true`,
			`name == "Alice"`,
			`priority >= 100`,
			`status in [1, 2, 3]`,
			`role == "admin"`, // not in WithFieldTypes → skipped
		} {
			t.Run(expr, func(t *testing.T) {
				node := filtertest.MustParseFilter(t, expr)
				_, err := trans.Translate(node)
				require.NoError(t, err)
			})
		}
	})

	t.Run("mismatch", func(t *testing.T) {
		for _, expr := range []string{
			`status == "qwer"`,
			`active == 1`,
			`name == 42`,
			`status in [1, "qwer", 3]`,
			`"qwer" == status`,
		} {
			t.Run(expr, func(t *testing.T) {
				node := filtertest.MustParseFilter(t, expr)
				_, err := trans.Translate(node)
				require.ErrorIs(t, err, filter.ErrFieldTypeMismatch)
			})
		}
	})
}
