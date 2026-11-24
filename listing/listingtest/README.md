# listingtest

```go
import "github.com/altessa-s/go-aip/listing/listingtest"
```

The conformance suite of [`listing`](../README.md) engines: every engine runs `Run` to prove it serves the List contract exactly as
the others do. Engines without array storage can use `RunWithoutArrays`; it removes the repeated fixture field and keeps the remaining
scenarios. Each scenario checks the engine against an expectation computed independently of the package.

## Contract

`Run(t, factory, opts...)` runs shared request validation, filter semantics, sort-pattern reversal, paging, presence, and token-binding cases. Factory
receives schema, pagetoken.Signer, and fixture items, and returns a Lister over the backend. Expectations are computed independently of the engine.

`RunWithoutArrays` uses the scalar-only contract for backends configured without repeated fields. `WithSchemaOptions` supplies listing.ProfileOption
values; `WithSignerFactory` replaces the test signer. Fixture builders, Values, ID, and Collect are available to backend tests.

## Live runs

| Engine     | Where it runs                                                                           |
|------------|-----------------------------------------------------------------------------------------|
| memory     | `backend/memory`, `go test ./...`                                                       |
| PostgreSQL | `integration/docker/postgres/list_test.go`, against a live server                       |
| MariaDB    | `integration/docker/mariadb/list_test.go`, against a live server                        |
| ClickHouse | `integration/docker/clickhouse/list_test.go`, against a live server on MergeTree tables |
| MongoDB    | `integration/docker/mongo/list_test.go`, against a live server                          |

The SQL and MongoDB runs live in the test-only module [`integration/docker`](../../integration/docker/README.md), build only with the
`integration_docker` tag and start their server in Docker (each run skips without Docker): `make test-integration-docker`, or
`go -C integration/docker test -race -tags integration_docker ./...`. Each stores the fixture under the listing storage contract of its backend;
a SQL `EnsureIndexes` (which runs `CheckSchema` first) must accept the table, and the MongoDB run also checks every order against its index
with explain.

## Usage

```go
func TestConformance(t *testing.T) {
    t.Parallel()
    listingtest.Run(t, func(t *testing.T, s *listing.Schema, codec pagetoken.Signer, items []listingtest.Widget) listingtest.Lister {
        return newEngineList(t, s, codec, items) // a fresh store holding items
    })
}
```

## Scenarios

| Scenario                                     | Proves                                                                                                                 |
|----------------------------------------------|------------------------------------------------------------------------------------------------------------------------|
| `PagesEveryOrder`                            | Every accepted order (patterns, reversals, multi-key, id) pages to the expected sequence at page sizes 1–500           |
| `IdentityOrders`                             | An empty (identity-only) order and an explicit descending identity suffix page across several pages                    |
| `PagesFiltered`                              | Filtered lists page in both directions of multi-key orders                                                             |
| `MatchesContract`                            | Optional zero values, empty arrays, `size()`, timestamps, bools and string functions follow the shared proto3 contract |
| `DefaultEqualities`                          | A default restriction applies until the filter refers to its field                                                     |
| `Restriction`                                | `Request.Restriction` applies to a blank, narrowing or widening filter and after the default equalities, on every page |
| `Cutoff`                                     | A changing cutoff preserves the cursor and filters each page. `WithCutoffRefusal(true)` requires explicit refusal. SQL suites also run `RunCutoffCount`. |
| `RestrictionTokenBinding`                    | A token binds the restriction (another or none is refused); a refused restriction is `listing.ErrInvalidRestriction`   |
| `OrdersTimestampsAtMillis`                   | Times below `listing.TimestampPrecision` tie and break by id                                                           |
| `TokenBinding`                               | A token binds subject, parent, order and filter meaning — not its spelling or the page size                            |
| `RejectsRequests`                            | Every invalid request is a field error on the right field with the cause                                               |
| `PageSize`, `WithoutCodec`, `ParentRequired` | Page-size options, single-page lists and the required parent                                                           |

## Fixture

| Symbol                                                       | Purpose                                                                                              |
|--------------------------------------------------------------|------------------------------------------------------------------------------------------------------|
| `Widget`                                                     | The resource, with the bson tags of its stored form                                                  |
| `Fields`, `Patterns`, `Functions`, `NewSchema(tb, opts...)`  | The widget schema                                                                                    |
| `NewSchemaWithoutArrays(tb, opts...)`                        | The widget schema without its repeated field                                                         |
| `FieldPaths`                                                 | The stored paths of the fields whose public name differs (`id` → `_id`, `createTime` → `created_at`) |
| `MakeWidgets(n)`, `Values`, `ID`                             | Items and their in-memory projection                                                                 |
| `Collect(tb, lister, req)`, `IDs`, `FieldOf(err)`, `Subject` | Helpers for an engine's own tests                                                                    |

## Isolated configuration

| API                                                     | Purpose                                    |
|---------------------------------------------------------|--------------------------------------------|
| `Fields()`, `Functions()`, `Patterns()`, `FieldPaths()` | Fresh fixture declarations for each caller |
| `KindEnum()`, `OptionalKindEnum()`                      | Fresh enum dictionaries                    |

`Run` and `RunWithoutArrays` accept generated options. All mandatory cases remain enabled; injected dependencies must preserve the fixture contract.

Conformance includes empty identity-only orders and explicit descending identity suffixes, with continuation across multiple pages.

## Options

| Option              | Type     | Default               | Behavior                                                                                |
|---------------------|----------|-----------------------|-----------------------------------------------------------------------------------------|
| `WithSchemaOptions` | `Option` | nil / unset           | SchemaOptions configure fixture parsers and policies before scenario-specific settings. |
| `WithSignerFactory` | `Option` | fixed-key test signer | Replaces the token service without weakening the mandatory cases.                       |

## Named values

| Constant  | Meaning                                   |
|-----------|-------------------------------------------|
| `Subject` | "/acme.test.v1.WidgetService/ListWidgets" |

## API

| Symbol             | Purpose                                                                                                                                                                                                                                                                   |
|--------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Lister`           | Lister lists one page of widgets under subject.                                                                                                                                                                                                                           |
| `Factory`          | Factory returns the list of items under schema, served by the engine under test with codec signing its page tokens (nil: a single page). It is called once per scenario; a storage-backed engine stores items in a fresh collection.                                      |
| `Run`              | Run runs the conformance suite: every engine pages every order, matches every filter, binds and refuses tokens, and rejects every invalid request exactly as the others do. Each scenario checks the engine against an expectation computed independently of the package. |
| `RunWithoutArrays` | RunWithoutArrays runs the conformance suite on the same fixture without its repeated field. Array-specific cases are omitted; all other paging, filtering, token, validation and presence cases remain enabled.                                                           |
| `SignerFactory`    | SignerFactory constructs an independent signer for each conformance scenario.                                                                                                                                                                                             |
| `Option`           | Option is a functional option for configuring options.                                                                                                                                                                                                                    |

## Related packages

- [listing](../README.md)
- [backend/memory](../../backend/memory/README.md)
- [backend/mongo](../../backend/mongo/README.md#list-engine)
- [backend/sqlbase](../../backend/sqlbase/README.md#listing-engine), [backend/postgres](../../backend/postgres/README.md),
  [backend/mariadb](../../backend/mariadb/README.md), [backend/clickhouse](../../backend/clickhouse/README.md#list-engine)
- [listing guide](../../docs/listing.md)
