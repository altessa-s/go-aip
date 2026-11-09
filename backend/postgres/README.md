# postgres

```go
import "github.com/altessa-s/go-aip/backend/postgres"
```

Everything go-aip renders and evaluates for PostgreSQL, in one package:

| Kind             | Constructor                                                                                                          | Provides                                                                      |
|------------------|----------------------------------------------------------------------------------------------------------------------|-------------------------------------------------------------------------------|
| Filter           | `NewFilterTranslator(policy, opts ...FilterOption) (*FilterTranslator, error)`                                       | A `WHERE` clause from a `filter.Node` (AIP-160)                               |
| Schema filter    | `NewFilterTranslatorWithSchema(policy, schema, opts ...FilterOption) (*SchemaFilterTranslator, error)`               | A typed `WHERE` clause (NUMERIC, arrays, JSONB) matching in-memory evaluation |
| Schema / profile | `NewSchema(fields, opts ...SchemaOption) (*Schema, error)`                                                           | Typed schema, compiled `Program` and the `filter/evaluator` profile           |
| Order            | `NewOrderTranslator(policy, opts ...OrderOption) (*sqlbase.OrderTranslator, error)`                                  | The body of `ORDER BY` from an `orderby.Spec` (AIP-132)                       |
| Projection       | `NewProjectionTranslator(policy, opts ...ProjectionOption) (*sqlbase.ProjectionTranslator, error)`                   | The `SELECT` column list from a `projection.Spec` (AIP-157)                   |
| List engine      | `NewListEngine[T](q, codec, schema, table, columns, dest, opts ...ListEngineOption) (*sqlbase.ListEngine[T], error)` | Keyset pages over a PostgreSQL table (AIP-132, AIP-158)                       |
| List dialect     | `ListDialect{}`                                                                                                      | The PostgreSQL `sqlbase.ListDialect` used by `NewListEngine`                  |

Every translator constructor takes the resource's shared, client-facing policy (built once in API names and reused by every backend) followed by the
storage options that bind it to PostgreSQL column names. `OrderOption`, `ProjectionOption` and `ListEngineOption` are aliases of the sqlbase option
types; their `With…` functions stay in sqlbase (`sqlbase.WithOrderOptions`, `sqlbase.WithProjectionOptions`, `sqlbase.WithList…`). The order, projection
and list-engine constructors are the dialect-generic [`sqlbase`](../sqlbase/README.md) ones with the PostgreSQL dialect fixed; use
`sqlbase.NewOrderTranslator` / `sqlbase.NewProjectionTranslator` with an application `sqlbase.ColumnResolver` for trusted storage expressions, and
`sqlbase.NewListEngine` with a dialect that embeds `ListDialect` to override selected listing behavior.

All translators, schemas, compiled programs and list engines are immutable and safe for concurrent use after construction; injected callbacks must be
too. The package is independent of database drivers; the live conformance suite targets PostgreSQL 16 and runs in the test-only module
[`integration/docker`](../../integration/docker/README.md) (`go -C integration/docker test -race -tags integration_docker ./postgres/`, needs
Docker). The filter conformance expectations and the projection decoder shared by the unit and live tests live in
[`postgrestest`](postgrestest/README.md).

## Filter translator

`FilterTranslator` translates a `filter.Node` AST into a PostgreSQL `WHERE` clause. Two output modes share one AST walk: a parameterized clause with
numbered `$n` placeholders plus a bound argument slice, and a self-contained clause with literals rendered in place.

### Contract

`NewFilterTranslator` takes the shared `*filter.Policy` (field authorization, types, limits, decimal conversion, value codecs and regex rules, in API
names, built once per resource) and this package's `FilterOption` values; `WithFilterOptions` binds the policy to storage names
(`filter.WithFieldMapping`). A nil policy fails with `filter.ErrPolicyRequired`; `filter.NewPolicy` requires an allowlist or an explicit trusted
`WithAnyField` opt-out.

`ValidateAST` uses the configured renderer to check operand shapes and values without contacting a server. Unsupported operations remain explicit
errors. Translators support concurrent calls with per-call traversal state; injected callbacks must also support concurrent use.

`WithFilterLiteralEncoder` converts values before backend encoding; it does not return executable query fragments. Without an encoder, decimal operands
are normalized through the shared policy, including membership, and rendered exactly as `CAST($n AS NUMERIC)` (see Decimals).
`WithFilterPredicateRenderer` installs a trusted application predicate renderer that binds client values through its `ValueFunc`; field authorization
and limits run first.

`Translate` returns SQL and ordered arguments; `TranslateInline` renders literals in place. Bind parameters for runtime queries. Both constructors give
NULL the default evaluator's semantics rather than SQL three-valued logic (see Null semantics); `NewFilterTranslatorWithSchema` renders the same
compiled program that `evaluator.WithProfile(schema)` evaluates, so SQL and in-memory matching agree on NULL rows. The schema translator takes its
settings, the listing contract included (`WithSchemaContractMode`), from the `Schema` and only the shared `WithFilterOptions` and `WithFilterCollector`
from its options; `WithFilterLiteralEncoder` and `WithFilterPredicateRenderer` fail it with `filter.ErrInvalidOptions`: the schema owns value encoding
and rendering.

### Usage

```go
parser, _ := filterparser.New()
ast, _ := parser.Parse(ctx, `status == 2 && type in [1, 2]`)

policy, err := filter.NewPolicy(
    filter.WithUntrustedInput(true),
    filter.WithAllowedFields("status", "type"),
) // build once per resource
if err != nil { return err }

trans, err := postgres.NewFilterTranslator(policy)
if err != nil {
    return err
}

where, args, err := trans.Translate(ast)
// where: ("status" = $1) AND ("type" IN ($2, $3))
// args:  []any{int64(2), int64(1), int64(2)}

rows, err := conn.Query(ctx, "SELECT * FROM events WHERE "+where, args...)
```

**Placeholders are numbered from `$1`,** counting up in the order the arguments are collected. A clause cannot be spliced into a query that already
binds parameters without renumbering — build the whole `WHERE` from one `Translate` call, or append the returned args after your own and shift
accordingly.

### Methods

