// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package crossbackend_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/lua"
	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/meili"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/redis"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
)

// TestTranslators_BareLiteralIsNotAPredicate is a cross-backend regression for
// a query-injection hole a fuzz target found in the SQL translator.
//
// Every translator decided "did this node produce a clause?" by type-asserting
// the walk's result to the backend's clause type. For the text backends that
// type is `string` — and a CEL expression that is just a string literal makes
// VisitLiteral return exactly that, a Go string. The assertion could not tell
// the two apart, so the literal was returned as the clause and emitted into the
// query verbatim: unquoted, unbound, and past the field allow-list.
//
// `parser.Parse(ctx, userInput)` followed by `translator.Translate(node)` is the
// documented shape for untrusted filters, so the payload below is a complete
// filter expression a client could send. It lives here rather than in each
// backend's package because the invariant is one statement about all of them,
// and a per-backend copy is what let the hole persist in six places at once.
func TestTranslators_BareLiteralIsNotAPredicate(t *testing.T) {
	t.Parallel()

	parser, err := filterparser.New(filterparser.WithNoCache(true))
	require.NoError(t, err)

	backends := translatorBackends()

	// Each of these parses cleanly into a single literal node — the parser has
	// no reason to object, which is what made the translator the only line of
	// defense.
	expressions := map[string]string{
		"sql injection payload": `"'; DROP TABLE users; --"`,
		"tautology":             `"1=1 OR 1=1"`,
		"bare quote":            `"'"`,
		"plain string":          `"plain"`,
		"empty string":          `""`,
		"integer":               `123`,
		"boolean":               `true`,
	}

	for backendName, translate := range backends {
		for exprName, expression := range expressions {
			t.Run(backendName+"/"+exprName, func(t *testing.T) {
				t.Parallel()

				node, parseErr := parser.Parse(t.Context(), expression)
				require.NoError(t, parseErr, "the parser accepts this; the translator is what must refuse it")

				require.ErrorIs(t, translate(t, node), filter.ErrInvalidExpression,
					"a bare literal was accepted as a filter clause")
			})
		}
	}
}

// TestTranslators_LiteralFieldIsRejected is a cross-backend regression for an
// allow-list bypass: the MongoDB, Meilisearch, RediSearch and Lua translators
// resolved a comparison's field side by visiting it and accepting any string,
// so a string literal stood in for a field name without passing the
// allow-list. With only `name` allowed, `"$where" == "return true"` became the
// MongoDB document {"$where": "return true"}, and a Meilisearch field side
// could carry its own `OR` clause. A field operand must be an identifier.
//
// The SQL translators are exempt: they bind both literals as parameters
// (`$1 = $2`), a constant comparison that names no column.
func TestTranslators_LiteralFieldIsRejected(t *testing.T) {
	t.Parallel()

	parser, err := filterparser.New(filterparser.WithNoCache(true))
	require.NoError(t, err)

	expressions := map[string]string{
		"disallowed field":    `"secret" == 1`,
		"mongo operator":      `"$where" == "return true"`,
		"meili clause":        `"name = 1 OR secret" == 1`,
		"redis selector":      `"name:x | @secret" == "y"`,
		"membership":          `"secret" in ["a"]`,
		"literal call target": `"secret".startsWith("a")`,
	}

	sqlBackends := map[string]bool{"postgres": true, "mariadb": true, "clickhouse": true}
	for backendName, translate := range translatorBackends() {
		if sqlBackends[backendName] {
			continue
		}

		for exprName, expression := range expressions {
			t.Run(backendName+"/"+exprName, func(t *testing.T) {
				t.Parallel()

				node, parseErr := parser.Parse(t.Context(), expression)
				require.NoError(t, parseErr)

				require.ErrorIs(t, translate(t, node), filter.ErrInvalidExpression,
					"a string literal was accepted as a field reference")
			})
		}
	}
}

