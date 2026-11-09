// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package mariadb_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/mariadb/mariadbtest"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/integration/docker/internal/dbtest"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/evaluation"
)

// conformanceDDL stores filtertest.ConformanceRecords: a column per field, nullable, text in utf8mb4_nopad_bin (exact, the
// evaluator's comparison), tags as a JSON array, and the nested address.city in a table address joined by record id, so a
// qualified `address`.`city` reads it. The records have no location.city (the storage path of a mapping case): its table holds
// a NULL city for every record.
const conformanceDDL = `
CREATE TABLE conformance (
	id VARCHAR(8) PRIMARY KEY, name VARCHAR(64), age BIGINT, score DOUBLE, active BOOLEAN, created DATETIME(6),
	tags JSON COLLATE utf8mb4_nopad_bin, status BIGINT, data VARBINARY(64), nickname VARCHAR(64), note VARCHAR(64),
	secret VARCHAR(64), price DECIMAL(65,30)
);
CREATE TABLE address (rid VARCHAR(8) PRIMARY KEY, city VARCHAR(64));
CREATE TABLE location (rid VARCHAR(8) PRIMARY KEY, city VARCHAR(64));`

const conformanceSelect = "SELECT c.id FROM conformance AS c JOIN address ON address.rid = c.id JOIN location ON location.rid = c.id " +
	"WHERE %s ORDER BY c.id"

// TestConformanceLive runs the filter conformance suite with every translation executed by MariaDB over the conformance
// records: the rows the server selects must be the rows the default evaluator selects.
func TestConformanceLive(t *testing.T) {
	t.Parallel()

	db := dbtest.MariaDB(t)
	storeConformanceRecords(t, db)

	sqlErrors := liveErrors(t)
	b := mariadbtest.FilterConformanceBackend()
	b.Reference = func(policy *filter.Policy, opts ...filter.TranslatorOption) (filtertest.MatchFunc, error) {
		e, err := evaluator.New(policy, evaluator.WithFilterOptions(opts...))
		if err != nil {
			return nil, err
		}

		return e.Evaluate, nil
	}
	b.Match = func(policy *filter.Policy, opts ...filter.TranslatorOption) (filtertest.MatchFunc, error) {
		tr, err := mariadb.NewFilterTranslator(policy, mariadb.WithFilterOptions(opts...))
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
		var tags any

		if list, ok := r["tags"].([]any); ok {
			encoded, err := json.Marshal(list)
			require.NoError(t, err)

			tags = string(encoded)
		}

		_, err := db.ExecContext(t.Context(), `INSERT INTO conformance VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r["id"], r["name"], r["age"], r["score"], r["active"], r["created"], tags, r["status"], r["data"], r["nickname"], r["note"],
			r["secret"], r["price"].(decimal.Decimal).String())
		require.NoError(t, err)

		_, err = db.ExecContext(t.Context(), "INSERT INTO address VALUES (?, ?)", r["id"], r["address"].(map[string]any)["city"])
		require.NoError(t, err)
		_, err = db.ExecContext(t.Context(), "INSERT INTO location VALUES (?, NULL)", r["id"])
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

// TestContractTranslatorLive compares the listing contract translator executed by MariaDB with the listing evaluator, on every
// combination of null and non-null contract values.
func TestContractTranslatorLive(t *testing.T) {
	t.Parallel()

	db := dbtest.MariaDB(t)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE contract (id VARCHAR(8) NOT NULL PRIMARY KEY, rating BIGINT, score VARCHAR(16),
		active BOOLEAN, priority BIGINT NOT NULL, note VARCHAR(16) NOT NULL, tags JSON COLLATE utf8mb4_nopad_bin NOT NULL)`)
	require.NoError(t, err)

	var rows []map[string]any

	for _, rating := range []any{nil, int64(1), int64(2)} {
		for _, score := range []any{nil, "", "apple", "berry"} {
			for _, active := range []any{nil, true, false} {
				for _, priority := range []int64{0, 4} {
					for _, note := range []string{"", "n"} {
						for _, tags := range [][]any{{}, {"t1"}, {"t2", "u1"}} {
							row := map[string]any{
								"id": fmt.Sprintf("%04d", len(rows)), "rating": rating, "score": score, "active": active, "priority": priority,
								"note": note, "tags": tags,
							}
							rows = append(rows, row)

							encoded, err := json.Marshal(tags)
							require.NoError(t, err)
							_, err = db.ExecContext(t.Context(), `INSERT INTO contract VALUES (?, ?, ?, ?, ?, ?, ?)`, row["id"], rating, score,
								active, priority, note, string(encoded))
							require.NoError(t, err)
						}
					}
				}
			}
		}
	}

	schema, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "rating", Kind: filter.FieldKindInt, Presence: true},
		{Name: "score", Kind: filter.FieldKindString, Presence: true},
		{Name: "active", Kind: filter.FieldKindBool, Presence: true},
		{Name: "priority", Kind: filter.FieldKindInt},
		{Name: "note", Kind: filter.FieldKindString, Optional: true},
		{Name: "tags", Kind: filter.FieldKindString, Array: true},
	}, []string{"id"}, nil)
	require.NoError(t, err)

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"id": sqlbase.Scalar("id", sqlbase.Text), "rating": sqlbase.PresenceScalar("rating", sqlbase.Integer),
		"score": sqlbase.PresenceScalar("score", sqlbase.Text), "active": sqlbase.PresenceScalar("active", sqlbase.Boolean),
		"priority": sqlbase.Scalar("priority", sqlbase.Integer), "note": sqlbase.Scalar("note", sqlbase.Text),
		"tags": sqlbase.Array("tags", sqlbase.Text),
	})
	require.NoError(t, err)
	contractSchema, err := mariadb.NewSchema(fields, mariadb.WithSchemaContractMode(true))
	require.NoError(t, err)
	tr, err := mariadb.NewFilterTranslatorWithSchema(schema.FilterPolicy(), contractSchema)
	require.NoError(t, err)

	profile, err := evaluation.New(schema, evaluation.NewReader(nil, 0))
	require.NoError(t, err)
	ref, err := evaluator.New(schema.FilterPolicy(), evaluator.WithProfile(profile))
	require.NoError(t, err)

	cases := make([]liveCase, 0, len(contractExprs))
	for _, expr := range contractExprs {
		q, err := schema.Compile(t.Context(), listing.Request{Filter: expr})
		require.NoError(t, err, expr)

		cases = append(cases, liveCase{expr: expr, node: q.Filter})
	}

	compareLive(t, db, "SELECT id FROM contract WHERE %s ORDER BY id", rows, cases, tr.Translate, ref.Evaluate)
}

