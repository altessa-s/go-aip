# orderby

```go
import "github.com/altessa-s/go-aip/orderby"
```

Package `orderby` parses the AIP-132 [`order_by`](https://google.aip.dev/132#ordering) DSL into a flat AST and translates it to database-specific sort
representations via small per-backend translators bound to a shared `Policy`. Includes the same defense-in-depth controls as `filter`: length caps,
key count caps, field allow-lists, and an explicit "untrusted input requires allow-list" guard.

## Contract

`NewParser` parses comma-separated field paths and optional asc/desc directions into a `Spec`. ParserOption controls limits, caching, duplicate
handling, direction case, and numeric path segments. PolicyOption (`NewPolicy`) controls authorization and the default order in API names, once per
resource. TranslatorOption controls, per backend, mapping, custom resolution, storage denies and tie-breakers.

`Policy.Validate` applies the default order to an empty spec and authorizes every key (allow-list, field access, denied fields, direction and null
placement); it is idempotent and keeps API names. `TranslatorContext.Resolve` validates through the bound policy, applies exact or longest-prefix
mapping or KeyResolver and storage denies, then appends missing tie-breakers. It returns a detached specification and rejects duplicate storage keys.
Tie-breaker names are storage names and cannot overlap denied storage paths, including those a denied API field reaches through the mapping. Shared
fieldpolicy.Access remains restrictive alongside an allowlist.

`Key` carries direction, null placement, and collation. Basic backends reject unsupported attributes rather than dropping them. The SQL dialects of
backend/sqlbase render them where the backend can, and custom SQL resolvers can implement `sqlbase.KeyRenderer`. Request collation text still
needs validation. `WithUntrustedInput(true)` requires an allow-list that restricts the fields.

### Parser / policy / translator

The three option layers are described once in the [root README](../README.md#parser--policy--translator). Build one `Policy` per resource
with `NewPolicy` and pass it as the first argument of every translator constructor; a nil policy fails with `ErrPolicyRequired`, and
`NewPolicy` raises `ErrAllowlistRequired` and rejects a default order the policy refuses.

## Grammar

```
order_by  := key ("," key)*
key       := field_path direction?
field_path:= ident ("." ident)*
direction := "asc" | "desc"
```

Whitespace around commas and between the field and direction tokens is insignificant. The default direction is ascending. Empty or whitespace-only
input parses to an empty `Spec` (no error) — AIP-132's "no ordering specified" semantics. Identifiers are case-sensitive. The parser rejects inputs that
mention the same field path twice (`ErrDuplicateKey`) — `"slug, slug desc"` is almost always a client bug, and the wire-level semantics differ between
backends, so flagging it at parse time gives one consistent error regardless of the target. `WithAllowDuplicateKeys(true)` relaxes only the parser;
translators still reject keys that resolve to the same storage name.

## Key types

| Type / Interface                         | Description                                                                                                                          |
|------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------|
| `Parser`                                 | Order_by parser with LRU caching                                                                                                     |
| `Spec`                                   | Parsed result — flat slice of `Key` values; `String()` renders the canonical form (`"a desc, b"`), `Reverse()` flips every direction |
| `Key`                                    | Single sort entry: `Name` (raw dotted form), `Direction`, and the programmatic `Nulls` and `Collation`; `IsDescending()`             |
| `NullOrder`                              | Null placement of a key: `NullsDefault` (the backend's), `NullsFirst` or `NullsLast`                                                 |
| `Direction`                              | `DirectionAscending` (default) or `DirectionDescending`                                                                              |
| `Policy` / `PolicyOption`                | Backend-independent field policy of one resource, shared by every translator                                                         |
| `TranslatorContext` / `TranslatorOption` | A `Policy` bound to the storage options of one backend                                                                               |

## Parser options

See the [options reference](#options) and contract above.

## Policy and translator options

See the [options reference](#options). The allow-list keys (a policy option) are matched against the **API** name (`Key.Name`), not the mapped
storage column. Pair `WithFieldMapping("createdAt" → "created_at")` on the translator with `WithAllowedFields("createdAt")` on the policy, not
`…("created_at")` — otherwise every sort is rejected.

### Nested paths

Dotted field paths (`address.city`, `user.profile.name`) are accepted by the parser by default and emitted verbatim by the MongoDB, Meilisearch and
RediSearch translators, which support them natively. The SQL translators read a dotted path as `table.column` and reject deeper ones; map nested
fields onto columns there. PostgreSQL and MariaDB also refuse a dotted key that is not declared (`Policy.DeclaresField`,
`TranslatorContext.DeclaresField`: an exact `WithAllowedFields` or `WithFieldMapping` entry) with `ErrFieldNotAllowed`. Three knobs make working with
nested schemas less verbose:

```go
policy, err := orderby.NewPolicy(
    // Allow any subkey of address.* without listing each leaf.
    // Note: "address.*" matches subpaths only — list "address" too
    // if the bare parent should also be sortable.
    orderby.WithAllowedFields("createdAt", "address", "address.*"),
)
if err != nil {
    return err
}

trans, err := mongo.NewOrderTranslator(policy, mongo.WithOrderOptions(
    // Rewrite the leading segment(s) of dotted paths once; deep paths
    // pass through. address.zip.code → addr.zip.code.
    orderby.WithFieldPrefixMapping(map[string]string{"address.": "addr."}),
))
```

Array-index segments (`tags.0`, `items.5.name`) are **rejected by default** to stay strict-AIP-132. Opt in at the parser:

```go
parser, _ := orderby.NewParser(orderby.WithAllowArrayIndexPaths(true))
ob, _ := parser.Parse(ctx, "tags.0 desc")
```

Whether the backend actually has that index is the caller's concern — `orderby` only validates the syntax.

### Untrusted input

When translating order_by strings from external clients, pair `WithUntrustedInput(true)` with `WithAllowedFields(...)` on the policy. Without an
allow-list a hostile client can sort on any indexed field. Untrusted input without an allow-list that restricts the fields is misconfiguration —
`NewPolicy` returns `ErrAllowlistRequired` instead of building the policy. A non-empty `WithAllowedFields` list or a `fieldpolicy.AllowList` given to
`WithFieldAccess` counts, unless it matches every field (`"*"`, the zero `AllowList`); an opaque `WithFieldAccess` policy needs a `WithAllowedFields`
list beside it, as in filter and projection. A malformed allow-list entry (`"a..b"`, `"a*"`) fails `NewPolicy` with an error wrapping
`fieldpolicy.ErrMalformedPattern` in any mode.

```go
policy, err := orderby.NewPolicy(
    orderby.WithUntrustedInput(true),
    orderby.WithAllowedFields("createdAt", "slug"),
)
```

## Usage

```go
parser, _ := orderby.NewParser()

ob, err := parser.Parse(ctx, "create_time desc, slug")
if err != nil {
    return err
}

policy, err := orderby.NewPolicy(orderby.WithAllowedFields("create_time", "slug"))
if err != nil {
    return err
}

trans, err := mongo.NewOrderTranslator(policy)
if err != nil {
    return err
}
sort, _ := trans.Translate(ob)
// sort == bson.D{{Key: "create_time", Value: int32(-1)}, {Key: "slug", Value: int32(1)}}

cursor, err := col.Find(ctx, filter, options.Find().SetSort(sort))
```

## Backends

The order translators live in the backend packages:

| Package                                                                                                                                                   | Output                      | Constructor                                                                                                     |
|-----------------------------------------------------------------------------------------------------------------------------------------------------------|-----------------------------|-----------------------------------------------------------------------------------------------------------------|
| [backend/mongo](../backend/mongo/README.md)                                                                                                               | `bson.D`                    | `mongo.NewOrderTranslator(policy *orderby.Policy, opts ...mongo.OrderOption)`                                   |
| [backend/meili](../backend/meili/README.md)                                                                                                               | `[]string` (`field:asc`)    | `meili.NewOrderTranslator(policy *orderby.Policy, opts ...meili.OrderOption)`                                   |
| [backend/redis](../backend/redis/README.md)                                                                                                               | `SortBy{Field, Descending}` | `redis.NewOrderTranslator(policy *orderby.Policy, opts ...redis.OrderOption)`                                   |
| [backend/sqlbase](../backend/sqlbase/README.md#order-translator)                                                                                          | `string` (`ORDER BY` body)  | `sqlbase.NewOrderTranslator(policy *orderby.Policy, dialect sqlbase.OrderDialect, opts ...sqlbase.OrderOption)` |
| [backend/postgres](../backend/postgres/README.md), [backend/mariadb](../backend/mariadb/README.md), [backend/clickhouse](../backend/clickhouse/README.md) | `string` (`ORDER BY` body)  | `NewOrderTranslator(policy *orderby.Policy, opts ...OrderOption)`: sqlbase with the dialect fixed               |
| [backend/memory](../backend/memory/README.md)                                                                                                             | `memory.Comparator`         | `memory.NewOrderTranslator(policy *orderby.Policy, opts ...memory.OrderOption)`                                 |

Every order translator has the same options: `WithOrderOptions(...orderby.TranslatorOption)` for the storage options and
`WithOrderCollector(metrics.Meter)` for the `orderby_<backend>_translate_duration_seconds` histogram; the memory comparator adds
`WithOrderValueEncoding` and `WithOrderMaxDataBudget`. [orderbytest](orderbytest) runs one conformance suite over every translator.

The MongoDB translator emits `bson.D` (not `bson.M`) because sort precedence is significant — the MongoDB driver respects insertion order only on
ordered documents. RediSearch's `FT.SEARCH ... SORTBY` accepts a single field; multi-key inputs return `ErrTooManySortKeys`, which wraps
`ErrUnsupportedOrdering`.

## Extended ordering

| API                          | Purpose                                                                      |
|------------------------------|------------------------------------------------------------------------------|
| `Key.Nulls`, `Key.Collation` | Programmatic null placement and collation                                    |
| `Policy.Validate`            | Apply the default order and authorize keys in API names                      |
| `TranslatorContext.Resolve`  | Validate with the policy, resolve keys, and reject duplicate storage columns |

The AIP parser remains a simple field/direction grammar. Construct extended keys programmatically. The SQL dialects render null placement and
collation (PostgreSQL and ClickHouse with `NULLS FIRST/LAST`, MariaDB by emulating the placement opposite to its native one with a leading
`col IS NULL` key); the memory comparator renders every null placement but no collation; MongoDB and Meilisearch accept only their native null
placement (`CheckNativeOrdering`) and RediSearch neither attribute (`CheckBasicOrdering`). An attribute a translator cannot express is
`ErrUnsupportedOrdering`. Public `sqlbase.KeyRenderer` supports other SQL syntaxes. `Reverse` also reverses explicit null placement.

## Consistency contracts

Boolean options take an explicit bool, including WithNoCache, WithAllowArrayIndexPaths, WithCaseSensitiveDirection, and WithUntrustedInput.
Later options can disable an earlier preset.

WithTiebreaker takes trusted storage keys. They bypass the policy's allow-list and the field mapping, while storage deny-lists still apply.
Tiebreakers are appended after client mapping and deduplicated by storage name; a client-selected direction is preserved.

## Policy propagation

Spec.String includes explicit null placement and quoted collation names in canonical output. Extended forms require a custom parser; the default AIP
grammar remains unchanged.

## Options

| Option                       | Type               | Default                    | Behavior                                                                                                                                    |
|------------------------------|--------------------|----------------------------|---------------------------------------------------------------------------------------------------------------------------------------------|
| `WithAllowArrayIndexPaths`   | `ParserOption`     | false                      | Permit numeric field-path segments during parsing.                                                                                          |
| `WithAllowDuplicateKeys`     | `ParserOption`     | false                      | Permit repeated sort fields during parsing; storage duplicates are still checked during resolution.                                         |
| `WithCaseSensitiveDirection` | `ParserOption`     | false                      | Require case-sensitive direction keywords when true.                                                                                        |
| `WithMaxExpressionLength`    | `ParserOption`     | DefaultMaxExpressionLength | Limit input expression length in bytes.                                                                                                     |
| `WithMaxFieldNameLength`     | `ParserOption`     | DefaultMaxFieldNameLength  | Limit a field path length in bytes, including dots.                                                                                         |
| `WithMaxFieldPathDepth`      | `ParserOption`     | DefaultMaxFieldPathDepth   | Limit the number of dotted path segments.                                                                                                   |
| `WithMaxKeys`                | `ParserOption`     | DefaultMaxKeys             | Limit parsed sort keys.                                                                                                                     |
| `WithCacheSize`              | `ParserOption`     | DefaultParserCacheSize     | Set the parsed-specification cache capacity.                                                                                                |
| `WithCollector`              | `ParserOption`     | nil / unset                | Parser `metrics.Meter`.                                                                                                                     |
| `WithNoCache`                | `ParserOption`     | false                      | Disable parsed-specification caching when true.                                                                                             |
| `WithAllowedFields`          | `PolicyOption`     | nil / unset                | Restrict public field names; matched before storage mapping.                                                                                |
| `WithDefaultOrder`           | `PolicyOption`     | nil / unset                | Use these public sort keys when the request order is empty; `NewPolicy` rejects a key the policy refuses.                                   |
| `WithDeniedFields`           | `PolicyOption`     | nil / unset                | Reject denied public paths, including overlaps; translators also deny the storage paths they reach through the mapping.                     |
| `WithFieldAccess`            | `PolicyOption`     | nil / unset                | Add a shared field authorization policy. Existing allow-lists remain restrictive.                                                           |
| `WithUntrustedInput`         | `PolicyOption`     | false                      | Mark the order as client input; `NewPolicy` then requires an allow-list that restricts the fields.                                          |
| `WithDeniedStorageFields`    | `TranslatorOption` | nil / unset                | Reject storage paths regardless of their public alias.                                                                                      |
| `WithFieldMapping`           | `TranslatorOption` | identity mapping           | Exact mapping from API field names to storage names, so the API can expose user-friendly names while sorting on the storage representation. |
| `WithFieldPrefixMapping`     | `TranslatorOption` | nil / unset                | Rewrite the leading segments of a dotted field path. Keys and values are normalized to end in "." so rewrites stay on segment boundaries.   |
| `WithKeyResolver`            | `TranslatorOption` | nil / unset                | Resolve an authorized public key to a storage key, replacing the mapping, without mutating caller input.                                    |
| `WithTiebreaker`             | `TranslatorOption` | nil / unset                | Append trusted storage keys after mapping client keys; existing storage keys retain their direction.                                        |

## Defaults and limits

| Constant                     | Value  |
|------------------------------|--------|
| `DefaultMaxExpressionLength` | `1024` |
| `DefaultMaxFieldNameLength`  | `128`  |
| `DefaultMaxFieldPathDepth`   | `8`    |
| `DefaultMaxKeys`             | `32`   |
| `DefaultParserCacheSize`     | `1000` |

## Named values

| Constant              | Meaning                                                                                         |
|-----------------------|-------------------------------------------------------------------------------------------------|
| `DirectionAscending`  | DirectionAscending is the default direction. Equivalent to the explicit `asc` token in the DSL. |
| `DirectionDescending` | DirectionDescending is the direction selected by an explicit `desc` token in the DSL.           |
| `NullsDefault`        | Backend default null placement.                                                                 |
| `NullsFirst`          | Nulls precede non-null values.                                                                  |
| `NullsLast`           | Nulls follow non-null values.                                                                   |

## API

| Symbol                                                                   | Purpose                                                                                                                                   |
|--------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------|
| `KeyResolver`                                                            | KeyResolver maps an authorized API key to a storage key. It must be concurrency safe.                                                     |
| `CheckBasicOrdering`                                                     | Reject every explicit null placement and collation with `ErrUnsupportedOrdering`.                                                         |
| `CheckNativeOrdering`, `NullsSmallest`, `NullsAlwaysLast`                | Accept an explicit null placement equal to the backend's fixed native one, reject the opposite and every collation.                       |
| `Telemetry`, `NewTelemetry`                                              | Per-backend translation duration histogram `orderby_<backend>_translate_duration_seconds`, used by the translators' `WithOrderCollector`. |
| `NewParser`                                                              | NewParser creates a new order_by parser.                                                                                                  |
| `Parser.Parse`, `Parser.MustParse`                                       | Parse an order_by string (cached); `MustParse` panics on an error, for fixed orders in code.                                              |
| `Spec.Clone`, `Spec.IsEmpty`, `Spec.Reverse`, `Spec.String`              | Copy, emptiness, every direction and explicit null placement flipped, canonical spelling.                                                 |
| `ParserOption`                                                           | ParserOption is a functional option for configuring parserOptions.                                                                        |
| `NewPolicy`                                                              | Build and validate the shared `Policy`; `ErrAllowlistRequired` or the `Validate` error of a refused default order key.                    |
| `PolicyOption`                                                           | Functional option of `NewPolicy`.                                                                                                         |
| `Policy.Validate`                                                        | Apply the default order and authorize a spec; errors map to INVALID_ARGUMENT.                                                             |
| `Policy.IsFieldAllowed`, `Policy.DeniedFields`, `Policy.DeclaresField`   | Inspect the policy's field rules; `DeclaresField` reports an exact `WithAllowedFields` entry.                                             |
| `NewTranslatorContext`                                                   | Bind a policy to the storage options of one backend; `ErrPolicyRequired` for nil, `ErrFieldNotAllowed` for a denied tie-breaker.          |
| `TranslatorContext.Policy`                                               | The bound policy.                                                                                                                         |
| `TranslatorContext.Resolve`                                              | Validate a spec through the policy, map it to storage names, apply storage denies and append the tie-breakers.                            |
| `TranslatorContext.ApplyFieldMapping`, `TranslatorContext.DeclaresField` | The storage name of an API field under the exact or prefix mapping; whether a field is declared by the policy or an exact mapping.        |

## Errors

| Sentinel                        | When it fires                                                                                               |
|---------------------------------|-------------------------------------------------------------------------------------------------------------|
| `ErrParseFailed`                | Clause cannot be tokenized into `field [asc\|desc]`                                                         |
| `ErrEmptyClause`                | Clause between commas is empty (`",a"`, `"a,,b"`)                                                           |
| `ErrInvalidFieldPath`           | Field path contains a non-ident character or empty segment; a SQL translator's mapped name is not a column  |
| `ErrInvalidDirection`           | Direction token other than `asc`/`desc` (case-sensitive when configured)                                    |
| `ErrExpressionTooLong`          | Raw input exceeds `WithMaxExpressionLength`                                                                 |
| `ErrMaxKeysExceeded`            | More clauses than `WithMaxKeys`                                                                             |
| `ErrMaxFieldPathDepthExceeded`  | Dotted segments exceed `WithMaxFieldPathDepth`                                                              |
| `ErrMaxFieldNameLengthExceeded` | Field-path length exceeds `WithMaxFieldNameLength`                                                          |
| `ErrDuplicateKey`               | Same field path appears twice; two keys resolve to the same storage name (translator)                       |
| `ErrFieldNotAllowed`            | Field refused by the policy (allow-list, field access, denied fields) or resolving to a denied storage path |
| `ErrUnsupportedOrdering`        | Null placement or collation the backend cannot express                                                      |
| `ErrAllowlistRequired`          | `WithUntrustedInput` configured without an allow-list that restricts the fields (raised at `NewPolicy`)     |
| `ErrPolicyRequired`             | A translator constructor or `NewTranslatorContext` received a nil policy                                    |
| `ErrTooManySortKeys`            | RediSearch translator received more than one resolved key; wraps `ErrUnsupportedOrdering`                   |

## Related packages

- [go-aip](../README.md)
- [fieldbehavior](../fieldbehavior/README.md)
- [fieldmask](../fieldmask/README.md)
- [fieldpolicy](../fieldpolicy/README.md)
- [filter](../filter/README.md)
- [listing](../listing/README.md)
- [projection](../projection/README.md)
- [orderby guide](../docs/orderby.md)
