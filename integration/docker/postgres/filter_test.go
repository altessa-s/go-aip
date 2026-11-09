// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package postgres_test

import (
	"database/sql"
	"fmt"
	"sync"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/postgres/postgrestest"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/integration/docker/internal/dbtest"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/evaluation"
)

// conformanceDDL stores filtertest.ConformanceRecords: a column per field, nullable, text in COLLATE "C" (byte order, the
// evaluator's), and the nested address.city in a table address joined by record id, so a qualified "address"."city" reads it.
// The records have no location.city (the storage path of a mapping case): its table holds a NULL city for every record.
const conformanceDDL = `
CREATE TABLE conformance (
	id text PRIMARY KEY, name text COLLATE "C", age bigint, score double precision, active boolean, created timestamptz,
	tags text[] COLLATE "C", status bigint, data bytea, nickname text COLLATE "C", note text COLLATE "C",
	secret text COLLATE "C", price numeric
);
CREATE TABLE address (rid text PRIMARY KEY, city text COLLATE "C");
CREATE TABLE location (rid text PRIMARY KEY, city text COLLATE "C");`

const conformanceSelect = `SELECT c.id FROM conformance AS c JOIN address ON address.rid = c.id JOIN location ON location.rid = c.id
WHERE %s ORDER BY c.id`

// TestConformanceLive runs the filter conformance suite with every translation executed by PostgreSQL over the conformance
// records: the rows the server selects must be the rows the default evaluator selects.
func TestConformanceLive(t *testing.T) {
	t.Parallel()

	db := dbtest.Postgres(t)
	storeConformanceRecords(t, db)

	sqlErrors := liveErrors(t)
	b := postgrestest.FilterConformanceBackend()
	b.Reference = func(policy *filter.Policy, opts ...filter.TranslatorOption) (filtertest.MatchFunc, error) {
		e, err := evaluator.New(policy, evaluator.WithFilterOptions(opts...))
		if err != nil {
			return nil, err
		}

		return e.Evaluate, nil
	}
	b.Match = func(policy *filter.Policy, opts ...filter.TranslatorOption) (filtertest.MatchFunc, error) {
		tr, err := postgres.NewFilterTranslator(policy, postgres.WithFilterOptions(opts...))
		if err != nil {
			return nil, err
		}

		return selectedBy(t, db, conformanceSelect, tr.Translate, sqlErrors), nil
	}
	filtertest.RunConformance(t, b)
}