// TestTranslators_CallWithoutTargetIsRejected is a regression for a remote
// crash a fuzz target found: `contains()` — a call the parser accepts, with no
// target — made the Meilisearch, Lua and RediSearch translators dereference a
// nil Node. A filter expression a client controls could therefore panic the
// process, which is a denial of service in any handler that does not recover.
//
// The SQL and MongoDB translators already reported it as a malformed
// expression; this pins that every backend does.
func TestTranslators_CallWithoutTargetIsRejected(t *testing.T) {
	t.Parallel()

	parser, err := filterparser.New(filterparser.WithNoCache(true))
	require.NoError(t, err)

	for backendName, translate := range translatorBackends() {
		for _, expression := range []string{"contains()", "startsWith()", "endsWith()", "size()"} {
			t.Run(backendName+"/"+expression, func(t *testing.T) {
				t.Parallel()

				node, parseErr := parser.Parse(t.Context(), expression)
				if parseErr != nil {
					t.Skipf("the parser rejects %q on its own: %v", expression, parseErr)
				}

				// The assertion is that this returns at all: a panic here is
				// the bug, and the error value only has to be non-nil.
				require.Error(t, translate(t, node),
					"%q must be rejected, not translated", expression)
			})
		}
	}
}

// TestTranslators_SizeCallFormsAgree pins that `size(name)` and `name.size()`
// get the same answer from every backend. The global form used to leave the
// operand in Args with a nil Target, which only the SQL translators unpacked:
// MongoDB and Lua rejected it, and the Evaluator panicked.
func TestTranslators_SizeCallFormsAgree(t *testing.T) {
	t.Parallel()

	parser, err := filterparser.New(filterparser.WithNoCache(true))
	require.NoError(t, err)

	method, err := parser.Parse(t.Context(), `name.size() > 3`)
	require.NoError(t, err)
	global, err := parser.Parse(t.Context(), `size(name) > 3`)
	require.NoError(t, err)

	for backendName, translate := range translatorBackends() {
		t.Run(backendName, func(t *testing.T) {
			t.Parallel()

			methodErr := translate(t, method)

			globalErr := translate(t, global)
			if methodErr == nil {
				require.NoError(t, globalErr)
				return
			}

			require.Error(t, globalErr)
			require.Equal(t, methodErr.Error(), globalErr.Error())
		})
	}
}

// translatorBackends is every filter backend, reduced to "translate this node,
// report the error". Sharing one table across the cross-backend regressions is
// what keeps a newly added backend from being covered by none of them. The
// MongoDB translator is a separate module (backend/mongo), whose
// filter_crossbackend_test.go runs the same regressions against it.
func translatorBackends() map[string]func(t *testing.T, node filter.Node) error {
	opts := []filter.PolicyOption{
		filter.WithUntrustedInput(true),
		filter.WithAllowedFields("name"),
	}

	return map[string]func(t *testing.T, node filter.Node) error{
		"postgres": func(t *testing.T, node filter.Node) error {
			tr, err := postgres.NewFilterTranslator(filtertest.MustPolicy(t, opts...))
			require.NoError(t, err)

			_, _, translateErr := tr.Translate(node)

			return translateErr
		},
		"mariadb": func(t *testing.T, node filter.Node) error {
			tr, err := mariadb.NewFilterTranslator(filtertest.MustPolicy(t, opts...))
			require.NoError(t, err)

			_, _, translateErr := tr.Translate(node)

			return translateErr
		},
		"clickhouse": func(t *testing.T, node filter.Node) error {
			tr, err := clickhouse.NewFilterTranslator(filtertest.MustPolicy(t, opts...))
			require.NoError(t, err)

			_, _, translateErr := tr.Translate(node)

			return translateErr
		},
		"meili": func(t *testing.T, node filter.Node) error {
			tr, err := meili.NewFilterTranslator(filtertest.MustPolicy(t, opts...))
			require.NoError(t, err)

			_, translateErr := tr.Translate(node)

			return translateErr
		},
		"lua": func(t *testing.T, node filter.Node) error {
			tr, err := lua.NewFilterTranslator(filtertest.MustPolicy(t, opts...), lua.WithFilterTableVar("doc"))
			require.NoError(t, err)

			_, translateErr := tr.Translate(node)

			return translateErr
		},
		"redis": func(t *testing.T, node filter.Node) error {
			tr, err := redis.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, opts...), redisSchema(map[string]redis.FieldType{"name": redis.FieldTypeText}))
			require.NoError(t, err)

			_, translateErr := tr.Translate(node)

			return translateErr
		},
	}
}
