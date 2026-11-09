// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/protobuf/encoding/prototext"

	"github.com/altessa-s/go-aip/apierror"
	"github.com/altessa-s/go-aip/apierror/aipmap"
	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/listing"
	"github.com/altessa-s/go-aip/pagination/pagetoken"
	"github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// goldenCall is one recorded statement and the token of its page.
type goldenCall struct {
	query, args, token string
}

// listQueryGolden pins the SQL text, bound arguments and page tokens of a scoped, filtered two-page list for each
// dialect and order kind. The values were recorded before the per-order plans were precompiled.
var listQueryGolden = map[string][2]goldenCall{
	"postgres/unrestricted": {
		{query: "SELECT \"id\"::text, \"name\", \"age\", \"id\", \"name\", \"age\" FROM \"widgets\" AS \"_listing\" WHERE ((\"name\" <> ($1)::text)) AND (\"tenant\" = $2) ORDER BY \"_listing\".\"name\" DESC, \"_listing\".\"age\" ASC, \"_listing\".\"id\" ASC LIMIT 3", args: "[]interface {}{\"zulu\", \"acme\"}", token: "AQAAAZt8qYyIzs6qDPLnfxjlZ_ar2UDmkP6jCcMqhhv0zNLLZuamf2us6Jy5ohSYx5Z1YRphkxu6K3siayI6W3sicyI6ImJyYXZvIn0seyJuIjo4fV0sImkiOnsicyI6ImIifX30yF4BEFG97OiPWiZ3SQ2BZlmodwrpbBLUt-jpqWl1lA"},
		{query: "SELECT \"id\"::text, \"name\", \"age\", \"id\", \"name\", \"age\" FROM \"widgets\" AS \"_listing\" WHERE ((\"name\" <> ($1)::text)) AND (\"tenant\" = $2) AND (((\"_listing\".\"name\" < $3 OR (\"_listing\".\"name\" = $4 AND ((\"_listing\".\"age\" > $5 OR (\"_listing\".\"age\" = $6 AND (\"_listing\".\"id\" > $7)))))))) ORDER BY \"_listing\".\"name\" DESC, \"_listing\".\"age\" ASC, \"_listing\".\"id\" ASC LIMIT 3", args: "[]interface {}{\"zulu\", \"acme\", \"bravo\", \"bravo\", 8, 8, \"b\"}", token: "AQAAAZt8qYyIzs6qDPLnfxjlZ_ar2UDmkP6jCcMqhhv0zNLLZuamf2us6Jy5ohSYx5Z1YRphkxu6K3siayI6W3sicyI6ImJyYXZvIn0seyJuIjo4fV0sImkiOnsicyI6ImIifX30yF4BEFG97OiPWiZ3SQ2BZlmodwrpbBLUt-jpqWl1lA"},
	},
	"clickhouse/declared": {
		{query: "SELECT `id`, `age`, `id`, `name`, `age` FROM `widgets` AS `_listing` WHERE ((`name` != ?)) AND (`tenant` = ?) ORDER BY `_listing`.`age` DESC, `_listing`.`id` DESC LIMIT 3", args: "[]interface {}{\"zulu\", \"acme\"}", token: "AQAAAZt8qYyI5z02X819ityp35bQ9psfov6jCcMqhhv0zNLLZuamf2us6Jy5ohSYx5Z1YRphkxu6HXsiayI6W3sibiI6OH1dLCJpIjp7InMiOiJiIn19XCrp2xx1eyhfTYz9U0dXsq-aMUMTHl8HZAHeXQo6r00"},
		{query: "SELECT `id`, `age`, `id`, `name`, `age` FROM `widgets` AS `_listing` WHERE ((`name` != ?)) AND (`tenant` = ?) AND (((`_listing`.`age` < ? OR (`_listing`.`age` = ? AND `_listing`.`id` < ?)))) ORDER BY `_listing`.`age` DESC, `_listing`.`id` DESC LIMIT 3", args: "[]interface {}{\"zulu\", \"acme\", 8, 8, \"b\"}", token: "AQAAAZt8qYyI5z02X819ityp35bQ9psfov6jCcMqhhv0zNLLZuamf2us6Jy5ohSYx5Z1YRphkxu6HXsiayI6W3sibiI6OH1dLCJpIjp7InMiOiJiIn19XCrp2xx1eyhfTYz9U0dXsq-aMUMTHl8HZAHeXQo6r00"},
	},
	"mariadb/declared": {
		{query: "SELECT CAST(`id` AS CHAR), `age`, `id`, `name`, `age` FROM `widgets` AS `_listing` WHERE ((`name` <> ?)) AND (`tenant` = ?) ORDER BY `_listing`.`age` DESC, `_listing`.`id` DESC LIMIT 3", args: "[]interface {}{\"zulu\", \"acme\"}", token: "AQAAAZt8qYyI5z02X819ityp35bQ9psfov6jCcMqhhv0zNLLZuamf2us6Jy5ohSYx5Z1YRphkxu6HXsiayI6W3sibiI6OH1dLCJpIjp7InMiOiJiIn19XCrp2xx1eyhfTYz9U0dXsq-aMUMTHl8HZAHeXQo6r00"},
		{query: "SELECT CAST(`id` AS CHAR), `age`, `id`, `name`, `age` FROM `widgets` AS `_listing` WHERE ((`name` <> ?)) AND (`tenant` = ?) AND (((`_listing`.`age` < ? OR (`_listing`.`age` = ? AND `_listing`.`id` < ?)))) ORDER BY `_listing`.`age` DESC, `_listing`.`id` DESC LIMIT 3", args: "[]interface {}{\"zulu\", \"acme\", 8, 8, \"b\"}", token: "AQAAAZt8qYyI5z02X819ityp35bQ9psfov6jCcMqhhv0zNLLZuamf2us6Jy5ohSYx5Z1YRphkxu6HXsiayI6W3sibiI6OH1dLCJpIjp7InMiOiJiIn19XCrp2xx1eyhfTYz9U0dXsq-aMUMTHl8HZAHeXQo6r00"},
	},
	"clickhouse/unrestricted": {
		{query: "SELECT `id`, `name`, `age`, `id`, `name`, `age` FROM `widgets` AS `_listing` WHERE ((`name` != ?)) AND (`tenant` = ?) ORDER BY `_listing`.`name` DESC, `_listing`.`age` ASC, `_listing`.`id` ASC LIMIT 3", args: "[]interface {}{\"zulu\", \"acme\"}", token: "AQAAAZt8qYyIzs6qDPLnfxjlZ_ar2UDmkP6jCcMqhhv0zNLLZuamf2us6Jy5ohSYx5Z1YRphkxu6K3siayI6W3sicyI6ImJyYXZvIn0seyJuIjo4fV0sImkiOnsicyI6ImIifX30yF4BEFG97OiPWiZ3SQ2BZlmodwrpbBLUt-jpqWl1lA"},
		{query: "SELECT `id`, `name`, `age`, `id`, `name`, `age` FROM `widgets` AS `_listing` WHERE ((`name` != ?)) AND (`tenant` = ?) AND (((`_listing`.`name` < ? OR (`_listing`.`name` = ? AND (`_listing`.`age` > ? OR (`_listing`.`age` = ? AND `_listing`.`id` > ?)))))) ORDER BY `_listing`.`name` DESC, `_listing`.`age` ASC, `_listing`.`id` ASC LIMIT 3", args: "[]interface {}{\"zulu\", \"acme\", \"bravo\", \"bravo\", 8, 8, \"b\"}", token: "AQAAAZt8qYyIzs6qDPLnfxjlZ_ar2UDmkP6jCcMqhhv0zNLLZuamf2us6Jy5ohSYx5Z1YRphkxu6K3siayI6W3sicyI6ImJyYXZvIn0seyJuIjo4fV0sImkiOnsicyI6ImIifX30yF4BEFG97OiPWiZ3SQ2BZlmodwrpbBLUt-jpqWl1lA"},
	},
	"mariadb/unrestricted": {
		{query: "SELECT CAST(`id` AS CHAR), `name`, `age`, `id`, `name`, `age` FROM `widgets` AS `_listing` WHERE ((`name` <> ?)) AND (`tenant` = ?) ORDER BY `_listing`.`name` DESC, `_listing`.`age` ASC, `_listing`.`id` ASC LIMIT 3", args: "[]interface {}{\"zulu\", \"acme\"}", token: "AQAAAZt8qYyIzs6qDPLnfxjlZ_ar2UDmkP6jCcMqhhv0zNLLZuamf2us6Jy5ohSYx5Z1YRphkxu6K3siayI6W3sicyI6ImJyYXZvIn0seyJuIjo4fV0sImkiOnsicyI6ImIifX30yF4BEFG97OiPWiZ3SQ2BZlmodwrpbBLUt-jpqWl1lA"},
		{query: "SELECT CAST(`id` AS CHAR), `name`, `age`, `id`, `name`, `age` FROM `widgets` AS `_listing` WHERE ((`name` <> ?)) AND (`tenant` = ?) AND (((`_listing`.`name` < ? OR (`_listing`.`name` = ? AND (`_listing`.`age` > ? OR (`_listing`.`age` = ? AND `_listing`.`id` > ?)))))) ORDER BY `_listing`.`name` DESC, `_listing`.`age` ASC, `_listing`.`id` ASC LIMIT 3", args: "[]interface {}{\"zulu\", \"acme\", \"bravo\", \"bravo\", 8, 8, \"b\"}", token: "AQAAAZt8qYyIzs6qDPLnfxjlZ_ar2UDmkP6jCcMqhhv0zNLLZuamf2us6Jy5ohSYx5Z1YRphkxu6K3siayI6W3sicyI6ImJyYXZvIn0seyJuIjo4fV0sImkiOnsicyI6ImIifX30yF4BEFG97OiPWiZ3SQ2BZlmodwrpbBLUt-jpqWl1lA"},
	},
	"postgres/declared": {
		{query: "SELECT \"id\"::text, \"age\", \"id\", \"name\", \"age\" FROM \"widgets\" AS \"_listing\" WHERE ((\"name\" <> ($1)::text)) AND (\"tenant\" = $2) ORDER BY \"_listing\".\"age\" DESC, \"_listing\".\"id\" DESC LIMIT 3", args: "[]interface {}{\"zulu\", \"acme\"}", token: "AQAAAZt8qYyI5z02X819ityp35bQ9psfov6jCcMqhhv0zNLLZuamf2us6Jy5ohSYx5Z1YRphkxu6HXsiayI6W3sibiI6OH1dLCJpIjp7InMiOiJiIn19XCrp2xx1eyhfTYz9U0dXsq-aMUMTHl8HZAHeXQo6r00"},
		{query: "SELECT \"id\"::text, \"age\", \"id\", \"name\", \"age\" FROM \"widgets\" AS \"_listing\" WHERE ((\"name\" <> ($1)::text)) AND (\"tenant\" = $2) AND ((\"_listing\".\"age\", \"_listing\".\"id\") < ($3, $4)) ORDER BY \"_listing\".\"age\" DESC, \"_listing\".\"id\" DESC LIMIT 3", args: "[]interface {}{\"zulu\", \"acme\", 8, \"b\"}", token: "AQAAAZt8qYyI5z02X819ityp35bQ9psfov6jCcMqhhv0zNLLZuamf2us6Jy5ohSYx5Z1YRphkxu6HXsiayI6W3sibiI6OH1dLCJpIjp7InMiOiJiIn19XCrp2xx1eyhfTYz9U0dXsq-aMUMTHl8HZAHeXQo6r00"},
	},
}