// storeConformanceRecords creates the conformance tables and stores filtertest.ConformanceRecords in them.
func storeConformanceRecords(t *testing.T, db *sql.DB) {
	t.Helper()

	_, err := db.ExecContext(t.Context(), conformanceDDL)
	require.NoError(t, err)

	for _, r := range filtertest.ConformanceRecords() {
		var tags []string
		if list, ok := r["tags"].([]any); ok {
			tags = make([]string, 0, len(list))
			for _, tag := range list {
				tags = append(tags, tag.(string))
			}
		}

		_, err := db.ExecContext(t.Context(), `INSERT INTO conformance VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
			r["id"], r["name"], r["age"], r["score"], r["active"], r["created"], tags, r["status"], r["data"], r["nickname"], r["note"],
			r["secret"], r["price"].(decimal.Decimal).String())
		require.NoError(t, err)

		_, err = db.ExecContext(t.Context(), "INSERT INTO address VALUES ($1, $2)", r["id"], r["address"].(map[string]any)["city"])
		require.NoError(t, err)
		_, err = db.ExecContext(t.Context(), "INSERT INTO location VALUES ($1, NULL)", r["id"])
		require.NoError(t, err)
	}
}

// liveErrors collects the queries the server refused while the suite ran. A refused query selects no record, which the suite
// counts as agreeing with a reference that selects none, so the test fails on every refusal once its cases are done.
func liveErrors(t *testing.T) *sync.Map {
	t.Helper()

	var refused sync.Map

	t.Cleanup(func() {
		refused.Range(func(query, err any) bool {
			t.Errorf("the server refused %s: %v", query, err)

			return true
		})
	})

	return &refused
}

// selectedBy returns a filtertest.MatchFunc reporting whether the server selects a record: it translates each filter once,
// runs it with query (a SELECT of record ids with a %s for the WHERE body) and answers for every record of that filter from
// the selected ids.
func selectedBy(t *testing.T, db *sql.DB, query string, translate func(filter.Node) (string, []any, error),
	refused *sync.Map,
) filtertest.MatchFunc {
	var (
		mu       sync.Mutex
		node     filter.Node
		selected map[string]bool
		failure  error
	)

	return func(n filter.Node, record map[string]any) (bool, error) {
		mu.Lock()
		defer mu.Unlock()

		if selected == nil || n != node {
			node, failure = n, nil

			where, args, err := translate(n)
			if err == nil {
				var ids []string

				ids, err = selectIDs(t, db, fmt.Sprintf(query, where), args)
				if err != nil {
					refused.Store(where, err)
				}

				selected = make(map[string]bool, len(ids))
				for _, id := range ids {
					selected[id] = true
				}
			}

			failure = err
		}

		if failure != nil {
			return false, failure
		}

		return selected[record["id"].(string)], nil
	}
}

// selectIDs runs query and returns the ids it selects.
func selectIDs(t *testing.T, db *sql.DB, query string, args []any) ([]string, error) {
	rows, err := db.QueryContext(t.Context(), query, args...)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	var ids []string

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}

		ids = append(ids, id)
	}

	return ids, rows.Err()
}

// liveCase is one filter of a live comparison and the reference's selection.
type liveCase struct {
	expr string
	node filter.Node
}

// compareLive runs every filter through translate on the server (query selects ids from table with a %s WHERE body) and
// compares the ids it selects with the ids reference accepts among rows.
func compareLive(t *testing.T, db *sql.DB, query string, rows []map[string]any, cases []liveCase,
	translate func(filter.Node) (string, []any, error), reference func(filter.Node, map[string]any) (bool, error),
) {
	t.Helper()

	for _, c := range cases {
		t.Run(c.expr, func(t *testing.T) {
			t.Parallel()

			var want []string

			for _, row := range rows {
				ok, err := reference(c.node, row)
				require.NoError(t, err, "reference on %v", row)

				if ok {
					want = append(want, row["id"].(string))
				}
			}

			where, args, err := translate(c.node)
			require.NoError(t, err)

			got, err := selectIDs(t, db, fmt.Sprintf(query, where), args)
			require.NoError(t, err, "SQL: %s %v", where, args)
			require.Equal(t, want, got, "SQL: %s %v", where, args)
		})
	}
}

// plainExprs exercise the three-valued NULL semantics of the schema translator: null scalars, null arrays and null array
// elements, negation pushed through every operator, field operands and size()/string functions of null values.
var plainExprs = []string{
	`i == 1`, `i != 1`, `!(i == 1)`, `!(i != 1)`, `i > 1`, `!(i > 1)`, `!(i <= 1)`, `1 != i`, `!(1 == i)`,
	`i in [1, 2]`, `!(i in [1, 2])`, `i in [1, null]`, `!(i in [1, null])`, `i in [null]`, `!(i in [null])`, `i in []`, `!(i in [])`,
	`i == null`, `i != null`, `!(i == null)`, `!(i != null)`,
	`s.contains("a")`, `!s.contains("a")`, `s.startsWith("x")`, `!s.startsWith("x")`, `!s.endsWith("a")`, `!s.matches("^a")`,
	`s == s2`, `s != s2`, `!(s == s2)`, `!(s != s2)`, `s.contains(s2)`, `!s.contains(s2)`, `"xa".contains(s2)`, `!"xa".contains(s2)`,
	`s in [s2, "x"]`, `!(s in [s2, "x"])`, `null in [s, "x"]`, `!(null in [s, "x"])`, `null in [null]`, `!(null in [null])`,
	`b`, `!b`, `b == true`, `b != true`, `!(b == false)`,
	`tags == "a"`, `tags != "a"`, `!(tags == "a")`, `!(tags != "a")`, `tags > "a"`, `!(tags > "a")`, `tags <= "a"`, `!(tags <= "a")`,
	`"a" in tags`, `!("a" in tags)`, `tags.contains("a")`, `!tags.contains("a")`, `!tags.startsWith("b")`,
	`size(tags) == 0`, `!(size(tags) == 0)`, `size(s) == 1`, `size(s) != 1`, `!(size(s) == 1)`, `size(s) == null`, `!(size(s) == null)`,
	`size(s) != null`, `size(s) in [1, 2]`, `!(size(s) in [1, 2])`, `!(size(s) in [])`, `i != size(s)`, `!(i == size(s))`,
	`i == 1 && s == "a"`, `!(i == 1 && s == "a")`, `!(i == 1 || s == "a")`, `!(i == 1 || s.contains("x"))`, `!(!(i == 1) && b == true)`,
	`(i == 1) == false`, `(i == 1) != true`, `!((i == 1) == false)`, `(s.contains("a")) != true`, `(i > 1) == false`,
	`tags == s`, `!(tags == s)`, `s in tags`, `!(s in tags)`, `!(tags > s)`, `!tags.contains(s)`,
}

// TestSchemaTranslatorLive compares the schema translator (nullable columns, no listing contract) executed by PostgreSQL with
// the same schema evaluating the rows in memory, on every combination of null and non-null values.
func TestSchemaTranslatorLive(t *testing.T) {
	t.Parallel()

	db := dbtest.Postgres(t)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE plain (id text PRIMARY KEY, i bigint, s text COLLATE "C", s2 text COLLATE "C",
		b boolean, tags text[] COLLATE "C")`)
	require.NoError(t, err)

	var rows []map[string]any

	for _, i := range []any{nil, int64(1), int64(2)} {
		for _, s := range []any{nil, "a", "b", "xa"} {
			for _, s2 := range []any{nil, "a"} {
				for _, b := range []any{nil, true, false} {
					for _, tags := range []any{nil, []any{}, []any{"a"}, []any{"b", nil}, []any{nil}} {
						row := map[string]any{"id": fmt.Sprintf("%04d", len(rows)), "i": i, "s": s, "s2": s2, "b": b, "tags": tags}
						rows = append(rows, row)
						_, err := db.ExecContext(t.Context(), `INSERT INTO plain VALUES ($1, $2, $3, $4, $5, $6)`, row["id"], i, s, s2, b,
							nullableStrings(tags))
						require.NoError(t, err)
					}
				}
			}
		}
	}

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"i": sqlbase.Scalar("i", sqlbase.Integer), "s": sqlbase.Scalar("s", sqlbase.Text), "s2": sqlbase.Scalar("s2", sqlbase.Text),
		"b": sqlbase.Scalar("b", sqlbase.Boolean), "tags": sqlbase.Array("tags", sqlbase.Text),
	})
	require.NoError(t, err)
	schema, err := postgres.NewSchema(fields)
	require.NoError(t, err)

	policy := filtertest.MustPolicy(t, filter.WithAnyField(true), filter.WithArrayFields("tags"))
	tr, err := postgres.NewFilterTranslatorWithSchema(policy, schema)
	require.NoError(t, err)
	ref, err := evaluator.New(policy, evaluator.WithProfile(schema))
	require.NoError(t, err)

	cases := make([]liveCase, 0, len(plainExprs))
	for _, expr := range plainExprs {
		cases = append(cases, liveCase{expr: expr, node: filtertest.MustParseFilter(t, expr)})
	}

	compareLive(t, db, "SELECT id FROM plain WHERE %s ORDER BY id", rows, cases, tr.Translate, ref.Evaluate)
}

