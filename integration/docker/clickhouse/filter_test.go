// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

//go:build integration_docker

package clickhouse_test

import (
	"database/sql"
	"fmt"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/clickhouse/clickhousetest"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/integration/docker/internal/dbtest"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/listing/evaluation"
)

// conformanceDDL stores filtertest.ConformanceRecords: a Nullable column per field (String compares by bytes, the evaluator's
// comparison), tags as a native Array(String) and the nested address.city as the Nested-style column `address.city`, the one
// quoted name the scalar translator renders. The records have no location.city (the storage path of a mapping case): that
// column is NULL for every record.
const conformanceDDL = "CREATE TABLE conformance (id String, name Nullable(String), age Nullable(Int64), score Nullable(Float64), " +
	"active Nullable(Bool), created Nullable(DateTime64(6, 'UTC')), tags Array(String), status Nullable(Int64), data Nullable(String), " +
	"nickname Nullable(String), note Nullable(String), secret Nullable(String), price Nullable(Decimal(38, 10)), " +
	"`address.city` Nullable(String), `location.city` Nullable(String)) ENGINE = MergeTree ORDER BY id"

// conformanceColumns are the columns of conformanceDDL in insert order.
var conformanceColumns = []string{
	"id", "name", "age", "score", "active", "created", "tags", "status", "data", "nickname", "note", "secret", "price",
	"`address.city`", "`location.city`",
}

const conformanceSelect = "SELECT id FROM conformance WHERE %s ORDER BY id"

// knownEmptyArray pins the documented storage limit of a native Array column: it cannot be NULL, so an absent array is stored
// empty and an array null test matches an empty array too.
const knownEmptyArray = "an Array column cannot be NULL: an absent array is stored empty, so `tags == null` also matches the empty array (documented)"

// TestConformanceLive runs the filter conformance suite with every translation executed by ClickHouse over the conformance
// records: the rows the server selects must be the rows the default evaluator selects.
func TestConformanceLive(t *testing.T) {
	t.Parallel()

	db := dbtest.ClickHouse(t)
	storeConformanceRecords(t, db)

	sqlErrors := liveErrors(t)
	b := clickhousetest.FilterConformanceBackend()
	b.Expect = maps.Clone(b.Expect)
	known := b.Expect["array equal null"]
	known.Known = knownEmptyArray
	b.Expect["array equal null"] = known
	b.Reference = func(policy *filter.Policy, opts ...filter.TranslatorOption) (filtertest.MatchFunc, error) {
		e, err := evaluator.New(policy, evaluator.WithFilterOptions(opts...))
		if err != nil {
			return nil, err
		}

		return e.Evaluate, nil
	}
	b.Match = func(policy *filter.Policy, opts ...filter.TranslatorOption) (filtertest.MatchFunc, error) {
		tr, err := clickhouse.NewFilterTranslator(policy, clickhouse.WithFilterOptions(opts...))
		if err != nil {
			return nil, err
		}

		return selectedBy(t, db, conformanceSelect, tr.Translate, sqlErrors), nil
	}
	filtertest.RunConformance(t, b)
}

// storeConformanceRecords creates the conformance table and stores filtertest.ConformanceRecords in it.
func storeConformanceRecords(t *testing.T, db *sql.DB) {
	t.Helper()

	_, err := db.ExecContext(t.Context(), conformanceDDL)
	require.NoError(t, err)

	var rows [][]any

	for _, r := range filtertest.ConformanceRecords() {
		price := r["price"].(decimal.Decimal)
		rows = append(rows, []any{r["id"], r["name"], r["age"], r["score"], r["active"], r["created"], stringArray(r["tags"]), r["status"],
			r["data"], r["nickname"], r["note"], r["secret"], &price, r["address"].(map[string]any)["city"], nil})
	}

	insertRows(t, db, "conformance", conformanceColumns, rows)
}