func goldenEngine(t *testing.T, dialect string, unrestricted bool, reply func() sqlbasetest.Reply) (*sqlbase.ListEngine[row], *sqlbasetest.FakeDB) {
	t.Helper()

	var profile *listing.Profile
	if unrestricted {
		profile = listing.NewProfile(listing.WithUnrestrictedOrder(true))
	}

	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "name", Kind: filter.FieldKindString},
		{Name: "age", Kind: filter.FieldKindInt}}, []string{"age desc"}, profile)
	require.NoError(t, err)

	db, fake := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply { return reply() })
	codec := pagetokentest.Codec(t, pagetoken.WithClock(func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }), pagetoken.WithExpiryDisabled(true))
	dest := func(r *row) []any { return []any{&r.ID, &r.Name, &r.Age} }
	opts := []sqlbase.ListEngineOption{sqlbase.WithListScopeKeys("tenant")}

	var list *sqlbase.ListEngine[row]

	switch dialect {
	case "postgres":
		list, err = sqlbase.NewListEngine[row](db, codec, schema, "widgets", []string{"id", "name", "age"}, dest, postgres.ListDialect{}, opts...)
	case "mariadb":
		list, err = sqlbase.NewListEngine[row](db, codec, schema, "widgets", []string{"id", "name", "age"}, dest, mariadb.ListDialect{}, opts...)
	default:
		list, err = clickhouse.NewListEngine[row](db, codec, schema, "widgets", []string{"id", "name", "age"}, dest, opts...)
	}

	require.NoError(t, err)

	return list, fake
}