// nullableStrings converts a row's tags to a PostgreSQL text[] argument with NULL elements.
func nullableStrings(v any) []*string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}

	out := make([]*string, 0, len(list))
	for _, e := range list {
		if s, ok := e.(string); ok {
			out = append(out, &s)
		} else {
			out = append(out, nil)
		}
	}

	return out
}

// contractFields are the listing fields of the contract comparison: presence scalars (nullable), a required integer, a
// zero-valued Optional string and a NOT NULL array.
func contractFields() []listing.Field {
	return []listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "rating", Kind: filter.FieldKindInt, Presence: true},
		{Name: "score", Kind: filter.FieldKindString, Presence: true},
		{Name: "active", Kind: filter.FieldKindBool, Presence: true},
		{Name: "priority", Kind: filter.FieldKindInt},
		{Name: "note", Kind: filter.FieldKindString, Optional: true},
		{Name: "tags", Kind: filter.FieldKindString, Array: true},
	}
}

// contractExprs exercise the listing contract: presence nulls under negation, Optional zero values, empty arrays.
var contractExprs = []string{
	`rating == 1`, `rating != 1`, `!(rating == 1)`, `!(rating != 1)`, `rating > 1`, `!(rating > 1)`, `!!(rating > 1)`,
	`rating in [1, 2]`, `!(rating in [1, 2])`, `rating == null`, `rating != null`, `!(rating == null)`,
	`score == "apple"`, `score != ""`, `!(score == "")`, `score.startsWith("a")`, `!score.startsWith("a")`, `!score.contains("e")`,
	`!score.endsWith("y")`, `size(score) == 0`, `!(size(score) > 0)`, `active`, `!active`, `active == true`, `active != true`,
	`!(rating == 1 || priority == 4)`, `!(rating == 1 && score.startsWith("b"))`, `!(!(rating != 2) || active == false)`,
	`rating != 1 && priority == 4`, `!(tags == "t1") || rating != 2`, `tags != "t1"`, `!(tags == "u1")`, `!(tags > "t1")`,
	`!tags.startsWith("u")`, `!(tags in ["t1", "u1"])`, `!("t2" in tags)`, `size(tags) == 0`, `!(size(tags) < 2)`, `tags == null`,
	`!(tags == null)`, `priority != 4`, `!(priority == 0)`, `score == null`, `!(score != null)`,
	`note == ""`, `note != ""`, `!(note == "n")`, `note < "n"`, `!(note < "n")`, `size(note) == 0`, `note == null`, `note != null`,
	`!(note in ["n"])`, `note.startsWith("n") || score == null`, `!(note.startsWith("n") && score != null)`,
}