// liveCase is one filter of a live comparison.
type liveCase struct {
	expr string
	node filter.Node
}

// compareLive runs every filter through translate on the server (query selects ids with a %s WHERE body) and compares the ids
// it selects with the ids reference accepts among rows.
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

// TestScalarTranslatorLive compares the scalar translator executed by MariaDB with the default evaluator on null, absent
// and empty values.
func TestScalarTranslatorLive(t *testing.T) {
	t.Parallel()

	db := dbtest.MariaDB(t)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE scalar (id VARCHAR(8) PRIMARY KEY, name VARCHAR(16), age BIGINT, active BOOLEAN,
		note VARCHAR(16), nickname VARCHAR(16), tags JSON COLLATE utf8mb4_nopad_bin)`)
	require.NoError(t, err)

	rows := scalarRecords()
	for _, r := range rows {
		var tags any

		if list, ok := r["tags"].([]any); ok {
			encoded, err := json.Marshal(list)
			require.NoError(t, err)

			tags = string(encoded)
		}

		_, err := db.ExecContext(t.Context(), `INSERT INTO scalar VALUES (?, ?, ?, ?, ?, ?, ?)`, r["id"], r["name"], r["age"], r["active"],
			r["note"], r["nickname"], tags)
		require.NoError(t, err)
	}

	policy := liveScalarPolicy(t)
	tr, err := mariadb.NewFilterTranslator(policy)
	require.NoError(t, err)
	ref, err := evaluator.New(policy)
	require.NoError(t, err)

	cases := make([]liveCase, 0, len(scalarExprs))
	for _, expr := range scalarExprs {
		cases = append(cases, liveCase{expr: expr, node: filtertest.MustParseFilter(t, expr)})
	}

	compareLive(t, db, "SELECT id FROM scalar WHERE %s ORDER BY id", rows, cases, tr.Translate, ref.Evaluate)
}

// substringExprs exercise substring() in the contract translator: code points, ranges beyond the end, NULL texts, field
// operands and in lists, alone and under negation.
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
}

// TestSchemaSubstringLive compares substring() of the contract translator executed by MariaDB with the default evaluator, on
// every combination of NOT NULL, NULL, empty, ASCII and Cyrillic texts.
func TestSchemaSubstringLive(t *testing.T) {
	t.Parallel()

	db := dbtest.MariaDB(t)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE substr (id VARCHAR(8) NOT NULL PRIMARY KEY, name VARCHAR(16) NOT NULL,
		note VARCHAR(16), other VARCHAR(16))`)
	require.NoError(t, err)

	var rows []map[string]any

	for _, name := range []string{"", "a", "ab", "жёлтый"} {
		for _, note := range []any{nil, "", "b", "ёж", "abc"} {
			for _, other := range []any{nil, "a"} {
				row := map[string]any{"id": fmt.Sprintf("%04d", len(rows)), "name": name, "note": note, "other": other}
				rows = append(rows, row)
				_, err := db.ExecContext(t.Context(), `INSERT INTO substr VALUES (?, ?, ?, ?)`, row["id"], name, note, other)
				require.NoError(t, err)
			}
		}
	}

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"name": sqlbase.Scalar("name", sqlbase.Text), "note": sqlbase.PresenceScalar("note", sqlbase.Text),
		"other": sqlbase.PresenceScalar("other", sqlbase.Text),
	})
	require.NoError(t, err)

	policy := filtertest.MustPolicy(t, filter.WithAnyField(true))
	contractSchema, err := mariadb.NewSchema(fields, mariadb.WithSchemaContractMode(true))
	require.NoError(t, err)
	tr, err := mariadb.NewFilterTranslatorWithSchema(policy, contractSchema)
	require.NoError(t, err)
	ref, err := evaluator.New(policy)
	require.NoError(t, err)

	cases := make([]liveCase, 0, len(substringExprs))
	for _, expr := range substringExprs {
		cases = append(cases, liveCase{expr: expr, node: filtertest.MustParseFilter(t, expr)})
	}

	compareLive(t, db, "SELECT id FROM substr WHERE %s ORDER BY id", rows, cases, tr.Translate, ref.Evaluate)
}