func TestListQueryGolden(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		dialect      string
		unrestricted bool
		orderBy      string
		rows         [][]driver.Value
	}{
		{"postgres", false, "age desc", ageRows()},
		{"mariadb", false, "age desc", ageRows()},
		{"clickhouse", false, "age desc", ageRows()},
		{"postgres", true, "name desc, age", nameAgeRows()},
		{"mariadb", true, "name desc, age", nameAgeRows()},
		{"clickhouse", true, "name desc, age", nameAgeRows()},
	} {
		name := tc.dialect + "/declared"
		if tc.unrestricted {
			name = tc.dialect + "/unrestricted"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			columns := make([]string, len(tc.rows[0]))
			for i := range columns {
				columns[i] = fmt.Sprintf("c%d", i)
			}

			list, fake := goldenEngine(t, tc.dialect, tc.unrestricted, func() sqlbasetest.Reply {
				return sqlbasetest.Reply{Columns: columns, Rows: tc.rows}
			})

			req := listing.Request{PageSize: 2, OrderBy: tc.orderBy, Filter: `name != "zulu"`}

			var got [2]goldenCall

			for i := range got {
				out, err := list.List(t.Context(), "widgets.List", req, sqlbase.Scope{"tenant": "acme"})
				require.NoError(t, err)
				require.Len(t, out.Items, 2)

				call := fake.Calls()[i]
				got[i] = goldenCall{query: call.Query, args: fmt.Sprintf("%#v", call.Args), token: out.NextPageToken}
				req.PageToken = out.NextPageToken
			}

			if os.Getenv("SQLBASE_GOLDEN_PRINT") != "" {
				t.Logf("%q: {\n\t{query: %q, args: %q, token: %q},\n\t{query: %q, args: %q, token: %q},\n},", name,
					got[0].query, got[0].args, got[0].token, got[1].query, got[1].args, got[1].token)

				return
			}

			require.Equal(t, listQueryGolden[name], got)
		})
	}
}

