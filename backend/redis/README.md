# redis

```go
import "github.com/altessa-s/go-aip/backend/redis"
```

Everything go-aip renders for the Redis Query Engine (RediSearch), in one package: the `FT.SEARCH` query string of a filter, its
`SORTBY` instruction and its `RETURN` clause. The package renders arguments only; it never contacts Redis and depends on no client library.

| Part                                            | Main API                                                                                                             |
|-------------------------------------------------|----------------------------------------------------------------------------------------------------------------------|
| [Filter translator](#filter-translator)         | `FilterTranslator`, `NewFilterTranslator`, `NewFilterTranslatorWithSchema`, `FilterOption`, `FieldType`, `EscapeTag` |
| [Schema](#schema)                               | `Schema`, `NewSchema`, `SchemaOption`                                                                                |
| [Order translator](#order-translator)           | `OrderTranslator`, `NewOrderTranslator`, `OrderOption`, `SortBy`                                                     |
| [Projection translator](#projection-translator) | `ProjectionTranslator`, `NewProjectionTranslator`, `ProjectionOption`, `Return`                                      |

Every option is prefixed with its kind: `WithFilter…`, `WithOrder…`, `WithProjection…`, `WithSchema…`. As in the SQL and MongoDB backends,
the storage description is a `Schema` (`NewSchema`/`SchemaOption`/`WithSchema…`) passed to `NewFilterTranslatorWithSchema`; here it describes
the `FT.CREATE` index (field types, `INDEXMISSING`, `INDEXEMPTY`). Each translator takes the resource's shared,
client-facing policy (`filter.Policy`, `orderby.Policy`, `projection.Policy`, built once per resource and shared with the other backends)
plus the storage options of this backend; the three option layers are described in the
[root README](../../README.md#parser--policy--translator). Translators are safe for concurrent use after construction.

```go
cmd := []any{"FT.SEARCH", "idx:users", query} // query from FilterTranslator.Translate
if sortBy.Field != "" {                         // sortBy from OrderTranslator.Translate
    direction := "ASC"
    if sortBy.Descending {
        direction = "DESC"
    }
    cmd = append(cmd, "SORTBY", sortBy.Field, direction)
}
for _, a := range returnArgs { // returnArgs from ProjectionTranslator.Translate
    cmd = append(cmd, a)
}
cmd = append(cmd, "DIALECT", "2")
```

## Filter translator

`FilterTranslator` translates filter AST nodes into RediSearch query syntax for server-side filtering on Redis with the RediSearch module.

### Contract

`NewFilterTranslatorWithSchema(policy, schema, opts ...FilterOption)` takes the shared `*filter.Policy` (field authorization, types, limits, decimal
conversion, value codecs and regex rules, in API names, built once per resource), the index `*Schema` ([Schema](#schema)) and this package's
`FilterOption` values; `WithFilterOptions` binds the policy to storage names (`filter.WithFieldMapping`). `NewFilterTranslator(policy, opts...)`
uses an empty schema: every field is TAG and no field is `INDEXMISSING` or `INDEXEMPTY`. A nil schema fails with `filter.ErrInvalidOptions`. A nil
policy fails with `filter.ErrPolicyRequired`; `filter.NewPolicy` requires an allowlist or an explicit trusted `WithAnyField` opt-out.

`ValidateAST` uses the configured renderer to check operand shapes and values without contacting a server. Unsupported operations remain explicit
errors. Translators support concurrent calls with per-call traversal state; injected callbacks must also support concurrent use.

`WithFilterLiteralEncoder` converts values before backend encoding; it does not return executable query fragments. Decimal operands are normalized
through the shared policy, including membership. Backend-specific storage semantics can require schema configuration instead of a scalar encoder.

`WithFilterPredicateRenderer` supplies an application renderer after policy validation; unhandled nodes use built-in rendering. The callback remains
responsible for safe backend encoding.

The schema's field map declares NUMERIC, TAG, and TEXT representation. A field missing from it is treated as TAG; `WithSchemaStrict(true)`
rejects undeclared fields instead, and requires a non-empty field map (`NewSchema` fails with `filter.ErrInvalidOptions` otherwise). Index
configuration must match separator and case behavior. Search tokenization is backend semantics, not general scalar SQL comparison.

### Schema

`NewSchema(fields map[string]FieldType, opts ...SchemaOption) (*Schema, error)` takes the field map keyed by the column name **after** field
mapping is applied, and copies it; `WithSchemaStrict`, `WithSchemaIndexMissing` and `WithSchemaIndexEmpty` complete the index description. A
`Schema` is immutable and can be shared by every translator over the same index. The schema is not advisory: a field's type decides the shape
of every query built against it, and a mismatch between this map and the actual `FT.CREATE` produces queries that are silently wrong rather than
rejected.

| Operation                                                       | `FieldTypeNumeric`             | `FieldTypeTag`         | `FieldTypeText` |
|-----------------------------------------------------------------|--------------------------------|------------------------|-----------------|
| `field == v`                                                    | `@field:[v v]`                 | `@field:{v}`           | `@field:(v)`    |
| `field != v`                                                    | `-@field:[v v]`                | `-@field:{v}`          | `-@field:(v)`   |
| `field in [a, b]`                                               | `(@field:[a a]\|@field:[b b])` | `@field:{a\|b}`        | `@field:(a\|b)` |
| `field > v` (and `<`…)                                          | `@field:[(v +inf]`             | rejected               | rejected        |
| `"x" in arrayField`                                             | `@field:[x x]`                 | `@field:{x}`           | `@field:(x)`    |
| `field.startsWith(p)`                                           | rejected                       | `@field:{p*}`          | `@field:p*`     |
| `field.contains(s)`                                             | rejected                       | `@field:{*s*}`         | `@field:*s*`    |
| `field.endsWith(s)`                                             | rejected                       | `@field:{*s}`          | `@field:*s`     |
| `field == null`                                                 | `ismissing(@field)` ¹          | same ¹                 | same ¹          |
| `field != null`, `has(field)`                                   | `-ismissing(@field)` ¹         | same ¹                 | same ¹          |
| `field in [a, null]`                                            | `((…)\|(ismissing(@field)))` ¹ | same ¹                 | same ¹          |
| `field == ""`                                                   | rejected                       | `@field:{""}` ²        | `@field:""` ²   |
| `field.contains("")` (and the other string functions with `""`) | rejected                       | `-ismissing(@field)` ¹ | same ¹          |

¹ Only for fields listed with `WithSchemaIndexMissing`; otherwise `filter.ErrUnsupportedOperation`.
² Only for fields listed with `WithSchemaIndexEmpty`; otherwise `filter.ErrUnsupportedOperation`. An empty-string `in` element is joined
as its own clause: `code in ["a", ""]` is `((@code:{a})|(@code:{""}))`.

"Rejected" means `filter.ErrUnsupportedOperation`.

### Minimum RediSearch versions

| Query form                                  | Needs                                                                                                                             |
|---------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------|
| equality, ranges, `in`, `startsWith`        | any RediSearch 2.x                                                                                                                |
| `contains` (infix), `endsWith` (suffix)     | RediSearch 2.6+; declare the field `WITHSUFFIXTRIE` for an efficient lookup                                                       |
| `== null`, `!= null`, `has()` (`ismissing`) | RediSearch 2.10+ (Redis 7.4 / Redis Query Engine), the field declared `INDEXMISSING`, and the query run with `DIALECT 2` or later |
| `== ""`, `!= ""` (`@f:{""}`, `@f:""`)       | RediSearch 2.10+, the TAG or TEXT field declared `INDEXEMPTY`, and `DIALECT 2` or later                                           |

### Arrays

A field declared with `filter.WithArrayFields` is an attribute RediSearch indexes from every element (a TAG with a separator, a JSON
array of tags, text or numbers). Equality matches a document when any element matches, so `tags == "red"` and `"red" in tags` both
translate to `@tags:{red}`; the literal is checked against the declared element kind. `null in tags` is rejected. `size()` has no
counterpart.

### Null and presence

RediSearch indexes no null value: the tag or term `null` is an ordinary string. `field == null` therefore asks whether the attribute is
absent and translates to `ismissing(@field)`, `field != null` and `has(field)` to `-ismissing(@field)` — but only for fields declared
`INDEXMISSING` and listed with `WithSchemaIndexMissing` (storage names). On every other field the three forms fail with
`filter.ErrUnsupportedOperation` rather than matching documents tagged `null`. `ismissing` matches documents that do not contain the
attribute (a hash without the field, a JSON document without the path); store an absent value as absent, not as a `null` or empty
literal. A `null` element of an `in` list matches an absent attribute as well: `note in ["x", null]` translates to
`((@note:{x})|(ismissing(@note)))` and `note in [null]` to `ismissing(@note)`, again only on `WithSchemaIndexMissing` fields. Ordered
comparisons with null (`age > null`) are rejected.

### Empty strings

The empty tag query `@f:{}` is a RediSearch syntax error and an empty TEXT term matches nothing, so the translator never emits either.
RediSearch 2.10 indexes the empty string of a TAG or TEXT field declared `INDEXEMPTY`, queried as `@f:{""}` (TAG) and `@f:""`
(TEXT); list such fields (storage names) with `WithSchemaIndexEmpty`. On every other field a comparison with `""` fails with
`filter.ErrUnsupportedOperation`. A string function with an empty needle (`name.contains("")`, `startsWith("")`, `endsWith("")`) holds
for every string, so it translates to the presence test `-ismissing(@field)` on a `WithSchemaIndexMissing` field and is rejected elsewhere:
the empty wildcard `@f:{**}` is no valid term.

### Timestamps and bytes

A `timestamp(...)` literal becomes Unix epoch seconds — the unit the Meilisearch translator uses — with the sub-second part kept as an
exact decimal fraction (`1704164645.5`), so store timestamps as a NUMERIC epoch in seconds. For another unit convert the `time.Time`
in `WithFilterLiteralEncoder` (for example to `UnixMilli()`). A bytes literal is compared as its UTF-8 text, escaped like any string;
bytes that are not valid UTF-8 fail with `filter.ErrUnsupportedType`.

### Bare identifiers

A bare identifier used as a condition becomes a boolean TAG test — `active` translates to `@active:{true}`, `!active` to `-@active:{true}` — at the
root of an expression and on either side of a logical operator. The TAG form is fixed rather than resolved through the schema: a boolean is only
ever indexed as a TAG, and a NUMERIC or TEXT rendering of `true` would not mean anything.

### Not supported

These have no counterpart in the RediSearch query syntax and fail with `filter.ErrUnsupportedOperation`: `matches()`, `size()`,
`substring()`, ordered comparisons (`<`, `<=`, `>`, `>=`) on TAG and TEXT fields, string functions on NUMERIC fields, field-to-field
operands, `has()` / null comparisons and `null` inside an `in` list on fields not listed with `WithSchemaIndexMissing`, comparisons with
`""` on fields not listed with `WithSchemaIndexEmpty`. Decimal fields are rejected at construction (`filter.ErrUnsupportedType`) unless
`WithFilterLiteralEncoder` converts `decimal.Decimal` to a printable value: RediSearch stores NUMERIC values as doubles.

A prefix shorter than `MINPREFIX` (2 by default) is dropped by RediSearch, so `startsWith("A")` matches nothing. String predicates
match case-insensitively — a TEXT field is tokenized case-folded, and a TAG field folds case unless declared `CASESENSITIVE` — and on a
TEXT field they apply to single terms, not to the whole value.

### Literal operands

Comparison values, `in` list elements, and string-function arguments must be literals. Dynamic operands such as `a == b`, `a in [b]`, or
`a.contains(b)` return `filter.ErrUnsupportedOperation`; a field reference is never converted into a string value.

`NewSchema` copies the map; subsequent caller mutations do not change the translator. Empty `in` lists match nothing for TAG, TEXT, and NUMERIC
fields, including under negation. Literal escaping backslash-escapes every ASCII character but a letter, a digit and the underscore — query
punctuation, parameter markers, the fuzzy `%` and prefix `*` markers, whitespace (a tab or a newline included) and control characters — so a
value is always one term of one field. `EscapeTag(s)` exposes the same escaping for raw RediSearch queries built outside the translator.

### Field names

Every storage name (after the field mapping) is written as `@name` with the same escaping — RediSearch unescapes a field name — so a dotted,
dashed or punctuated name stays one attribute: `address.city` renders `@address\.city`, `a-b` renders `@a\-b`. The name is checked first,
independently of the policy, which may let a client-chosen suffix through (a prefix allow-list such as `labels.*`, or a prefix mapping): a name
that is empty, not valid UTF-8, holds white space or control characters (which RediSearch cannot escape), or has an empty dot-separated segment
(`a..b`, `.a`, `a.`) fails with `filter.ErrInvalidExpression`, whatever the policy allows. The order translator refuses the same names with
`orderby.ErrInvalidFieldPath`.

### Errors

| Sentinel                         | When                                                                                                                                        |
|----------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------|
| `filter.ErrPolicyRequired`       | nil policy                                                                                                                                  |
| `filter.ErrInvalidOptions`       | nil schema; `WithSchemaStrict(true)` with an empty field map (from `NewSchema`)                                                             |
| `filter.ErrFieldNotAllowed`      | field outside the allow-list, or missing from the schema with `WithSchemaStrict(true)`                                                      |
| `filter.ErrFieldTypeMismatch`    | literal kind differs from `filter.WithFieldTypes` (element kind for array fields)                                                           |
| `filter.ErrUnsupportedOperation` | the forms listed under "Not supported"                                                                                                      |
| `filter.ErrUnsupportedType`      | literal type with no RediSearch form (decimal without encoder, non-UTF-8 bytes)                                                             |
| `filter.ErrInvalidExpression`    | malformed expression (literal as clause, non-number for NUMERIC, missing operand); a storage name refused under [Field names](#field-names) |

### Configuration and compatibility

| API           | Purpose                                                       |
|---------------|---------------------------------------------------------------|
| `ValidateAST` | Check the configured renderer without contacting the database |

The constructors accept this package's generated `FilterOption` values; the index description is a separate `Schema` argument. Shared policies
and callbacks must be safe for concurrent use. The translator exposes no `filter.Visitor` methods; call `Translate`. `ValidateAST` checks actual
expression shapes and values, including field-to-field operands, rather than promising support for an operator regardless of its operands.
Unsupported expressions return filter sentinel errors. Backend-native differences remain explicit.

Literal encoders return values, never executable query text. They can choose timestamp units or stored enum representations.

`WithSchemaStrict(true)` rejects unknown mapped fields; the default retains TAG fallback, so `NewFilterTranslator` (no schema) renders every
field as TAG.

Field authorization runs before any shape check: a denied field anywhere in the expression, a function argument included
(`name.contains(secret)`), fails with `filter.ErrFieldNotAllowed`, never with `filter.ErrUnsupportedOperation`.

`WithFilterPredicateRenderer` installs a trusted, concurrent-safe callback for application predicates. Field authorization and limits run first.
Return `handled=false` to delegate to built-ins; encode every client value safely before returning query text.

### Options

| Option                        | Default | Behavior                                                                                      |
|-------------------------------|---------|-----------------------------------------------------------------------------------------------|
| `WithFilterCollector`         | nil     | Records `filter_redis_translate_duration_seconds`; nil disables collection.                   |
| `WithFilterOptions`           | nil     | Binds the shared policy to the storage names of this backend (`filter.WithFieldMapping`).     |
| `WithFilterLiteralEncoder`    | nil     | Converts values before backend quoting; must be safe for concurrent use.                      |
| `WithFilterPredicateRenderer` | nil     | Trusted predicate override; return `handled=false` to use the built-ins.                      |

Schema options (`SchemaOption`, for `NewSchema`):

| Option                   | Default | Behavior                                                                                             |
|--------------------------|---------|------------------------------------------------------------------------------------------------------|
| `WithSchemaIndexMissing` | nil     | Storage fields declared `INDEXMISSING`; enables `has()`, `== null`, `!= null` via `ismissing()`.     |
| `WithSchemaIndexEmpty`   | nil     | TAG and TEXT storage fields declared `INDEXEMPTY`; enables `== ""`, `!= ""` via `@f:{""}` / `@f:""`. |
| `WithSchemaStrict`       | false   | Rejects fields missing from the schema instead of falling back to TAG; requires a non-empty map.     |

### Named values

| Constant           | Meaning                                            |
|--------------------|----------------------------------------------------|
| `FieldTypeNumeric` | A NUMERIC field indexed with SORTABLE.             |
| `FieldTypeTag`     | A TAG field (exact match, pipe-separated values).  |
| `FieldTypeText`    | A TEXT field (full-text search with prefix/infix). |

### API

| Symbol                                                   | Purpose                                                                                                    |
|----------------------------------------------------------|------------------------------------------------------------------------------------------------------------|
| `FilterTranslator`                                       | Converts filter AST nodes to RediSearch query strings; `Translate` returns `*` for a nil node.             |
| `NewFilterTranslator(policy, opts...)`                   | Binds the shared `*filter.Policy` over an all-TAG index; nil policy fails with `filter.ErrPolicyRequired`. |
| `NewFilterTranslatorWithSchema(policy, schema, opts...)` | Binds the policy to the index `schema`; a nil schema fails with `filter.ErrInvalidOptions`.                |
| `Schema`, `NewSchema(fields, opts...)`                   | Immutable description of the `FT.CREATE` index: field types, strictness, `INDEXMISSING`, `INDEXEMPTY`.     |
| `SchemaOption`                                           | Functional option of `NewSchema`.                                                                          |
| `FilterTranslator.ValidateAST(node)`                     | Renders and discards the query (`filter.Compatibility`).                                                   |
| `FilterOption`                                           | Functional option of `NewFilterTranslator` and `NewFilterTranslatorWithSchema`.                            |
| `FieldType`                                              | RediSearch index type of a storage field.                                                                  |
| `EscapeTag(s)`                                           | The translator's escaping, for raw RediSearch queries built outside it.                                    |

### Configuration example

```go
policy, err := filter.NewPolicy(
    filter.WithAllowedFields("status", "priority", "id", "lastRunAt"),
    filter.WithMaxDepth(10),
) // build once per resource
if err != nil { return err }

schema, err := redis.NewSchema(map[string]redis.FieldType{
    "status": redis.FieldTypeTag, "priority": redis.FieldTypeNumeric, "id": redis.FieldTypeTag, "last_run_at": redis.FieldTypeNumeric,
}, redis.WithSchemaIndexMissing("last_run_at")) // storage names, after the field mapping
if err != nil { return err }

trans, err := redis.NewFilterTranslatorWithSchema(policy, schema, redis.WithFilterOptions(filter.WithFieldMapping(map[string]string{
    "lastRunAt": "last_run_at",
})))
```

## Order translator

`OrderTranslator` translates an `orderby.Spec` into a single-field RediSearch sort instruction. RediSearch's `FT.SEARCH ... SORTBY` accepts
exactly one field; multi-key inputs return `orderby.ErrTooManySortKeys`.

### Contract

`NewOrderTranslator(policy, opts...)` binds the resource's shared `orderby.Policy` (allow-list, field access, denied API fields, default order) to
the storage options passed through `WithOrderOptions` (mapping, KeyResolver, denied storage fields, tie-breakers); a nil policy returns
`orderby.ErrPolicyRequired`. `WithOrderCollector` records the translation duration as the histogram `orderby_redis_translate_duration_seconds`.
Translation validates every order with the policy before mapping it, so it never renders a key the policy refuses.

RediSearch has no per-key null placement or collation: any explicit placement or collation returns `orderby.ErrUnsupportedOrdering`.
`FT.SEARCH ... SORTBY` takes one field, so an order that resolves to more than one key — several client keys, or one key plus a tie-breaker —
returns `orderby.ErrTooManySortKeys`, which wraps `orderby.ErrUnsupportedOrdering`: one `errors.Is(err, orderby.ErrUnsupportedOrdering)`
check classifies every order a backend cannot represent. Configure parser behavior separately with `orderby.ParserOption`.

### Usage

```go
parser, _ := orderby.NewParser()
ob, _ := parser.Parse(ctx, "createdAt desc")

policy, err := orderby.NewPolicy(orderby.WithAllowedFields("createdAt"))
if err != nil {
    return err
}

trans, err := redis.NewOrderTranslator(policy)
if err != nil {
    return err
}
sortBy, _ := trans.Translate(ob)
// sortBy == redis.SortBy{Field: "createdAt", Descending: true}

args := []any{"FT.SEARCH", "idx", "*"}
if sortBy.Field != "" {
    direction := "ASC"
    if sortBy.Descending {
        direction = "DESC"
    }
    args = append(args, "SORTBY", sortBy.Field, direction)
}
```

`SortBy.Field` is empty for an empty `Spec` when the policy has no default order and the translator no tie-breakers; callers should skip the
`SORTBY` clause in that case. A default order or tie-breaker that resolves to more than one key returns `orderby.ErrTooManySortKeys`.

### Options

| Option               | Default | Behavior                                                                                                        |
|----------------------|---------|-----------------------------------------------------------------------------------------------------------------|
| `WithOrderOptions`   | nil     | Storage options: `orderby.TranslatorOption` values (mapping, KeyResolver, denied storage fields, tie-breakers). |
| `WithOrderCollector` | nil     | Records `orderby_redis_translate_duration_seconds`; nil disables collection.                                    |

### API

| Symbol                                | Purpose                                                                                        |
|---------------------------------------|------------------------------------------------------------------------------------------------|
| `SortBy`                              | The single-field instruction of `FT.SEARCH ... SORTBY`; a zero value means no `SORTBY` clause. |
| `OrderTranslator`                     | Converts an `orderby.Spec` into a `SortBy`; safe for concurrent use.                           |
| `NewOrderTranslator(policy, opts...)` | Binds the shared `orderby.Policy` to the storage options of `WithOrderOptions`.                |
| `OrderOption`                         | Functional option of `NewOrderTranslator`.                                                     |

### Errors

`Translate` returns `orderby.ErrFieldNotAllowed` when an entry references a field the policy refuses (allow-list, field access, denied
fields) or that resolves to a denied storage path, `orderby.ErrDuplicateKey` when two keys resolve to the same storage name,
`orderby.ErrTooManySortKeys` (wrapping `orderby.ErrUnsupportedOrdering`) for more than one resolved key, `orderby.ErrUnsupportedOrdering`
for null placement or a collation, and `orderby.ErrInvalidFieldPath` for a storage name refused under [Field names](#field-names).
`NewOrderTranslator` returns `orderby.ErrPolicyRequired` for a nil policy and `orderby.ErrFieldNotAllowed` for a tie-breaker on a denied
storage path. `orderby.ErrAllowlistRequired` is raised by `orderby.NewPolicy`.

## Projection translator

`ProjectionTranslator` translates a [`projection.Spec`](../../projection/README.md) into the `RETURN` clause of a Redis Query Engine
(RediSearch) `FT.SEARCH` command.

### Contract

`NewProjectionTranslator(policy, opts...)` binds the resource's shared `projection.Policy` to storage options passed with `WithProjectionOptions`
(mapping, denied and required storage fields) and renders the selection of every view it serves once, so a misconfigured view fails at
construction. A nil policy returns `projection.ErrPolicyRequired`. `Translate(spec)` takes a read mask alone; `TranslateView(spec, view)` applies
the AIP-157 rule of `projection.Policy.Select`.

### Output

| Selection                                               | Arguments to append to `FT.SEARCH`      |
|---------------------------------------------------------|-----------------------------------------|
| Mask with paths, a view, or FULL under an allow-list    | `RETURN n field …` plus required fields |
| `ModeNone`, no required fields                          | `RETURN 0` — document keys only         |
| `ModeNone` with required fields                         | `RETURN n required …`                   |
| Omitted mask, `*` or FULL, no allow-list, no denies     | `nil` — no `RETURN` clause              |
| Omitted mask, `*` or FULL, no allow-list, denied fields | construction error (no exclusion form)  |

Fields appear in lexical order of their storage name and are never aliased (`AS`). A storage name is an attribute identifier or, on a JSON index, a
JSONPath such as `$.address.city` reached through the field mapping; dotted names are passed as they are — RediSearch attributes are flat, so
`address` and `address.city` are two attributes and neither is collapsed into the other. Each name is a separate argument, so no quoting applies.

### Usage

```go
policy, err := projection.NewPolicy(
	projection.WithUntrustedInput(true),
	projection.WithAllowedFields("name", "city"),
)
trans, err := redis.NewProjectionTranslator(policy,
	redis.WithProjectionOptions(
		projection.WithFieldMapping(map[string]string{"city": "$.address.city"}),
		projection.WithRequiredFields("id"),
	),
)
args, err := trans.Translate(spec) // "name" → ["RETURN", "2", "id", "name"]
cmd := []any{"FT.SEARCH", "idx:users", query}
for _, a := range args {
	cmd = append(cmd, a)
}
```

### Options

| Option                    | Default | Behavior                                                                                             |
|---------------------------|---------|------------------------------------------------------------------------------------------------------|
| `WithProjectionOptions`   | nil     | Storage options: `projection.TranslatorOption` values (mapping, denied and required storage fields). |
| `WithProjectionCollector` | nil     | Records `projection_redis_translate_duration_seconds`; nil disables collection.                      |

### API

| Symbol                                           | Purpose                                                                         |
|--------------------------------------------------|---------------------------------------------------------------------------------|
| `Return`                                         | The `RETURN` keyword.                                                           |
| `ProjectionTranslator`                           | Renders the `RETURN` clause; safe for concurrent use.                           |
| `NewProjectionTranslator(policy, opts...)`       | Binds the shared policy; rejects policies that need an exclusion.               |
| `ProjectionTranslator.Translate(spec)`           | Returns the `RETURN` arguments; `nil` for every field.                          |
| `ProjectionTranslator.TranslateView(spec, view)` | Like `Translate`, with the AIP-157 view deciding when the read mask is omitted. |
| `ProjectionOption`                               | Functional option of `NewProjectionTranslator`.                                 |

### Errors

| Error                                 | When                                                                                             |
|---------------------------------------|--------------------------------------------------------------------------------------------------|
| `projection.ErrPolicyRequired`        | Nil policy                                                                                       |
| `projection.ErrAllFieldsUnresolvable` | Every authorized field would exclude denied fields; list the allowed fields                      |
| `projection.ErrUnsupportedSelection`  | An exclusion selection reaches the renderer (prevented at construction)                          |
| `projection.ErrInvalidFieldPath`      | Grammar error, or a storage name that is empty, `AS`, or holds white space or control characters |
| `projection.ErrFieldNotAllowed`       | A path outside the policy, or reaching a denied storage path                                     |
| `projection.ErrUnsupportedPath`       | Wildcards, map keys or indexes (AIP-161)                                                         |
| `projection.ErrInvalidView`           | A view the policy does not serve                                                                 |

## Related packages

- [filter](../../filter/README.md) and the [filter guide](../../docs/filter.md)
- [orderby](../../orderby/README.md) and the [orderby guide](../../docs/orderby.md)
- [projection](../../projection/README.md), [projection/projectiontest](../../projection/projectiontest/README.md) and the
  [projection guide](../../docs/projection.md)
- [backend/meili](../meili/README.md) — the other search-engine backend
- [backend/sqlbase](../sqlbase/README.md) — the shared SQL backend machinery
