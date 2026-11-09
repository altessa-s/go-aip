// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
)

// orderDialectCase is one built-in dialect with its quoting and identifier limit.
type orderDialectCase struct {
	name     string
	dialect  sqlbase.OrderDialect
	quote    string
	maxIdent int
	// maxParts is the largest number of dot-separated parts; zero means any.
	maxParts int
	// qualifies reports that the dialect reads a dot as a table qualifier.
	qualifies bool
}

func orderDialects() []orderDialectCase {
	return []orderDialectCase{
		{name: "postgres", dialect: postgres.OrderDialect(), quote: `"`, maxIdent: 63, maxParts: 2, qualifies: true},
		{name: "mariadb", dialect: mariadb.OrderDialect(), quote: "`", maxIdent: 64, maxParts: 2, qualifies: true},
		{name: "clickhouse", dialect: clickhouse.OrderDialect(), quote: "`", maxIdent: 255, qualifies: true},
	}
}

// q quotes the dot-separated parts of name the way the dialect does.
func (d orderDialectCase) q(name string) string {
	parts := strings.Split(name, ".")
	for i, p := range parts {
		parts[i] = d.quote + p + d.quote
	}

	return strings.Join(parts, ".")
}

func mustOrderTranslator(tb testing.TB, policy *orderby.Policy, d sqlbase.OrderDialect, opts ...orderby.TranslatorOption) *sqlbase.OrderTranslator {
	tb.Helper()

	tr, err := sqlbase.NewOrderTranslator(policy, d, sqlbase.WithOrderOptions(opts...))
	require.NoError(tb, err)

	return tr
}

func TestTranslator_Identifiers(t *testing.T) {
	t.Parallel()

	for _, d := range orderDialects() {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()

			mapping := orderby.WithFieldMapping(map[string]string{
				"createdAt": "created_at",
				"city":      "addr.city",
				"expr":      "lower(name)",
				"json":      "data->>'x'",
				"mapKey":    "attrs['x']",
				"deep":      "a.b.c",
				"quoted":    d.q("x"),
				"boundary":  strings.Repeat("c", d.maxIdent),
				"long":      strings.Repeat("c", d.maxIdent+1),
			})

			deep := d.q("a.b.c") + " ASC"
			if d.maxParts == 2 {
				deep = ""
			}

			for _, tc := range []struct {
				name, in, want string
			}{
				{name: "multi", in: "create_time desc, slug", want: d.q("create_time") + " DESC, " + d.q("slug") + " ASC"},
				{name: "mapped", in: "createdAt desc", want: d.q("created_at") + " DESC"},
				{name: "qualified", in: "city", want: d.q("addr.city") + " ASC"},
				{name: "case kept", in: "updatedAt", want: d.q("updatedAt") + " ASC"},
				{name: "identifier boundary", in: "boundary", want: d.q(strings.Repeat("c", d.maxIdent)) + " ASC"},
				{name: "three parts", in: "deep", want: deep},
				{name: "expression", in: "expr"},
				{name: "json path", in: "json"},
				{name: "map key", in: "mapKey"},
				{name: "quote", in: "quoted"},
				{name: "too long", in: "long"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()

					got, err := mustOrderTranslator(t, orderbytest.MustPolicy(t), d.dialect, mapping).Translate(orderbytest.MustParseOrderBy(t, tc.in))
					if tc.want == "" {
						require.ErrorIs(t, err, orderby.ErrInvalidFieldPath)
						require.Empty(t, got)

						return
					}

					require.NoError(t, err)
					require.Equal(t, tc.want, got)
				})
			}
		})
	}
}