// ageRows returns three rows for the order "age desc": identity, age, then the projection.
func ageRows() [][]driver.Value {
	return [][]driver.Value{
		{"c", int64(9), "c", "charlie", int64(9)},
		{"b", int64(8), "b", "bravo", int64(8)},
		{"a", int64(7), "a", "alpha", int64(7)},
	}
}

// nameAgeRows returns three rows for the order "name desc, age": identity, name, age, then the projection.
func nameAgeRows() [][]driver.Value {
	return [][]driver.Value{
		{"c", "charlie", int64(9), "c", "charlie", int64(9)},
		{"b", "bravo", int64(8), "b", "bravo", int64(8)},
		{"a", "alpha", int64(7), "a", "alpha", int64(7)},
	}
}

// TestListChecksEveryScannedRow keeps the per-row boundary checks on rows whose cursor position is not built: the
// look-ahead row and the rows before the last item.
func TestListChecksEveryScannedRow(t *testing.T) {
	t.Parallel()

	aligned := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	unaligned := aligned.Add(time.Nanosecond)

	for _, tc := range []struct {
		name  string
		codec bool
		at    [3]time.Time
	}{
		{"first row without codec", false, [3]time.Time{unaligned, aligned, aligned}},
		{"first row with codec", true, [3]time.Time{unaligned, aligned, aligned}},
		{"look-ahead row", true, [3]time.Time{aligned, aligned, unaligned}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rows := make([][]driver.Value, len(tc.at))
			for i, at := range tc.at {
				id := fmt.Sprint(i)
				rows[i] = []driver.Value{id, at, id}
			}

			db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
				return sqlbasetest.Reply{Columns: []string{"id", "at", "id"}, Rows: rows}
			})
			schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}, {Name: "at", Kind: filter.FieldKindTimestamp}}, []string{"at"}, nil)
			require.NoError(t, err)

			var codec pagetoken.Signer
			if tc.codec {
				codec = pagetokentest.Codec(t)
			}

			l, err := sqlbase.NewListEngine[row](db, codec, schema, "items", []string{"id"}, func(r *row) []any { return []any{&r.ID} }, postgres.ListDialect{})
			require.NoError(t, err)
			_, err = l.List(t.Context(), "items.List", listing.Request{PageSize: 2, OrderBy: "at"}, nil)
			require.ErrorIs(t, err, sqlbase.ErrCorruptRecord)
			require.ErrorIs(t, apierror.HiddenCause(err), pagetoken.ErrUnsortableValue)
			require.ErrorContains(t, apierror.HiddenCause(err), "decode sort key")
		})
	}
}