// stringArray converts a record's array (a []any of strings, or absent) to the value of an Array(String) column: an absent
// array is stored empty.
func stringArray(v any) []string {
	list, _ := v.([]any)

	out := make([]string, 0, len(list))
	for _, e := range list {
		out = append(out, e.(string))
	}

	return out
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

// compileCases compiles exprs with schema, so each filter carries the listing request rules.
func compileCases(t *testing.T, schema *listing.Schema, exprs []string) []liveCase {
	t.Helper()

	cases := make([]liveCase, 0, len(exprs))
	for _, expr := range exprs {
		q, err := schema.Compile(t.Context(), listing.Request{Filter: expr})
		if err != nil {
			t.Errorf("compile %s: %v", expr, err)

			continue
		}

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
	`tags in ["t1", "u1"]`,
	`"t1" in tags && !(tags == "u1")`, `tags.contains("1")`, `!tags.contains("1")`, `tags.endsWith("1")`,
}

// TestContractTranslatorLive compares the listing contract translator executed by ClickHouse with the listing evaluator, on
// every combination of null and non-null contract values.
func TestContractTranslatorLive(t *testing.T) {
	t.Parallel()

	db := dbtest.ClickHouse(t)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE contract (id String, rating Nullable(Int64), score Nullable(String),
		active Nullable(Bool), priority Int64, note String, tags Array(String)) ENGINE = MergeTree ORDER BY id`)
	require.NoError(t, err)

	var (
		rows   []map[string]any
		stored [][]any
	)

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
							stored = append(stored, []any{row["id"], rating, score, active, priority, note, stringArray(tags)})
						}
					}
				}
			}
		}
	}

	insertRows(t, db, "contract", []string{"id", "rating", "score", "active", "priority", "note", "tags"}, stored)

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
	contractSchema, err := clickhouse.NewSchema(fields, clickhouse.WithSchemaContractMode(true))
	require.NoError(t, err)
	tr, err := clickhouse.NewFilterTranslatorWithSchema(schema.FilterPolicy(), contractSchema)
	require.NoError(t, err)

	compareLive(t, db, "SELECT id FROM contract WHERE %s ORDER BY id", rows, compileCases(t, schema, contractExprs), tr.Translate,
		contractReference(t, schema))
}

// typedProfile is one stored JSON document of the typed comparison with the values its paths hold for the evaluator: a missing
// key, a JSON null, a value of another type and text that is not JSON are all absent (nil).
type typedProfile struct {
	json                         string
	city, first, last, vip, seen any
}

// typedProfiles are the JSON documents of the typed comparison.
func typedProfiles() []typedProfile {
	seen := time.Date(2024, 3, 1, 10, 0, 0, 123000000, time.UTC)

	return []typedProfile{
		{json: `{"address":{"city":"Oslo"},"scores":[1,2.5],"vip":true,"seen":"2024-03-01T10:00:00.123Z"}`,
			city: "Oslo", first: int64(1), last: 2.5, vip: true, seen: seen},
		{json: `{}`},
		{json: `{"address":{"city":null},"scores":[],"vip":null,"seen":null}`},
		{json: `{"address":{"city":"Bern"},"scores":[0],"vip":false,"seen":"2025-06-15T00:00:00Z"}`,
			city: "Bern", first: int64(0), last: 0.0, vip: false, seen: time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)},
		{json: `{"address":{"city":""},"scores":[-2,-1.25],"vip":false,"seen":"2024-03-01T12:00:00.123+02:00"}`,
			city: "", first: int64(-2), last: -1.25, vip: false, seen: seen},
		{json: `{"address":{"city":5},"scores":["1","x"],"vip":"true","seen":7}`},
		{json: `{"address":"Oslo","scores":[1.5,3],"vip":1,"seen":"not a time"}`, last: 3.0},
		{json: `{"address":{"city":"Kyiv"},"scores":[9223372036854775808],"vip":true}`, city: "Kyiv", last: 9223372036854775808.0, vip: true},
		{json: `{"scores":[true,false],"seen":"2024-03-01T10:00:00.123456Z"}`, seen: seen},
		{json: `not json`},
	}
}

// typedExprs exercise every column kind and JSON path kind of the schema translator: typed operands, NULL under negation,
// timestamps truncated to the precision, exact decimals, bytes and JSON paths that are missing, null or of another type.
var typedExprs = []string{
	`i > 1`, `!(i > 1)`, `i == null`, `i != 2`, `!(i in [1])`,
	`f >= 1.5`, `!(f < 1.5)`, `f == 2`, `f != 2`, `f in [1.5, 2.0]`,
	`ts > timestamp("2024-01-01T00:00:00Z")`, `ts == timestamp("2024-03-01T10:00:00.123Z")`,
	`ts == timestamp("2024-03-01T10:00:00.123999Z")`, `!(ts <= timestamp("2024-03-01T10:00:00.123Z"))`, `ts == null`,
	`ts != timestamp("2024-03-01T10:00:00.123Z")`, `ts in [timestamp("2025-06-15T00:00:00Z")]`,
	`dec > 1.5`, `dec == 10.50`, `dec == 10.5`, `dec in [0.1, 7]`, `!(dec == 0.1)`, `dec <= 0.10`, `dec == null`,
	`data == b"ab"`, `data < b"b"`, `data != b"x"`, `data == b""`, `data in [b"x", b"ab"]`, `data > b"\xfe"`,
	`city == "Oslo"`, `city != "Oslo"`, `!(city == "Oslo")`, `city == null`, `city != null`, `city.startsWith("O")`,
	`!city.contains("s")`, `size(city) == 4`, `size(city) == 0`, `!(city in ["Bern"])`, `city < "C"`,
	`first == 1`, `first > 0`, `!(first > 0)`, `first == null`, `first != 0`, `first in [0, -2]`,
	`last == 2.5`, `last < 3`, `!(last >= 0)`, `last == null`, `last == 3`,
	`vip`, `!vip`, `vip == false`, `vip == null`, `vip != true`,
	`seen > timestamp("2024-01-01T00:00:00Z")`, `seen == timestamp("2024-03-01T10:00:00.123Z")`, `seen == null`,
	`!(seen < timestamp("2025-01-01T00:00:00Z"))`,
}

// TestSchemaTranslatorLive compares the schema translator executed by ClickHouse with the listing evaluator over every column
// kind (Nullable Int64, Float64, DateTime64, Decimal and String, a String of bytes) and every JSON path kind read from a JSON
// String column, on null, missing and mistyped values.
func TestSchemaTranslatorLive(t *testing.T) {
	t.Parallel()

	db := dbtest.ClickHouse(t)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE typed (id String, i Nullable(Int64), f Nullable(Float64),
		ts Nullable(DateTime64(3, 'UTC')), dec Nullable(Decimal(38, 10)), data String, s Nullable(String), profile String)
		ENGINE = MergeTree ORDER BY id`)
	require.NoError(t, err)

	var (
		rows   []map[string]any
		stored [][]any
	)

	profiles := typedProfiles()
	times := []any{nil, time.Date(2024, 3, 1, 10, 0, 0, 123000000, time.UTC), time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)}
	decimals := []any{nil, decimal.RequireFromString("0.1"), decimal.RequireFromString("10.5")}
	strs := []any{nil, "Oslo", ""}

	for _, i := range []any{nil, int64(1), int64(2)} {
		for _, f := range []any{nil, 1.5, 2.0} {
			for _, ts := range times {
				for _, dec := range decimals {
					for _, data := range [][]byte{{}, []byte("ab"), []byte("x"), {0xff, 0x00}} {
						n := len(rows)
						p := profiles[n%len(profiles)]
						row := map[string]any{
							"id": fmt.Sprintf("%04d", n), "i": i, "f": f, "ts": ts, "dec": dec, "data": data, "s": strs[n%len(strs)],
							"city": p.city, "first": p.first, "last": p.last, "vip": p.vip, "seen": p.seen,
						}
						rows = append(rows, row)

						var stDec *decimal.Decimal
						if d, ok := dec.(decimal.Decimal); ok {
							stDec = &d
						}

						stored = append(stored, []any{row["id"], i, f, ts, stDec, string(data), row["s"], p.json})
					}
				}
			}
		}
	}

	insertRows(t, db, "typed", []string{"id", "i", "f", "ts", "dec", "data", "s", "profile"}, stored)

	schema, err := listing.NewSchema([]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "i", Kind: filter.FieldKindInt, Presence: true},
		{Name: "f", Kind: filter.FieldKindFloat, Presence: true},
		{Name: "ts", Kind: filter.FieldKindTimestamp, Presence: true},
		{Name: "dec", Kind: filter.FieldKindDecimal, Presence: true},
		{Name: "data", Kind: filter.FieldKindBytes},
		{Name: "s", Kind: filter.FieldKindString, Presence: true},
		{Name: "city", Kind: filter.FieldKindString, Presence: true},
		{Name: "first", Kind: filter.FieldKindInt, Presence: true},
		{Name: "last", Kind: filter.FieldKindFloat, Presence: true},
		{Name: "vip", Kind: filter.FieldKindBool, Presence: true},
		{Name: "seen", Kind: filter.FieldKindTimestamp, Presence: true},
	}, []string{"id"}, nil)
	require.NoError(t, err)

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"id": sqlbase.Scalar("id", sqlbase.Text), "i": sqlbase.PresenceScalar("i", sqlbase.Integer),
		"f": sqlbase.PresenceScalar("f", sqlbase.Float), "ts": sqlbase.PresenceScalar("ts", sqlbase.Timestamp),
		"dec": sqlbase.PresenceScalar("dec", sqlbase.Numeric), "data": sqlbase.Scalar("data", sqlbase.Bytes),
		"s":     sqlbase.PresenceScalar("s", sqlbase.Text),
		"city":  sqlbase.JSONPath("profile", sqlbase.Text, "address", "city"),
		"first": sqlbase.JSONPath("profile", sqlbase.Integer, "scores", "0"),
		"last":  sqlbase.JSONPath("profile", sqlbase.Float, "scores", "-1"),
		"vip":   sqlbase.JSONPath("profile", sqlbase.Boolean, "vip"),
		"seen":  sqlbase.JSONPath("profile", sqlbase.Timestamp, "seen"),
	})
	require.NoError(t, err)
	contractSchema, err := clickhouse.NewSchema(fields, clickhouse.WithSchemaContractMode(true))
	require.NoError(t, err)
	tr, err := clickhouse.NewFilterTranslatorWithSchema(schema.FilterPolicy(), contractSchema)
	require.NoError(t, err)

	compareLive(t, db, "SELECT id FROM typed WHERE %s ORDER BY id", rows, compileCases(t, schema, typedExprs), tr.Translate,
		contractReference(t, schema))
}