// TestOrderTranslator_DeclaredQualifiers pins the declared-field rule of PostgreSQL, MariaDB and ClickHouse: a dot may name a
// table qualifier there, so a dotted client key must be an exact allow-list or mapping entry; one only a wildcard, an open
// policy or a prefix rule admits fails with ErrFieldNotAllowed, which could otherwise reach a column of another joined table
// (NEW-3 for ClickHouse). A server default order and tie-breaker are configured, not composed.
func TestOrderTranslator_DeclaredQualifiers(t *testing.T) {
	t.Parallel()

	for _, d := range orderDialects() {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()

			wildcard := orderbytest.MustPolicy(t, orderby.WithUntrustedInput(true), orderby.WithAllowedFields("name", "address.city", "t.*", "labels.*"),
				orderby.WithDefaultOrder(orderby.Key{Name: "t.created"}))
			prefix := orderby.WithFieldPrefixMapping(map[string]string{"labels": "l"})
			exact := orderby.WithFieldMapping(map[string]string{"labels.team": "team"})

			for _, tc := range []struct {
				name   string
				policy *orderby.Policy
				opts   []orderby.TranslatorOption
				in     string
				want   string
				refuse bool
			}{
				{name: "declared", policy: wildcard, in: "address.city desc", want: d.q("address.city") + " DESC"},
				{name: "wildcard", policy: wildcard, in: "t.secret", want: d.q("t.secret") + " ASC", refuse: d.qualifies},
				{name: "open policy", policy: orderbytest.MustPolicy(t), in: "address.city", want: d.q("address.city") + " ASC", refuse: d.qualifies},
				{name: "prefix mapping", policy: wildcard, opts: []orderby.TranslatorOption{prefix}, in: "labels.env", want: d.q("l.env") + " ASC", refuse: d.qualifies},
				{name: "exact mapping", policy: wildcard, opts: []orderby.TranslatorOption{prefix, exact}, in: "labels.team", want: d.q("team") + " ASC"},
				{name: "second key", policy: wildcard, in: "name, t.secret desc", want: d.q("name") + " ASC, " + d.q("t.secret") + " DESC", refuse: d.qualifies},
				{name: "default order", policy: wildcard, want: d.q("t.created") + " ASC"},
				{
					name: "tiebreaker", policy: wildcard, opts: []orderby.TranslatorOption{orderby.WithTiebreaker(orderby.Key{Name: "t.id"})}, in: "name",
					want: d.q("name") + " ASC, " + d.q("t.id") + " ASC",
				},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()

					spec := orderby.Spec{}
					if tc.in != "" {
						spec = orderbytest.MustParseOrderBy(t, tc.in)
					}

					got, err := mustOrderTranslator(t, tc.policy, d.dialect, tc.opts...).Translate(spec)
					if tc.refuse {
						require.ErrorIs(t, err, orderby.ErrFieldNotAllowed)
						require.Empty(t, got)

						return
					}

					require.NoError(t, err)
					require.Equal(t, tc.want, got)
				})
			}
		})
	}
}

// TestTranslateOrder_ResolverQualifiers keeps an application resolver out of the declared-field rule unless it reports
// qualified names itself.
func TestTranslateOrder_ResolverQualifiers(t *testing.T) {
	t.Parallel()

	cfg := orderContext(t, orderbytest.MustPolicy(t, orderby.WithAllowedFields("t.*")))
	spec := orderbytest.MustParseOrderBy(t, "t.b")

	got, err := sqlbase.TranslateOrder(cfg, plainResolver{}, spec)
	require.NoError(t, err)
	require.Equal(t, "t.b ASC", got)

	_, err = sqlbase.TranslateOrder(cfg, qualifyingResolver{}, spec)
	require.ErrorIs(t, err, orderby.ErrFieldNotAllowed)
}

// qualifyingResolver is an application resolver that reads a dot as a table qualifier.
type qualifyingResolver struct{ plainResolver }

func (qualifyingResolver) QualifiesNames() bool { return true }

func TestTranslator_EmptyAndAllowList(t *testing.T) {
	t.Parallel()

	for _, d := range orderDialects() {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()

			got, err := mustOrderTranslator(t, orderbytest.MustPolicy(t), d.dialect).Translate(orderby.Spec{})
			require.NoError(t, err)
			require.Empty(t, got)

			tr := mustOrderTranslator(t, orderbytest.MustPolicy(t, orderby.WithAllowedFields("slug"), orderby.WithUntrustedInput(true)), d.dialect)
			_, err = tr.Translate(orderbytest.MustParseOrderBy(t, "slug, secret desc"))
			require.ErrorIs(t, err, orderby.ErrFieldNotAllowed)
			got, err = tr.Translate(orderbytest.MustParseOrderBy(t, "slug desc"))
			require.NoError(t, err)
			require.Equal(t, d.q("slug")+" DESC", got)

			_, err = sqlbase.NewOrderTranslator(nil, d.dialect)
			require.ErrorIs(t, err, orderby.ErrPolicyRequired)
		})
	}

	_, err := sqlbase.NewOrderTranslator(orderbytest.MustPolicy(t), sqlbase.OrderDialect{})
	require.ErrorIs(t, err, orderby.ErrInvalidFieldPath)
}