// contractRows are every combination of null and non-null contract values, as the listing evaluator reads them.
func contractRows() []map[string]any {
	var rows []map[string]any

	for _, rating := range []any{nil, int64(1), int64(2)} {
		for _, score := range []any{nil, "", "apple", "berry"} {
			for _, active := range []any{nil, true, false} {
				for _, priority := range []int64{0, 4} {
					for _, note := range []string{"", "n"} {
						for _, tags := range [][]any{{}, {"t1"}, {"t2", "u1"}} {
							rows = append(rows, map[string]any{
								"id": fmt.Sprintf("%04d", len(rows)), "rating": rating, "score": score, "active": active, "priority": priority,
								"note": note, "tags": tags,
							})
						}
					}
				}
			}
		}
	}

	return rows
}

// contractCases compiles contractExprs with schema, so each filter carries the listing request rules.
func contractCases(t *testing.T, schema *listing.Schema) []liveCase {
	t.Helper()

	cases := make([]liveCase, 0, len(contractExprs))
	for _, expr := range contractExprs {
		q, err := schema.Compile(t.Context(), listing.Request{Filter: expr})
		require.NoError(t, err, expr)

		cases = append(cases, liveCase{expr: expr, node: q.Filter})
	}

	return cases
}

// contractReference is the listing evaluator every engine agrees with: the memory engine's profile over schema.
func contractReference(t *testing.T, schema *listing.Schema) func(filter.Node, map[string]any) (bool, error) {
	t.Helper()

	profile, err := evaluation.New(schema, evaluation.NewReader(nil, 0))
	require.NoError(t, err)
	ev, err := evaluator.New(schema.FilterPolicy(), evaluator.WithProfile(profile))
	require.NoError(t, err)

	return ev.Evaluate
}

