// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package clickhouse_test

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/clickhouse/clickhousetest"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/integration/docker/internal/dbtest"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
	"github.com/altessa-s/go-aip/projection"
	"github.com/altessa-s/go-aip/projection/projectiontest"

	chdriver "github.com/ClickHouse/clickhouse-go/v2"
)

// serverCode is the code of a ClickHouse exception, comparable with errors.Is through [serverError].
type serverCode int32

func (c serverCode) Error() string { return fmt.Sprintf("ClickHouse exception %d", int32(c)) }

// errUnsupportedCollation is ClickHouse's UNSUPPORTED_COLLATION_LOCALE.
const errUnsupportedCollation serverCode = 186

// serverError wraps the code of a ClickHouse exception in err, so an expected server refusal matches it with errors.Is.
func serverError(err error) error {
	if e, ok := errors.AsType[*chdriver.Exception](err); ok {
		return fmt.Errorf("%w: %w", serverCode(e.Code), err)
	}

	return err
}

// TestOrderConformanceLive runs the order conformance suite with every ORDER BY executed by ClickHouse over
// orderbytest.ConformanceRecords; each case expects the record ids in the order the server returns them (ties broken by id).
// ClickHouse sorts NULL last in both directions unless the order names a placement. The collation case names the collation
// C, which is not an ICU locale ClickHouse knows.
func TestOrderConformanceLive(t *testing.T) {
	t.Parallel()

	db := dbtest.ClickHouse(t)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE ordered (id String, name String, age Nullable(Float64), created_at Int64)
		ENGINE = MergeTree ORDER BY id`)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), "CREATE TABLE address (rid String, city String) ENGINE = MergeTree ORDER BY rid")
	require.NoError(t, err)

	var ordered, addresses [][]any

	for _, r := range orderbytest.ConformanceRecords() {
		var age *float64

		switch v := r["age"].(type) {
		case int:
			age = new(float64(v))
		case float64:
			age = &v
		default:
		}

		ordered = append(ordered, []any{r["id"], r["name"], age, int64(r["created_at"].(int))})
		addresses = append(addresses, []any{r["id"], r["address"].(map[string]any)["city"]})
	}

	insertRows(t, db, "ordered", []string{"id", "name", "age", "created_at"}, ordered)
	insertRows(t, db, "address", []string{"rid", "city"}, addresses)

	type O = orderbytest.Outcome

	orderbytest.RunConformance(t, orderbytest.Backend{
		New: func(policy *orderby.Policy, opts ...orderby.TranslatorOption) (orderbytest.TranslateFunc, error) {
			tr, err := clickhouse.NewOrderTranslator(policy, sqlbase.WithOrderOptions(opts...))
			if err != nil {
				return nil, err
			}

			return orderedBy(t, db, "SELECT o.id FROM ordered AS o JOIN address ON address.rid = o.id ORDER BY %s, o.id", tr.Translate), nil
		},
		Expect: map[string]O{
			orderbytest.CaseEmpty:             {},
			orderbytest.CaseAscending:         {Want: "2 3 1 4 5"},
			orderbytest.CaseDescending:        {Want: "5 4 1 3 2"},
			orderbytest.CaseMultipleKeys:      {Want: "1 5 3 2 4"},
			orderbytest.CaseNestedPath:        {Want: "4 2 5 3 1"},
			orderbytest.CaseFieldMapping:      {Want: "3 5 1 4 2"},
			orderbytest.CasePrefixMapping:     {Want: "4 2 5 3 1"},
			orderbytest.CaseDefaultOrder:      {Want: "1 5 3 2 4"},
			orderbytest.CaseTiebreaker:        {Want: "2 3 1 4 5"},
			orderbytest.CaseTiebreakerPresent: {Want: "5 4 3 2 1"},
			orderbytest.CaseAscNullsFirst:     {Want: "2 4 3 1 5"},
			orderbytest.CaseAscNullsLast:      {Want: "3 1 5 2 4"},
			orderbytest.CaseDescNullsFirst:    {Want: "2 4 1 5 3"},
			orderbytest.CaseDescNullsLast:     {Want: "1 5 3 2 4"},
			orderbytest.CaseCollation:         {Err: errUnsupportedCollation},
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

		return strings.Join(ids, " "), serverError(err)
	}
}

// TestProjectionConformanceLive runs the projection conformance suite with every rendered column list, the exclusion
// `* EXCEPT (…)` included, selected from real tables, so each projection the suite accepts is valid ClickHouse over the storage
// names it reads.
func TestProjectionConformanceLive(t *testing.T) {
	t.Parallel()

	db := dbtest.ClickHouse(t)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE projected (id String, name String, email String, password String,
		created_at DateTime64(3, 'UTC'), secret String, api_key String, token String) ENGINE = MergeTree ORDER BY id`)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), "CREATE TABLE address (rid String, city String, zip String) ENGINE = MergeTree ORDER BY rid")
	require.NoError(t, err)

	projectiontest.RunConformance(t, projectiontest.Backend{
		New: func(p *projection.Policy, storage ...projection.TranslatorOption) (projectiontest.Translator, error) {
			tr, err := clickhouse.NewProjectionTranslator(p, sqlbase.WithProjectionOptions(storage...))
			if err != nil {
				return nil, err
			}

			return projectiontest.Adapt(selected{t: t, db: db, tr: tr,
				query: "SELECT %s FROM projected AS p JOIN address ON address.rid = p.id LIMIT 0"}, clickhousetest.DecodeColumns), nil
		},
		Capabilities: projectiontest.Capabilities{Exclusion: true},
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