| Method                                   | Description                                                                  |
|------------------------------------------|------------------------------------------------------------------------------|
| `Translate(node) (string, []any, error)` | Parameterized clause; every literal becomes a `$n` and is returned in `args` |
| `TranslateInline(node) (string, error)`  | Self-contained clause with literals rendered in place                        |
| `ValidateAST(node) error`                | Check the configured renderer without contacting the database                |

A `nil` node translates to `1 = 1` (match all) in both modes, so `"... WHERE " + where` stays valid without a special case. `SchemaFilterTranslator`
has the same methods.

### Operation mapping

| CEL                        | PostgreSQL                                                                         |
|----------------------------|------------------------------------------------------------------------------------|
| `==` `<` `>` `<=` `>=`     | `=` `<` `>` `<=` `>=`                                                              |
| `field != v`               | `("field" != $1 OR "field" IS NULL)`                                               |
| `field == other`           | `"field" IS NOT DISTINCT FROM "other"`                                             |
| `field != other`           | `"field" IS DISTINCT FROM "other"`                                                 |
| `&&` `\|\|`                | `AND` `OR`                                                                         |
| `!`                        | pushed down to each predicate (see Null semantics)                                 |
| `!(field == v)`            | `("field" != $1 OR "field" IS NULL)`                                               |
| `!(field in [a, b])`       | `("field" NOT IN ($1, $2) OR "field" IS NULL)`                                     |
| `!(field > v)`             | `NOT ("field" > $1)`                                                               |
| `field`                    | `"field" = TRUE`                                                                   |
| `field in [a, b]`          | `"field" IN ($1, $2)`                                                              |
| `field in []`              | `1 = 0`                                                                            |
| `field in [a, null]`       | `("field" IN ($1) OR "field" IS NULL)`                                             |
| `field == null`            | `"field" IS NULL`                                                                  |
| `field != null`            | `"field" IS NOT NULL`                                                              |
| `has(field)`               | `"field" IS NOT NULL`                                                              |
| `field.contains(s)`        | `strpos("field", $1) > 0`                                                          |
| `field.startsWith(s)`      | `starts_with("field", $1)`                                                         |
| `field.endsWith(s)`        | `right("field", length($1)) = $2`                                                  |
| `field.matches(re)`        | `"field" ~ $1`                                                                     |
| `field.size()` / `size(f)` | `length("field")`                                                                  |
| `arr == x` (array field)   | `$1 = ANY("arr")`                                                                  |
| `arr != x`                 | `("arr" IS NULL OR NOT ($1 = ANY("arr")))`                                         |
| `arr < x` (any element)    | `$1 > ANY("arr")`                                                                  |
| `x in arr`                 | `$1 = ANY("arr")`                                                                  |
| `arr in [a, b]`            | `(($1 = ANY("arr")) OR ($2 = ANY("arr")))`                                         |
| `arr.contains(s)`          | `EXISTS (SELECT 1 FROM unnest("arr") AS _aip_e(v) WHERE strpos(_aip_e.v, $1) > 0)` |
| `arr.size()`               | `COALESCE(cardinality("arr"), 0)`                                                  |
| decimal `field > 1.5`      | `"field" > CAST($1 AS NUMERIC)`                                                    |
| `f.substring(1, 3) == s`   | `(length("f") >= 3 AND substr("f", 2, 2) = $1)`                                    |

`contains` and `startsWith` take the needle as a plain string — there is no LIKE pattern, so a `%` or `_` in the operand stays literal. `starts_with()`
requires PostgreSQL 11 or newer. `size()` is rejected outside a comparison: it is an integer expression, and on its own there is no predicate for
`WHERE` to test.

**`endsWith` binds its operand twice.** PostgreSQL has no `endsWith`, so the needle both sizes the suffix and is compared to it. One CEL argument
consumes two placeholders — `args` reflects that, and the numbering of everything after it shifts accordingly.

### Arrays

Fields declared by `filter.WithArrayFields` are PostgreSQL arrays and get element semantics, matching the evaluator and the MongoDB translator: `==` and
the ordered operators hold when some element satisfies them (`x op' ANY(col)` with the converse operator), `!=` when no element equals the operand,
`x in arr` is `arr == x`, and `arr in [a, b]` is an OR of equalities. `!=` also holds for a NULL (absent) array, like the evaluator:
`("arr" IS NULL OR NOT ($1 = ANY("arr")))`. String predicates test each element through `unnest()`. `size()` is `COALESCE(cardinality(col), 0)`, so a
NULL array has size 0 (multi-dimensional arrays count every element). `arr == null`, `arr != null` and `has(arr)` test the column itself. Field
operands, a field argument to an array string predicate and a bare array identifier used as a condition fail with `filter.ErrUnsupportedOperation`. A
NULL array satisfies no other comparison, and every negated element test holds for it: `!(arr > x)` is `("arr" IS NULL OR NOT ($1 < ANY("arr")))`, and
`!(arr in [a, b])` an AND of negated equalities. A NULL element that decides the outcome makes `= ANY` unknown: unmatched, but under `!` it stays
unmatched too, where the evaluator, for which a null element never equals or orders against a value, matches.

### Decimals

Fields declared `filter.FieldKindDecimal` need no encoder: operands keep their source spelling (`Policy.DecimalOperand`), are bound as strings and cast
with `CAST($n AS NUMERIC)` (inline: `CAST(E'1.5' AS NUMERIC)`), which holds any decimal exactly. With `WithFilterLiteralEncoder` the encoder receives
the `decimal.Decimal` instead.

### substring()

`field.substring(start[, end])` is supported as a comparison or `in` operand with int literal bounds and renders as `substr(col, start+1, end-start)`.
Invalid bounds (`start < 0`, `end < start`, a non-int literal) fail with `filter.ErrInvalidExpression`; non-literal bounds with
`filter.ErrUnsupportedOperation`. The evaluator treats an `end` past the string as an error, so the comparison is guarded with `length(col) >= end`
and such rows never match instead of comparing a truncated value. Any int64 bound is accepted, as in the evaluator: the bounds are ordered before
`end - start` is computed, so `substring(1, -9223372036854775808)` fails instead of wrapping, and the start and length are capped at the int4 range
`substr()` takes (a bound that large has a guard no PostgreSQL text meets), so the call always resolves.