func TestTranslator_Attributes(t *testing.T) {
	t.Parallel()

	key := func(dir orderby.Direction, nulls orderby.NullOrder, collation string) orderby.Spec {
		return orderby.Spec{Keys: []orderby.Key{{Name: "name", Direction: dir, Nulls: nulls, Collation: collation}}}
	}
	asc, desc := orderby.DirectionAscending, orderby.DirectionDescending

	for _, tc := range []struct {
		name    string
		dialect sqlbase.OrderDialect
		spec    orderby.Spec
		want    string
	}{
		{name: "postgres nulls first", dialect: postgres.OrderDialect(), spec: key(asc, orderby.NullsFirst, ""), want: `"name" ASC NULLS FIRST`},
		{name: "postgres nulls last", dialect: postgres.OrderDialect(), spec: key(desc, orderby.NullsLast, ""), want: `"name" DESC NULLS LAST`},
		{name: "postgres collation", dialect: postgres.OrderDialect(), spec: key(desc, orderby.NullsDefault, "C"), want: `"name" COLLATE "C" DESC`},
		{
			name: "postgres qualified collation", dialect: postgres.OrderDialect(), spec: key(asc, orderby.NullsLast, "pg_catalog.default"),
			want: `"name" COLLATE "pg_catalog"."default" ASC NULLS LAST`,
		},
		{name: "postgres collation with quote", dialect: postgres.OrderDialect(), spec: key(asc, orderby.NullsDefault, `C" DESC`)},
		{name: "postgres collation expression", dialect: postgres.OrderDialect(), spec: key(asc, orderby.NullsDefault, "en-US")},
		{name: "postgres collation three parts", dialect: postgres.OrderDialect(), spec: key(asc, orderby.NullsDefault, "a.b.c")},
		{name: "postgres unknown nulls", dialect: postgres.OrderDialect(), spec: key(asc, orderby.NullsLast+1, "")},
		{name: "mariadb asc nulls first native", dialect: mariadb.OrderDialect(), spec: key(asc, orderby.NullsFirst, ""), want: "`name` ASC"},
		{name: "mariadb desc nulls last native", dialect: mariadb.OrderDialect(), spec: key(desc, orderby.NullsLast, ""), want: "`name` DESC"},
		{
			name: "mariadb asc nulls last emulated", dialect: mariadb.OrderDialect(), spec: key(asc, orderby.NullsLast, ""),
			want: "`name` IS NULL ASC, `name` ASC",
		},
		{
			name: "mariadb desc nulls first emulated", dialect: mariadb.OrderDialect(), spec: key(desc, orderby.NullsFirst, ""),
			want: "`name` IS NULL DESC, `name` DESC",
		},
		{name: "mariadb collation", dialect: mariadb.OrderDialect(), spec: key(asc, orderby.NullsDefault, "utf8mb4_bin"), want: "`name` COLLATE utf8mb4_bin ASC"},
		{
			name: "mariadb collation and emulated nulls", dialect: mariadb.OrderDialect(), spec: key(desc, orderby.NullsFirst, "utf8mb4_uca1400_ai_ci"),
			want: "`name` IS NULL DESC, `name` COLLATE utf8mb4_uca1400_ai_ci DESC",
		},
		{name: "mariadb collation with quote", dialect: mariadb.OrderDialect(), spec: key(asc, orderby.NullsDefault, "utf8mb4_bin' --")},
		{name: "mariadb collation with dash", dialect: mariadb.OrderDialect(), spec: key(asc, orderby.NullsDefault, "utf8-bin")},
		{name: "mariadb collation leading digit", dialect: mariadb.OrderDialect(), spec: key(asc, orderby.NullsDefault, "8bin")},
		{name: "mariadb collation too long", dialect: mariadb.OrderDialect(), spec: key(asc, orderby.NullsDefault, strings.Repeat("a", 65))},
		{name: "mariadb collation quoted identifier", dialect: mariadb.OrderDialect(), spec: key(asc, orderby.NullsDefault, "`utf8mb4_bin`")},
		{name: "mariadb unknown nulls", dialect: mariadb.OrderDialect(), spec: key(asc, orderby.NullsLast+1, "")},
		{name: "clickhouse nulls first", dialect: clickhouse.OrderDialect(), spec: key(desc, orderby.NullsFirst, ""), want: "`name` DESC NULLS FIRST"},
		{name: "clickhouse nulls last", dialect: clickhouse.OrderDialect(), spec: key(asc, orderby.NullsLast, ""), want: "`name` ASC NULLS LAST"},
		{name: "clickhouse collation", dialect: clickhouse.OrderDialect(), spec: key(asc, orderby.NullsDefault, "tr"), want: "`name` ASC COLLATE 'tr'"},
		{
			name: "clickhouse nulls and collation", dialect: clickhouse.OrderDialect(), spec: key(desc, orderby.NullsLast, "en_US"),
			want: "`name` DESC NULLS LAST COLLATE 'en_US'",
		},
		{
			name: "clickhouse bcp47 locale", dialect: clickhouse.OrderDialect(), spec: key(asc, orderby.NullsDefault, "de-u-co-phonebk"),
			want: "`name` ASC COLLATE 'de-u-co-phonebk'",
		},
		{name: "clickhouse locale with quote", dialect: clickhouse.OrderDialect(), spec: key(asc, orderby.NullsDefault, "en' DESC --")},
		{name: "clickhouse locale with backslash", dialect: clickhouse.OrderDialect(), spec: key(asc, orderby.NullsDefault, `en\`)},
		{name: "clickhouse locale with dot", dialect: clickhouse.OrderDialect(), spec: key(asc, orderby.NullsDefault, "en.UTF-8")},
		{name: "clickhouse locale leading digit", dialect: clickhouse.OrderDialect(), spec: key(asc, orderby.NullsDefault, "1en")},
		{name: "clickhouse locale leading dash", dialect: clickhouse.OrderDialect(), spec: key(asc, orderby.NullsDefault, "-en")},
		{name: "clickhouse locale too long", dialect: clickhouse.OrderDialect(), spec: key(asc, orderby.NullsDefault, strings.Repeat("a", 65))},
		{name: "clickhouse unknown nulls", dialect: clickhouse.OrderDialect(), spec: key(asc, orderby.NullsLast+1, "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := mustOrderTranslator(t, orderbytest.MustPolicy(t), tc.dialect).Translate(tc.spec)
			if tc.want == "" {
				require.ErrorIs(t, err, orderby.ErrUnsupportedOrdering)
				require.Empty(t, got)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}

	got, err := mustOrderTranslator(t, orderbytest.MustPolicy(t), clickhouse.OrderDialect()).Translate(key(asc, orderby.NullsDefault, strings.Repeat("a", 64)))
	require.NoError(t, err)
	require.Equal(t, "`name` ASC COLLATE '"+strings.Repeat("a", 64)+"'", got)
}

// expressionKeys renders a trusted expression and delegates key attributes
// to the PostgreSQL dialect.
type expressionKeys struct{ sqlbase.OrderDialect }

func (expressionKeys) Table(name string) (string, error) {
	if name == "tier" {
		return "metadata->>'tier'", nil
	}

	return "", orderby.ErrInvalidFieldPath
}

func TestDialect_DelegatedKeyRenderer(t *testing.T) {
	t.Parallel()

	spec := orderby.Spec{Keys: []orderby.Key{{Name: "tier", Direction: orderby.DirectionDescending, Nulls: orderby.NullsLast}}}
	got, err := sqlbase.TranslateOrder(orderContext(t, orderbytest.MustPolicy(t)), expressionKeys{postgres.OrderDialect()}, spec)
	require.NoError(t, err)
	require.Equal(t, "metadata->>'tier' DESC NULLS LAST", got)
}