// TestContractTranslatorLive compares the listing contract translator executed by PostgreSQL with the listing evaluator.
func TestContractTranslatorLive(t *testing.T) {
	t.Parallel()

	db := dbtest.Postgres(t)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE contract (id text COLLATE "C" PRIMARY KEY, rating bigint, score text COLLATE "C",
		active boolean, priority bigint NOT NULL, note text COLLATE "C" NOT NULL, tags text[] COLLATE "C" NOT NULL)`)
	require.NoError(t, err)

	rows := contractRows()
	for _, r := range rows {
		tags := make([]string, 0, 2)
		for _, tag := range r["tags"].([]any) {
			tags = append(tags, tag.(string))
		}

		_, err := db.ExecContext(t.Context(), `INSERT INTO contract VALUES ($1, $2, $3, $4, $5, $6, $7)`, r["id"], r["rating"], r["score"],
			r["active"], r["priority"], r["note"], tags)
		require.NoError(t, err)
	}

	schema, err := listing.NewSchema(contractFields(), []string{"id"}, nil)
	require.NoError(t, err)

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"id": sqlbase.Scalar("id", sqlbase.Text), "rating": sqlbase.PresenceScalar("rating", sqlbase.Integer),
		"score": sqlbase.PresenceScalar("score", sqlbase.Text), "active": sqlbase.PresenceScalar("active", sqlbase.Boolean),
		"priority": sqlbase.Scalar("priority", sqlbase.Integer), "note": sqlbase.Scalar("note", sqlbase.Text),
		"tags": sqlbase.Array("tags", sqlbase.Text),
	})
	require.NoError(t, err)
	sem, err := postgres.NewSchema(fields, postgres.WithSchemaContractMode(true))
	require.NoError(t, err)
	tr, err := postgres.NewFilterTranslatorWithSchema(schema.FilterPolicy(), sem)
	require.NoError(t, err)

	compareLive(t, db, "SELECT id FROM contract WHERE %s ORDER BY id", rows, contractCases(t, schema), tr.Translate,
		contractReference(t, schema))
}

// scalarExprs exercise the scalar translator's NULL semantics beyond the conformance table: negation of every operator over a
// null or absent column, null array elements, field operands, size() and substring() of null values.
var scalarExprs = []string{
	// Bounds past int4 and up to MaxInt64: the substring is unknown on every row, and the start is never overflowed.
	`name.substring(9223372036854775807) == "x"`, `!(name.substring(9223372036854775807) == "x")`,
	`name.substring(1, 9223372036854775807) != "x"`, `!(name.substring(2147483647) == "")`, `name.substring(3000000000, 3000000001) != ""`,
	`!(name.substring(0, 2147483648) in ["x"])`,
	`note != "x"`, `!(note == "x")`, `!(note != "x")`, `!(note < "m")`, `note < "m"`, `!(note in ["x"])`, `!(note in ["x", null])`,
	`note in ["x", null]`, `!(note in [null])`, `note in [null]`, `!(note in [])`,
	`!note.contains("x")`, `!note.startsWith("x")`, `!note.endsWith("x")`, `!note.matches("x")`, `!note.endsWith("")`,
	`!(size(note) > 0)`, `size(note) == 0`, `!(note.substring(0, 1) == "x")`, `note.substring(0, 1) != "x"`,
	`note != nickname`, `nickname != note`, `name != nickname`, `note == nickname`, `!(note == nickname)`, `!(note < nickname)`,
	`!(note != nickname)`, `!(note != null)`, `!(tags == "red")`, `tags != "red"`, `!(tags != "red")`, `!(tags > "c")`,
	`!tags.startsWith("r")`, `!("red" in tags)`, `!(tags in ["red", "green"])`, `!(tags in ["red", null])`, `tags in ["red", null]`,
	`!(tags == null)`, `!(size(tags) > 0)`, `!(name.contains(note))`, `name.contains(note)`, `!(note.contains(name))`,
	`!(nickname.contains(note))`, `!(name.endsWith(note))`, `!active`, `active != true`, `!(active == true)`,
	`!(note == "x" && age > 1)`, `!(note == "x" || age > 100)`, `!(!(note == "x") && age > 20)`, `note in [nickname, "x"]`,
	`!(note in [nickname, "x"])`, `!(note.substring(0, 1) in ["x"])`, `!(note.substring(1) in [])`, `!("x" == note)`, `"x" != note`,
	`!(note >= "")`, `!(age > 20)`, `age != 30`, `!(age in [30])`, `!(age in [30, null])`, `size(note) in [0, null]`,
	`!(size(note) in [0, null])`, `size(note) == null`, `size(note) != null`, `!(size(note) == null)`, `!(size(note) in [])`,
	`size(note) != age`, `age != size(note)`, `!(size(note) == age)`, `note.substring(0) != nickname`,
	`!(note.substring(0) != nickname)`, `note.substring(1) != null`, `!(note == null)`, `!(note != null && note != "")`,
}

// scalarRecords are the rows of the scalar comparison: null, absent and empty values in every column.
func scalarRecords() []map[string]any {
	return []map[string]any{
		{"id": "1", "name": "alice", "age": int64(30), "active": true, "note": "x", "nickname": "al", "tags": []any{"red", "blue"}},
		{"id": "2", "name": "bob", "age": int64(25), "active": false, "note": nil, "nickname": "bobby", "tags": []any{"green"}},
		{"id": "3", "name": "carol", "age": int64(41), "active": true, "nickname": "carol", "tags": []any{}},
		{"id": "4", "name": "Dave", "age": int64(19), "active": false, "note": "", "nickname": "D", "tags": []any{"red"}},
		{"id": "5", "name": "erin", "age": int64(30), "active": true, "note": "y", "nickname": "e"},
		{"id": "6", "name": "zed", "age": nil, "active": nil, "note": nil, "nickname": nil, "tags": nil},
		{"id": "7", "name": "x", "age": int64(1), "active": true, "note": "x", "nickname": "x", "tags": []any{"x"}},
	}
}

// liveScalarPolicy declares the columns of scalarRecords.
func liveScalarPolicy(t *testing.T) *filter.Policy {
	t.Helper()

	return filtertest.MustPolicy(t, filter.WithAllowedFields("name", "age", "active", "note", "nickname", "tags"),
		filter.WithFieldTypes(map[string]filter.FieldKind{
			"name": filter.FieldKindString, "age": filter.FieldKindInt, "active": filter.FieldKindBool, "note": filter.FieldKindString,
			"nickname": filter.FieldKindString, "tags": filter.FieldKindString,
		}),
		filter.WithArrayFields("tags"), filter.WithSafeRegex(true))
}

// TestScalarTranslatorLive compares the scalar translator executed by PostgreSQL with the default evaluator on null, absent
// and empty values.
func TestScalarTranslatorLive(t *testing.T) {
	t.Parallel()

	db := dbtest.Postgres(t)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE scalar (id text PRIMARY KEY, name text COLLATE "C", age bigint, active boolean,
		note text COLLATE "C", nickname text COLLATE "C", tags text[] COLLATE "C")`)
	require.NoError(t, err)

	rows := scalarRecords()
	for _, r := range rows {
		_, err := db.ExecContext(t.Context(), `INSERT INTO scalar VALUES ($1, $2, $3, $4, $5, $6, $7)`, r["id"], r["name"], r["age"],
			r["active"], r["note"], r["nickname"], nullableStrings(r["tags"]))
		require.NoError(t, err)
	}

	policy := liveScalarPolicy(t)
	tr, err := postgres.NewFilterTranslator(policy)
	require.NoError(t, err)
	ref, err := evaluator.New(policy)
	require.NoError(t, err)

	cases := make([]liveCase, 0, len(scalarExprs))
	for _, expr := range scalarExprs {
		cases = append(cases, liveCase{expr: expr, node: filtertest.MustParseFilter(t, expr)})
	}

	compareLive(t, db, "SELECT id FROM scalar WHERE %s ORDER BY id", rows, cases, tr.Translate, ref.Evaluate)
}