### Column names

Field names are checked against `WithAllowedFields`, run through `WithFieldMapping`, validated as plain SQL identifiers, and emitted double-quoted per
dot-separated segment: `address.city` becomes `"address"."city"`.

A dot is a **table qualifier** here, not nesting: `address.city` is the column `city` of the table or alias `address`. So a dotted field must be one
the policy declares (`filter.Policy.DeclaresField`: a `WithAllowedFields` entry, a `WithFieldTypes` declaration or a `WithFieldMapping` key); one
admitted only by a `WithFieldAccess` wildcard (`a.*`) or `WithAnyField` fails with `filter.ErrFieldNotAllowed`, since a client could otherwise name a
column of another joined table. A column name has at most two parts (`table.column`, never `schema.table.column`) and each part at most 63 bytes
(`sqlbase.PostgresMaxIdentLen`): PostgreSQL silently truncates a longer identifier, so `aaa…(63)_shadow` would read the column `aaa…(63)`. Both fail
with `filter.ErrInvalidExpression`.

The validation is stricter than quoting requires: the column position is the one part of the generated SQL that no placeholder can cover, so anything
that is not `[A-Za-z_][A-Za-z0-9_]*` (dot-separated) is rejected with `filter.ErrInvalidExpression`. To filter on a jsonb path or a computed value, map
the CEL name onto a generated column, or use the schema translator.

Quoting also pins the case. PostgreSQL folds unquoted identifiers to lower case, so an unquoted `createdAt` would resolve to the column `createdat`;
quoted, it means exactly `createdAt`. Where the table uses snake_case, say so with `WithFieldMapping` rather than relying on folding.

### Types

PostgreSQL is stricter than the other backends in this family:

- A **bare identifier** used as a condition compiles to `"col" = TRUE` and requires an actual `boolean` column — there is no integer-to-boolean
  coercion.
- **`size()`** compiles to `length()`, which covers `text`. Fields declared by `filter.WithArrayFields` use `cardinality()` (see Arrays); for a
  `jsonb` array map the field onto a generated column or use the schema translator.

### Null semantics

A SQL row always carries every column of its table, so there is no "missing field" for `has()` to detect — it compiles to `IS NOT NULL` and is
meaningful only against a nullable column. A NULL column otherwise behaves like a null or absent field in the default evaluator, not under SQL
three-valued logic: `col != "x"` is `("col" != $1 OR "col" IS NULL)` and matches the NULL row. Two field operands compare with
`IS [NOT] DISTINCT FROM`, so NULL equals NULL and differs from every value.

`!` is not rendered as `NOT` around a clause, since `NOT` keeps a NULL comparison unknown where the evaluator's `!` holds. It is pushed down through
`&&` and `||` (De Morgan) to each predicate, which renders its own negation: `!(col == "x")` is the `!=` form above, `!(col in ["a", "b"])` is
`("col" NOT IN ($1, $2) OR "col" IS NULL)`, `!col.contains("a")` is `(NOT (…) OR "col" IS NULL)` and `!has(col)` is `IS NULL`, so each matches the
NULL row. An ordered comparison, a bare boolean, `size()` / `substring()` of a NULL column and a NULL column as a string function argument are errors
in the evaluator; their negation stays `NOT (…)` and does not match NULL either. Array fields carry the NULL test themselves (see Arrays).