// failingScanner is a scan destination whose Scan fails with err.
type failingScanner struct{ err error }

func (f failingScanner) Scan(any) error { return f.err }

// TestListClassifiesUnreadableRows checks a row whose destination fails to scan is a corrupt record whatever the destination
// returned: an API error mapper classifies it INTERNAL without its text, and the scan error stays in the chain. A rule converting
// the corrupt record to an API error of its own, without a cause, keeps the scan error as the mapped error's hidden cause too
// (review finding R9-F2).
func TestListClassifiesUnreadableRows(t *testing.T) {
	t.Parallel()

	mapper, err := apierror.NewMapper(apierror.WithRules(aipmap.Rules()...))
	require.NoError(t, err)

	converting, err := apierror.NewMapper(apierror.WithRules(apierror.Rule{
		Target: sqlbase.ErrCorruptRecord, Code: code.Code_DATA_LOSS,
		Convert: func(error) *apierror.Error { return apierror.MustNew(code.Code_DATA_LOSS, "stored record is corrupt") },
	}))
	require.NoError(t, err)

	for name, cause := range map[string]error{
		"deadline":    fmt.Errorf("stored value: %w", context.DeadlineExceeded),
		"token":       fmt.Errorf("stored value %q: %w", "secret", pagetoken.ErrInvalidToken),
		"field error": coreerrs.NewFieldError("pageToken", "stored value secret"),
		// A scan error whose chain already classifies one cause is classified as a whole: the sibling selects nothing.
		"classified and deadline": errors.Join(apierror.Classify(sqlbase.ErrCorruptRecord, errors.New("bad")), context.DeadlineExceeded),
		"classified and api error": errors.Join(apierror.Classify(sqlbase.ErrCorruptRecord, errors.New("bad")),
			apierror.MustNew(code.Code_INVALID_ARGUMENT, "secret stored value")),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
				return sqlbasetest.Reply{Columns: []string{"id", "id"}, Rows: [][]driver.Value{{"a", "a"}}}
			})
			schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
			require.NoError(t, err)

			l, err := sqlbase.NewListEngine[row](db, nil, schema, "items", []string{"id"},
				func(*row) []any { return []any{failingScanner{err: cause}} }, postgres.ListDialect{})
			require.NoError(t, err)

			_, err = l.List(t.Context(), "items.List", listing.Request{PageSize: 2}, nil)
			require.ErrorIs(t, err, sqlbase.ErrCorruptRecord)
			require.NotErrorIs(t, err, cause)
			require.ErrorIs(t, apierror.HiddenCause(err), cause)

			e := mapper.Map(err)
			require.Equal(t, code.Code_INTERNAL, e.Code())
			require.Equal(t, apierror.MessageInternal, e.Message())
			require.Empty(t, e.FieldViolations())
			require.ErrorIs(t, apierror.HiddenCause(e), cause)

			e = converting.Map(err)
			require.Equal(t, code.Code_DATA_LOSS, e.Code())
			require.Equal(t, "stored record is corrupt", e.Message())
			require.ErrorIs(t, apierror.HiddenCause(e), cause)
			require.NotErrorIs(t, e, cause)
		})
	}
}

// ctxQuerier runs queries on db and hands each query context to queried. db runs the query detached from the end of that
// context, so neither the query start nor rows.Next can observe the end first: only the destination or signer waiting on the
// handed context does, which makes the order "the query context ended, then Scan or Issue returned its error" deterministic
// whenever the time cap fires.
type ctxQuerier struct {
	db      sqlbase.Querier
	queried chan<- context.Context
}

