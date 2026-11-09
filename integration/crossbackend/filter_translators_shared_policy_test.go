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

	filtereval "github.com/altessa-s/go-aip/filter/evaluator"
)

// TestSharedPolicy: one policy built per resource serves several translators and the evaluator alike. Each binds it to its
// own storage names; the API rules (allow-list, untrusted input) behave identically everywhere and binding one backend
// never changes what another sees.
func TestSharedPolicy(t *testing.T) {
	t.Parallel()

	policy := filtertest.MustPolicy(t, filter.WithUntrustedInput(true), filter.WithAllowedFields("name", "age"))

	mapped, err := postgres.NewFilterTranslator(policy, postgres.WithFilterOptions(filter.WithFieldMapping(map[string]string{"name": "user_name"})))
	require.NoError(t, err)
	plain, err := postgres.NewFilterTranslator(policy)
	require.NoError(t, err)
	maria, err := mariadb.NewFilterTranslator(policy)
	require.NoError(t, err)
	eval, err := filtereval.New(policy, filtereval.WithFilterOptions(filter.WithFieldMapping(map[string]string{"name": "n"})))
	require.NoError(t, err)

	allowed := filtertest.MustParseFilter(t, `name == "x" && age > 1`)

	where, args, err := mapped.Translate(allowed)
	require.NoError(t, err)
	require.Equal(t, `("user_name" = $1) AND ("age" > $2)`, where)
	require.Equal(t, []any{"x", int64(1)}, args)

	where, args, err = plain.Translate(allowed)
	require.NoError(t, err)
	require.Equal(t, `("name" = $1) AND ("age" > $2)`, where, "the mapping of another translator does not leak")
	require.Equal(t, []any{"x", int64(1)}, args)

	_, mariaArgs, err := maria.Translate(allowed)
	require.NoError(t, err)
	require.Equal(t, args, mariaArgs)

	matched, err := eval.Evaluate(allowed, map[string]any{"n": "x", "age": int64(2)})
	require.NoError(t, err)
	require.True(t, matched)

	for _, expr := range []string{`secret == "x"`, `user_name == "x"`, `n == "x"`} {
		denied := filtertest.MustParseFilter(t, expr)
		_, _, err = mapped.Translate(denied)
		require.ErrorIs(t, err, filter.ErrFieldNotAllowed, expr)
		_, _, err = plain.Translate(denied)
		require.ErrorIs(t, err, filter.ErrFieldNotAllowed, expr)
		_, _, err = maria.Translate(denied)
		require.ErrorIs(t, err, filter.ErrFieldNotAllowed, expr)
		require.ErrorIs(t, eval.Validate(denied), filter.ErrFieldNotAllowed, expr)
	}
}

// TestTranslatorsRequirePolicy: no translator is built without a policy, so none translates untrusted input without the
// allow-list its policy must carry.
func TestTranslatorsRequirePolicy(t *testing.T) {
	t.Parallel()

	for name, build := range map[string]func() error{
		"clickhouse": func() error { _, err := clickhouse.NewFilterTranslator(nil); return err },
		"lua":        func() error { _, err := lua.NewFilterTranslator(nil, lua.WithFilterTableVar("row")); return err },
		"mariadb":    func() error { _, err := mariadb.NewFilterTranslator(nil); return err },
		"meili":      func() error { _, err := meili.NewFilterTranslator(nil); return err },
		"postgres":   func() error { _, err := postgres.NewFilterTranslator(nil); return err },
		"redis":      func() error { _, err := redis.NewFilterTranslator(nil); return err },
		"evaluator":  func() error { _, err := filtereval.New(nil); return err },
	} {
		require.ErrorIs(t, build(), filter.ErrPolicyRequired, name)
	}
}