A `null` element of an `in` list matches a NULL column, as in the evaluator: `x in ["a", null]` is `(x IN ($1) OR x IS NULL)`, a list of only nulls is
`x IS NULL`, and its negation is `x NOT IN ($1)` (`x IS NOT NULL` for only nulls). An ordered comparison with `null` (`age > null`) fails with
`filter.ErrUnsupportedOperation`, and two field operands of incompatible declared kinds (`name == age` with a string `name` and an int `age`) with
`filter.ErrFieldTypeMismatch`; see [sqlbase](../sqlbase/README.md#shared-semantics).

### Regular expressions

`matches()` compiles to the case-sensitive POSIX operator `~`. PostgreSQL's engine backtracks, so a crafted pattern can be made expensive server-side.
`filter.WithMaxRegexLength` bounds the blast radius but does not eliminate it — expose `matches()` to trusted callers, or tighten the cap. A
`statement_timeout` is the reliable backstop.

### Timestamps

`timestamp(...)` literals bind as `time.Time` in the parameterized form and render as a UTC `TIMESTAMP WITH TIME ZONE` literal at microsecond precision
inline.

### Inline rendering

`TranslateInline` emits escape-string literals, `E'...'`. The prefix is what makes one escaping rule correct on any server: inside a plain `'...'`
literal the backslash is an escape character only when `standard_conforming_strings` is off, so no plain-literal escaping is correct in both settings.
The quote is doubled (`''`), which every `backslash_quote` setting accepts.

The output is pure ASCII, so it reads the same in every client encoding. Every code point above U+007F is written as `\uXXXX` or `\UXXXXXXXX`, a NUL as
`\000` (which the server refuses, PostgreSQL text holding none), and a byte that is not valid UTF-8 as `\xNN`. In a multibyte client encoding (GBK,
BIG5, SJIS, GB18030, UHC) a raw byte >= 0x80 could pair with the escaping backslash after it into one character and end the literal early: the filter
`name == "丿\\' OR 1=1 -- "` would otherwise select every row after `SET client_encoding = 'GBK'`. The schema translator's `Inline` and `InlineContract`
share the same quoter (JSON path keys included) and refuse a non-ASCII column or collation name, which only a custom column validator admits, with
`filter.ErrInvalidExpression`.

### Field operands

Comparisons, `in` list elements, and `contains` / `startsWith` / `endsWith` arguments can reference other fields. Field mappings and allowlists apply to
every reference. Lists containing dynamic operands render as equality disjunctions; constant lists retain `IN`. Literal values remain bound parameters.

`matches` requires a literal pattern so regex validation and length limits apply before execution.

### Configuration and compatibility

Both filter constructors accept this package's generated `FilterOption` values; the schema constructor takes the schema followed by the same options.
Shared policies and callbacks must be safe for concurrent use. The translator exposes no `filter.Visitor` methods; call `Translate`. `ValidateAST`
checks actual expression shapes and values, including field-to-field operands, rather than promising support for an operator regardless of its
operands. Unsupported expressions return filter sentinel errors. Backend-native differences remain explicit.

Literal encoders return values, never executable query text. They can choose timestamp units or stored enum representations. Schema-aware rendering
uses the declared storage semantics instead; configure it through the schema options.

### Filter options

| Option                        | Default | Behavior                                                                                                                                                                                                            |
|-------------------------------|---------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `WithFilterOptions`           | nil     | Binds the shared policy to the storage names of this backend (`filter.WithFieldMapping`)                                                                                                                            |
| `WithFilterCollector`         | nil     | A `metrics.Meter` recording the translation duration under the backend label `postgres`; nil disables it                                                                                                            |
| `WithFilterLiteralEncoder`    | nil     | Converts values before backend quoting; must be concurrency safe. Scalar translator only                                                                                                                            |
| `WithFilterPredicateRenderer` | nil     | Trusted predicate override (`sqlbase.PredicateRenderer`) that binds client values through its `ValueFunc`; return handled=false to use built-ins. Runs after field authorization and limits. Scalar translator only |

```go
trans, err := postgres.NewFilterTranslator(policy, postgres.WithFilterOptions(filter.WithFieldMapping(map[string]string{
    "organizationIds": "organization_id",
    "createdAt":       "created_at",
})))
```

## Typed schema and evaluator profile

`Schema` is PostgreSQL's immutable typed schema. It compiles a filter AST once into a `Program` that renders parameterized or inline SQL and evaluates
rows in memory with the same NULL semantics, those of the default evaluator. The same schema is the `filter/evaluator` profile
(`evaluator.WithProfile(schema)`) and the input of `NewFilterTranslatorWithSchema`, so SQL and in-memory matching agree.

### Contract

`NewSchema(fields, opts...)` wraps a neutral `sqlbase.Schema` with PostgreSQL evaluation and rendering semantics. A NULL column is a null value as in
the default evaluator, not an SQL UNKNOWN: the rendered SQL holds for a row exactly where the program evaluates to true. SQL NULL, JSON null, and
missing JSON paths remain distinct. A nil field schema or invalid option values fail with `filter.ErrInvalidOptions`.

The compiled `Program` supports exact NUMERIC, nullable scalars, bytea, timestamps, arrays, and JSONB. `ArrayData` records dimensions and lower bounds;
nullable elements remain nullable. JSONB ordering requires an explicitly selected string comparator matching database collation. The default text
collation is C; a custom collation also requires a corresponding text comparator.

`WithSchemaContractMode(true)` selects listing storage semantics, including zero-valued Optional fields and empty arrays. Precision, array dimensions,
value depth, data work, alias prefixes, and comparators are configurable. Compiled programs snapshot the AST and shared dependencies must support
concurrent use.

### Key types

| Type                     | Purpose                                                                      |
|--------------------------|------------------------------------------------------------------------------|
| `Schema`                 | Frozen map of CEL field names to storage descriptors plus PostgreSQL options |
| `sqlbase.Field`          | Immutable scalar, SQL array or typed JSONB path descriptor                   |
| `Program`                | Validated expression with owned literals; safe for concurrent use            |
| `ArrayData`, `Dimension` | Preserve flattened array values, dimensions and non-default lower bounds     |
| `JSONNull`               | A JSON null returned by `Lookup`, distinct from SQL nil                      |

### Schema

Field constructors and kind constants below belong to `sqlbase`. PostgreSQL execution options belong to this package.

| Constructor / kind                | Meaning                                                 |
|-----------------------------------|---------------------------------------------------------|
| `Scalar(column, kind)`            | SQL column, including nullable values                   |
| `Array(column, elementKind)`      | SQL array with typed elements                           |
| `JSONPath(column, kind, path...)` | `#>>` plus scalar cast, or `#>` for `JSON`              |
| `Boolean`, `Integer`, `Float`     | boolean, bigint-compatible integer, double precision    |
| `Text`, `Bytes`, `Timestamp`      | UTF-8 text, bytea, timestamptz at microsecond precision |
| `Numeric`                         | Exact PostgreSQL numeric, including NaN and infinities  |
| `JSON`, `UUID`                    | JSONB values and canonical UUIDs                        |

```go
fields, err := sqlbase.NewSchema(map[string]sqlbase.Field{
    "amount": sqlbase.Scalar("amount", sqlbase.Numeric),
    "tags": sqlbase.Array("tags", sqlbase.Text),
    "profile.city": sqlbase.JSONPath("profile", sqlbase.Text, "address", "city"),
    "profile": sqlbase.Scalar("profile", sqlbase.JSON),
})
if err != nil { return err }
schema, err := postgres.NewSchema(fields)
```

Columns are validated identifiers, never SQL snippets; a part longer than 63 bytes fails with `filter.ErrInvalidExpression` when the program is
compiled, since PostgreSQL would truncate it. JSON paths are separate quoted components and support negative array indices. The schema supplies
PostgreSQL storage names; conflicting shared field mappings and types are rejected. An allowlist remains mandatory unless `filter.WithAnyField` is
explicitly selected.

### Translation and evaluation

```go
policy, err := filter.NewPolicy(filter.WithAllowedFields("amount", "tags")) // one policy for both
if err != nil { return err }

trans, err := postgres.NewFilterTranslatorWithSchema(policy, schema)
eval, err := evaluator.New(policy,
    evaluator.WithProfile(schema),
    evaluator.WithValueDecoder(sqlbase.Decoder{}),
)
prepared, err := eval.Prepare(node)
matched, err := prepared.Evaluate(row)
where, args, err := trans.Translate(node)
```

The [sqlbase decoder](../sqlbase/README.md#value-decoder) supports `database/sql` nullable types and driver valuers. `Compile` validates all branches;
`CompileContract` compiles for the listing contract whatever the schema's mode; `CompileEvaluator` is the `evaluator.Profile` hook. `Prepare` compiles
once; later changes to the original AST cannot affect it. `Schema.Lookup` reads a declared field from a row, keeping SQL NULL as nil and JSON null as
`JSONNull`.

| Program method                      | Output                                                           |
|-------------------------------------|------------------------------------------------------------------|
| `SQL() (string, []any, error)`      | Parameterized `WHERE` clause; text comparisons use `COLLATE "C"` |
| `Inline() (string, error)`          | The same clause with safely quoted typed literals, pure ASCII    |
| `SQLContract()`, `InlineContract()` | Rendered against the listing storage contract                    |
| `Evaluate(row, maxWork, decoder)`   | In-memory result with the default evaluator's NULL semantics     |

### Semantics

| Expression                              | Behavior                                                             |
|-----------------------------------------|----------------------------------------------------------------------|
| `x == null`, `x != null`                | SQL `IS NULL` / `IS NOT NULL`                                        |
| `x != v`, `!(x == v)`                   | Holds for a NULL column: `(x <> v OR x IS NULL)`                     |
| `x == y` (two columns)                  | Null-safe: `IS [NOT] DISTINCT FROM`                                  |
| `x > v`, `!(x > v)`                     | Unknown for a NULL operand, also negated                             |
| `!`, `&&`, `\|\|`                       | `!` pushed down to the predicates; unknown absorbed; true matches    |
| `x in [a, null]`                        | A null `x` is in a list with a null literal or NULL column element   |
| `!(x in [a, b])`                        | Holds for a NULL column                                              |
| `value in array`                        | SQL `= ANY`; a NULL element never matches                            |
| `array == value`, `array > value`       | Some non-NULL element satisfies the comparison                       |
| `array != value`, negated element test  | No element satisfies it; holds for a NULL array and NULL elements    |
| `array == [a, b]`                       | Whole-array comparison, with PostgreSQL null-element ordering        |
| `text.contains(s)`, `!text.contains(s)` | A NULL target contains nothing, so the negation holds for it         |
| `array.contains(text)`                  | Some non-NULL element satisfies the string predicate                 |
| `size(array)`                           | Cardinality, counting all dimensions; 0 for a NULL array             |
| `size(text) == null`                    | Never holds; `size()` of a NULL text is unknown                      |
| `text.substring(i, j) == s`             | `substr()` behind a `char_length()` guard; unknown when out of range |
| `json.contains('{"a":1}')`              | JSONB containment (`@>`), including nested objects and arrays        |
| `json == 'null'`                        | JSON null, distinct from SQL NULL                                    |
| `has(json.path)`                        | True for an existing JSON null; false for a missing path or SQL NULL |
| Typed JSON scalar path                  | JSON null and missing path both become SQL NULL, as with `#>>`       |
| JSONB path `size()`                     | Array length; non-array JSON values are an evaluation/database error |

### Program NULL semantics

The program evaluates every predicate to true, false or unknown, where unknown stands for a NULL operand that the default evaluator (`evaluator.New`
without a profile) finds unknown too. `&&` and `||` absorb unknown as SQL's AND and OR do, and only true matches, as in the default evaluator's
[three-valued logic](../../filter/evaluator/README.md#three-valued-logic). A NULL column, a missing JSON path and the null literal are null values:
equal to null, unequal to every other value, in no list without a null element, and containing no string. An ordered comparison with a NULL operand, a
bare boolean NULL, `size()` of a NULL text, bytes or JSON value, `substring()` of a NULL text or beyond its end, and a NULL string-function argument are
unknown. Under the listing contract only presence columns and JSON values can be NULL: a NULL that `Evaluate` reads from a NOT NULL column stands for
the column's zero value (an empty array), as the contract stores an absent value.

SQL's `NOT` keeps an unknown result unknown, while `!x.contains("a")` holds for a NULL `x` in the default evaluator, so `!` is never rendered
around a clause. It is pushed down through `&&` and `||` (De Morgan) to each predicate, which renders its own exact negation: `!(x == v)` is
`(x <> v OR x IS NULL)`, `!(x in [a, b])` is `(NOT (x = ANY(…)) OR x IS NULL)`, a negated string function adds `OR x IS NULL`, a negated array test
is `NOT COALESCE(… = ANY(arr), FALSE)`, and a negated ordered comparison stays `NOT (…)`, which never matches NULL. A predicate used as a value
(`(x == 1) == false`) renders as `CASE WHEN … THEN TRUE WHEN … THEN FALSE END`, NULL where it is unknown.

`field.substring(start[, end])` of a text column or text JSON path is a comparison or `in` operand (also an `in` list element), as in the
[scalar translator](#substring), with the same bounds (int literals, `0 <= start <= end`; otherwise `filter.ErrInvalidExpression`, non-literal
bounds `filter.ErrUnsupportedOperation`) and code-point semantics, in every mode (`SQL`, `Inline`, `SQLContract`, `InlineContract`, `Evaluate`).
It renders as `substr(col, start+1, end-start)` inside `CASE WHEN char_length(col) >= end THEN … END`, a NULL computed value exactly where the
default evaluator finds the substring unknown, so such a row matches neither the comparison nor its negation. A bound at or beyond 2^31-1 is never
in range (PostgreSQL text holds fewer than 2^30 characters) and renders `NULL::text`. `substring()` of an array field, of a non-field or as a
`size()` or string-function operand fails with `filter.ErrUnsupportedOperation`. A policy with the portable contract (the listing one) refuses
`substring()` in `filter.Validate`, before compilation.

Deliberate difference from the default evaluator:

- A NULL column compared with the elements of an array (`arr == col`, `col in arr`) is false for an empty or NULL array and unknown otherwise:
  `= ANY` cannot find NULL equal to a NULL element, as the default evaluator does.

The `schema_null_semantics_test.go` suite checks the program against the default evaluator on every combination of NULL, absent and present values.

Numeric source spelling is preserved, so `1.00000000000000001` never becomes `1`. List constructors promote numeric element types (`Integer` → `Numeric`
→ `Float`) and coerce every element before comparison, including nested lists. Contextual NUMERIC literals retain their exact source spelling. Lists
containing only untyped NULLs resolve to `text[]`. The accepted numeric bounds are PostgreSQL's 131072 integer digits and 16383 fractional digits,
further bounded by work limits. Arrays and JSON data are traversed with depth and work bounds; cyclic or oversized input returns
`filter.ErrDataLimitExceeded`.

### Input contract

Rows use SQL column names as keys. Values represent the stored row, after database column coercions (for example NUMERIC scale rounding). Use exact
decimal strings or `decimal.Decimal` for NUMERIC. For JSONB with exact numbers, use raw JSON or decode with `json.Decoder.UseNumber`; precision already
lost by decoding into float64 cannot be recovered. A nil array is SQL NULL; a non-nil empty array is empty. Native nested rectangular slices represent
dimensions with lower bound 1. Use `ArrayData` when lower bounds differ; metadata discarded by a driver cannot be reconstructed. Up to six dimensions
are supported. For SQL arrays of JSONB, nested JSON arrays remain elements; use `ArrayData` to describe multidimensional SQL structure explicitly.
Strings must be valid UTF-8 without NUL. JSONB also rejects malformed surrogate escapes. Timestamp values use UTC microseconds; textual timestamps
extracted from JSON must use RFC3339. Fractional seconds in JSON text follow PostgreSQL's rounding to nearest even microseconds, including fractions
longer than nine digits and carries into the next second.

Text expressions, including literals and arrays, use explicit `COLLATE "C"` for deterministic byte ordering in every database locale. Generated SQL
preserves the grouping of nested predicates and allocates local array aliases without shadowing referenced columns or relation names.

### Validation

The live suite of `integration/docker/postgres` (`TestSchemaTranslatorLive`, `TestContractTranslatorLive`) compares in-memory evaluation with
the rows parameterized SQL selects on a live PostgreSQL server, and `TestInlineClientEncodingsLive` runs the inline SQL. A skipped database test is
not parity evidence.

The type rules follow PostgreSQL's [numeric](https://www.postgresql.org/docs/16/datatype-numeric.html),
[JSONB](https://www.postgresql.org/docs/16/datatype-json.html) and [logical operator](https://www.postgresql.org/docs/16/functions-logical.html)
contracts.

### Semantic overrides

`WithSchemaMaxDataBudget` sets the work budget for compiling literal values; runtime evaluation receives its own explicit budget.

A nondefault collation requires an explicit matching comparator. The caller supplies a collation installed in the database and a comparator
that reproduces its ordering. Backend storage capabilities still apply when increasing limits or retaining finer timestamp precision.

Schema compilation applies policy literal validation to array elements and declared decimal fields. Regex length and compiler checks
remain active when unsafe syntax is enabled. PostgreSQL regex compatibility remains an additional backend requirement.

The `PortableRegex` policy rule controls portable syntax restrictions: under it a pattern must be in the portable subset of `filter.CheckPortableRegex`
(QuoteMeta text, `^`, `$` and `.*`, with an optional ASCII-only leading `(?i)`), the same rule the MariaDB and ClickHouse schema translators apply, or
it fails with `filter.ErrUnsupportedOperation`. With that rule disabled, applications may supply a custom regex compiler and validator matching
PostgreSQL semantics. Compiled matchers are retained by the program and reused for scalar and array evaluation.

JSONB's native ordering uses the database default collation, not the text expression's `COLLATE` clause. Whole-JSONB `<`, `<=`, `>` and `>=` are
rejected without an explicit comparator, so evaluation cannot silently use the wrong locale. For a database created with `LC_COLLATE='C'`, pass
`postgres.WithSchemaJSONBStringCompare(strings.Compare)` to `NewSchema`. For another locale, supply a concurrency-safe comparator with the same ordering
and deterministic tie-breaking as that database. Typed numeric/text JSON paths do not need a JSONB comparator.

Outside the portable subset, regexes use the common POSIX ERE subset with C collation; unsupported engine-specific constructs fail at
compilation.

### Schema options

| Option                         | Default                     | Behavior                                                                                                 |
|--------------------------------|-----------------------------|----------------------------------------------------------------------------------------------------------|
| `WithSchemaArrayAliasPrefix`   | `DefaultArrayAliasPrefix`   | Prefix of generated array element aliases, followed by a counter; a plain identifier of at most 55 bytes |
| `WithSchemaContractMode`       | false                       | Renders against non-null, C-collated listing columns (the only contract switch)                          |
| `WithSchemaMaxDataBudget`      | `DefaultMaxDataBudget`      | Work budget for compiling literal values                                                                 |
| `WithSchemaJSONBStringCompare` | nil                         | Comparator reproducing the database's default collation; required for whole-JSONB ordering               |
| `WithSchemaMaxArrayDimensions` | `DefaultMaxArrayDimensions` | Bounds nested arrays accepted by the profile                                                             |
| `WithSchemaMaxValueDepth`      | `DefaultMaxValueDepth`      | Bounds pointer unwrapping and nested JSON values                                                         |
| `WithSchemaTextCollation`      | `DefaultTextCollation`      | SQL collation of text comparisons; a nondefault value needs `WithSchemaTextCompare`                      |
| `WithSchemaTextCompare`        | bytewise comparison         | Ordering matching the text collation                                                                     |
| `WithSchemaTimestampPrecision` | `DefaultTimestampPrecision` | Timestamp normalization; zero preserves precision                                                        |

### Defaults and limits

| Constant                    | Value              |
|-----------------------------|--------------------|
| `DefaultArrayAliasPrefix`   | `"_aip_element_"`  |
| `DefaultMaxDataBudget`      | `1 << 22`          |
| `DefaultMaxArrayDimensions` | `6`                |
| `DefaultMaxValueDepth`      | `64`               |
| `DefaultTextCollation`      | `"C"`              |
| `DefaultTimestampPrecision` | `time.Microsecond` |

NUMERIC literals are bounded by `sqltype.MaxIntegerDigits` (131072) and `sqltype.MaxFractionDigits` (16383), PostgreSQL's unconstrained NUMERIC.

## Listing contract

`NewFilterTranslatorWithSchema(policy, schema)` over a schema built with `WithSchemaContractMode(true)` renders the listing storage contract: columns
provide their own `COLLATE "C"`, scalars and arrays are non-null, optional scalars store zero values, and arrays store empty arrays. Presence columns
are the nullable ones: a NULL presence column follows the default evaluator (`rating != 1` is `("rating" <> $1 OR "rating" IS NULL)`, `!` is pushed down
to the predicates), as the memory listing engine evaluates it with the [`listing/evaluation`](../../listing/evaluation/README.md) profile.

## Order translator

`NewOrderTranslator(policy, opts...)` is `sqlbase.NewOrderTranslator(policy, OrderDialect(), opts...)`. It binds the shared
`orderby.Policy` (allow-list, field access, denied API fields, default order) to the storage options passed through `sqlbase.WithOrderOptions`
(mapping, KeyResolver, denied storage fields, tie-breakers) and validates every order with the policy before mapping it. `Translate(spec)` returns the
body of `ORDER BY` without the keywords, `""` for an empty spec.

| Spec                                            | PostgreSQL               |
|-------------------------------------------------|--------------------------|
| `name`                                          | `"name" ASC`             |
| `age desc, name`                                | `"age" DESC, "name" ASC` |
| `address.city`                                  | `"address"."city" ASC`   |
| `age` with `Key.Nulls = orderby.NullsFirst`     | `"age" ASC NULLS FIRST`  |
| `age desc` with `Key.Nulls = orderby.NullsLast` | `"age" DESC NULLS LAST`  |
| `name` with `Key.Collation = "C"`               | `"name" COLLATE "C" ASC` |

Null placement and collation are programmatic `orderby.Key` attributes; the default AIP-132 parser produces neither.

A column reference is `col` or `table.col`, each part a plain identifier (`[A-Za-z_][A-Za-z0-9_]*`, at most 63 bytes) quoted on its own; anything else
after mapping is `orderby.ErrInvalidFieldPath` — a sort column cannot be bound as a parameter. Map a nested or computed field onto a generated column
to sort by it, and add a unique column last for a deterministic order. A collation is a quoted `name` or `schema.name` identifier written before the
direction. Without `NULLS FIRST/LAST` PostgreSQL sorts NULL as larger than any value: last in `ASC`, first in `DESC`.

| Option                       | Default | Behavior                                                                                                                            |
|------------------------------|---------|-------------------------------------------------------------------------------------------------------------------------------------|
| `sqlbase.WithOrderOptions`   | nil     | Storage options: `orderby.WithFieldMapping`, `orderby.WithKeyResolver`, `orderby.WithDeniedStorageFields`, `orderby.WithTiebreaker` |
| `sqlbase.WithOrderCollector` | nil     | Records `orderby_postgres_translate_duration_seconds`; nil disables it                                                              |

```go
policy, err := orderby.NewPolicy(orderby.WithAllowedFields("createTime", "name"), orderby.WithUntrustedInput(true))
trans, err := postgres.NewOrderTranslator(policy,
	sqlbase.WithOrderOptions(orderby.WithFieldMapping(map[string]string{"createTime": "created_at"})))
body, err := trans.Translate(spec) // "createTime desc, name" → "created_at" DESC, "name" ASC
if body != "" {
	query += " ORDER BY " + body
}
```

## Projection translator

`NewProjectionTranslator(policy, opts...)` is `sqlbase.NewProjectionTranslator(policy, ProjectionDialect(), opts...)`. It binds the
shared `projection.Policy` to the storage options passed with `sqlbase.WithProjectionOptions` (mapping, denied and required storage fields) and
validates the columns of every view it serves. `Translate(spec)` and `TranslateView(spec, view)` return the quoted column list without the keyword, in
lexical order of the storage names; `""` means every column and is rendered as `*` by the caller.

PostgreSQL has no exclusion projection: a policy without an allow-list whose every authorized field would mean "every column but the denied ones" is
rejected at construction with `projection.ErrAllFieldsUnresolvable`; list the allowed fields with `projection.WithAllowedFields`. A `ModeNone`
selection without required fields is `projection.ErrUnsupportedSelection`. Column parts are plain identifiers of at most 63 bytes.

| Option                            | Default | Behavior                                                                                            |
|-----------------------------------|---------|-----------------------------------------------------------------------------------------------------|
| `sqlbase.WithProjectionOptions`   | nil     | Storage options: `projection.TranslatorOption` values (mapping, denied and required storage fields) |
| `sqlbase.WithProjectionCollector` | nil     | Records `projection_postgres_translate_duration_seconds`; nil disables it                           |

```go
policy, err := projection.NewPolicy(projection.WithUntrustedInput(true), projection.WithAllowedFields("name", "createTime"))
trans, err := postgres.NewProjectionTranslator(policy, sqlbase.WithProjectionOptions(
	projection.WithFieldMapping(map[string]string{"createTime": "created_at"}),
	projection.WithRequiredFields("id"),
))
cols, err := trans.Translate(spec) // "createTime" → "created_at", "id"
if cols == "" {
	cols = "*"
}
```

## List engine

`NewListEngine[T](q, codec, schema, table, columns, dest, opts...)` is
`sqlbase.NewListEngine(q, codec, schema, table, columns, dest, ListDialect{}, opts...)`: keyset pages over a PostgreSQL table with the backend-neutral
[`listing`](../../listing/README.md) contract. The application owns the table and its data; the engine emits parameterized queries and order indexes.
`dest` returns Scan destinations in the same order as `columns`; a nil codec serves a single page only. `ListEngineOption` is an alias of
`sqlbase.ListEngineOption`; the options (`sqlbase.WithListColumns`, `WithListScopeKeys`, `WithListParentKey`, `WithListIDColumn`, `WithListIDType`,
`WithListTimestampPrecision`, `WithListMaxTime`, …) are documented with the [sqlbase listing engine](../sqlbase/README.md#listing-engine).

`MustNewListEngine` takes the same arguments for a configuration fixed in code (a package-level `var`, like `listing.MustSchema`): it returns
the engine `NewListEngine` builds and panics on a refused one with an error that starts `sqlbase.MustNewListEngine: ` (it is
`sqlbase.MustNewListEngine` with `ListDialect` fixed) and wraps the `NewListEngine` error.

`ListDialect` is the PostgreSQL `sqlbase.ListDialect`: syntax (row-value cursor comparisons), storage validation, schema checks, index management
with advisory locking, and `NewTranslator`, which builds this package's schema filter translator in listing contract mode with the engine's timestamp
precision and text collation. Its zero value is ready to use; embed it in a custom dialect to override selected methods and pass that to
`sqlbase.NewListEngine`.

PostgreSQL storage: Required and Optional scalars use NOT NULL columns (Optional stores its zero value), Presence fields use nullable columns, arrays
are NOT NULL without null elements, text uses `COLLATE "C"`, timestamps are `timestamptz(3)` aligned to the configured precision, and
`sqlbase.IDUUID` stores string identities as `uuid`. Run `CheckSchema` before deployment; `EnsureIndexes` applies the same checks before DDL.

```go
list, err := postgres.NewListEngine[Widget](db, codec, widgetSchema, "widgets", []string{"id", "name"},
	func(w *Widget) []any { return []any{&w.ID, &w.Name} },
	sqlbase.WithListIDColumn("widget_id"),
	sqlbase.WithListColumns(map[string]string{"id": "widget_id"}),
	sqlbase.WithListScopeKeys("tenant_id"), sqlbase.WithListParentKey("tenant_id"),
)
if err != nil {
	return err
}
page, err := list.List(ctx, "widgets.List", req, sqlbase.Scope{"tenant_id": req.Parent})
```

## Errors

| Kind       | Error                                             | When                                                                                                                                                        |
|------------|---------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------|
| all        | `ErrPolicyRequired` (filter, orderby, projection) | nil policy                                                                                                                                                  |
| Filter     | `filter.ErrInvalidOptions`                        | encoder/renderer on the schema translator, nil schema                                                                                                       |
| Filter     | `filter.ErrInvalidExpression`                     | a column name that is not a plain identifier, has a part over 63 bytes or more than two parts, invalid `substring()` bounds, a non-ASCII name in inline SQL |
| Filter     | `filter.ErrFieldNotAllowed`                       | a field outside the allow-list, a dotted field the policy does not declare                                                                                  |
| Filter     | `filter.ErrUnsupportedOperation`                  | an operation PostgreSQL cannot render with the evaluator's semantics                                                                                        |
| Filter     | `filter.ErrFieldTypeMismatch`                     | field operands of incompatible declared kinds                                                                                                               |
| Schema     | `filter.ErrInvalidOptions`                        | nil field schema, invalid schema options, a collation without its comparator                                                                                |
| Schema     | `filter.ErrDataLimitExceeded`                     | literal or row data beyond the depth, dimension or work budget                                                                                              |
| Order      | `orderby.ErrFieldNotAllowed`                      | a key the policy refuses or that resolves to a denied storage path                                                                                          |
| Order      | `orderby.ErrInvalidFieldPath`                     | a key that does not map to a plain column reference                                                                                                         |
| Order      | `orderby.ErrUnsupportedOrdering`                  | a collation that is not a plain identifier                                                                                                                  |
| Projection | `projection.ErrFieldNotAllowed`                   | a path outside the policy                                                                                                                                   |
| Projection | `projection.ErrInvalidFieldPath`                  | a path that does not map to a plain column reference                                                                                                        |
| Projection | `projection.ErrUnsupportedSelection`              | a `ModeNone` selection without required fields                                                                                                              |
| Projection | `projection.ErrAllFieldsUnresolvable`             | a policy that would need an exclusion projection                                                                                                            |
| List       | `listing.ErrInvalidSchema`                        | nil schema, querier or destination, invalid columns, scope keys or options                                                                                  |

## API

| Symbol                          | Purpose                                                                                               |
|---------------------------------|-------------------------------------------------------------------------------------------------------|
| `FilterTranslator`              | Scalar filter translator; `Translate`, `TranslateInline`, `ValidateAST`                               |
| `NewFilterTranslator`           | Builds a `FilterTranslator` from the shared `filter.Policy`                                           |
| `SchemaFilterTranslator`        | Schema-aware filter translator over a `Schema`; same methods                                          |
| `NewFilterTranslatorWithSchema` | Builds a `SchemaFilterTranslator`; the schema's `WithSchemaContractMode` selects the listing contract |
| `FilterOption`                  | Generated option of both filter constructors                                                          |
| `Schema`, `NewSchema`           | Typed PostgreSQL schema; the `filter/evaluator` profile                                               |
| `SchemaOption`                  | Generated option of `NewSchema`                                                                       |
| `Program`                       | Compiled expression: SQL, inline SQL and in-memory evaluation                                         |
| `ArrayData`, `Dimension`        | Explicit SQL array values with dimensions and lower bounds                                            |
| `JSONNull`                      | JSON null, distinct from SQL NULL                                                                     |
| `OrderDialect`                  | The PostgreSQL `sqlbase.OrderDialect`, for `sqlbase.NewOrderTranslator` or a resolver                 |
| `NewOrderTranslator`            | `sqlbase.OrderTranslator` with the PostgreSQL dialect                                                 |
| `OrderOption`                   | Alias of `sqlbase.OrderOption`                                                                        |
| `ProjectionDialect`             | The PostgreSQL `sqlbase.ProjectionDialect`                                                            |
| `NewProjectionTranslator`       | `sqlbase.ProjectionTranslator` with the PostgreSQL dialect                                            |
| `ProjectionOption`              | Alias of `sqlbase.ProjectionOption`                                                                   |
| `NewListEngine`                 | `sqlbase.ListEngine` with `ListDialect`                                                               |
| `MustNewListEngine`             | Panicking `NewListEngine` for package-level declarations fixed in code                                |
| `ListEngineOption`              | Alias of `sqlbase.ListEngineOption`                                                                   |
| `ListDialect`                   | The PostgreSQL `sqlbase.ListDialect`                                                                  |

## Related packages

- [backend/sqlbase](../sqlbase/README.md) — the shared SQL walk, field schema, decoder, order and projection translators and listing engine
- [backend/mariadb](../mariadb/README.md), [backend/clickhouse](../clickhouse/README.md) — the other SQL backends
- [filter](../../filter/README.md), [filter/evaluator](../../filter/evaluator/README.md), [orderby](../../orderby/README.md),
  [projection](../../projection/README.md), [listing](../../listing/README.md) — the backend-neutral DSLs and contracts
- [filter guide](../../docs/filter.md), [orderby guide](../../docs/orderby.md), [projection guide](../../docs/projection.md),
  [listing guide](../../docs/listing.md)