func (q ctxQuerier) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	q.queried <- ctx

	return q.db.QueryContext(context.WithoutCancel(ctx), query, args...)
}

// blockingScanner is a scan destination whose Scan returns err once the query context it receives has ended, as when the time
// cap or the caller's context ends during a slow Scan.
type blockingScanner struct {
	queried <-chan context.Context
	before  func()
	err     error
}

func (b blockingScanner) Scan(any) error {
	qctx := <-b.queried
	if b.before != nil {
		b.before()
	}

	<-qctx.Done()

	return b.err
}

// secretScanCauses are scan errors that would select a client status or expose a stored value if they were visible.
func secretScanCauses() map[string]error {
	return map[string]error{
		"field error": coreerrs.NewFieldError("storedField", "secret stored value"),
		"api error":   apierror.MustNew(code.Code_INVALID_ARGUMENT, "secret stored value", apierror.WithReason("SECRET_REASON")),
		"token":       fmt.Errorf("stored value %q: %w", "secret", pagetoken.ErrInvalidToken),
		"deadline":    fmt.Errorf("secret stored value: %w", context.DeadlineExceeded),
	}
}

// requireNoScanSecret checks no text of the mapped error names the hidden scan error.
func requireNoScanSecret(t testing.TB, e *apierror.Error) {
	t.Helper()

	texts := []string{e.Message(), e.Error(), e.Reason(), prototext.Format(e.Proto())}
	for _, v := range e.FieldViolations() {
		texts = append(texts, v.GetField(), v.GetDescription())
	}

	for _, text := range texts {
		require.NotContains(t, strings.ToLower(text), "secret")
		require.NotContains(t, text, "storedField")
	}
}

// TestListHidesScanErrorsWhenTheQueryEnds checks a destination's Scan failing after the query context ended reports the end of
// the query alone — the time cap as ErrTimeout (on filter when one was given, else unavailable), the caller's context as its
// error — whatever the Scan returned: neither its status, its text nor its sentinel is visible, and apierror.HiddenCause keeps it
// for logs.
func TestListHidesScanErrorsWhenTheQueryEnds(t *testing.T) {
	t.Parallel()

	mapper, err := apierror.NewMapper(apierror.WithRules(aipmap.Rules()...))
	require.NoError(t, err)

	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
	require.NoError(t, err)

	newEngine := func(t *testing.T, before func(), cause error, opts ...sqlbase.ListEngineOption) *sqlbase.ListEngine[row] {
		t.Helper()

		db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
			return sqlbasetest.Reply{Columns: []string{"id", "id"}, Rows: [][]driver.Value{{"a", "a"}}}
		})
		queried := make(chan context.Context, 1)
		l, err := sqlbase.NewListEngine[row](ctxQuerier{db: db, queried: queried}, nil, schema, "items", []string{"id"},
			func(*row) []any { return []any{blockingScanner{queried: queried, before: before, err: cause}} }, postgres.ListDialect{},
			opts...)
		require.NoError(t, err)

		return l
	}

	for name, cause := range secretScanCauses() {
		t.Run(name+"/time cap", func(t *testing.T) {
			t.Parallel()

			for _, filtered := range []bool{false, true} {
				req := listing.Request{PageSize: 2}
				if filtered {
					req.Filter = `id == "a"`
				}

				_, err := newEngine(t, nil, cause, sqlbase.WithListMaxTime(time.Millisecond)).List(t.Context(), "items.List", req, nil)
				require.ErrorIs(t, err, sqlbase.ErrTimeout)
				require.NotErrorIs(t, err, cause)
				require.NotErrorIs(t, err, sqlbase.ErrCorruptRecord)
				require.ErrorIs(t, apierror.HiddenCause(err), cause, "the scan error stays for logs")
				require.Contains(t, apierror.HiddenCause(err).Error(), "secret")

				e := mapper.Map(err)
				requireNoScanSecret(t, e)

				if filtered {
					require.Equal(t, code.Code_INVALID_ARGUMENT, e.Code())
					require.Len(t, e.FieldViolations(), 1)
					require.Equal(t, listing.FieldFilter, e.FieldViolations()[0].GetField())
				} else {
					require.ErrorIs(t, err, coreerrs.ErrUnavailable)
					require.Equal(t, code.Code_UNAVAILABLE, e.Code())
					require.Empty(t, e.FieldViolations())
				}
			}
		})

		t.Run(name+"/canceled", func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			_, err := newEngine(t, cancel, cause).List(ctx, "items.List", listing.Request{PageSize: 2, Filter: `id == "a"`}, nil)
			require.ErrorIs(t, err, context.Canceled)
			require.NotErrorIs(t, err, cause)
			require.NotErrorIs(t, err, sqlbase.ErrTimeout)
			require.ErrorIs(t, apierror.HiddenCause(err), cause)

			e := mapper.Map(err)
			require.Equal(t, code.Code_CANCELLED, e.Code()) //nolint:misspell // google.rpc.Code
			requireNoScanSecret(t, e)
		})

		t.Run(name+"/error mapper", func(t *testing.T) {
			t.Parallel()

			var seen error

			_, err := newEngine(t, nil, cause, sqlbase.WithListMaxTime(time.Millisecond),
				sqlbase.WithListErrorMapper(func(_ context.Context, err error, _ bool) error {
					seen = err

					return err
				})).List(t.Context(), "items.List", listing.Request{PageSize: 2}, nil)
			require.Same(t, seen, err)
			require.ErrorIs(t, seen, context.DeadlineExceeded, "the mapper receives the query context's error")
			require.NotErrorIs(t, seen, cause)
			require.NotContains(t, seen.Error(), "secret")
			require.ErrorIs(t, apierror.HiddenCause(seen), cause)
		})
	}
}

