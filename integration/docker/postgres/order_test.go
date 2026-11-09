// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package postgres_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/postgres/postgrestest"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/integration/docker/internal/dbtest"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
	"github.com/altessa-s/go-aip/projection"
	"github.com/altessa-s/go-aip/projection/projectiontest"
)

// TestOrderConformanceLive runs the order conformance suite with every ORDER BY executed by PostgreSQL over
// orderbytest.ConformanceRecords; each case expects the record ids in the order the server returns them (ties broken by id).
// PostgreSQL sorts NULL as the largest value: last ascending, first descending, unless the order names a placement.
func TestOrderConformanceLive(t *testing.T) {
	t.Parallel()

	db := dbtest.Postgres(t)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE ordered (id text PRIMARY KEY, name text COLLATE "C", age double precision,
		created_at bigint); CREATE TABLE address (rid text PRIMARY KEY, city text COLLATE "C")`)
	require.NoError(t, err)

	for _, r := range orderbytest.ConformanceRecords() {
		_, err := db.ExecContext(t.Context(), "INSERT INTO ordered VALUES ($1, $2, $3, $4)", r["id"], r["name"], r["age"], r["created_at"])
		require.NoError(t, err)
		_, err = db.ExecContext(t.Context(), "INSERT INTO address VALUES ($1, $2)", r["id"], r["address"].(map[string]any)["city"])
		require.NoError(t, err)
	}

	type O = orderbytest.Outcome

	orderbytest.RunConformance(t, orderbytest.Backend{
		New: func(policy *orderby.Policy, opts ...orderby.TranslatorOption) (orderbytest.TranslateFunc, error) {
			tr, err := postgres.NewOrderTranslator(policy, sqlbase.WithOrderOptions(opts...))
			if err != nil {
				return nil, err
			}

			return orderedBy(t, db, "SELECT o.id FROM ordered AS o JOIN address ON address.rid = o.id ORDER BY %s, o.id", tr.Translate), nil
		},
		Expect: map[string]O{
			orderbytest.CaseEmpty:             {},
			orderbytest.CaseAscending:         {Want: "2 3 1 4 5"},
			orderbytest.CaseDescending:        {Want: "5 4 1 3 2"},
			orderbytest.CaseMultipleKeys:      {Want: "2 4 1 5 3"},
			orderbytest.CaseNestedPath:        {Want: "4 2 5 3 1"},
			orderbytest.CaseFieldMapping:      {Want: "3 5 1 4 2"},
			orderbytest.CasePrefixMapping:     {Want: "4 2 5 3 1"},
			orderbytest.CaseDefaultOrder:      {Want: "2 4 1 5 3"},
			orderbytest.CaseTiebreaker:        {Want: "2 3 1 4 5"},
			orderbytest.CaseTiebreakerPresent: {Want: "5 4 3 2 1"},
			orderbytest.CaseAscNullsFirst:     {Want: "2 4 3 1 5"},
			orderbytest.CaseAscNullsLast:      {Want: "3 1 5 2 4"},
			orderbytest.CaseDescNullsFirst:    {Want: "2 4 1 5 3"},
			orderbytest.CaseDescNullsLast:     {Want: "1 5 3 2 4"},
			orderbytest.CaseCollation:         {Want: "2 3 1 4 5"},
		},
	})
}

// orderedBy returns an orderbytest.TranslateFunc that runs the rendered ORDER BY body with query (a SELECT of record ids with
// a %s for the body) and renders the ids in the server's order. An empty body renders "" without a query.
func orderedBy(t *testing.T, db *sql.DB, query string, translate func(orderby.Spec) (string, error)) orderbytest.TranslateFunc {
	return func(spec orderby.Spec) (string, error) {
		body, err := translate(spec)
		if err != nil || body == "" {
			return "", err
		}

		ids, err := selectIDs(t, db, strings.Replace(query, "%s", body, 1), nil)

		return strings.Join(ids, " "), err
	}
}

// TestProjectionConformanceLive runs the projection conformance suite with every rendered column list selected from a real
// table, so each projection the suite accepts is valid PostgreSQL over the storage names it reads.
func TestProjectionConformanceLive(t *testing.T) {
	t.Parallel()

	db := dbtest.Postgres(t)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE projected (id text PRIMARY KEY, name text, email text, password text,
		created_at timestamptz, secret text, api_key text, token text); CREATE TABLE address (rid text PRIMARY KEY, city text, zip text)`)
	require.NoError(t, err)

	projectiontest.RunConformance(t, projectiontest.Backend{
		New: func(p *projection.Policy, storage ...projection.TranslatorOption) (projectiontest.Translator, error) {
			tr, err := postgres.NewProjectionTranslator(p, sqlbase.WithProjectionOptions(storage...))
			if err != nil {
				return nil, err
			}

			return projectiontest.Adapt(selected{t: t, db: db, tr: tr,
				query: "SELECT %s FROM projected AS p JOIN address ON address.rid = p.id LIMIT 0"}, postgrestest.DecodeColumns), nil
		},
	})
}

// selected executes every column list its translator renders with query (a SELECT with a %s for the list, * when empty) and
// returns the list when the server accepts it.
type selected struct {
	t     *testing.T
	db    *sql.DB
	tr    projectiontest.Source[string]
	query string
}

func (s selected) Translate(spec projection.Spec) (string, error) {
	return s.run(s.tr.Translate(spec))
}

func (s selected) TranslateView(spec projection.Spec, view projection.View) (string, error) {
	return s.run(s.tr.TranslateView(spec, view))
}

func (s selected) run(columns string, err error) (string, error) {
	if err != nil {
		return "", err
	}

	list := columns
	if list == "" {
		list = "*"
	}

	rows, err := s.db.QueryContext(s.t.Context(), strings.Replace(s.query, "%s", list, 1))
	if err != nil {
		return "", err
	}

	return columns, rows.Close()
}
