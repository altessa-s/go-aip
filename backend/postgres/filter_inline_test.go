// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// TestSchemaFilterTranslator_TranslateInline renders literals inline, safely quoted and cast, with the same predicate as the bound
// form, in the default and the contract mode; a JSON literal is spelled as PostgreSQL's JSONB text.
func TestSchemaFilterTranslator_TranslateInline(t *testing.T) {
	t.Parallel()

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"name": sqlbase.Scalar("name", sqlbase.Text),
		"doc":  sqlbase.Scalar("doc", sqlbase.JSON),
	})
	require.NoError(t, err)

	node := filtertest.MustParseFilter(t, `name == "o'k" && doc.contains('{"k": "a\\"b", "n": [1, 2.50, null, false]}')`)

	for _, contract := range []bool{false, true} {
		schema, err := postgres.NewSchema(fields, postgres.WithSchemaContractMode(contract))
		require.NoError(t, err)
		tr, err := postgres.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithAllowedFields("name", "doc")), schema)
		require.NoError(t, err)

		bound, args, err := tr.Translate(node)
		require.NoError(t, err)
		require.Len(t, args, 2)
		require.NotContains(t, bound, "o\\'k")

		inline, err := tr.TranslateInline(node)
		require.NoError(t, err, "contract=%v", contract)
		require.Contains(t, inline, `(E'o''k')::text`, "contract=%v", contract)
		require.Contains(t, inline, `{"k": "a\\"b", "n": [1, 2.50, null, false]}`, "contract=%v", contract)
		require.NoError(t, tr.ValidateAST(node))
	}
}