// endingSigner resolves tokens with signer and fails to issue one with err once the query context it receives has ended, as a
// signer or a position codec failing after the time cap or the caller's context ended.
type endingSigner struct {
	pagetoken.Signer

	queried <-chan context.Context
	before  func()
	err     error
}

func (s endingSigner) Issue([]byte, pagetoken.Bindings) (string, error) {
	qctx := <-s.queried
	if s.before != nil {
		s.before()
	}

	<-qctx.Done()

	return "", s.err
}

// TestListHidesTokenErrorsWhenTheQueryEnds: review finding R9-F3 — once the rows were scanned, a failure issuing the next page
// token was returned as it was, so a signer or position codec failing after the time cap or the caller's context ended could
// return a field or API error selecting a client status and exposing its text. It reports the end of the query alone, as a scan
// failing then does, the issuance error hidden for logs.
func TestListHidesTokenErrorsWhenTheQueryEnds(t *testing.T) {
	t.Parallel()

	mapper, err := apierror.NewMapper(apierror.WithRules(aipmap.Rules()...))
	require.NoError(t, err)

	schema, err := listing.NewSchema([]listing.Field{{Name: "id", Kind: filter.FieldKindString}}, []string{"id"}, nil)
	require.NoError(t, err)

	newEngine := func(t *testing.T, before func(), cause error, opts ...sqlbase.ListEngineOption) *sqlbase.ListEngine[row] {
		t.Helper()

		db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
			return sqlbasetest.Reply{Columns: []string{"id", "id"}, Rows: [][]driver.Value{{"a", "a"}, {"b", "b"}}}
		})
		queried := make(chan context.Context, 1)
		signer := endingSigner{Signer: pagetokentest.Codec(t), queried: queried, before: before, err: cause}
		l, err := sqlbase.NewListEngine[row](ctxQuerier{db: db, queried: queried}, signer, schema, "items", []string{"id"},
			func(r *row) []any { return []any{&r.ID} }, postgres.ListDialect{}, opts...)
		require.NoError(t, err)

		return l
	}

	for name, cause := range secretScanCauses() {
		t.Run(name+"/time cap", func(t *testing.T) {
			t.Parallel()

			for _, filtered := range []bool{false, true} {
				req := listing.Request{PageSize: 1}
				if filtered {
					req.Filter = `id != "z"`
				}

				_, err := newEngine(t, nil, cause, sqlbase.WithListMaxTime(time.Millisecond)).List(t.Context(), "items.List", req, nil)
				require.ErrorIs(t, err, sqlbase.ErrTimeout)
				require.NotErrorIs(t, err, cause)
				require.ErrorIs(t, apierror.HiddenCause(err), cause, "the issuance error stays for logs")

				e := mapper.Map(err)
				requireNoScanSecret(t, e)

				if filtered {
					require.Equal(t, code.Code_INVALID_ARGUMENT, e.Code())
					require.Len(t, e.FieldViolations(), 1)
					require.Equal(t, listing.FieldFilter, e.FieldViolations()[0].GetField())
				} else {
					require.Equal(t, code.Code_UNAVAILABLE, e.Code())
					require.Empty(t, e.FieldViolations())
				}
			}
		})

		t.Run(name+"/canceled", func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			_, err := newEngine(t, cancel, cause).List(ctx, "items.List", listing.Request{PageSize: 1}, nil)
			require.ErrorIs(t, err, context.Canceled)
			require.NotErrorIs(t, err, cause)
			require.NotErrorIs(t, err, sqlbase.ErrTimeout)
			require.ErrorIs(t, apierror.HiddenCause(err), cause)

			e := mapper.Map(err)
			require.Equal(t, code.Code_CANCELLED, e.Code()) //nolint:misspell // google.rpc.Code
			requireNoScanSecret(t, e)
		})

		t.Run(name+"/error mapper", func(t *testing.T) {
			t.Parallel()

			var seen error

			_, err := newEngine(t, nil, cause, sqlbase.WithListMaxTime(time.Millisecond),
				sqlbase.WithListErrorMapper(func(_ context.Context, err error, _ bool) error {
					seen = err

					return err
				})).List(t.Context(), "items.List", listing.Request{PageSize: 1}, nil)
			require.Same(t, seen, err)
			require.ErrorIs(t, seen, context.DeadlineExceeded, "the mapper receives the query context's error")
			require.NotErrorIs(t, seen, cause)
			require.ErrorIs(t, apierror.HiddenCause(seen), cause)
		})
	}

	// While the query context lasts, an issuance failure is returned as it is.
	db, _ := sqlbasetest.NewFakeDB(t, func(string, []any) sqlbasetest.Reply {
		return sqlbasetest.Reply{Columns: []string{"id", "id"}, Rows: [][]driver.Value{{"a", "a"}, {"b", "b"}}}
	})
	failure := errors.New("signer unavailable")
	l, err := sqlbase.NewListEngine[row](db, failingSigner{Signer: pagetokentest.Codec(t), err: failure}, schema, "items", []string{"id"},
		func(r *row) []any { return []any{&r.ID} }, postgres.ListDialect{})
	require.NoError(t, err)

	_, err = l.List(t.Context(), "items.List", listing.Request{PageSize: 1}, nil)
	require.ErrorIs(t, err, failure)
}