// scalarExprs exercise the scalar translator's NULL semantics beyond the conformance table: negation of every operator over a
// null column, field operands, size() and substring() of null values, and array element tests.
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
	`!tags.startsWith("r")`, `!("red" in tags)`, `!(tags in ["red", "green"])`, `!(size(tags) > 0)`, `!(name.contains(note))`,
	`name.contains(note)`, `!(note.contains(name))`, `!(nickname.contains(note))`, `!(name.endsWith(note))`, `!active`,
	`active != true`, `!(active == true)`, `!(note == "x" && age > 1)`, `!(note == "x" || age > 100)`,
	`!(!(note == "x") && age > 20)`, `note in [nickname, "x"]`, `!(note in [nickname, "x"])`, `!(note.substring(0, 1) in ["x"])`,
	`!(note.substring(1) in [])`, `!("x" == note)`, `"x" != note`, `!(note >= "")`, `!(age > 20)`, `age != 30`, `!(age in [30])`,
	`!(age in [30, null])`, `size(note) in [0, null]`, `!(size(note) in [0, null])`, `size(note) == null`, `size(note) != null`,
	`!(size(note) == null)`, `!(size(note) in [])`, `size(note) != age`, `age != size(note)`, `!(size(note) == age)`,
	`note.substring(0) != nickname`, `!(note.substring(0) != nickname)`, `note.substring(1) != null`, `!(note == null)`,
	`!(note != null && note != "")`, `size(name) == 5`, `size(nickname) == 1`, `name.substring(1, 3) == "li"`,
}

