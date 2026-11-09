# Ordering

[orderby](../orderby/) parses AIP-132 order_by values into a detached Spec. A shared Policy applies authorization and the default order;
translators bound to it apply mapping, tie-breakers, and backend-specific ordering capabilities.

## When to reach for `orderby`

| Scenario                                                         | Use                                                                                                                                                                             |
|------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Accept `order_by` from a `List*Request` and sort a MongoDB query | `Parser` + `NewPolicy` + `backend/mongo.NewOrderTranslator`                                                                                                                     |
| Sort a ClickHouse, MariaDB/MySQL or PostgreSQL query             | `NewOrderTranslator` of `backend/clickhouse`, `backend/mariadb` or `backend/postgres` (`sqlbase.NewOrderTranslator` with an `OrderDialect` for an application `ColumnResolver`) |
| Sort a Meilisearch search request                                | `backend/meili.NewOrderTranslator`                                                                                                                                              |
| Sort a RediSearch `FT.SEARCH` (single key only)                  | `backend/redis.NewOrderTranslator`                                                                                                                                              |
| Sort records held in memory                                      | `backend/memory.NewOrderTranslator` → `Comparator` for `slices.SortStableFunc`                                                                                                  |
| Expose API field names that differ from storage columns          | `WithFieldMapping` (exact) or `WithFieldPrefixMapping` (subtree)                                                                                                                |
| Untrusted `order_by` from end users                              | `NewPolicy(WithUntrustedInput, WithAllowedFields)` (mandatory pair)                                                                                                             |
| Serve one resource from several backends                         | One `Policy`, one translator per backend bound to it                                                                                                                            |
| Sort on nested documents (`address.city`)                        | Default — parser already accepts dotted paths                                                                                                                                   |
| Allow whole subtrees without enumerating leaves                  | `WithAllowedFields("address.*")`                                                                                                                                                |
| Sort on array positions (`tags.0`)                               | `WithAllowArrayIndexPaths(true)` (opt-in, off by default)                                                                                                                       |

## Parser / policy / translator

