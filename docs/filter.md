# Filtering

The [filter](../filter/) package defines a shared AST, immutable field policy, and validation. [parser](../filter/parser/) translates CEL
syntax to that AST; [evaluator](../filter/evaluator/) and the backend filter translators (one package per backend under
[backend/](../README.md#backends)) consume it under explicit policies.

## When to reach for `filter`

| Scenario                                                                         | Use                                                                                 |
|----------------------------------------------------------------------------------|-------------------------------------------------------------------------------------|
| Accept a user-defined query string and run it against MongoDB                    | `Parser` + `backend/mongo.NewFilterTranslator`                                      |
| The same against ClickHouse, MariaDB/MySQL or PostgreSQL                         | `backend/clickhouse`, `backend/mariadb`, `backend/postgres` (`NewFilterTranslator`) |
| In-memory predicate over decoded data (config gating, in-process search)         | `parser.Parser` + `evaluator.Evaluator`                                             |
| Server-side filter on plain Redis keys via `EVAL`                                | `backend/lua`                                                                       |
| Server-side filter on a RediSearch index                                         | `backend/redis`                                                                     |
| Server-side filter on a Meilisearch index                                        | `backend/meili`                                                                     |
| Expose semantic API filters (`createdAfter`, `between`) that map to real columns | `WithCustomFunctions` + `CompareField`                                              |
| Untrusted CEL from end users                                                     | `WithUntrustedInput` + `WithAllowedFields` (mandatory pair)                         |
| Decouple API field names from DB columns                                         | `filter.WithFieldMapping` on the translator (`WithFilterOptions`)                   |

## Parser / policy / translator

Every option belongs to exactly one of three layers:

1. **Parser** (`parser.Option`) — the syntax and resource limits of the request text: expression length, the LRU cache, the callable
   functions, custom function expansions, enum value names, the timestamp literal grammar.
2. **Policy** (`filter.PolicyOption`, built with `filter.NewPolicy`) — what the API lets the client ask for, in CEL-side (API) field names and
   independent of any backend: the allowlist, a shared `fieldpolicy.Access`, the untrusted-input guard, field kinds, enum values, array fields,
   value codecs, the portable contract, the regex rules and the depth/operation limits of a parsed AST. A policy is built once per resource
   (`listing.Schema.FilterPolicy` builds it from the schema) and passed to every translator and evaluator; it is immutable and never changed by them.
3. **Translator** (the backend's `FilterOption`, with `filter.TranslatorOption` inside `WithFilterOptions`) — how one backend expresses a validated
   filter: the API → storage name mapping (`filter.WithFieldMapping`), the dialect and its storage semantics (a schema profile such as
   `postgres.NewSchema` with its contract mode, collations and decimal formats), rendering hooks (`WithFilterPredicateRenderer`,
   `WithFilterLiteralEncoder`) and telemetry (`WithFilterCollector`). `filter.NewTranslatorContext` binds the shared policy to these storage names.

No translator or evaluator is built without a policy (`filter.ErrPolicyRequired`), and a policy for untrusted input cannot be built without
an allowlist or an access policy that restricts the fields (`filter.ErrAllowlistRequired`), so untrusted input never reaches a backend unchecked.
Field names are dotted paths of plain identifiers (`[A-Za-z_][A-Za-z0-9_]*` per segment): the parser refuses CEL backtick escapes that are not
plain identifiers and root-scoped names, and `filter.CheckFieldReference` refuses any other name, so a wildcard allowlist (`labels.*`) never admits
a name a translator would have to escape.

## Quick start

### Parser → MongoDB query

```go
parser, err := filterparser.New()
if err != nil { return err }

ast, err := parser.Parse(ctx, `name == "John" && age >= 18`)
if err != nil { return err }

policy, err := filter.NewPolicy(filter.WithAllowedFields("name", "age")) // once per resource
if err != nil { return err }

trans, err := mongo.NewFilterTranslator(policy)
if err != nil { return err }

bsonFilter, err := trans.Translate(ast)
// → {"$and":[{"name":"John"},{"age":{"$gte":18}}]}
```

### Parser → SQL

The SQL translators return the clause and its bind arguments separately, so no literal from the filter ever enters the query text:

```go
trans, err := postgres.NewFilterTranslator(policy) // the same policy as above
if err != nil { return err }

where, args, err := trans.Translate(ast)
// where → ("name" = $1) AND ("age" >= $2)
// args  → []any{"John", int64(18)}

rows, err := conn.Query(ctx, `SELECT * FROM users WHERE `+where, args...)
```

### Parser → in-memory evaluator

```go
parser, _ := filterparser.New()
ast, _ := parser.Parse(ctx, `status == "active" && size(tags) > 0`)

policy, err := filter.NewPolicy(filter.WithAllowedFields("status", "tags"))
if err != nil { return err }

eval, err := evaluator.New(policy)
if err != nil { return err }

ok, err := eval.Evaluate(ast, map[string]any{
    "status": "active",
    "tags":   []any{"vip"},
})
// ok == true
```

An `Evaluator` keeps only its configuration; each `Evaluate` call walks the tree with its own data and depth/operation counters, so one
instance can be built at startup and shared by every goroutine.

A null or absent value gets SQL's three-valued logic, so the evaluator selects the records the SQL translators select: an ordering comparison
(`age > 1`), a bare boolean, `size()` or `substring()` of a null value, a `substring()` out of range and a null string-function argument are
unknown; `&&` and `||` absorb unknown as SQL's `AND` and `OR` do, in either order (`age > 1 || active` holds where `active` does), `!` keeps it
unknown (`!(age > 1)` does not select a null `age`), and an unknown filter does not match — `Evaluate` returns false with a nil error. Equality
with null, `in`, `has()` and a string function on a null target are never unknown. Errors are kept for the problems that are not a null: a value
of an incomparable type, the data budget, the operation limits. The MongoDB profile keeps MongoDB's rules (`$not` matches a missing field). See
the [evaluator guide](../filter/evaluator/README.md#three-valued-logic).

### Untrusted input (HTTP query parameter)

```go
policy, err := filter.NewPolicy(
    filter.WithUntrustedInput(true),
    filter.WithAllowedFields("name", "status", "createdAt"),
    filter.WithMaxDepth(10),
)
if err != nil { return err }

trans, err := mongo.NewFilterTranslator(policy)
if err != nil { return err }
```

Every policy requires an allowlist, and every translator and evaluator requires a policy: without a non-empty `WithAllowedFields` (or a
`WithFieldAccess` that restricts the fields) `filter.NewPolicy` fails with `ErrAllowlistRequired`, instead of letting a hostile client filter on
`passwordHash > ""` to enumerate accounts. `WithAnyField` opts out for a filter the service builds itself; `WithUntrustedInput` always requires the
allowlist and rejects `matches()` — a client regex would run on the database's backtracking engine — unless `WithSafeRegex` admits it. Every `matches()`
pattern, trusted or not, is held to a safe subset: single-character repetitions only (no repeated parenthesized group, not even one Go folds into a
class such as `(?:a|b)*`), at most two unbounded and four variable repetitions, counted bounds up to 100, alternations and bounded variable repetitions
whose choices (the branches of each alternation as written, so `(?:.|a)(?:.|a)…$` counts 2 per group although Go folds it into `.{n}$`; `m-n+1`, 2 for
`?`) multiply to at most 1000, no `.*.*`-like run of unbounded repetitions over shared characters, every literal `{` escaped and no octal escape
`\1`–`\7`; any other pattern fails with `ErrInvalidRegex`. `WithUnsafeRegex` lifts the subset for patterns the service writes itself and cannot be
combined with `WithUntrustedInput`.

## CEL syntax

| Category      | Operators / functions                                                                   |
|---------------|-----------------------------------------------------------------------------------------|
| Comparison    | `==`, `!=`, `<`, `<=`, `>`, `>=`                                                        |
| Logical       | `&&`, `\|\|`, `!`                                                                       |
| Membership    | `in`, `has(field)`                                                                      |
| String        | `field.contains(s)`, `field.startsWith(s)`, `field.endsWith(s)`, `field.matches(regex)` |
| Substring     | `field.substring(start)` or `field.substring(start, end)` as a comparison operand       |
| Other         | `field.size()` or `size(field)`, `timestamp("RFC3339 string")`                          |
| Nested fields | dot notation: `address.city == "NYC"`                                                   |

`timestamp()` is parsed at AST construction time and stored as `time.Time` in a `LiteralNode` — it is not deferred to the translator.

## Custom functions

Register CEL functions that the parser expands into AST nodes before evaluation or translation. This is how you keep the API free of storage
field names: a client sends `createdAfter("2024-01-01")` and the parser rewrites it to `createdAt > "2024-01-01"`. Translators never see the
virtual name.

`Constant(field, op, value)` and `CompareField(field, op)` are the two fundamental building blocks:

- `Constant` — parameter-less predicate (`name() → field op value`). Natural fit for domain status enums and fixed thresholds.
- `CompareField` — one-argument predicate (`name(arg) → field op arg`). Natural fit for "after / before" comparisons over a caller-supplied operand.

```go
filterparser.WithCustomFunctions(map[string]filter.CustomFunction{
    "isActive":     functions.Constant("status", filter.OpEqual, "active"),
    "isPending":    functions.Constant("status", filter.OpEqual, "pending"),
    "hasFailures":  functions.Constant("failures", filter.OpGT, int64(0)),
})
```

`CompareField` is shown below for the "with argument" case:

```go
parser, _ := filterparser.New(filterparser.WithCustomFunctions(map[string]filter.CustomFunction{
    "createdAfter":  functions.CompareField("createdAt", filter.OpGT),
    "updatedAfter":  functions.CompareField("updatedAt", filter.OpGT),
    "createdBefore": functions.CompareField("createdAt", filter.OpLT),
}))
```

Multi-argument and compound expansions are fine — the handler can return any `Node`:

```go
between := func(args []filter.Node) (filter.Node, error) {
    if len(args) != 3 {
        return nil, fmt.Errorf("between: 3 args, got %d", len(args))
    }
    field := args[0]
    return &filter.BinaryOpNode{
        Op:    filter.OpAnd,
        Left:  &filter.BinaryOpNode{Op: filter.OpGTE, Left: field, Right: args[1]},
        Right: &filter.BinaryOpNode{Op: filter.OpLTE, Left: field, Right: args[2]},
    }, nil
}
```

Constraints:

- Names cannot collide with built-ins unless explicitly permitted by `parser.WithAllowBuiltinOverride` (`contains`, `startsWith`, `endsWith`, `matches`,
  `size`, `has`, `timestamp`, `substring`). Validation runs at `parser.New` — misconfiguration fails fast.
- Handlers run on every parser cache miss; keep them cheap and side-effect free.
- The parser does not bound the depth of trees a handler returns. `WithMaxDepth` enforces the cap during translator/evaluator traversal.
- Handlers return built-in nodes or `filter.ExtensionNode` values that lower to acyclic built-in trees. Results are copied before caching; literals
  may hold immutable scalars, reference-free arrays and structs, `time.Time`, and copied `[]byte` values. Mutable references and unsupported nodes
  fail with `ErrUnsupportedType`.
- The expanded AST contains the **target** field (`createdAt`), so `WithFieldMapping` and `WithAllowedFields` operate on that name, not on
  the virtual one.

### Built-in presets

`TimestampFilters()` returns a ready-made set covering the common `createdAt`/`updatedAt`/`deletedAt` predicates. Functions are not registered
automatically — opt in explicitly:

```go
filterparser.WithCustomFunctions(functions.TimestampFilters())
```

| Function          | Expansion       |
|-------------------|-----------------|
| `createAfter(t)`  | `createdAt > t` |
| `createBefore(t)` | `createdAt < t` |
| `updateAfter(t)`  | `updatedAt > t` |
| `updateBefore(t)` | `updatedAt < t` |
| `deleteAfter(t)`  | `deletedAt > t` |
| `deleteBefore(t)` | `deletedAt < t` |

The map is freshly built per call, so callers can `delete` unwanted entries before passing it on. Field names are fixed (camelCase); use
`WithFieldMapping` on the translator when storage columns follow a different convention (e.g. snake_case).

For partial registration use `SelectTimestampFilters(names ...string)` — pass the `TimestampFunc*` constants for the entries you want:

```go
filterparser.WithCustomFunctions(functions.SelectTimestampFilters(
    functions.TimestampFuncCreateAfter,
    functions.TimestampFuncCreateBefore,
    functions.TimestampFuncUpdateAfter,
    functions.TimestampFuncUpdateBefore,
))
```

Unknown names are silently skipped (no validation), which keeps the helper safe for dynamically constructed lists. Pass empty input to
get an empty map.

#### `BetweenFilter()` — generic range predicate

```go
filterparser.WithCustomFunctions(functions.BetweenFilter())
```

| Function                 | Expansion                    |
|--------------------------|------------------------------|
| `between(field, lo, hi)` | `field >= lo && field <= hi` |

The first argument must be an identifier (a field reference), the bounds may be any expression the translator accepts. The expanded
And-tree is portable across all translators, so the same call works against MongoDB, RediSearch, and the in-memory evaluator.

#### `SoftDeleteFilters()` — soft-delete predicates

```go
filterparser.WithCustomFunctions(functions.SoftDeleteFilters())
```

| Function        | Expansion           |
|-----------------|---------------------|
| `notDeleted()`  | `deletedAt == null` |
| `onlyDeleted()` | `deletedAt != null` |

Equality with `null` matches both missing and explicitly null values under MongoDB and the in-memory evaluator — the semantics callers
typically want for soft delete. For a stricter "field absent" check, use the built-in `!has(deletedAt)` directly.

### Explicit registration

Pass `parser.WithCustomFunctions(functions)` to each parser that should receive those handlers. Parsers snapshot the supplied map;
there is no process-wide registry. Reusing an option is safe when handlers themselves are concurrency safe.

## Translators

| Package                                                 | Output                    | Use when                                                      |
|---------------------------------------------------------|---------------------------|---------------------------------------------------------------|
| [`backend/mongo`](../backend/mongo/README.md)           | `bson.M`                  | MongoDB collection scan or aggregation `$match`               |
| [`backend/clickhouse`](../backend/clickhouse/README.md) | SQL `WHERE` clause + args | ClickHouse                                                    |
| [`backend/mariadb`](../backend/mariadb/README.md)       | SQL `WHERE` clause + args | MariaDB or MySQL                                              |
| [`backend/postgres`](../backend/postgres/README.md)     | SQL `WHERE` clause + args | PostgreSQL                                                    |
| [`backend/redis`](../backend/redis/README.md)           | RediSearch query string   | Server-side filter on a Redis hash with the RediSearch module |
| [`backend/lua`](../backend/lua/README.md)               | Lua boolean expression    | Filter plain Redis keys via `EVAL`                            |
| [`backend/meili`](../backend/meili/README.md)           | Meilisearch filter string | Server-side filter on a Meilisearch index                     |

Every backend's `NewFilterTranslator` takes the shared `*filter.Policy` first, then the backend's `FilterOption` values; `WithFilterOptions` carries the
`filter.TranslatorOption` values that bind the policy to the backend's storage names (`filter.WithFieldMapping`). Every constructor returns
`(*FilterTranslator, error)` (the SQL schema constructors `(*SchemaFilterTranslator, error)`) — `ErrPolicyRequired` for a nil policy; the allowlist
error (`ErrAllowlistRequired`) is raised earlier by `filter.NewPolicy`, so the misconfiguration surfaces at startup rather than on the first request.
The SQL schema translators take their storage settings from a `<backend>.Schema` (`NewSchema(*sqlbase.Schema, ...SchemaOption)`) and only the shared
`WithFilterOptions` and `WithFilterCollector` from their options; a scalar-only option fails them with `ErrInvalidOptions` instead of being ignored.

### SQL

The three SQL translators share one AST walk and differ only in a dialect — identifier quoting, bind-marker spelling, string predicates, inline
literal rendering. Their `Translate` returns three values, `(where string, args []any, err error)`; `TranslateInline` renders the same clause with
literals in place, for view definitions and generated DDL. PostgreSQL numbers its placeholders (`$1`, `$2`, …), the other two use positional `?`.
A `nil` AST translates to `1 = 1`, so `"... WHERE " + where` needs no special case.

Three dialect differences are worth knowing before designing a filter surface: MariaDB's default collations make every string comparison
case-insensitive (and, being PAD SPACE, make `==`, `!=`, `<`, `<=`, `>`, `>=` and `in` ignore trailing spaces; `contains`, `startsWith`, `endsWith` and
`size()` stay exact; a `$` in `matches()` is sent as `\z` because PCRE and ICU would also match it before a final newline), PostgreSQL requires a real
`boolean` column for a bare-identifier condition, and only ClickHouse reads a dotted CEL name as one (Nested) column instead of a qualified
`"table"."column"`.

SQL inline output is pure ASCII (PostgreSQL `\uXXXX`, MariaDB `_utf8mb4 X'…'`, ClickHouse `\xNN`); in MariaDB/PostgreSQL a dotted field must be
declared.

The SQL translators also accept another field as an operand — in comparisons, `in` list elements and `contains` / `startsWith` / `endsWith`
arguments — with field mapping and the allowlist applied to every reference. A list with a field reference renders as an equality disjunction
instead of `IN`, and `matches()` still requires a literal pattern. The MongoDB translator renders field operands through `$expr` (whole-value
comparison, nulls equal, ordering type-bracketed; `matches()` literal only), and Lua and the evaluator accept them too. The Meilisearch and
RediSearch translators accept literals only in value positions and return `ErrUnsupportedOperation` for `a == b`, `a in [b]` or
`a.contains(b)`; custom visitors get the same check from `filter.AcceptLiteral` and `filter.VisitLiteralElements`.

SQL array fields (`filter.WithArrayFields`) use element semantics like the evaluator and MongoDB: PostgreSQL arrays (`= ANY`, `unnest`, `cardinality`),
MariaDB JSON arrays (`JSON_CONTAINS`, `JSON_TABLE`, `JSON_LENGTH`; MariaDB 10.6+), ClickHouse `Array(T)` (`has`, `arrayExists`, `length`). `arr != x`
holds when no element equals x or the array is NULL/absent (`(col IS NULL OR NOT (…))`); ClickHouse Array columns cannot be NULL, so null/presence tests
there are `empty()` / `notEmpty()` and an empty array also matches `arr == null`. Field-vs-field comparisons of incompatible declared kinds fail with
`ErrFieldTypeMismatch` on SQL, Lua and Mongo. A NULL SQL column matches like a null or absent field in the evaluator rather than under three-valued
logic: `x != v` is `(x != v OR x IS NULL)`, `!` is pushed down to each predicate (`!(x == v)`, `!(x in [...])` and `!x.contains(s)` match NULL; a
negated ordered comparison does not, as it stays unknown in the evaluator too), and two field operands compare null-safe (`IS NOT DISTINCT FROM`, `<=>`,
`COALESCE(a = b, a IS NULL AND b IS NULL)` on ClickHouse). The schema-aware PostgreSQL, MariaDB and ClickHouse translators
(`NewFilterTranslatorWithSchema`, also in listing contract mode) follow the same rules, as does the in-memory evaluation they pair with
(`evaluator.WithProfile`, the memory listing engine). A denied field anywhere in the expression fails with `ErrFieldNotAllowed` before any shape check.
Field operands next to an array field fail with `filter.ErrUnsupportedOperation`. A decimal field needs no literal encoder: the value is bound and cast
(`CAST(? AS NUMERIC)`, `CAST(? AS DECIMAL(p,s))`, `toDecimal128(?, s)`). `substring()` renders as a comparison operand (`substr`, `SUBSTRING`,
`substringUTF8`) guarded by the string length, so an out-of-range call does not match; the schema-aware translators render it as a value that is NULL
where the substring does not exist (a NULL field or a range beyond its end), which the listing contract never sees: its request validation takes only
fields and literals as operands.

### Search back ends

`backend/meili` rejects `endsWith`, `matches` (regex), counting `size()`, field operands and `substring()` with `ErrUnsupportedOperation` —
Meilisearch's filter grammar has no counterparts. `size(f) == 0` renders as `IS EMPTY` (absent and null included) and `size(f) > 0` as `f EXISTS AND f
IS NOT NULL AND f IS NOT EMPTY`; other sizes are rejected. `== ""` renders as `IS EMPTY` (Meilisearch 1.2+). `timestamp(...)` literals are emitted as
Unix seconds with the exact fraction kept (`1672531199.5`): store the corresponding fields as numeric epoch seconds and add them to the index's
`filterableAttributes`. `contains()` needs Meilisearch's experimental `containsFilter` feature (1.10+) and is rejected unless
`WithFilterContainsFilter(true)` declares it; `startsWith()` works on 1.17+ without the feature, or on 1.11+ with it. `WithFilterServerVersion` declares
an older server, and an operator that release lacks fails with `ErrUnsupportedOperation` instead of on the server. A `tags = "red"` comparison matches
an array attribute by element (`filter.WithArrayFields`, `"red" in tags`).

Meilisearch also splits a question CEL's `null` treats as one — an attribute can be absent, or present and null — so `field == null` compiles to
`(field IS NULL OR field NOT EXISTS)` and `field != null` to `(field EXISTS AND field IS NOT NULL)`. A bare `IS NOT NULL` would match documents
that never had the attribute, which is how a soft-delete filter comes to return deleted rows. For the same reason a `null` element in an `in`
list becomes `(field IN [...] OR (field IS NULL OR field NOT EXISTS))`.

`backend/redis` takes its index description as a `redis.Schema` (`redis.NewSchema(map[string]FieldType, ...SchemaOption)`, passed to
`redis.NewFilterTranslatorWithSchema`), keyed by the column name **after** field mapping. It is not advisory: a field's type decides the shape of every
query built against it, so a schema that disagrees with the actual `FT.CREATE` produces queries that are silently wrong rather than rejected. A field
missing from the schema is treated as TAG; `WithSchemaStrict(true)` rejects it with `ErrFieldNotAllowed` instead, and `NewSchema` fails with
`ErrInvalidOptions` when strict mode has no fields. `== null`, `!= null` and `has()` translate to `ismissing(@field)` only for fields declared
`INDEXMISSING` and listed in `WithSchemaIndexMissing` (RediSearch 2.10+); on other fields they fail with `ErrUnsupportedOperation` rather than matching
a `null` tag. `endsWith()` and infix `contains()` need RediSearch 2.6+. Timestamps are sent as epoch seconds (an exact fraction for sub-second values)
and bytes as UTF-8 text.

`backend/lua` reads the record from the variable `d` (`WithFilterTableVar` names another). Array fields (`WithArrayFields`) are tested element-wise like
Mongo and the evaluator; timestamps render as Unix epoch seconds (as Meili), override with `WithFilterLiteralEncoder`; a predicate the evaluator finds
unknown (or fails for that record) is a non-match, also under negation, since `!` is pushed down to the predicates. Redis, Meilisearch and Lua print
literals into the query text, so a decimal field needs a `WithFilterLiteralEncoder` that returns a string or a number; without one it fails with
`ErrUnsupportedType`.

### Backend options

Every backend package names its filter options with the `WithFilter` prefix. Y = supported; – = not applicable.

| Option / port                 | postgres                                            | mariadb                                                           | clickhouse                                                           | mongo                                            | redis                                            | meili | lua | evaluator                    |
|-------------------------------|-----------------------------------------------------|-------------------------------------------------------------------|----------------------------------------------------------------------|--------------------------------------------------|--------------------------------------------------|-------|-----|------------------------------|
| `WithFilterOptions`           | Y                                                   | Y                                                                 | Y                                                                    | Y                                                | Y                                                | Y     | Y   | Y                            |
| `WithFilterLiteralEncoder`    | Y                                                   | Y                                                                 | Y                                                                    | Y                                                | Y                                                | Y     | Y   | `WithValueEncoding`          |
| `WithFilterPredicateRenderer` | Y                                                   | Y                                                                 | Y                                                                    | Y                                                | Y                                                | Y     | Y   | –                            |
| `WithFilterCollector`         | Y                                                   | Y                                                                 | Y                                                                    | Y                                                | Y                                                | Y     | Y   | Y (`WithCollector`)          |
| `Compatibility.ValidateAST`   | Y                                                   | Y                                                                 | Y                                                                    | Y                                                | Y                                                | Y     | Y   | Y (`ValidateAST = Validate`) |
| `TranslateInline`             | Y                                                   | Y                                                                 | Y                                                                    | –                                                | –                                                | –     | –   | –                            |
| Schema constructor            | `NewFilterTranslatorWithSchema` (`postgres.Schema`) | `NewFilterTranslatorWithSchema` (`mariadb.Schema`, contract mode) | `NewFilterTranslatorWithSchema` (`clickhouse.Schema`, contract mode) | `NewFilterTranslatorWithSchema` (`mongo.Schema`) | `NewFilterTranslatorWithSchema` (`redis.Schema`) | –     | –   | `WithProfile`                |

Backend-specific options: the SQL schema settings are `SchemaOption`s of `postgres.NewSchema`, `mariadb.NewSchema` and `clickhouse.NewSchema` with one
name per setting (`WithSchemaContractMode`, `WithSchemaMaxDataBudget`, `WithSchemaTimestampPrecision`, plus `WithSchemaTextCollation`,
`WithSchemaTextCompare`, `WithSchemaJSONBStringCompare`, `WithSchemaArrayAliasPrefix`, `WithSchemaMaxValueDepth`, `WithSchemaMaxArrayDimensions` and
`WithSchemaDecimalFormat` where the backend has them), and the schema translators accept only `WithFilterOptions` and `WithFilterCollector`;
`mariadb.WithFilterDecimalFormat` and `clickhouse.WithFilterTimestampPrecision` configure the scalar translators; `redis.NewSchema` takes
`redis.WithSchemaStrict`, `redis.WithSchemaIndexMissing` and `redis.WithSchemaIndexEmpty`; `lua.WithFilterTableVar` (the Lua variable holding the
record, `lua.DefaultTableVar` by default); `meili.WithFilterServerVersion(meili.Version)` and `meili.WithFilterContainsFilter(bool)` declare server
capabilities (the only translator with version gating).

### Backend capabilities

Y = translated; – = `ErrUnsupportedOperation`. Each backend README (linked from the [filter README](../filter/README.md#backends)) details its
semantics.

| Expression                                            | postgres / mariadb / clickhouse                              | mongo            | redis                                               | meili                                                        | lua                  | evaluator |
|-------------------------------------------------------|--------------------------------------------------------------|------------------|-----------------------------------------------------|--------------------------------------------------------------|----------------------|-----------|
| `==`, `!=`, ordered comparison of numbers             | Y                                                            | Y                | Y                                                   | Y                                                            | Y                    | Y         |
| Ordered comparison of strings                         | Y                                                            | Y                | – (TAG/TEXT)                                        | Y                                                            | Y                    | Y         |
| Bare boolean, `!`, `&&`, `\|\|`                       | Y                                                            | Y                | Y                                                   | Y                                                            | Y (`== true`)        | Y         |
| `in [...]`, `in []`                                   | Y                                                            | Y                | Y                                                   | Y                                                            | Y                    | Y         |
| `null` element in an `in` list                        | Y (`(x IN (…) OR x IS NULL)`)                                | Y                | `redis.WithSchemaIndexMissing` (`… \| ismissing`)   | Y (`IN [...] OR (IS NULL OR NOT EXISTS)`)                    | Y                    | Y         |
| `== null`, `!= null`, `has()`                         | Y                                                            | Y                | `redis.WithSchemaIndexMissing` (`ismissing`, 2.10+) | Y                                                            | Y                    | Y         |
| `contains()`                                          | Y                                                            | Y                | Y (2.6+)                                            | `meili.WithFilterContainsFilter(true)` (1.10+, experimental) | Y                    | Y         |
| `startsWith()`                                        | Y                                                            | Y                | Y                                                   | Y (1.17+, or 1.11+ with `containsFilter`)                    | Y                    | Y         |
| `endsWith()`                                          | Y                                                            | Y                | Y (2.6+)                                            | –                                                            | Y                    | Y         |
| `matches()`                                           | Y                                                            | Y                | –                                                   | –                                                            | –                    | Y         |
| `size()` of a string                                  | Y                                                            | Y                | –                                                   | –                                                            | Y                    | Y         |
| `size()` of an array, counting                        | Y (`cardinality` / `JSON_LENGTH` / `length`)                 | Y                | –                                                   | –                                                            | Y                    | Y         |
| `size()` of an array, emptiness (`== 0`, `> 0`)       | Y                                                            | Y                | –                                                   | Y (`IS EMPTY`; `EXISTS AND IS NOT NULL AND IS NOT EMPTY`)    | Y                    | Y         |
| `"x" in tags`, `tags == "x"` (array field)            | Y (`= ANY` / `JSON_CONTAINS` / `has`)                        | Y                | Y (TAG)                                             | Y (`filter.WithArrayFields`)                                 | Y (element-wise)     | Y         |
| `timestamp()` literal                                 | Y (bound `time.Time`)                                        | Y (BSON date)    | Y (epoch seconds)                                   | Y (epoch seconds)                                            | Y (epoch seconds)    | Y         |
| `bytes` literal                                       | Y                                                            | Y (binary)       | Y (UTF-8 text)                                      | Y (string)                                                   | Y (string)           | Y         |
| Field operand (`a == b`, `a in [b]`, `a.contains(b)`) | Y                                                            | Y (`$expr`)      | –                                                   | –                                                            | Y                    | Y         |
| `substring()` (comparison operand)                    | Y (`substr` / `SUBSTRING` / `substringUTF8`, length-guarded) | Y (`$substrCP`)  | –                                                   | –                                                            | Y                    | Y         |
| Decimal field without a literal encoder               | Y (`CAST … NUMERIC` / `DECIMAL(p,s)` / `toDecimal128`)       | Y (`Decimal128`) | `ErrUnsupportedType`                                | `ErrUnsupportedType`                                         | `ErrUnsupportedType` | Y         |

The evaluator is the reference semantics: it supports every operator/operand shape of every translator plus `substring()`.

Backend limits, kept as explicit errors rather than counted as gaps:

- Redis: `matches`, `size()`, `substring()`, ordered comparisons on TAG/TEXT, field operands; `has()`/null only
  on fields declared `INDEXMISSING` (`redis.WithSchemaIndexMissing`); `== ""` only on fields declared `INDEXEMPTY`
  (`redis.WithSchemaIndexEmpty`, RediSearch 2.10+, `@f:{""}` / `@f:""`); a string function with `""` is the presence test
  (`redis.WithSchemaIndexMissing`).
- Mongo: ordered comparisons with `null` and ordered comparisons of bytes fail with `ErrUnsupportedOperation` (BinData orders by length
  first). The same holds for every backend for ordered comparisons with `null`.
- Meili: `endsWith`/`matches`/counting `size()`/field operands/`substring`; `contains()` needs the experimental `containsFilter` feature
  (`meili.WithFilterContainsFilter`); operators newer than `meili.WithFilterServerVersion` → `ErrUnsupportedOperation`.
- Lua: `matches` (Lua patterns are not RE2). Array fields (`filter.WithArrayFields`) are tested element-wise like Mongo and the evaluator;
  timestamps render as Unix epoch seconds (as Meili), override with `lua.WithFilterLiteralEncoder`; a predicate the evaluator finds unknown or
  fails for a record is a non-match, also under negation. Dotted paths do not walk lists of documents.
- Meili: `NOT (…)` matches a document whose attribute is null or missing, so `!` is pushed down to the predicates (De Morgan) and a negated
  ordering, bare boolean or emptiness test renders its opposite (`!(a < v)` → `a >= v`), which keeps the evaluator's unknown unmatched.
  Mongo: `$nor` matches a missing field under negation, and the MongoDB profile of the evaluator follows it.

## Security limits

| Option                           | Default      | What it bounds                                                                                                         |
|----------------------------------|--------------|------------------------------------------------------------------------------------------------------------------------|
| `WithMaxExpressionLength`        | 4096 bytes   | Reject before parsing — protects parser memory and the LRU cache                                                       |
| `parser.WithCacheSize`           | 1000 entries | LRU cache for parsed AST                                                                                               |
| `parser.WithCacheBytes`          | 8 MiB        | Approximate bytes of cached ASTs; an AST above a sixteenth of it is not cached                                         |
| `WithMaxDepth`                   | 20 levels    | AST nesting during translation/evaluation                                                                              |
| `WithMaxOperations`              | 1000 visits  | Node visits; translation checks the whole AST before rendering (`Policy.CheckLimits`)                                  |
| `WithMaxRegexLength`             | 1024 bytes   | Pattern length passed to `matches()`                                                                                   |
| `WithAllowedFields`              | (required)   | Whitelist of queryable field names; `filter.NewPolicy` without it fails with `ErrAllowlistRequired`                    |
| `WithAnyField`                   | off          | Opt out of the required allowlist for a filter the service builds itself; an allowlist given still applies             |
| `WithAllowedFunctions`           | (none)       | Whitelist of callable function names (built-in and custom). Operators and `has()` always allowed                       |
| `WithFieldTypes`                 | (none)       | Declared kind per field; a literal of another type is rejected with `ErrFieldTypeMismatch`                             |
| `WithEnumValues`                 | (none)       | Allowed integer set per enum field; a value outside it is rejected with `ErrEnumValueNotAllowed`                       |
| `mongo.WithSchemaZeroWhenAbsent` | (none)       | Fields (with their kinds) whose absent MongoDB values read as zero; set on the `backend/mongo` schema                  |
| `WithUntrustedInput`             | off          | Marks input as user-supplied; refuses to construct without an allowlist, even with `WithAnyField`; rejects `matches()` |
| `WithSafeRegex`                  | off          | Under `WithUntrustedInput`, admits `matches()` with patterns within the safe subset (no ReDoS shapes)                  |
| `WithUnsafeRegex`                | off          | Lifts the safe regex subset; conflicts with `WithUntrustedInput` (`ErrInvalidOptions`)                                 |

Regex length, AST depth, and operation limits belong to the shared policy (`filter.PolicyOption`): they bound a parsed AST, including one a
service builds by hand; cache size and expression length belong to `parser.Option`. Choose limits for the endpoint and its expected query complexity.

## Field mapping

Map CEL field names to storage column names without rewriting expressions. The mapping is a translator option: the policy keeps naming
the API fields, and each backend maps them its own way:

```go
policy, err := filter.NewPolicy(filter.WithAllowedFields("userName", "createdAt"))
if err != nil { return err }

trans, err := mongo.NewFilterTranslator(policy, mongo.WithFilterOptions(filter.WithFieldMapping(map[string]string{
    "userName":  "user_name",
    "createdAt": "created_at",
})))
if err != nil { return err }
```

The mapping applies after custom-function expansion, so the chain `createdAfter` (custom) → `createdAt` (parser AST) → `created_at` (translator) works
automatically.

## Errors

| Error                      | When                                                                                                                      |
|----------------------------|---------------------------------------------------------------------------------------------------------------------------|
| `ErrParseFailed`           | CEL grammar / syntax errors at parse time                                                                                 |
| `ErrEmptyExpression`       | Empty or whitespace-only input                                                                                            |
| `ErrExpressionTooLong`     | Input above `WithMaxExpressionLength`                                                                                     |
| `ErrInvalidExpression`     | Structurally invalid AST (wrong arity, nil node from custom handler, a field that is not a plain dotted path, etc.)       |
| `ErrUnsupportedOperation`  | Operator/function not supported by the target translator (e.g., regex `matches` on Lua)                                   |
| `ErrUnsupportedType`       | Literal type not representable in the target backend                                                                      |
| `ErrFieldNotAllowed`       | Field outside `WithAllowedFields` or denied by `WithFieldAccess`                                                          |
| `ErrFunctionNotAllowed`    | Function outside `WithAllowedFunctions` (built-in or custom); `matches()` under untrusted input without `WithSafeRegex`   |
| `ErrFieldTypeMismatch`     | Literal type does not match the kind declared via `WithFieldTypes`                                                        |
| `ErrEnumValueNotAllowed`   | Integer literal outside the set declared via `WithEnumValues`                                                             |
| `ErrAllowlistRequired`     | `NewPolicy` without a nonempty allowlist or restricting `WithFieldAccess`, unless trusted input opts out (`WithAnyField`) |
| `ErrMaxDepthExceeded`      | AST nesting above `WithMaxDepth`                                                                                          |
| `ErrMaxOperationsExceeded` | Visits above `WithMaxOperations`                                                                                          |
| `ErrInvalidRegex`          | Pattern fails RE2 compilation, exceeds `WithMaxRegexLength` or falls outside the safe subset                              |
| `ErrDataLimitExceeded`     | Evaluation reads more data than `evaluator.WithMaxDataBudget` allows                                                      |
| `ErrPolicyRequired`        | A translator or evaluator built with a nil policy                                                                         |
| `ErrInvalidOptions`        | Contradictory or malformed options (a pattern in `WithAllowedFields`, `WithUnsafeRegex` with untrusted input)             |

All sentinels match via `errors.Is`. Don't string-match error messages.

## Metrics

Subsystem `filter`:

| Metric                          | Type      | Description                   |
|---------------------------------|-----------|-------------------------------|
| `filter_parse_duration_seconds` | histogram | Parse latency (cache hit ≈ 0) |
| `filter_parse_errors_total`     | counter   | Parse failures (any cause)    |

Pass a [`metrics.Meter`](../metrics/README.md) (`github.com/altessa-s/go-aip/metrics`) through `parser.WithCollector(c)`; a go-atlas
`observability/metrics` collector is adapted to it. Without one the parser records nothing. Backend filter translators also accept their own
`WithFilterCollector(c)` and record `filter_<backend>_translate_duration_seconds` histograms (`mongo`, `clickhouse`, `mariadb`, `postgres`, `meili`,
`redis`, or `lua`). The evaluator's `evaluator.WithCollector(c)` records `evaluate_duration_seconds` and `prepare_duration_seconds` under the subsystem
`filter_evaluator`. There is no built-in translation error counter; count failures at the call site when needed. `eval.RegexCacheStatsSnapshot()`
exposes the individual evaluator's regex-cache hits, misses, and size. `eval.ResetRegexCacheStats()` resets counters while retaining cached patterns;
these statistics are not automatically exported as metrics.

## Failure modes

**`ErrUnsupportedOperation` from a translator at runtime.** Not every operator/function is implementable in every backend (Lua doesn't do regex;
RediSearch has no `matches`, `size()` or `substring()`, and `has()` only on `INDEXMISSING` fields; Meilisearch has no `endsWith`, `matches`, counting
`size()` or `substring()`; see [Backend capabilities](#backend-capabilities)). There is no lenient mode to fall back on — an operation the backend
cannot express is an error, not a silently dropped clause. Narrow the queryable surface with `WithAllowedFunctions`, and translate the expression once
at request-validation time so the failure reaches the API layer before you have started building a response.

**Cache-poisoning by deep input.** `WithMaxExpressionLength` is the first line of defense — long expressions never enter the LRU cache. Keep
the limit conservative for untrusted callers (under 1 KiB is plenty for typical filters). `parser.WithCacheBytes` bounds the cached ASTs by
size, so distinct maximal filters pin at most about 8 MiB per parser by default; parse failures go to a small separate negative cache.

**"My custom function isn't firing."** Check three things in order: (1) `parser.New` returned no error (a name collision or nil handler fails
construction), (2) the function is invoked as a call (`createdAfter("...")`), not a comparison (`createdAfter == "..."`), (3) handlers run on
parser cache misses only, and a failure they return is cached too — disable the cache with `parser.WithNoCache` while debugging or
invalidate by changing the expression text.

**`ErrAllowlistRequired` in production but not staging.** The policy was built with `WithUntrustedInput(true)` somewhere (rightly), but no
`WithAllowedFields` was wired through that environment's config. `filter.NewPolicy` returns the error, so it surfaces at process start
rather than at the first query — explicit allowlists per-environment beat shared defaults.

**Translator + custom function disagreement.** `WithFieldMapping` and `WithAllowedFields` operate on the **target** field name produced by
the custom function, not on the virtual call name. If you whitelist `createdAfter` you'll get `ErrFieldNotAllowed` for `createdAt`.

**`ErrFunctionNotAllowed` for a function you registered.** `WithAllowedFunctions` and `WithCustomFunctions` are independent — registering a
handler does not auto-allow the name. When a whitelist is configured, every callable name (built-ins like `contains` and your custom
functions) must appear in it. This is intentional: it lets one explicit function map serve multiple endpoints, each narrowing the visible
function set on its own.

## Related docs

- [`metrics`](../metrics/README.md) — the `Meter` the parser and translators record into.
- [`filtertest`](../filter/filtertest/README.md) — the conformance suite every filter translator and the evaluator run;
  [integration/crossbackend](../integration/crossbackend/README.md) checks one policy through several backends at once, and
  [integration/docker](../integration/docker/README.md) runs the SQL and MongoDB translators against live servers.
- go-atlas documents its YAML configuration and its repository-wide metrics reference.

## Package boundaries and API migration

The root `filter` package owns AST nodes and immutable policy construction. Its policy carries shared field and expression rules; MongoDB-specific
absence and omission rules live in `backend/mongo.Schema`. `parser.New` owns CEL and its cache; `evaluator.New(policy, opts...)` owns evaluation over
the shared policy; `evaluator.WithFilterOptions` maps its names to data keys. Pure expansions live in `functions`. The BSON storage encoding of the
evaluator (`evaluator.ValueEncoding`) is `mongo.ValueEncoding` in `backend/mongo`, separate from query translation. MongoDB size constraints live in
`backend/mongo` (`mongo.CheckSizeOperand`).

`Evaluate`, `Validate` and `Prepare` check all field references, including short-circuited branches. Pass one `mongo.Schema` to
`mongo.NewFilterTranslatorWithSchema` and `evaluator.WithProfile` so translation and evaluation use the same missing-value rules. The [migration
table](../filter/README.md#migration) lists the source-breaking API moves.

### Typed PostgreSQL parity

`backend/postgres` owns the shared immutable schema and compiled program used by `postgres.NewFilterTranslatorWithSchema` and `evaluator.WithProfile`.
The program compiles the AST for both SQL rendering and in-memory matching, and both give NULL the default evaluator's semantics rather than SQL
three-valued logic: a NULL column is a null value (`x != v` and `!(x == v)` hold for it, two columns compare with `IS [NOT] DISTINCT FROM`), `!` is
pushed down to the predicates, and an ordered comparison with NULL is unknown, also negated. It supports PostgreSQL NUMERIC (without Decimal128 limits),
arrays with explicit dimensions/lower bounds, and JSONB extraction, equality and containment. Use the SQL value decoder in `backend/sqlbase.Decoder` for
nullable wrappers and driver valuers.

The [schema contract](../backend/postgres/README.md#typed-schema-and-evaluator-profile) documents input representation and collation.
Whole-JSONB ordering requires an explicit comparator matching the database; it is never silently evaluated using the wrong locale.
The live suites of [integration/docker](../integration/docker/README.md) (`TestSchemaTranslatorLive`, `TestContractTranslatorLive`,
`TestInlineClientEncodingsLive` in `integration/docker/postgres`) compare the rows PostgreSQL selects with the in-memory evaluation of the
same schema.

## Parse and translate

```go
package example

import (
    "context"

    "github.com/altessa-s/go-aip/backend/postgres"
    "github.com/altessa-s/go-aip/filter"
    "github.com/altessa-s/go-aip/filter/parser"
)

func Where(ctx context.Context, expression string) (string, []any, error) {
    p, err := parser.New(parser.WithAllowedFunctions("contains", "startsWith", "timestamp"))
    if err != nil { return "", nil, err }
    node, err := p.Parse(ctx, expression)
    if err != nil { return "", nil, err }
    policy, err := filter.NewPolicy(
        filter.WithAllowedFields("name", "createdAt"),
        filter.WithFieldTypes(map[string]filter.FieldKind{
            "name": filter.FieldKindString,
            "createdAt": filter.FieldKindTimestamp,
        }),
        filter.WithUntrustedInput(true),
    )
    if err != nil { return "", nil, err }
    translator, err := postgres.NewFilterTranslator(policy, postgres.WithFilterOptions(
        filter.WithFieldMapping(map[string]string{"createdAt": "created_at"}),
    ))
    if err != nil { return "", nil, err }
    return translator.Translate(node)
}
```

Construct and reuse parsers/translators during setup. SQL Translate returns a clause and separate arguments; do not interpolate client literals. Build
the policy once and pass it to every backend constructor; each takes its own FilterOption values, and WithFilterOptions binds the policy to its storage
names. The evaluator takes the same policy (evaluator.New(policy, ...)).

## Parser and literal ownership

There is no global function registry or automatic preset installation. Pass handlers through parser.WithCustomFunctions;
[functions](../filter/functions/) supplies AST expansions such as CompareField, TextSearch, BetweenFilter, and SoftDeleteFilters.
WithAllowBuiltinOverride explicitly permits replacing named builtins. WithEnvironmentOptions and WithTimestampParser configure the parser instance.

WithAllowedFunctions covers named builtins and custom handlers; operators and has remain grammar. Parse returns owned ASTs; ParseShared explicitly
returns read-only cached nodes. The default cache holds 1,000 expressions and about 8 MiB of ASTs (WithCacheBytes) and the input limit is 4,096 bytes;
WithNoCache disables caching.

WithEnumNames maps public enum names to immutable stored values. Integers fitting int64 normalize to int64; mutable references are rejected.
WithOrderedEnums explicitly admits ordering of stored enum values. Backend renderers remain responsible for representing custom enum values.

## Extension and policy contracts

| Seam                                                            | Responsibility                                                                                              |
|-----------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------|
| `ExtensionNode.Lower`                                           | Lower application syntax to an equivalent built-in AST without cycles before snapshots and canonicalization |
| `WithValueCodecs`                                               | Validate literals for application field kinds                                                               |
| `WithDecimalParser`                                             | Convert decimal operands consistently in comparisons and membership                                         |
| `WithFieldAccess`                                               | Add shared field authorization; existing allowlists stay restrictive                                        |
| `WithContractRules`                                             | Override individual portable validation rules                                                               |
| `WithRegexCompiler`, `WithRegexValidator`, `WithRegexCacheSize` | Inject instance regex behavior and cache ownership                                                          |
| Translator literal/predicate hooks                              | Convert application values or render configured predicates after policy validation                          |

A nonempty allowlist (or a restricting `WithFieldAccess`) is required unless trusted callers explicitly select WithAnyField(true).
WithUntrustedInput(true) always requires one. Default AST limits are depth 20 and 1,000 operations; regex length is 1,024 bytes. WithPortableContract
selects the narrower common listing contract; individual ContractRule values can enable or disable its checks.

Regex cache capacity defaults to 256 and zero disables caching. An injected compiler owns its cache and must support concurrent calls. Safe patterns are
validated before database execution. WithSafeRegex(true) permits that subset for untrusted input; WithUnsafeRegex(true) disables safe-subset enforcement
only for trusted input and conflicts with untrusted mode. Structural limits still apply.

## Semantic profiles and SQL metadata

[evaluator](../filter/evaluator/#composable-execution) defines the backend-neutral profile contracts (Profile, Compiler, Binder, Program, FieldAccessor,
ValueDecoder) and the building blocks a profile's program reads data with (Reader, Guard, CompareResult, CompareExact, LiteralPatterns), without a
driver; each backend implements its profile in its own package. [sqlbase](../backend/sqlbase/#sql-field-schema) holds neutral column, kind, array,
nullable-presence, and JSON path metadata. Column and kind validation are replaceable; it does not choose a dialect.

[backend/postgres](../backend/postgres/README.md#typed-schema-and-evaluator-profile) wraps neutral metadata with typed PostgreSQL SQL rendering
and evaluation. It supports exact NUMERIC, nullable arrays and dimensions, bytea, timestamps, JSONB, and the default evaluator's NULL semantics.
Use the same schema with postgres.NewFilterTranslatorWithSchema and evaluator.WithProfile; sqlbase.Decoder adapts database/sql values. JSONB
ordering needs an explicit comparator matching database collation.

[backend/mongo](../backend/mongo/README.md#evaluator-profile) owns omission, zero-when-absent, presence, and size behavior and the MongoDB matching
rules (`mongo.Schema`, an evaluator Binder). Supply it to the MongoDB translator and evaluator; `mongo.ValueEncoding()` is the BSON ValueEncoding.
MariaDB's typed translator consumes the neutral sqlbase schema directly; it does not import PostgreSQL semantics.

`evaluator.WithComparator` can replace scalar comparison, including membership. A custom Profile must implement Compiler or Binder; FieldAccessor
optionally supplies lookup. A Compiler profile uses ValueDecoder, while a Binder profile and the default evaluator read stored forms through
ValueEncoding. Optional extraction and sizing interfaces keep adapters small. Data work defaults to 2^22 units per evaluation, separate from AST limits.

## Backends and compatibility

| Backend     | Output                             | Extension boundary                                                                            |
|-------------|------------------------------------|-----------------------------------------------------------------------------------------------|
| MongoDB     | BSON filter                        | Storage schema, literal encoder, predicate renderer                                           |
| PostgreSQL  | SQL and $n arguments or inline SQL | Scalar sqlbase dialect or typed PostgreSQL schema, predicate renderer                         |
| MariaDB     | SQL and ? arguments or inline SQL  | Neutral typed schema, collation/decimal/timestamp policies                                    |
| ClickHouse  | SQL and arguments or inline SQL    | sqlbase dialect or neutral typed schema (JSON paths), timestamp precision, predicate renderer |
| Meilisearch | Filter expression                  | Literal encoder, predicate renderer, server version/features                                  |
| RediSearch  | Query string                       | map[string]FieldType, strict schema, INDEXMISSING fields, literal/predicate hooks             |
| Lua         | Boolean expression                 | Record variable, literal encoder and predicate renderer                                       |
| In-memory   | Boolean (`evaluator.Evaluator`)    | Profile, value encoding/decoder, comparator                                                   |

[filter.Compatibility](../filter/README.md#translator-ports) checks ValidateAST against actual operand shapes, values, and configuration without
contacting a server. Unsupported combinations return explicit filter errors. Public [sqlbase](../backend/sqlbase/#filter-predicate-builder) exposes the
shared traversal for application SQL backends: `sqlbase.NewFilterTranslator(policy, dialect, ...FilterOption)` with `WithFilterLiteralEncoder` /
`WithFilterPredicateRenderer`; optional `ArrayDialect`, `DecimalDialect`, `SubstringDialect`. Client values must remain safely encoded or bound.

Wrapped failures preserve causes for errors.Is/errors.As. Canonical encoding preserves literal type and boundaries for cursor binding. For a complete
List RPC contract and cross-engine conformance, use [listing](listing.md); every filter translator and the evaluator run the shared conformance suite
of [filtertest](../filter/filtertest/README.md) (golden renderings or sentinels per case, Lua executed and compared with the evaluator); signing and
codecs are covered in [pagination](pagination.md).