// scalarRecords are the rows of the scalar comparison: null and empty values in every column. Arrays are always present,
// since an Array column cannot be NULL.
func scalarRecords() []map[string]any {
	return []map[string]any{
		{"id": "1", "name": "alice", "age": int64(30), "active": true, "note": "x", "nickname": "al", "tags": []any{"red", "blue"}},
		{"id": "2", "name": "bob", "age": int64(25), "active": false, "note": nil, "nickname": "bobby", "tags": []any{"green"}},
		{"id": "3", "name": "carol", "age": int64(41), "active": true, "note": nil, "nickname": "carol", "tags": []any{}},
		{"id": "4", "name": "Dave", "age": int64(19), "active": false, "note": "", "nickname": "D", "tags": []any{"red"}},
		{"id": "5", "name": "erin", "age": int64(30), "active": true, "note": "y", "nickname": "e", "tags": []any{}},
		{"id": "6", "name": "zed", "age": nil, "active": nil, "note": nil, "nickname": nil, "tags": []any{}},
		{"id": "7", "name": "x", "age": int64(1), "active": true, "note": "x", "nickname": "x", "tags": []any{"x"}},
		{"id": "8", "name": "界é", "age": int64(2), "active": false, "note": "é", "nickname": "界", "tags": []any{"界"}},
	}
}