// substringExprs exercise substring() in the schema translators: code points, ranges beyond the end, NULL texts and JSON
// paths, field operands and in lists, alone and under negation.
var substringExprs = []string{
	// Bounds past int4 and up to MaxInt64: the substring is unknown on every row, and the start is never overflowed.
	`name.substring(9223372036854775807) == "x"`, `!(name.substring(9223372036854775807) == "x")`,
	`name.substring(1, 9223372036854775807) != "x"`, `!(name.substring(2147483647) == "")`, `name.substring(3000000000, 3000000001) != ""`,
	`!(name.substring(0, 2147483648) in ["x"])`,
	`name.substring(0, 1) == "a"`, `!(name.substring(0, 1) == "a")`, `name.substring(0, 1) != "a"`, `!(name.substring(0, 1) != "a")`,
	`name.substring(1) == "b"`, `!(name.substring(1) == "b")`, `name.substring(1) < "b"`, `!(name.substring(1) >= "b")`,
	`name.substring(2, 4) == "лт"`, `name.substring(0, 2) == "жё"`, `!(name.substring(5) == "й")`, `name.substring(6) == ""`,
	`name.substring(7) != "x"`, `!(name.substring(0, 7) == "x")`, `note.substring(0) == "b"`, `!(note.substring(0) == "b")`,
	`note.substring(0, 1) != "a"`, `!(note.substring(1) != "b")`, `note.substring(1, 2) == "ж"`, `note.substring(0) != name`,
	`!(note.substring(0) == name)`, `name.substring(1) != note`, `!(name.substring(1) == note)`, `other != name.substring(0, 1)`,
	`!(other == note.substring(0, 1))`, `name.substring(1) in ["b", "a"]`, `!(name.substring(1) in ["b", "a"])`,
	`note.substring(1) in ["b", null]`, `!(note.substring(1) in ["b", null])`, `!(note.substring(1) in [null])`, `note.substring(1) in []`,
	`!(note.substring(1) in [])`, `note.substring(0, 1) == null`, `note.substring(0, 1) != null`, `!(note.substring(0, 1) == null)`,
	`!(note.substring(0, 1) != null)`, `(note.substring(0, 1) == "a") == false`, `(name.substring(1) != "b") != true`,
	`note.substring(1) == "b" || other == "a"`, `!(note.substring(1) == "b" && other == "a")`, `name.substring(0, 1) == note.substring(0, 1)`,
	`!(name.substring(0, 1) != note.substring(0, 1))`,
	`city.substring(0, 1) == "O"`, `!(city.substring(0, 1) == "O")`, `city.substring(1) != "ж"`, `!(city.substring(4) != "")`,
	`city.substring(0) in ["Oslo", "x"]`, `!(city.substring(1, 2) in ["s"])`, `city.substring(0, 1) != null`,
}

