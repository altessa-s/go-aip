# sqlbasetest

```go
import "github.com/altessa-s/go-aip/backend/sqlbase/sqlbasetest"
```

Test kit of [backend/sqlbase](../README.md) and of SQL dialects built on it, in this repository or out of it. `NewFakeDB` opens a `*sql.DB` over a
scripted `database/sql` driver that records every statement and answers from a script, so a list engine over a custom `sqlbase.ListDialect`,
index management or schema checks are testable for statement shape, argument order and result mapping without a server or a third-party
driver. The package imports no database driver.

## Key types

| Symbol                 | Description                                                                                                   |
|------------------------|---------------------------------------------------------------------------------------------------------------|
| `NewFakeDB(tb, reply)` | Opens a `*sql.DB` over a new `FakeDB`, closed through `tb.Cleanup`; a nil `reply` succeeds affecting one row  |
| `FakeDB`               | The scripted driver (a `driver.Connector`): `Calls`, `Commits`, `Rollbacks`, `RowsClosed`, `DisableRecording` |
| `Reply`                | The answer to one statement: `Columns` and `Rows` for a query, `Affected` for an exec, or `Err` for both      |
| `Call`                 | One received statement: `Query`, `Args` (the driver values) and `InTx`                                        |

## Semantics

- Statements are answered in the order they arrive; the reply function receives the query text and the argument values.
- A prepared statement records each execution like a direct one. Its statements serve only the context methods (`ExecContext`,
  `QueryContext`, which `database/sql` always uses); the legacy `driver.Stmt` `Exec`/`Query`, called directly on the driver, return an error.
- A transaction only marks the statements issued inside it (`Call.InTx`) and counts commits and rollbacks; nothing is undone.
- `DisableRecording` keeps the script answering but stops collecting `Calls`, so a benchmark issuing millions of statements runs in constant
  memory.
- `FakeDB` is safe for concurrent use.

## Usage

```go
db, fake := sqlbasetest.NewFakeDB(t, func(query string, args []any) sqlbasetest.Reply {
	return sqlbasetest.Reply{Columns: []string{"id", "name"}, Rows: [][]driver.Value{{"1", "a"}}}
})
engine, err := sqlbase.NewListEngine[Widget](db, nil, schema, "widgets", []string{"id", "name"}, dest, myDialect{})
require.NoError(t, err)
_, err = engine.List(ctx, "widgets.List", listing.Request{PageSize: 10}, nil)
require.NoError(t, err)
calls := fake.Calls() // the SELECT the dialect rendered, with its bound arguments
```

## Related packages

- [backend/sqlbase](../README.md): the list engine, dialect ports and SQL style helpers.
- [listing/listingtest](../../../listing/listingtest/README.md): the list engine conformance suite.