// TestScalarTranslatorLive compares the scalar translator executed by ClickHouse with the default evaluator on null and empty
// values.
func TestScalarTranslatorLive(t *testing.T) {
	t.Parallel()

	db := dbtest.ClickHouse(t)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE scalar (id String, name Nullable(String), age Nullable(Int64),
		active Nullable(Bool), note Nullable(String), nickname Nullable(String), tags Array(String)) ENGINE = MergeTree ORDER BY id`)
	require.NoError(t, err)

	rows := scalarRecords()

	stored := make([][]any, 0, len(rows))
	for _, r := range rows {
		stored = append(stored, []any{r["id"], r["name"], r["age"], r["active"], r["note"], r["nickname"], stringArray(r["tags"])})
	}

	insertRows(t, db, "scalar", []string{"id", "name", "age", "active", "note", "nickname", "tags"}, stored)

	policy := filtertest.MustPolicy(t, filter.WithAllowedFields("name", "age", "active", "note", "nickname", "tags"),
		filter.WithFieldTypes(map[string]filter.FieldKind{
			"name": filter.FieldKindString, "age": filter.FieldKindInt, "active": filter.FieldKindBool, "note": filter.FieldKindString,
			"nickname": filter.FieldKindString, "tags": filter.FieldKindString,
		}),
		filter.WithArrayFields("tags"), filter.WithSafeRegex(true))
	tr, err := clickhouse.NewFilterTranslator(policy)
	require.NoError(t, err)
	ref, err := evaluator.New(policy)
	require.NoError(t, err)

	cases := make([]liveCase, 0, len(scalarExprs))
	for _, expr := range scalarExprs {
		cases = append(cases, liveCase{expr: expr, node: filtertest.MustParseFilter(t, expr)})
	}

	compareLive(t, db, "SELECT id FROM scalar WHERE %s ORDER BY id", rows, cases, tr.Translate, ref.Evaluate)
}

// substringExprs exercise substring() in the schema translator: code points, ranges beyond the end, NULL texts and JSON
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

// TestSchemaSubstringLive compares substring() of the schema translator executed by ClickHouse with the default evaluator, on
// every combination of NOT NULL, NULL, empty, ASCII and Cyrillic texts and JSON paths.
func TestSchemaSubstringLive(t *testing.T) {
	t.Parallel()

	db := dbtest.ClickHouse(t)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE substr (id String, name String, note Nullable(String), other Nullable(String),
		profile String) ENGINE = MergeTree ORDER BY id`)
	require.NoError(t, err)

	docs := []struct {
		json string
		city any
	}{{`{}`, nil}, {`{"city": null}`, nil}, {`{"city": "Oslo"}`, "Oslo"}, {`{"city": "ёж"}`, "ёж"}}

	var (
		rows   []map[string]any
		stored [][]any
	)

	for _, name := range []string{"", "a", "ab", "жёлтый"} {
		for _, note := range []any{nil, "", "b", "ёж", "abc"} {
			for _, other := range []any{nil, "a"} {
				for _, doc := range docs {
					row := map[string]any{"id": fmt.Sprintf("%04d", len(rows)), "name": name, "note": note, "other": other, "city": doc.city}
					rows = append(rows, row)
					stored = append(stored, []any{row["id"], name, note, other, doc.json})
				}
			}
		}
	}

	insertRows(t, db, "substr", []string{"id", "name", "note", "other", "profile"}, stored)

	fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
		"name": sqlbase.Scalar("name", sqlbase.Text), "note": sqlbase.PresenceScalar("note", sqlbase.Text),
		"other": sqlbase.PresenceScalar("other", sqlbase.Text), "city": sqlbase.JSONPath("profile", sqlbase.Text, "city"),
	})
	require.NoError(t, err)

	policy := filtertest.MustPolicy(t, filter.WithAnyField(true))
	contractSchema, err := clickhouse.NewSchema(fields, clickhouse.WithSchemaContractMode(true))
	require.NoError(t, err)
	tr, err := clickhouse.NewFilterTranslatorWithSchema(policy, contractSchema)
	require.NoError(t, err)
	ref, err := evaluator.New(policy)
	require.NoError(t, err)

	cases := make([]liveCase, 0, len(substringExprs))
	for _, expr := range substringExprs {
		cases = append(cases, liveCase{expr: expr, node: filtertest.MustParseFilter(t, expr)})
	}

	compareLive(t, db, "SELECT id FROM substr WHERE %s ORDER BY id", rows, cases, tr.Translate, ref.Evaluate)
}
