// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package crossbackend_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/lua"
	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/redis"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
	filterfuncs "github.com/altessa-s/go-aip/filter/functions"
	filterparser "github.com/altessa-s/go-aip/filter/parser"
	regexanchor "github.com/altessa-s/go-aip/filter/regexanchor"
)

// TestUntrustedInput_RequiresAllowlist guards against regressing the
// CEL-from-untrusted-source vulnerability: a translator or evaluator
// marked with [filter.WithUntrustedInput] must refuse construction
// unless an explicit allow-list is also configured. Without that, a
// hostile client could filter on any field the storage layer indexes
// (e.g. `passwordHash > ""` to enumerate accounts), which is exactly
// what this knob exists to prevent.
//
// The check happens in [filter.NewPolicy], and every translator and
// evaluator constructor requires a policy, so misconfiguration surfaces
// at process start rather than on the first untrusted query.
func TestUntrustedInput_RequiresAllowlist(t *testing.T) {
	const safeField, expr = "name", `name == "x"`

	t.Run("lua translator", func(t *testing.T) {
		t.Parallel()
		node := filtertest.MustParseFilter(t, expr)

		_, err := filter.NewPolicy(filter.WithUntrustedInput(true))
		require.ErrorIs(t, err, filter.ErrAllowlistRequired)
		_, err = lua.NewFilterTranslator(nil, lua.WithFilterTableVar("row"))
		require.ErrorIs(t, err, filter.ErrPolicyRequired)

		trans, err := lua.NewFilterTranslator(filtertest.MustPolicy(t, filter.WithUntrustedInput(true), filter.WithAllowedFields(safeField)), lua.WithFilterTableVar("row"))
		require.NoError(t, err)
		_, err = trans.Translate(node)
		require.NoError(t, err)
	})

	t.Run("redis translator", func(t *testing.T) {
		t.Parallel()
		node := filtertest.MustParseFilter(t, expr)
		schema := map[string]redis.FieldType{safeField: redis.FieldTypeText}

		_, err := redis.NewFilterTranslatorWithSchema(nil, redisSchema(schema))
		require.ErrorIs(t, err, filter.ErrPolicyRequired)

		trans, err := redis.NewFilterTranslatorWithSchema(filtertest.MustPolicy(t, filter.WithUntrustedInput(true), filter.WithAllowedFields(safeField)), redisSchema(schema))
		require.NoError(t, err)
		_, err = trans.Translate(node)
		require.NoError(t, err)
	})

	t.Run("evaluator", func(t *testing.T) {
		t.Parallel()
		node := filtertest.MustParseFilter(t, expr)

		_, err := filter.NewPolicy(filter.WithAnyField(true), filter.WithUntrustedInput(true))
		require.ErrorIs(t, err, filter.ErrAllowlistRequired)
		_, err = filtereval.New(nil)
		require.ErrorIs(t, err, filter.ErrPolicyRequired)

		eval, err := filtereval.New(filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithUntrustedInput(true), filter.WithAllowedFields(safeField)))
		require.NoError(t, err)
		ok, err := eval.Evaluate(node, map[string]any{safeField: "x"})
		require.NoError(t, err)
		require.True(t, ok)
	})
}

// TestAllowlistRequiredByDefault pins the secure default: a policy, without which no translator or evaluator is built, fails
// at construction without an allow-list unless the caller opts out with [filter.WithAnyField], so a forgotten
// [filter.WithUntrustedInput] cannot expose a hidden field (`passwordHash.startsWith("a")` as a boolean oracle) to a client
// filter.
func TestAllowlistRequiredByDefault(t *testing.T) {
	t.Parallel()

	_, err := lua.NewFilterTranslator(nil, lua.WithFilterTableVar("row"))
	require.ErrorIs(t, err, filter.ErrPolicyRequired)
	_, err = redis.NewFilterTranslatorWithSchema(nil, redisSchema(map[string]redis.FieldType{"name": redis.FieldTypeText}))
	require.ErrorIs(t, err, filter.ErrPolicyRequired)
	_, err = filtereval.New(nil)
	require.ErrorIs(t, err, filter.ErrPolicyRequired)
	_, err = filter.NewPolicy()
	require.ErrorIs(t, err, filter.ErrAllowlistRequired)

	// WithAnyField does not lift the allow-list requirement of untrusted input.
	_, err = filter.NewPolicy(filter.WithAnyField(true), filter.WithUntrustedInput(true))
	require.ErrorIs(t, err, filter.ErrAllowlistRequired)

	hidden := filtertest.MustParseFilter(t, `passwordHash.startsWith("a")`)

	eval, err := filtereval.New(filtertest.MustPolicy(t, filter.WithAllowedFields("name")))
	require.NoError(t, err)
	_, err = eval.Evaluate(hidden, map[string]any{"passwordHash": "abc"})
	require.ErrorIs(t, err, filter.ErrFieldNotAllowed)
}

