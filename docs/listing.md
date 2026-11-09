# Listing

[listing](../listing/) defines a List request independently of storage: page size, page token, CEL filter, declared order patterns, parent, and read
mask. Engines implement the same portable proto3 contract over memory ([backend/memory](../backend/memory/README.md)), PostgreSQL
([backend/postgres](../backend/postgres/README.md)), MariaDB ([backend/mariadb](../backend/mariadb/README.md)), ClickHouse
([backend/clickhouse](../backend/clickhouse/README.md)) and MongoDB ([backend/mongo](../backend/mongo/README.md#list-engine)).

## Schema and profile

`NewSchema(fields, patterns, profile)` captures immutable metadata. Use nil for the portable preset or construct a reusable `NewProfile(opts...)`.
`MustSchema` takes the same three arguments. Policy options are `ProfileOption`, not SchemaOption; field and enum snapshots cannot mutate a constructed
schema.

```go
package example

import (
    "context"

    "github.com/altessa-s/go-aip/backend/memory"
    "github.com/altessa-s/go-aip/filter"
    "github.com/altessa-s/go-aip/listing"
    "github.com/altessa-s/go-aip/pagination/pagetoken"
    "github.com/altessa-s/go-aip/pagination/page"
)

type Widget struct {
    ID int64
    Name string
}

func ListWidgets(ctx context.Context, signer pagetoken.Signer, items []Widget, req listing.Request) (page.Page[Widget], error) {
    schema, err := listing.NewSchema([]listing.Field{
        {Name: "id", Kind: filter.FieldKindInt},
        {Name: "name", Kind: filter.FieldKindString},
    }, []string{"name"}, listing.NewProfile(listing.WithMaxPageSize(100)))
    if err != nil {
        return page.Page[Widget]{}, err
    }
    engine, err := memory.NewListEngine(signer, schema,
        func(w Widget) int64 { return w.ID },
        func(w Widget) map[string]any { return map[string]any{"id": w.ID, "name": w.Name} },
    )
    if err != nil {
        return page.Page[Widget]{}, err
    }
    lister := listing.Bind[Widget](engine, "widgets.List", items)
    return lister.List(ctx, req)
}
```

Construct schemas and engines once in application setup; the function shows the complete dependency wiring. Do not mutate a source bound to a Lister
while requests use it. `Engine[T, S]` exposes `List(context.Context, string, Request, S)`; `Lister[T]` exposes `List(context.Context, Request)` after
binding source and subject.

## Proto3 value contract

| Field declaration | Stored meaning                       | Filter behavior                                     | Default sorting           |
|-------------------|--------------------------------------|-----------------------------------------------------|---------------------------|
| Required scalar   | Present scalar                       | Two-valued comparison                               | Comparable scalar allowed |
| `Optional: true`  | Missing/null resolves to kind zero   | Two-valued implicit presence                        | Rejected                  |
| `Array: true`     | Missing/null resolves to empty array | Element predicates; inequality complements equality | Rejected                  |
| `Presence: true`  | Explicit nullable scalar             | Default evaluator's null semantics                  | Rejected                  |

Presence is incompatible with Optional and Array. Optional enums declare EnumZero whose stored value is the kind's zero. Enum names in filters are
converted to stored values; inputs and returned metadata are detached snapshots.

For implicit optional fields, null tests mean zero/nonzero; for arrays they mean empty/nonempty. `has` means nonzero or nonempty. For Presence fields,
null tests and `has` check actual presence. `size(null)` is zero. Otherwise a null Presence value follows the default evaluator: it equals null and
differs from every value, is in no list without a null element and contains no string, so `rating != 1`, `!(rating == 1)`, `!(rating in [1, 2])` and
`!score.startsWith("a")` select it; an ordered comparison and a bare boolean test on null are unknown, also under negation, and select nothing.
`&&` and `||` absorb unknown as SQL's `AND` and `OR` do, in either order (`rating > 1 || priority == 4` selects a null rating with priority 4).
Every engine agrees: the memory engine, the PostgreSQL, MariaDB and ClickHouse SQL that push `!` down to the predicates, and the MongoDB engine, which
puts the filter in negation normal form over the Presence fields.

Array equality, ordering, membership, and string predicates match any element. Array inequality matches when no element is equal. Required stored values
must satisfy their declared kind. All stored floats must be finite: [listing/evaluation](../listing/evaluation/README.md), which the memory engine
filters with, fails a record holding a NaN or an infinity with `evaluation.ErrNonFiniteFloat` (naming the field), and the memory engine fails a
non-finite sort or identity value with `pagetoken.ErrUnsortableValue`.

Portable validation rejects empty membership lists, null membership elements, ordered comparisons with null, and unsupported regex shapes. Builtins are
contains, startsWith, endsWith, size, and timestamp; operators and the has macro remain grammar. Custom functions can expand to validated predicates.
Timestamp literals and default equalities normalize to UTC at profile precision, one millisecond by default.

## Orders, masks, and errors

A request order must be a declared pattern or its full reversal. Prefixes and partial reversals are rejected. The first pattern is the default unless
WithDefaultOrderBy selects another accepted order. Engines add a unique identity in the last key's direction. WithEmptyOrderAllowed permits ID-only
ordering; WithUnrestrictedOrder accepts other combinations of declared eligible fields.

Hidden fields and fields outside Request.ReadMask cannot sort by default because signed tokens disclose sort values. WithUnreadableSortAllowed
explicitly permits them; use encrypted tokens when those values must remain confidential. Read masks select readable paths; they do not automatically
configure an engine's storage projection.

Default validation checks pageSize, parent, orderBy, and filter before the engine resolves pageToken. Page size zero becomes 50, values above 500 clamp,
and negatives fail. Default errors are field errors preserving sentinel causes. WithErrorFormatter changes schema and translation errors;
WithPageSizeResolver changes size policy. Engine execution failures have their own hooks (`sqlbase.WithListErrorMapper`,
`mongo.WithListErrorMapper`).

## Policies

Listing applies the parser / policy / translator model of the [root README](../README.md#parser--policy--translator) to its schema.
`NewSchema` builds the shared policies once: the filter policy from `Schema.FilterOptions()` and the order policy from
`Schema.OrderOptions()` (`Schema.OrderPolicy()`). Engines bind each policy to their storage mapping — for ordering an orderby translator or
`orderby.NewTranslatorContext` with the field mapping and tie-breakers — so every engine of a list authorizes requests the same way.

| API                          | Returns                                                                                                     |
|------------------------------|-------------------------------------------------------------------------------------------------------------|
| `Schema.OrderOptions()`      | `[]orderby.PolicyOption`: untrusted input, the sortable fields, and the default order in public names       |
| `Schema.OrderPolicy()`       | The `*orderby.Policy` built from `OrderOptions` in `NewSchema`                                              |
| `Schema.ProjectionOptions()` | `[]projection.PolicyOption`: the Hidden fields denied; callers append the allow-list and any resource views |

The sortable fields are the keys of the accepted orders or, under WithUnrestrictedOrder, every field that passes the pattern key checks; a schema with
no sortable field refuses every key. The schema cannot state the projection allow-list, because a list's fields are its filter fields rather than
every field of the resource: append `projection.WithAllowedFields` (a field access policy needs one too), and any resource views
(`projection.WithView`), to `Schema.ProjectionOptions()` before calling `projection.NewPolicy`. An omitted read mask returns every allowed field
except the Hidden ones (AIP-157); a List method that serves views should default to BASIC.

```go
policy, err := projection.NewPolicy(append(schema.ProjectionOptions(),
    projection.WithUntrustedInput(true),
    projection.WithAllowedFields("id", "name", "createTime"),
    projection.WithView(projection.ViewBasic, "id", "name"),
    projection.WithDefaultView(projection.ViewBasic),
)...)
fields, err := policy.Select(spec, view) // the mask if present, else the view
req.ReadMask = fields.Paths()            // what the response returns; nil for every field
```

When a view, not a mask, selects the fields, pass the selected paths as `Request.ReadMask` so the unreadable-sort check sees what the response
returns. See the [projection guide](projection.md).

## Server restriction

`Request.Restriction` is a server-built `filter.Node` that `Compile` ANDs after the client filter and the default equalities:
`(filter && defaults) && restriction`. It serves AIP-164 `show_deleted` (`softdelete.Policy.Restriction(showDeleted)` returns
`deleteTime == null` or nil) and tenant or caller scopes. It is part of `Query.Filter`, so every engine applies it and binds it into the page
token through `filter.Canonical`; a blank client filter still gets it, and a client filter can only narrow it.

| Rule                                                               | How it is met                                                                                                                                                                         |
|--------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| AIP-164: soft-deleted resources are excluded unless `show_deleted` | `Restriction = softdelete.Policy.Restriction(show)`; the client filter cannot widen the conjunction                                                                                   |
| AIP-158: a page token is rejected when the request changes         | The canonical filter includes the restriction: a token of `show_deleted=false` is `malformed` with `true`                                                                             |
| The restriction is trusted input                                   | Never read from the message; validated under the schema policy and the profile checks; a refusal is `ErrInvalidRestriction` (wraps `ErrInvalidSchema`, INTERNAL), never a field error |
| The client is not blamed for server limits                         | Restriction alone over the limits: `ErrInvalidRestriction`; only combined with the client filter: a field error on `filter`                                                           |

The AST skips the parser, so enum fields take stored values (a proto enum name is refused, not silently unmatched). The node is cloned per request
and timestamp literals are cut to the profile precision. It must be the same on every page of a walk. A field the client must not filter on at all
cannot appear in it (it shares the client's filter policy); scope such fields in the engine (`sqlbase.Scope`, the memory slice).

## Engines and storage

| Engine                                                                                                    | Source                                           | Requirements                                                                                                                                              |
|-----------------------------------------------------------------------------------------------------------|--------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------|
| [`memory.NewListEngine`](../backend/memory/README.md#list-engine), `MustNewListEngine`                    | `[]T`, typed id and field callbacks              | Small collections; filtering and sorting repeat per page; positive aligned cursor precision                                                               |
| [`postgres.NewListEngine`](../backend/postgres/README.md#list-engine), `MustNewListEngine`                | Querier, table, projection, scan callback, Scope | NOT NULL required/Optional columns, nullable Presence, C-collated text, arrays without null elements                                                      |
| [`mariadb.NewListEngine`](../backend/mariadb/README.md#order-projection-and-listing), `MustNewListEngine` | Same SQL port                                    | utf8mb4_nopad_bin text, signed BIGINT, indexable CHAR/VARCHAR sort/scope text; no IDUUID                                                                  |
| [`clickhouse.NewListEngine`](../backend/clickhouse/README.md#list-engine), `MustNewListEngine`            | Same SQL port                                    | String/Int64/Float64/Bool/DateTime64(3)/Decimal columns, `Nullable(T)` only for Presence, `Array(T)` without `Nullable` elements; no IDUUID, no collation |
| [`mongo.NewListEngine`](../backend/mongo/README.md#list-engine), `MustNewListEngine`                      | Collection, `bson.D` scope, document type `D`    | Declared patterns (no unrestricted order), page size up to `MaxListPageSize`, BSON values of the declared kind, whole-millisecond cursor precision        |

The SQL engines are the shared [sqlbase](../backend/sqlbase/README.md#listing-engine) `ListEngine` with the backend's `ListDialect` fixed;
`sqlbase.NewListEngine` takes an application dialect. `MustNewListEngine` (in sqlbase too) takes the same arguments as `NewListEngine`: for a
package-level declaration fixed in code, like `listing.MustSchema`, it panics on a refused configuration with an error that wraps the
`NewListEngine` error.

MariaDB arrays are disabled by default. Select WithListArrayStorage(ArraysJSON) to store them as JSON arrays; keep JSON numbers and booleans typed,
decimal values in the configured domain, and timestamp elements as UTC RFC 3339 strings. Text and JSON array columns use utf8mb4_nopad_bin, including
private scope keys. Prefix indexes cannot provide full text ordering. PostgreSQL arrays are NOT NULL and contain no null elements. ClickHouse repeated
fields are native `Array(T)` columns (never NULL; an absent array is empty) and text compares by bytes. Optional values in SQL are stored as their NOT
NULL zeros (an Optional ClickHouse timestamp stores the Unix epoch, the `DateTime64` default), while Presence values use SQL NULL.

Default timestamp storage is aligned to milliseconds: timestamptz(3) on PostgreSQL, DATETIME(3) on MariaDB and DateTime64(3) on ClickHouse. Configure
profile and engine precision consistently when changing it. Profile precision zero preserves nanoseconds, but engine cursor precision must be positive;
the MongoDB engine only supports whole-millisecond multiples. The default SQL decimal domain is DECIMAL(65,30).

Call CheckSchema before deployment and EnsureIndexes when provisioning SQL indexes. WithListTextCollation changes translation and metadata expectations;
applications own matching storage/index behavior. WithListSchemaCheckPolicy replaces individual named checks, including type, nullability, collation,
timestamp precision, decimal domain, and indexability. WithListIndexNamer replaces deterministic index names. ClickHouse's EnsureIndexes adds minmax
data skipping indexes; its primary index is the MergeTree sorting key, which should start with the scope keys and the dominant sort pattern.

## SQL dialect and engine setup

```go
package example

import (
    "database/sql"

    "github.com/altessa-s/go-aip/backend/mariadb"
    "github.com/altessa-s/go-aip/backend/sqlbase"
    "github.com/altessa-s/go-aip/listing"
    "github.com/altessa-s/go-aip/pagination/pagetoken"
)

type SQLWidget struct { ID, Name string }

func MariaWidgets(db *sql.DB, signer pagetoken.Signer, schema *listing.Schema) (*sqlbase.ListEngine[SQLWidget], error) {
    return mariadb.NewListEngine[SQLWidget](db, signer, schema, "widgets", []string{"id", "name"},
        func(w *SQLWidget) []any { return []any{&w.ID, &w.Name} },
        sqlbase.WithListScopeKeys("tenant_id"),
        sqlbase.WithListParentKey("tenant_id"),
        sqlbase.WithListArrayStorage(sqlbase.ArraysJSON),
    )
}
```

The table must satisfy the schema before this engine runs. Pass Scope with tenant_id on every List call; Request.Parent must match because
WithListParentKey explicitly binds it. Scope is independent of parent unless that option is configured. The engine does not infer parent binding from a
lone scope key.

A sqlbase.ListDialect composes ListSyntax, ListFilterDialect, ListSchemaDialect, and ListIndexDialect. It owns identifiers, limits, placeholders, filter
translation, typed cursor predicates, metadata checks, and index DDL. Filter SQL already contains native placeholders starting at 1 and is preserved.
WithListKindResolver maps logical kinds to neutral sqlbase kinds consistently across translation, scanning, scopes, identities, cursors, and checks.
Custom scalar codecs and driver.Valuer implementations must agree with the database ordering.

## Extension seams and budgets

| Layer            | Replaceable dependencies                                                                                                                    |
|------------------|---------------------------------------------------------------------------------------------------------------------------------------------|
| Schema profile   | FilterParser, OrderParser, parser options, builtins/functions, filter options, validators, default literals, size resolver, error formatter |
| Filter policy    | ExtensionNode.Lower, ValueCodec, decimal parser, individual ContractRules, regex compiler/validator/cache                                   |
| Memory execution | evaluation.FieldReader, BudgetedReader, evaluator.Profile, RequestProfile, ValueEncoding, work/scan/materialization limits                  |
| Cursor           | Signer, PayloadCodec, PositionCodec, ValueComparator, typed identity, timestamp precision                                                   |
| SQL              | Dialect components, kind resolver, column map, identity kind/type, table alias, collation, schema checks, index naming                      |

The [evaluation](../listing/evaluation/) profile supplies portable semantics without a driver. BindBudget shares a request-owned work budget with field
traversal and predicates. Memory WithListMaxWork (default DefaultMaxWork) requires a request-aware profile unless set to zero; WithListMaxScanned and
WithListMaxMaterialized add independent limits. Zero disables each request limit. The memory engine verifies the page token before it reads any item.

SQL WithListMaxTime is the only engine-added query deadline; its default is ten seconds and zero leaves only the caller deadline. ErrorMapper receives
the original query error and whether filtering was active. Without one, the SQL and MongoDB engines report a query whose context ended by that end
alone — the caller's context error, or ErrTimeout for the time cap — hiding the driver, scan or decoder error behind `apierror.Classify`, so a
FieldError, `apierror.Error` or sentinel it holds never selects the status or reaches the client; `apierror.HiddenCause` returns it for logs.

## Tokens and conformance

Tokens bind the subject, parent/scope, canonical filter (with the server restriction) and order, engine storage settings, and profile ContractID
(portable-v1 by default). Change ContractID whenever custom parsing or evaluation semantics change. PositionCodec.Name versions ordering and encoding. A
nil signer serves a single page and rejects supplied tokens. Engines do not exchange tokens, and pagination is not snapshot isolation. See
[pagination](pagination.md) for signing, rotation, encryption, and codecs.

[listingtest.Run](../listing/listingtest/) tests requests, filters, server restrictions, arrays, presence, ordering, pagination, and token rejection
through a Factory. RunWithoutArrays covers deliberately scalar-only engines. WithSchemaOptions supplies profile options and WithSignerFactory supplies a
signer.

Run `go test ./listing/...` for the module's coverage. The conformance suite runs against live PostgreSQL, MariaDB, ClickHouse and MongoDB servers
in the test-only module [`integration/docker`](../integration/docker/README.md) (build tag `integration_docker`, servers started in Docker): every
SQL engine stores the fixture under its storage contract, passes `CheckSchema` and `EnsureIndexes`, and is checked with the filter, order and
projection conformance suites executed by the server; the MongoDB engine runs the listing and filter conformance suites, and its explain-plan
index checks (`mongotest.AssertIndexedSorts`) prove every order is an index scan without a blocking sort.