// failingSigner resolves tokens with signer and fails to issue one with err.
type failingSigner struct {
	pagetoken.Signer

	err error
}

func (s failingSigner) Issue([]byte, pagetoken.Bindings) (string, error) { return "", s.err }

// TestListIssuesTokenFromLastItem resumes after the last returned item, not after the look-ahead row.
func TestListIssuesTokenFromLastItem(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	list, fake := goldenEngine(t, "postgres", false, func() sqlbasetest.Reply {
		if calls.Add(1) == 1 {
			return sqlbasetest.Reply{Columns: []string{"a", "b", "c", "d", "e"}, Rows: ageRows()}
		}

		return sqlbasetest.Reply{Columns: []string{"a", "b", "c", "d", "e"}}
	})

	req := listing.Request{PageSize: 2, OrderBy: "age desc"}
	first, err := list.List(t.Context(), "widgets.List", req, sqlbase.Scope{"tenant": "acme"})
	require.NoError(t, err)
	require.Equal(t, []row{{ID: "c", Name: "charlie", Age: 9}, {ID: "b", Name: "bravo", Age: 8}}, first.Items)

	req.PageToken = first.NextPageToken
	last, err := list.List(t.Context(), "widgets.List", req, sqlbase.Scope{"tenant": "acme"})
	require.NoError(t, err)
	require.Empty(t, last.Items)
	require.Empty(t, last.NextPageToken)
	require.Equal(t, []any{"acme", int64(8), "b"}, fake.Calls()[1].Args)
}