// substringRows stores every combination of NOT NULL, NULL, empty, ASCII and Cyrillic texts in table substr and returns them as
// the default evaluator reads them.
func substringRows(t *testing.T, db *sql.DB) []map[string]any {
	t.Helper()

	_, err := db.ExecContext(t.Context(), `CREATE TABLE substr (id text PRIMARY KEY, name text COLLATE "C" NOT NULL, note text COLLATE "C",
		other text COLLATE "C", doc jsonb)`)
	require.NoError(t, err)

	docs := []struct {
		json string
		city any
	}{{`{}`, nil}, {`{"city": null}`, nil}, {`{"city": "Oslo"}`, "Oslo"}, {`{"city": "ёж"}`, "ёж"}}

	var rows []map[string]any

	for _, name := range []string{"", "a", "ab", "жёлтый"} {
		for _, note := range []any{nil, "", "b", "ёж", "abc"} {
			for _, other := range []any{nil, "a"} {
				for _, doc := range docs {
					row := map[string]any{"id": fmt.Sprintf("%04d", len(rows)), "name": name, "note": note, "other": other, "city": doc.city}
					rows = append(rows, row)
					_, err := db.ExecContext(t.Context(), `INSERT INTO substr VALUES ($1, $2, $3, $4, $5)`, row["id"], name, note, other,
						doc.json)
					require.NoError(t, err)
				}
			}
		}
	}

	return rows
}

// TestSchemaSubstringLive compares substring() of the schema translator, plain and under the listing contract, executed by
// PostgreSQL with the default evaluator.
func TestSchemaSubstringLive(t *testing.T) {
	t.Parallel()

	db := dbtest.Postgres(t)
	rows := substringRows(t, db)

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"name": sqlbase.Scalar("name", sqlbase.Text), "note": sqlbase.PresenceScalar("note", sqlbase.Text),
		"other": sqlbase.PresenceScalar("other", sqlbase.Text), "city": sqlbase.JSONPath("doc", sqlbase.Text, "city"),
	})
	require.NoError(t, err)

	policy := filtertest.MustPolicy(t, filter.WithAnyField(true))
	ref, err := evaluator.New(policy)
	require.NoError(t, err)

	cases := make([]liveCase, 0, len(substringExprs))
	for _, expr := range substringExprs {
		cases = append(cases, liveCase{expr: expr, node: filtertest.MustParseFilter(t, expr)})
	}

	for name, contract := range map[string]bool{"plain": false, "contract": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			schema, err := postgres.NewSchema(fields, postgres.WithSchemaContractMode(contract))
			require.NoError(t, err)
			tr, err := postgres.NewFilterTranslatorWithSchema(policy, schema)
			require.NoError(t, err)

			compareLive(t, db, "SELECT id FROM substr WHERE %s ORDER BY id", rows, cases, tr.Translate, ref.Evaluate)
		})
	}
}