Options split into three layers, described once in the [root README](../README.md#parser--policy--translator): `ParserOption` bounds the
request text, `PolicyOption` (`NewPolicy`) states what a client may sort on in API names, and `TranslatorOption` states how one backend
expresses a validated order. Build the policy once per resource and pass it as the first argument of every translator constructor;
`Translate` always validates through it.

| Layer      | Options                                                                                                                       |
|------------|-------------------------------------------------------------------------------------------------------------------------------|
| Parser     | [Security limits](#security-limits), `WithAllowArrayIndexPaths`, `WithAllowDuplicateKeys`, `WithCaseSensitiveDirection`       |
| Policy     | `WithAllowedFields`, `WithFieldAccess`, `WithDeniedFields`, `WithDefaultOrder`, `WithUntrustedInput`                          |
| Translator | `WithFieldMapping`, `WithFieldPrefixMapping`, `WithKeyResolver`, `WithTiebreaker`, `WithDeniedStorageFields`; the SQL dialect |

## Quick start

### `order_by` → MongoDB sort document

```go
parser, err := orderby.NewParser()
if err != nil { return err }

spec, err := parser.Parse(ctx, "create_time desc, slug")
if err != nil { return err }

policy, err := orderby.NewPolicy(orderby.WithAllowedFields("create_time", "slug"))
if err != nil { return err }

trans, err := mongo.NewOrderTranslator(policy)
if err != nil { return err }

sort, _ := trans.Translate(spec)
// sort == bson.D{{"create_time", int32(-1)}, {"slug", int32(1)}}

cursor, err := col.Find(ctx, filter, options.Find().SetSort(sort))
```

### `order_by` → Meilisearch sort argument

```go
parser, _ := orderby.NewParser()
spec, _ := parser.Parse(ctx, "createdAt desc, name")
policy, _ := orderby.NewPolicy(orderby.WithAllowedFields("createdAt", "name"))
trans, _ := meili.NewOrderTranslator(policy)
sort, _ := trans.Translate(spec)
// sort == []string{"createdAt:desc", "name:asc"}

req := &meilisearch.SearchRequest{Sort: sort}
```

### `order_by` → RediSearch single SORTBY

```go
parser, _ := orderby.NewParser()
spec, _ := parser.Parse(ctx, "createdAt desc")
policy, _ := orderby.NewPolicy(orderby.WithAllowedFields("createdAt"))
trans, _ := redis.NewOrderTranslator(policy)
sb, _ := trans.Translate(spec)
// sb == redis.SortBy{Field: "createdAt", Descending: true}
```

The three translators above can share one policy. Always check the `NewPolicy` and constructor errors — the misconfiguration checks fire at
construction.

### Untrusted input (HTTP query parameter)

```go
policy, err := orderby.NewPolicy(
    orderby.WithUntrustedInput(true),
    orderby.WithAllowedFields("createdAt", "slug", "address.*"),
)
if errors.Is(err, orderby.ErrAllowlistRequired) {
    log.Fatal("untrusted-input policy without allow-list is a misconfiguration")
}
```

`WithUntrustedInput` makes a restrictive allow-list mandatory — a non-empty `WithAllowedFields` list or a `fieldpolicy.AllowList` given to
`WithFieldAccess`, neither of which may match every field (`"*"`, the zero `AllowList`); an opaque `WithFieldAccess` needs `WithAllowedFields`
beside it — without it **`NewPolicy`** refuses to run, not the first `Translate` call. `NewPolicy` also validates `WithDefaultOrder`: a default
key the policy refuses fails there instead of on the first request. Misconfiguration surfaces at process start rather than on the first request.

## DSL grammar

```
order_by  := key ("," key)*
key       := field_path direction?
field_path:= ident ("." ident)*
direction := "asc" | "desc"
```

- Whitespace around commas and between field and direction is insignificant.
- Direction default is ascending.
- Identifiers match `[A-Za-z_][A-Za-z0-9_]*` per segment and are **case-sensitive** — `Slug` and `slug` are different fields.
- Direction tokens are case-**insensitive** by default (`ASC`/`Asc`/`asc` all work); flip with `WithCaseSensitiveDirection`.
- Empty or whitespace-only input parses to an empty `Spec` with no error — AIP-132's "no ordering specified" semantics.
- Duplicate field paths (`"slug, slug desc"`) return `ErrDuplicateKey` — almost always a client bug, and wire-level semantics differ between
  backends, so the parser surfaces one consistent error regardless of the target. `WithAllowDuplicateKeys(true)` relaxes only the parser;
  translators still reject two keys that resolve to the same storage name.

## Security limits

| Option                       | Default      | What it bounds                                                   |
|------------------------------|--------------|------------------------------------------------------------------|
| `WithMaxExpressionLength`    | 1024 bytes   | Reject before parsing — protects parser memory and the LRU cache |
| `WithMaxKeys`                | 32           | Maximum number of sort keys (commas + 1)                         |
| `WithMaxFieldPathDepth`      | 8            | Maximum dotted segments in one field path                        |
| `WithMaxFieldNameLength`     | 128 bytes    | Maximum length of one field path including its dots              |
| `WithCacheSize`              | 1000 entries | LRU cache for parsed `Spec` values                               |
| `WithNoCache`                | --           | Disable caching entirely (typical for tests)                     |
| `WithCaseSensitiveDirection` | off          | Reject `DESC`/`Asc` — only lowercase tokens accepted             |
| `WithAllowArrayIndexPaths`   | off          | Relax grammar to accept all-digit segments (`tags.0`)            |
| `WithCollector`              | --           | `metrics.Meter` for parse latency and error counters             |

Length and key caps belong on the parser (`ParserOption`); the allow-list belongs on the policy (`PolicyOption`) and mapping on the translator
(`TranslatorOption`). Order_by strings are short by design — defaults are well above any realistic production payload, meant only as a safety stop
against pathologically large input.

## Policy options

| Option                         | Default | Description                                                                                         |
|--------------------------------|---------|-----------------------------------------------------------------------------------------------------|
| `WithAllowedFields(fields...)` | all     | Sortable fields: exact names, `.*` subtrees, or `*` for all fields                                  |
| `WithFieldAccess(access)`      | --      | Shared `fieldpolicy.Access`; restrictive alongside an allow-list                                    |
| `WithDeniedFields(paths...)`   | --      | API paths never sortable, even under a wildcard; translators also deny the storage paths they reach |
| `WithDefaultOrder(keys...)`    | --      | Keys, in API names, used for an empty request; validated by `NewPolicy`                             |
| `WithUntrustedInput(true)`     | off     | Marks input as user-supplied; requires an allow-list that restricts the fields (not `"*"`)          |

`Policy.Validate(spec)` applies the default order to an empty spec and authorizes every key; it is idempotent and keeps API names.
`Policy.IsFieldAllowed` and `Policy.DeniedFields` expose the same rules.

## Translator options

| Option                          | Default  | Description                                                                                     |
|---------------------------------|----------|-------------------------------------------------------------------------------------------------|
| `WithFieldMapping(map)`         | identity | Exact API-name → storage-name mapping                                                           |
| `WithFieldPrefixMapping(map)`   | --       | Subtree rewrite; trailing dots normalized, longest prefix wins, exact mapping overrides         |
| `WithKeyResolver(resolver)`     | --       | Custom resolution of an authorized API key to a storage key, replacing the mapping              |
| `WithTiebreaker(keys...)`       | --       | Trusted storage keys appended after the client keys; existing storage keys keep their direction |
| `WithDeniedStorageFields(p...)` | --       | Storage paths no key may resolve to, whatever API name maps onto them                           |

Allow-list keys match the **API** name (`Key.Name`), not the mapped storage column. Pair `WithFieldMapping("createdAt" → "created_at")` on the
translator with `WithAllowedFields("createdAt")` on the policy, not `…("created_at")` — otherwise every sort is rejected.

## Nested paths

Dotted field paths (`address.city`, `user.profile.name`) are accepted by the parser by default and emitted verbatim by the MongoDB, Meilisearch,
and RediSearch translators, which all support them natively; the memory translator walks them through nested documents. The SQL translators read a
dotted path as `table.column` and reject deeper paths; PostgreSQL and MariaDB also require it to be declared (an exact `WithAllowedFields` or
`WithFieldMapping` entry) — see [SQL](#sql-clickhouse-mariadbmysql-postgresql). Three knobs make working with nested schemas less verbose.

### Wildcard allow-list

`WithAllowedFields` accepts entries that end in `.*` as "any subtree under this prefix":

```go
policy, _ := orderby.NewPolicy(
    orderby.WithAllowedFields(
        "createdAt",     // exact
        "slug",          // exact
        "address.*",     // address.city, address.zip.code, but NOT bare "address"
    ),
)
```

| Pattern                 | Matches `address.city`? | Matches `address`? | Matches `billing.city`? |
|-------------------------|-------------------------|--------------------|-------------------------|
| `address` (exact)       | no                      | yes                | no                      |
| `address.*` (wildcard)  | yes                     | no                 | no                      |
| `address` + `address.*` | yes                     | yes                | no                      |
| `*` (bare star)         | yes                     | yes                | yes                     |

An embedded asterisk (`foo.*.bar`) matches exactly one nonempty segment (`foo.x.bar`). Wildcards are sorted longest-first internally, so
`address.deep.*` correctly precedes `address.*` when both are configured.

### Prefix field-mapping

`WithFieldPrefixMapping` (a translator option) rewrites the leading segments of dotted paths. Keys and values end in `.` so the rewrite happens on a
segment boundary:

```go
trans, _ := mongo.NewOrderTranslator(policy, mongo.WithOrderOptions(
    orderby.WithFieldPrefixMapping(map[string]string{
        "address.":      "addr.",       // address.city.zip → addr.city.zip
        "user.profile.": "users.prof.", // longest-match wins over "user."
    }),
))
```

Precedence: exact `WithFieldMapping` entries always win over any prefix rewrite. Multiple prefix matches resolve to the longest. A key or value
without the trailing `.` is normalized by adding one, so a key like `addr` never corrupts unrelated identifiers (`address` → `adress`).

### Array-index segments

Strict AIP-132 grammar rejects all-digit segments. Opt in at the parser:

```go
parser, _ := orderby.NewParser(orderby.WithAllowArrayIndexPaths(true))
spec, _ := parser.Parse(ctx, "tags.0 desc, items.5.name")
```

Numeric segments accept leading zeros (`tags.001`) — Mongo and JSONPath both do. Whether the backend has the corresponding index is the caller's
concern; `orderby` validates the syntax only.

## Translators

| Package                                                                                                                          | Output                      | Empty input           | Notes                                                                     |
|----------------------------------------------------------------------------------------------------------------------------------|-----------------------------|-----------------------|---------------------------------------------------------------------------|
| [`backend/mongo`](../backend/mongo/README.md)                                                                                    | `bson.D` (ordered)          | `nil`                 | `bson.D` preserves sort precedence; `bson.M` does not                     |
| [`backend/meili`](../backend/meili/README.md)                                                                                    | `[]string` (`"field:asc"`)  | `nil`                 | Mirrors the Meilisearch filter translator of the same package             |
| [`backend/redis`](../backend/redis/README.md)                                                                                    | `SortBy{Field, Descending}` | zero-value `SortBy{}` | Single-key only; multiple keys return `ErrTooManySortKeys`                |
| [`backend/sqlbase`](../backend/sqlbase/README.md#order-translator) (`backend/postgres`, `backend/mariadb`, `backend/clickhouse`) | `string` (`"col" DESC, …`)  | `""`                  | `ORDER BY` body per SQL dialect; mapped names must be column identifiers  |
| [`backend/memory`](../backend/memory/README.md)                                                                                  | `memory.Comparator`         | `nil`                 | `func(a, b map[string]any) int` for `slices.SortStableFunc`; no collation |

"Empty input" assumes the policy has no `WithDefaultOrder`; otherwise an empty spec renders the default order.

The constructors share the signature `NewOrderTranslator(policy *orderby.Policy, opts ...OrderOption) (*OrderTranslator, error)`; the
dialect-generic SQL one takes the dialect after the policy,
`sqlbase.NewOrderTranslator(policy, dialect sqlbase.OrderDialect, opts ...sqlbase.OrderOption)`, and `backend/postgres`, `backend/mariadb` and
`backend/clickhouse` fix it (their `OrderOption` aliases `sqlbase.OrderOption` and they return `*sqlbase.OrderTranslator`). The options are
`WithOrderOptions` (the `orderby.TranslatorOption` storage options) and `WithOrderCollector`; memory adds `WithOrderValueEncoding` and
`WithOrderMaxDataBudget`. A nil policy returns `ErrPolicyRequired`. `NewPolicy` validates the allow-list guard and the default order once, and the
constructor validates the tie-breakers; `Translate` validates each order through the policy and never reruns the configuration checks.

### MongoDB

```go
parser, _ := orderby.NewParser()
policy, _ := orderby.NewPolicy(orderby.WithAllowedFields("createdAt", "slug"))
trans, _ := mongo.NewOrderTranslator(policy, mongo.WithOrderOptions(
    orderby.WithFieldMapping(map[string]string{"createdAt": "created_at"}),
))

spec, _ := parser.Parse(ctx, "createdAt desc, slug")
sort, _ := trans.Translate(spec)
// sort == bson.D{{"created_at", int32(-1)}, {"slug", int32(1)}}
```

Ascending keys emit `int32(1)`, descending emit `int32(-1)`. Empty input returns `nil` — `SetSort(nil)` is a valid driver no-op.

### Meilisearch

```go
parser, _ := orderby.NewParser()
policy, _ := orderby.NewPolicy(orderby.WithAllowedFields("createdAt", "name"))
trans, _ := meili.NewOrderTranslator(policy)
spec, _ := parser.Parse(ctx, "createdAt desc, name")
sort, _ := trans.Translate(spec)
// sort == []string{"createdAt:desc", "name:asc"}
```

Empty input returns `nil`. The output matches Meilisearch's `sort` argument shape directly.

### RediSearch

```go
parser, _ := orderby.NewParser()
policy, _ := orderby.NewPolicy(orderby.WithAllowedFields("createdAt"))
trans, _ := redis.NewOrderTranslator(policy)
spec, _ := parser.Parse(ctx, "createdAt desc")
sb, _ := trans.Translate(spec)
// sb == redis.SortBy{Field: "createdAt", Descending: true}
```

Empty input returns zero-value `SortBy{}` (`Field == ""`). Multi-key input returns `ErrTooManySortKeys` — RediSearch `FT.SEARCH … SORTBY` takes
one field. Whether the index declares the field as `SORTABLE` is the caller's responsibility.

### SQL (ClickHouse, MariaDB/MySQL, PostgreSQL)

```go
parser, _ := orderby.NewParser()
policy, _ := orderby.NewPolicy(
    orderby.WithUntrustedInput(true),
    orderby.WithAllowedFields("createdAt", "slug"),
)
trans, _ := clickhouse.NewOrderTranslator(policy, sqlbase.WithOrderOptions(
    orderby.WithFieldMapping(map[string]string{"createdAt": "created_at"}),
))
spec, _ := parser.Parse(ctx, "createdAt desc, slug")
clause, _ := trans.Translate(spec)
// clause == "`created_at` DESC, `slug` ASC"

if clause != "" {
    query += " ORDER BY " + clause
}
```

The three dialects share one renderer in `backend/sqlbase` and are data (`sqlbase.OrderDialect` values owned by the backend packages):
`postgres.OrderDialect()` (fixed by `postgres.NewOrderTranslator`) quotes with double quotes (`col` or `table.col`, 63-byte parts),
`mariadb.OrderDialect()` with backticks (`col` or `table.col`, 64-byte parts) and `clickhouse.OrderDialect()` with backticks (any number of
parts of at most 255 bytes). On PostgreSQL and MariaDB a dotted client key must be declared — an exact `WithAllowedFields` or `WithFieldMapping`
entry — or it fails with `ErrFieldNotAllowed`, since a key a wildcard admits could name a column of another joined table. PostgreSQL and
ClickHouse render `NULLS FIRST/LAST` and collation (`COLLATE "name"` before the direction on PostgreSQL, `COLLATE 'locale'` last on ClickHouse).
MariaDB writes an unquoted collation name before the direction (`[A-Za-z][A-Za-z0-9_]*`, at most 64 bytes) and emulates the null placement
opposite to its native one with a leading `col IS NULL` key (`col IS NULL ASC, col ASC` for asc nulls last, `col IS NULL DESC, col DESC` for
desc nulls first). The output is the body of the clause without the `ORDER BY` keywords; empty input returns `""` so the caller can omit it.

A sort column cannot be bound as a placeholder, so names are validated instead of spliced in. After field mapping, every key must be a dotted name
of plain identifiers (`[A-Za-z_][A-Za-z0-9_]*`) within the dialect's limits; each part is quoted on its own. A mapping to anything else — an
expression, a JSON path, a ClickHouse Map subscript, a quote — returns `ErrInvalidFieldPath`. To sort by a nested
value, map the field onto a generated column (`MATERIALIZED` or `ALIAS` in ClickHouse).

Without an explicit `Key.Nulls`, no `NULLS FIRST/LAST` is emitted, so NULL placement follows the database:

| Dialect       | `ASC` | `DESC` |
|---------------|-------|--------|
| PostgreSQL    | last  | first  |
| MariaDB/MySQL | first | last   |
| ClickHouse    | last  | last   |

Add a unique column as the last key for a deterministic order.

## Diagnostics

All errors are sentinels wrapped via go-atlas-core `errors.Wrapf` for contextual detail. Use `errors.Is` for classification.

| Sentinel                        | When it fires                                                                                                                   |
|---------------------------------|---------------------------------------------------------------------------------------------------------------------------------|
| `ErrParseFailed`                | Clause cannot be tokenized into `field [asc\|desc]`                                                                             |
| `ErrEmptyClause`                | Clause between commas is empty (`",a"`, `"a,,b"`)                                                                               |
| `ErrInvalidFieldPath`           | Field path contains a non-ident character or empty segment; a SQL translator's mapped name is not a column                      |
| `ErrInvalidDirection`           | Direction token other than `asc`/`desc` (case-sensitive when configured)                                                        |
| `ErrExpressionTooLong`          | Raw input exceeds `MaxExpressionLength`                                                                                         |
| `ErrMaxKeysExceeded`            | More clauses than `MaxKeys`                                                                                                     |
| `ErrMaxFieldPathDepthExceeded`  | Dotted segments exceed `MaxFieldPathDepth`                                                                                      |
| `ErrMaxFieldNameLengthExceeded` | Field-path length exceeds `MaxFieldNameLength`                                                                                  |
| `ErrDuplicateKey`               | Same field path appears twice; two keys resolve to the same storage name (translator)                                           |
| `ErrFieldNotAllowed`            | Field refused by the policy (allow-list after wildcard scan, field access, denied fields) or resolving to a denied storage path |
| `ErrUnsupportedOrdering`        | Null placement or collation the policy or backend cannot express                                                                |
| `ErrAllowlistRequired`          | `WithUntrustedInput` configured without an allow-list that restricts the fields (raised at `NewPolicy`)                         |
| `ErrPolicyRequired`             | A translator constructor or `NewTranslatorContext` received a nil policy                                                        |
| `ErrTooManySortKeys`            | RediSearch translator received more than one resolved key; wraps `ErrUnsupportedOrdering`                                       |

Error precedence inside `Parse` is deterministic: length cap → key count cap → per-clause syntax (`ErrEmptyClause`, `ErrParseFailed`,
`ErrInvalidFieldPath`, `ErrInvalidDirection`, `ErrMaxFieldPathDepthExceeded`, `ErrMaxFieldNameLengthExceeded`) → `ErrDuplicateKey`. A malformed
duplicate (`"slug, slug upward"`) reports its syntactic problem first, not the duplicate.

## Performance

Benchmarks on Apple M4 Pro (data may vary by host; bench source lives next to each translator).

| Operation                                            | ns/op | allocs/op |
|------------------------------------------------------|-------|-----------|
| `Parse` cache hit (5 keys)                           | ~50   | 2         |
| `Parse` no-cache (5 keys, fresh)                     | ~370  | 13        |
| `Parse` no-cache (1 key)                             | ~170  | 4         |
| Mongo `Translate` (1 key, no options)                | ~35   | 2         |
| Mongo `Translate` (5 keys, no options)               | ~92   | 3         |
| Mongo `Translate` (3 keys, wildcard allow-list of 5) | ~104  | 3         |
| Mongo `Translate` (3 keys, prefix-mapping of 5)      | ~149  | 6         |
| Meili `Translate` (1 key)                            | ~50   | 2         |
| Meili `Translate` (5 keys)                           | ~205  | 6         |
| RediSearch `Translate` (1 key)                       | ~5    | 0         |
| SQL `Translate` (5 keys, any dialect)                | ~970  | 13        |

The wildcard scan and prefix-mapping scan add O(P) overhead per key, where P is the configured prefix count. For typical P ≤ 10 the overhead is
~20-30 ns/key.

`Parse` returns a deep-copied `Spec` on every call — callers can freely mutate `Keys` without corrupting a subsequent cache hit. The copy is one
slice allocation per call (Key fields are value types, no inner slices).

## Caching

The parser keeps an LRU of `Spec` values keyed by the raw input string. Only successful parses are cached; parse errors are not, so repeated
malformed input re-pays the validation cost on every call. Default capacity is 1000; disable with `WithNoCache(true)` (typical for tests, never
for production).

The `parse_duration_seconds` histogram covers both hits and misses — the operator-relevant latency. Cache hits show up as the fast P50/P90; misses
sit in the long tail. A growing tail with a healthy hit rate signals a workload shift, not a regression.
`WithCollector` takes a [`metrics.Meter`](../metrics/README.md) (`github.com/altessa-s/go-aip/metrics`); a go-atlas
`observability/metrics` collector is adapted to it.

## Best practices

- **Construct once, reuse.** `*Parser`, `*Policy` and each translator are safe for concurrent use; build them at startup, share across handlers.
- **Configure on the right layer.** Resource caps belong on the parser, the allow-list and default order on the policy, and mapping and
  tie-breakers on the translator. One policy per resource, shared by every backend.
- **Untrusted input always needs an allow-list.** The construction-time guard is your last line; treat `ErrAllowlistRequired` as a fail-fast
  signal, not a runtime branch.
- **Wildcards are a convenience, not a security feature.** A wildcard `address.*` still indexes through Mongo dotted notation — make sure the
  backend index actually supports the depth you advertise.
- **Allow-list keys are API names, not column names.** With `WithFieldMapping("createdAt" → "created_at")`, write
  `WithAllowedFields("createdAt")`.
- **Don't sneak array indices into APIs by default.** `WithAllowArrayIndexPaths` is opt-in for a reason — `tags.0` is rarely a stable, indexed
  sort target.
- **Don't try to stretch defaults.** If you genuinely need 50 sort keys or 12-deep field paths, your API design is leaking storage shape.

## See also

- [Package README](../orderby/README.md) — Go-doc-style quick reference.
- [Filter (`filter`)](filter.md) — sibling package for the CEL filter DSL with the same security model.
- [AIP-132](https://google.aip.dev/132) — Google's API Improvement Proposal for ordering and pagination.

## Parse and translate

```go
package example

import (
    "context"

    "github.com/altessa-s/go-aip/backend/postgres"
    "github.com/altessa-s/go-aip/backend/sqlbase"
    "github.com/altessa-s/go-aip/orderby"
)

func Order(ctx context.Context, expression string) (string, error) {
    p, err := orderby.NewParser(orderby.WithMaxKeys(4))
    if err != nil { return "", err }
    spec, err := p.Parse(ctx, expression)
    if err != nil { return "", err }
    policy, err := orderby.NewPolicy(
        orderby.WithAllowedFields("name", "createTime"),
        orderby.WithUntrustedInput(true),
    )
    if err != nil { return "", err }
    translator, err := postgres.NewOrderTranslator(policy, sqlbase.WithOrderOptions(
        orderby.WithFieldMapping(map[string]string{"createTime": "created_at"}),
        orderby.WithTiebreaker(orderby.Key{Name: "id", Direction: orderby.DirectionAscending}),
    ))
    if err != nil { return "", err }
    return translator.Translate(spec)
}
```

SQL output is the `ORDER BY` body; append the keyword in the application query. Reuse parser, policy and translator instances. MongoDB returns
`bson.D`, Meilisearch returns sort arguments, and RediSearch accepts one key.

## Grammar and limits

Fields are comma-separated, with optional `asc` or `desc`. Direction is case-insensitive by default; ascending is implicit.
`WithCaseSensitiveDirection` changes that rule. Duplicate keys and numeric array-index segments are rejected unless enabled. Limits default to
1,024 expression bytes, 32 keys, eight path segments, and 128 bytes per field name. The parser cache holds 1,000 specs; `WithNoCache` disables it.

`ParserOption` is separate from `PolicyOption` and `TranslatorOption`. All exported options and `Default*` constants are listed in the
[package reference](../orderby/README.md#options).

## Resolution and access

`Policy.Validate` uses `WithDefaultOrder` for an empty spec and checks API access and denies. `TranslatorContext.Resolve` calls it, then applies
exact/longest-prefix mapping or `KeyResolver`, checks storage denies, and rejects duplicate storage keys. `WithTiebreaker` appends missing storage
keys. Configured tie-breakers cannot overlap denied storage paths or the storage paths a denied API field reaches through the mapping;
`NewTranslatorContext` rejects them.

`WithAllowedFields` supports exact names and wildcard prefixes. `WithFieldAccess` supplies a shared `fieldpolicy.Access` and remains restrictive
alongside existing rules. `WithUntrustedInput(true)` requires an allow-list that restricts the fields: a non-empty `WithAllowedFields` list or a
restricting `fieldpolicy.AllowList` given to `WithFieldAccess`; an opaque `fieldpolicy.Access` needs a `WithAllowedFields` list beside it. All
three are `PolicyOption`s. Configured SQL expressions belong to a trusted resolver, not a client field name.

## Ordering capabilities

`Key` includes `Direction`, `Nulls`, and `Collation`. Basic renderers reject unsupported attributes with `ErrUnsupportedOrdering`. The
[sqlbase](../backend/sqlbase/README.md#order-translator) dialects render them where the backend can and validate the request collation: an
identifier on PostgreSQL, a plain collation name (`[A-Za-z][A-Za-z0-9_]*`, at most 64 bytes) on MariaDB, a plain locale (`[A-Za-z][A-Za-z0-9_-]*`,
at most 64 bytes) on ClickHouse. `sqlbase.TranslateOrder` accepts an application `ColumnResolver` for safe storage expressions; an optional
`KeyRenderer` (for example an embedded `OrderDialect`) renders a complete key with null placement and collation. Request collation is untrusted and
must be validated by that renderer.

| Feature              | sql PostgreSQL       | sql MariaDB                     | sql ClickHouse       | mongo       | meili           | redis                  | memory |
|----------------------|----------------------|---------------------------------|----------------------|-------------|-----------------|------------------------|--------|
| Multiple keys        | Y                    | Y                               | Y                    | Y           | Y               | – `ErrTooManySortKeys` | Y      |
| Explicit nulls       | Y `NULLS FIRST/LAST` | Y (opposite of native emulated) | Y `NULLS FIRST/LAST` | native only | nulls last only | –                      | Y      |
| Collation            | Y `COLLATE "id"`     | Y `COLLATE name` (unquoted)     | Y `COLLATE 'locale'` | –           | –               | –                      | –      |
| `WithOrderCollector` | Y                    | Y                               | Y                    | Y           | Y               | Y                      | Y      |

MongoDB and Meilisearch accept an explicit placement equal to their fixed native one (`orderby.CheckNativeOrdering`).

[List schemas](listing.md) build the order policy (`Schema.OrderPolicy`) and preserve these attributes, but further restrict orders to declared
patterns or full reversals by default. Storage engines validate actual supported orders, including the default. [Projection](projection.md) shares
access and mapping concepts; [pagination](pagination.md) binds the canonical order to cursor tokens.