// TestUntrustedInput_RejectsMatches pins S-6: a client regex runs on the database's backtracking engine (MongoDB PCRE,
// PostgreSQL ~, MariaDB REGEXP), where `(a|aa)+$` over a large collection pins the CPU. Under [filter.WithUntrustedInput]
// every translator and the evaluator reject matches() with [filter.ErrFunctionNotAllowed]; [filter.WithSafeRegex] admits a
// pattern whose repetitions apply to single characters only and rejects any other with [regexanchor.ErrInvalidRegex].
func TestUntrustedInput_RejectsMatches(t *testing.T) {
	t.Parallel()

	untrusted := []filter.PolicyOption{filter.WithUntrustedInput(true), filter.WithAllowedFields("name")}

	safe := append(slices.Clip(untrusted), filter.WithSafeRegex(true))
	for _, tc := range []struct {
		name string
		expr string
		opts []filter.PolicyOption
		want error
	}{
		{"untrusted literal", `name.matches("abc")`, untrusted, filter.ErrFunctionNotAllowed},
		{"untrusted catastrophic", `name.matches("(a|aa)+$")`, untrusted, filter.ErrFunctionNotAllowed},
		{"untrusted nested", `name.startsWith("a") || name.matches("^x")`, untrusted, filter.ErrFunctionNotAllowed},
		{"safe literal", `name.matches("abc")`, safe, nil},
		{"safe case-insensitive literal", `name.matches("(?i)a\\.b")`, safe, nil},
		{"safe anchored classes", `name.matches("^[a-z]+-[0-9]{2,4}.*x$")`, safe, nil},
		{"safe alternation of literals", `name.matches("^(foo|bar)$")`, safe, nil},
		{"safe rejects repeated alternation", `name.matches("(a|aa)+$")`, safe, regexanchor.ErrInvalidRegex},
		{"safe rejects nested quantifier", `name.matches("(a+)+b")`, safe, regexanchor.ErrInvalidRegex},
		{"safe rejects repeated group", `name.matches("(ab)*c")`, safe, regexanchor.ErrInvalidRegex},
		{"safe rejects bounded repeat of group", `name.matches("(a.*){5}")`, safe, regexanchor.ErrInvalidRegex},
		{"trusted holds the safe subset", `name.matches("(a|aa)+$")`, []filter.PolicyOption{filter.WithAllowedFields("name")}, regexanchor.ErrInvalidRegex},
		{"trusted unsafe opt-out", `name.matches("(a|aa)+$")`, []filter.PolicyOption{filter.WithAllowedFields("name"), filter.WithUnsafeRegex(true)}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			node := filtertest.MustParseFilter(t, tc.expr)
			for backend, err := range translateEverywhere(t, node, tc.opts) {
				if tc.want == nil {
					require.NoError(t, err, backend)
					continue
				}

				require.ErrorIs(t, err, tc.want, backend)
			}
		})
	}
}

// TestUntrustedInput_SafeRegexKeepsTextSearch pins that [functions.TextSearch], which expands into a quoted case-insensitive
// matches(), still works under untrusted input with [filter.WithSafeRegex].
func TestUntrustedInput_SafeRegexKeepsTextSearch(t *testing.T) {
	t.Parallel()

	parser, err := filterparser.New(filterparser.WithCustomFunctions(map[string]filter.CustomFunction{"search": filterfuncs.TextSearch([]string{"name"})}))
	require.NoError(t, err)
	node, err := parser.Parse(t.Context(), `search("a+(b)*")`)
	require.NoError(t, err)

	opts := []filter.PolicyOption{filter.WithUntrustedInput(true), filter.WithAllowedFields("name"), filter.WithSafeRegex(true)}
	for backend, err := range translateEverywhere(t, node, opts) {
		require.NoError(t, err, backend)
	}
}

// translateEverywhere translates node with every backend that runs matches() (the regex backends) and evaluates it with the default
// evaluator, reporting each error by backend name. The MongoDB translator and its evaluator profile, a separate module, run the same
// cases in backend/mongo.
func translateEverywhere(t *testing.T, node filter.Node, opts []filter.PolicyOption) map[string]error {
	t.Helper()

	out := map[string]error{}
	pg, err := postgres.NewFilterTranslator(filtertest.MustPolicy(t, opts...))
	require.NoError(t, err)

	_, _, out["postgres"] = pg.Translate(node)
	ma, err := mariadb.NewFilterTranslator(filtertest.MustPolicy(t, opts...))
	require.NoError(t, err)

	_, _, out["mariadb"] = ma.Translate(node)
	data := map[string]any{"name": "xyz"} // no short circuit: the default evaluator checks a pattern when it reaches it
	plain, err := filtereval.New(filtertest.MustPolicy(t, opts...))
	require.NoError(t, err)

	_, out["evaluator"] = plain.Evaluate(node, data)

	return out
}
