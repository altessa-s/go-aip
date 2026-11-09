# clickhouse

```go
import "github.com/altessa-s/go-aip/backend/clickhouse"
```

Everything go-aip renders for ClickHouse, in one package:

| Kind          | Constructor                                                                                                          | Renders                                                                                        |
|---------------|----------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------|
| Filter        | `NewFilterTranslator(policy, opts ...FilterOption) (*FilterTranslator, error)`                                       | A `WHERE` clause from a `filter.Node` (AIP-160)                                                |
| Schema filter | `NewFilterTranslatorWithSchema(policy, schema *Schema, opts ...FilterOption) (*SchemaFilterTranslator, error)`       | A typed `WHERE` clause under the listing storage contract (`Nullable`, `Array(T)`, JSON paths) |
| Order         | `NewOrderTranslator(policy, opts ...OrderOption) (*sqlbase.OrderTranslator, error)`                                  | The body of `ORDER BY` from an `orderby.Spec` (AIP-132)                                        |
| Projection    | `NewProjectionTranslator(policy, opts ...ProjectionOption) (*sqlbase.ProjectionTranslator, error)`                   | The `SELECT` column list from a `projection.Spec` (AIP-157)                                    |
| List engine   | `NewListEngine[T](q, codec, schema, table, columns, dest, opts ...ListEngineOption) (*sqlbase.ListEngine[T], error)` | Keyset `List` pages over a table (AIP-132, AIP-158) with `ListDialect` fixed                   |

Every constructor takes the resource's shared, client-facing policy (built once in API names and reused by every backend) followed by the storage
options that bind it to ClickHouse column names. The order and projection constructors are the dialect-generic [`sqlbase`](../sqlbase/README.md)
translators with the ClickHouse dialect fixed (`OrderOption` and `ProjectionOption` are aliases of the `sqlbase` option types); use
`sqlbase.NewOrderTranslator` / `sqlbase.NewProjectionTranslator` with an application `sqlbase.ColumnResolver` for trusted storage expressions. The list
engine is the shared [`sqlbase`](../sqlbase/README.md#listing-engine) keyset engine with this package's `ListDialect` fixed, and it filters with the
schema translator in listing contract mode, as the PostgreSQL and MariaDB engines do.

All translators are safe for concurrent use after construction; injected callbacks must be too.

## Filter translator

`FilterTranslator` translates a `filter.Node` AST into a ClickHouse SQL `WHERE` clause. Two output modes share one AST walk: a parameterized clause
with `?` placeholders plus a bound argument slice, and a self-contained clause with literals rendered in place.

### Contract

`NewFilterTranslator` takes the shared `*filter.Policy` (field authorization, types, limits, decimal conversion, value codecs and regex rules, in API
names, built once per resource) and this package's `FilterOption` values; `WithFilterOptions` binds the policy to storage names
(`filter.WithFieldMapping`). A nil policy fails with `filter.ErrPolicyRequired`; `filter.NewPolicy` requires an allowlist or an explicit trusted
`WithAnyField` opt-out.

`ValidateAST` uses the configured renderer to check operand shapes and values without contacting a server. Unsupported operations remain explicit
errors. Translators support concurrent calls with per-call traversal state; injected callbacks must also support concurrent use.

`WithFilterLiteralEncoder` converts values before backend encoding; it does not return executable query fragments. Without an encoder, decimal operands
are normalized through the shared policy, including membership, and rendered exactly as `toDecimal128(?, scale)` (see Decimals).
`WithFilterPredicateRenderer` installs a trusted application predicate renderer that binds client values through its `ValueFunc`; field authorization
and limits run first.

`Translate` returns SQL and ordered arguments; `TranslateInline` renders literals in place. Bind parameters for runtime queries. Placeholder numbering
and repeated operand bindings follow the selected dialect. NULL follows the default evaluator's semantics (see Null semantics).

### Usage

```go
parser, _ := filterparser.New()
ast, _ := parser.Parse(ctx, `status == 2 && type in [1, 2]`)

policy, err := filter.NewPolicy(
    filter.WithUntrustedInput(true),
    filter.WithAllowedFields("status", "type"),
) // build once per resource
if err != nil { return err }

trans, err := clickhouse.NewFilterTranslator(policy)
if err != nil {
    return err
}

where, args, err := trans.Translate(ast)
// where: (`status` = ?) AND (`type` IN (?, ?))
// args:  []any{int64(2), int64(1), int64(2)}

rows, err := conn.Query(ctx, "SELECT * FROM events WHERE "+where, args...)
```

`TranslateInline` produces `` (`status` = 2) AND (`type` IN (1, 2)) `` for the same AST — use it for materialized view definitions, generated DDL, logs
and debugging, and prefer `Translate` for anything driven by request data. Its output is pure ASCII: string literals escape the backslash, the quote and
the control characters, and write every byte >= 0x80 as `\xNN`, which ClickHouse reads back as that byte, so the text survives any layer that re-encodes
the statement.

### Operation mapping

| CEL                        | ClickHouse                                                                                   |
|----------------------------|----------------------------------------------------------------------------------------------|
| `==` `<` `>` `<=` `>=`     | `=` `<` `>` `<=` `>=`                                                                        |
| `field != v`               | `` (`field` != ? OR `field` IS NULL) ``                                                      |
| `field == other`           | `` COALESCE(`field` = `other`, `field` IS NULL AND `other` IS NULL) ``                       |
| `field != other`           | `` COALESCE(`field` != `other`, (`field` IS NULL) != (`other` IS NULL)) ``                   |
| `&&` `\|\|`                | `AND` `OR`                                                                                   |
| `!`                        | pushed down to each predicate (see Null semantics)                                           |
| `!(field == v)`            | `` (`field` != ? OR `field` IS NULL) ``                                                      |
| `!(field in [a, b])`       | `` (`field` NOT IN (?, ?) OR `field` IS NULL) ``                                             |
| `!(field > v)`             | `` NOT (`field` > ?) ``                                                                      |
| `field`                    | `` `field` = true ``                                                                         |
| `field in [a, b]`          | `` `field` IN (?, ?) ``                                                                      |
| `field in []`              | `1 = 0`                                                                                      |
| `field in [a, null]`       | `` (`field` IN (?) OR `field` IS NULL) ``                                                    |
| `field == null`            | `` `field` IS NULL ``                                                                        |
| `field != null`            | `` `field` IS NOT NULL ``                                                                    |
| `has(field)`               | `` `field` IS NOT NULL ``                                                                    |
| `field.contains(s)`        | `` position(`field`, ?) > 0 ``                                                               |
| `field.startsWith(s)`      | `` startsWith(`field`, ?) ``                                                                 |
| `field.endsWith(s)`        | `` endsWith(`field`, ?) ``                                                                   |
| `field.matches(re)`        | `` match(`field`, ?) ``                                                                      |
| `field.size()` / `size(f)` | `lengthUTF8` for strings, `length` for arrays (type-switched unless declared an array field) |
| `arr == x` (Array column)  | `` has(`arr`, ?) ``                                                                          |
| `arr != x`                 | `` NOT (has(`arr`, ?)) ``                                                                    |
| `arr < x` (any element)    | `` arrayExists(_aip_e -> _aip_e < ?, `arr`) ``                                               |
| `x in arr`                 | `` has(`arr`, ?) ``                                                                          |
| `arr.contains(s)`          | `` arrayExists(_aip_e -> position(_aip_e, ?) > 0, `arr`) ``                                  |
| `arr.size()`               | `` length(`arr`) ``                                                                          |
| `arr == null`              | `` empty(`arr`) ``                                                                           |
| `arr != null` / `has(arr)` | `` notEmpty(`arr`) ``                                                                        |
| decimal `field > 1.5`      | `` `field` > toDecimal128(?, 1) ``                                                           |
| `f.substring(1, 3) == s`   | `` (<size(f)> >= 3 AND substringUTF8(`f`, 2, 2) = ?) ``                                      |

`contains` compiles to `position()` rather than `LIKE '%…%'` so that a `%` or `_` in the operand stays literal — there is no pattern escaping to get
wrong. `size()` is rejected outside a comparison: a length is an integer expression, and on its own there is no predicate for `WHERE` to test.

### Arrays

Fields declared by `filter.WithArrayFields` are `Array(T)` columns (Nested sub-columns included) and get element semantics, matching the evaluator and
the MongoDB translator: `==` is `has(col, ?)`, ordered operators `arrayExists(_aip_e -> _aip_e op ?, col)`, `!=` holds when no element equals the
operand, `x in arr` is `arr == x`, and `arr in [a, b]` is an OR of `has()`. String predicates use `arrayExists` over each element; `size()` is
`length(col)`. Field operands, a field argument to an array string predicate and a bare array identifier fail with `filter.ErrUnsupportedOperation`.

An `Array` column cannot be `Nullable`, so an absent array is stored as an empty one. The translator therefore renders null and presence tests as
emptiness (`sqlbase.EmptyArrayDialect`): `arr == null` is `empty(col)`, `arr != null` and `has(arr)` are `notEmpty(col)`, and `arr != x` is plain
`NOT (has(col, ?))`, which already holds for an empty array — the evaluator holds `!=` for an absent array too; every other negated element test is a
plain `NOT (…)` as well. The one remaining difference is inherent to the storage: an empty array and an absent one are indistinguishable, so
`arr == null` also matches a record whose array is empty, where the evaluator (and the PostgreSQL and MariaDB translators, whose array columns can be
NULL) match only the absent one.

### Decimals

Fields declared `filter.FieldKindDecimal` need no encoder: operands keep their source spelling (`Policy.DecimalOperand`), are bound as strings and
converted with `toDecimal128(?, scale)` at the literal's own scale, so nothing is rounded. A literal with more than 38 significant digits fails with
`filter.ErrFieldTypeMismatch`. With `WithFilterLiteralEncoder` the encoder receives the `decimal.Decimal` instead.

The scalar translator takes nullable, decimal and array semantics from the policy declarations above; the [schema translator](#schema-filter-translator)
takes them from a typed `sqlbase.Schema`.

### substring()

`field.substring(start[, end])` is supported as a comparison or `in` operand with int literal bounds and renders as
`substringUTF8(col, start+1, end-start)` (code points). Invalid bounds fail with `filter.ErrInvalidExpression`, non-literal bounds with
`filter.ErrUnsupportedOperation`. The comparison is guarded with the column's code-point length (`size()`), so a row shorter than the bound never
matches — the evaluator finds it unknown rather than comparing a truncated value. Any int64 bound is accepted, as in the evaluator; the bounds are
ordered before `end - start` is computed, and the start of `substring(9223372036854775807)` is capped rather than overflowed (its guard never holds).

### Column names

Field names are checked against `WithAllowedFields`, run through `WithFieldMapping`, validated as plain ClickHouse identifiers, and emitted
backtick-quoted. Dots survive as part of a single quoted name — `address.city` becomes `` `address.city` ``, which is how Nested columns are stored.
A dot is part of the column name here, never a qualifier, so a dotted field needs no declaration beyond the allow-list. A name longer than 255 bytes
(`sqlbase.ClickHouseMaxIdentLen`) fails with `filter.ErrInvalidExpression`.

The validation is stricter than backtick quoting requires: the column position is the one part of the generated SQL that no placeholder can cover, so
anything that is not `[A-Za-z_][A-Za-z0-9_]*` (dot-separated) is rejected with `filter.ErrInvalidExpression`. To filter on a `Map` key or a computed
value, map the CEL name onto a materialized column.

### Null semantics

ClickHouse rows always carry every column of the table, so there is no "missing field" for `has()` to detect — it compiles to `IS NOT NULL` and is
meaningful only against a `Nullable` column. A NULL column otherwise behaves like a null or absent field in the default evaluator, not under SQL
three-valued logic: `col != "x"` is `` (`col` != ? OR `col` IS NULL) `` and matches the NULL row. Two field operands compare null-safe, so NULL equals
NULL and differs from every value. ClickHouse's `isNotDistinctFrom` is limited to `JOIN ON`, so the comparison uses the portable
`COALESCE(a = b, a IS NULL AND b IS NULL)` / `COALESCE(a != b, (a IS NULL) != (b IS NULL))`.

`!` is not rendered as `NOT` around a clause, since `NOT` keeps a NULL comparison unknown where the evaluator's `!` holds. It is pushed down through
`&&` and `||` (De Morgan) to each predicate, which renders its own negation: `!(col == "x")` is the `!=` form above, `!(col in ["a", "b"])` is
`` (`col` NOT IN (?, ?) OR `col` IS NULL) ``, `!col.contains("a")` is `` (NOT (…) OR `col` IS NULL) `` and `!has(col)` is `IS NULL`, so each matches
the NULL row. An ordered comparison, a bare boolean, `size()` / `substring()` of a NULL column and a NULL column as a string function argument are
unknown in the evaluator; their negation stays `NOT (…)` and does not match NULL either. Array columns are never NULL (see Arrays).

A `null` element of an `in` list matches a NULL column, as in the evaluator: `x in ["a", null]` is ``(`x` IN (?) OR `x` IS NULL)``, a list of only
nulls is `` `x` IS NULL ``, and its negation is `` `x` NOT IN (?) `` (`` `x` IS NOT NULL `` for only nulls). An ordered comparison with `null`
(`age > null`) fails with `filter.ErrUnsupportedOperation`, and two field operands of incompatible declared kinds (`name == age` with a string `name`
and an int `age`) with `filter.ErrFieldTypeMismatch`; see [sqlbase](../sqlbase/README.md#shared-semantics).

### Timestamps

`timestamp(...)` literals bind their exact UTC text in the parameterized form, converted with `toDateTime64(?, digits, 'UTC')` at the fewest fractional
digits that hold the value (`timestamp("2022-12-31T23:59:59.5Z")` is `toDateTime64(?, 1, 'UTC')` with `"2022-12-31 23:59:59.5"`), so nothing is rounded:
clickhouse-go formats a bound `time.Time` to whole seconds, which made `created < timestamp("…59.5Z")` miss a row stored at `…59`. A literal outside the
`DateTime64` range (1900-01-01 through 2299-12-31; 2262-04-11 at nanoseconds) fails with `filter.ErrFieldTypeMismatch`. Inline they render as
`toDateTime64('…', 3, 'UTC')`, keeping the fractional digits `WithFilterTimestampPrecision` needs (default 1ms: three digits) and dropping anything
finer. A literal encoder that returns a `time.Time` gets the same parameterized conversion.

### Field operands

Comparisons, `in` list elements, and `contains` / `startsWith` / `endsWith` arguments can reference other fields. Field mappings and allowlists apply to
every reference. Lists containing dynamic operands render as equality disjunctions; constant lists retain `IN`. Literal values remain bound parameters.

`matches` requires a literal pattern so regex validation and length limits apply before execution.

`size()` selects array length or UTF-8 code-point length using the column type. Both branches remain type-correct for String and Array columns; byte
length does not stand in for character count.

### Concurrency

A `FilterTranslator` is safe for concurrent `Translate` and `TranslateInline` calls. Each call owns its traversal state.

### Methods

| Method                                   | Description                                                                 |
|------------------------------------------|-----------------------------------------------------------------------------|
| `Translate(node) (string, []any, error)` | Parameterized clause; every literal becomes a `?` and is returned in `args` |
| `TranslateInline(node) (string, error)`  | Self-contained clause with literals rendered in place                       |
| `ValidateAST(node) error`                | Check the configured renderer without contacting the database               |

A `nil` node translates to `1 = 1` (match all) in both modes, so `"... WHERE " + where` stays valid without a special case.

### Configuration and compatibility

`NewFilterTranslator` accepts this package's generated `FilterOption` values. Shared policies and callbacks must be safe for concurrent use. The
translator exposes no `filter.Visitor` methods; call `Translate`. `ValidateAST` checks actual expression shapes and values, including field-to-field
operands, rather than promising support for an operator regardless of its operands. Unsupported expressions return filter sentinel errors.
Backend-native differences remain explicit.

Literal encoders return values, never executable query text. They can choose timestamp units or stored enum representations.

`WithFilterTimestampPrecision` configures inline `DateTime64` fractional digits: the digits the precision needs, at most 9 (default 1ms, three
digits; zero keeps nanoseconds). Bound timestamp values remain exact (see Timestamps).

### Filter options

| Option                         | Default                     | Behavior                                                                                                                                                                                     |
|--------------------------------|-----------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `WithFilterOptions`            | nil                         | Binds the shared policy to the storage names of this backend (`filter.WithFieldMapping`)                                                                                                     |
| `WithFilterCollector`          | nil                         | A `metrics.Meter` recording the translation duration under the backend label `clickhouse`; nil disables it                                                                                   |
| `WithFilterLiteralEncoder`     | nil                         | Converts values before backend quoting; must be concurrency safe                                                                                                                             |
| `WithFilterPredicateRenderer`  | nil                         | Trusted predicate override (`sqlbase.PredicateRenderer`) that binds client values through its `ValueFunc`; return handled=false to use built-ins. Runs after field authorization and limits. |
| `WithFilterTimestampPrecision` | `DefaultTimestampPrecision` | Inline `DateTime64` values: the fractional digits the precision needs, at most 9; zero keeps nanoseconds, a negative precision fails with `filter.ErrInvalidOptions` (scalar translator)     |

`NewFilterTranslatorWithSchema` takes only `WithFilterOptions` and `WithFilterCollector`: the scalar options (`WithFilterLiteralEncoder`,
`WithFilterPredicateRenderer`, `WithFilterTimestampPrecision`) fail it with `filter.ErrInvalidOptions` instead of being ignored, since its settings
come from the `Schema`.

### Schema options

`NewSchema(fields *sqlbase.Schema, opts ...SchemaOption) (*Schema, error)` binds the SQL fields to the settings of the schema translator. A nil
fields, a negative timestamp precision or an invalid decimal format fails with `filter.ErrInvalidOptions`.

| Option                         | Default                          | Behavior                                                                                                                           |
|--------------------------------|----------------------------------|------------------------------------------------------------------------------------------------------------------------------------|
| `WithSchemaContractMode`       | false                            | The listing storage contract, required by `NewFilterTranslatorWithSchema`                                                          |
| `WithSchemaDecimalFormat`      | `sqlbase.DefaultDecimalFormat()` | The `Decimal(P, S)` domain decimal operands are cast to; an operand must fit it exactly                                            |
| `WithSchemaMaxDataBudget`      | `DefaultMaxDataBudget`           | Literal validation work (bytes and values) per filter; beyond it `filter.ErrDataLimitExceeded`                                     |
| `WithSchemaTimestampPrecision` | `DefaultTimestampPrecision`      | Truncates and casts every timestamp operand to `DateTime64` with the digits the precision needs, at most 9; zero keeps nanoseconds |

| Constant                    | Value              |
|-----------------------------|--------------------|
| `DefaultTimestampPrecision` | `time.Millisecond` |
| `DefaultMaxDataBudget`      | `1 << 22`          |

The PostgreSQL and MariaDB schemas have four options this one lacks on purpose; each is fixed by the ClickHouse storage contract:

| PostgreSQL / MariaDB option    | ClickHouse                                                                                                                          |
|--------------------------------|-------------------------------------------------------------------------------------------------------------------------------------|
| `WithSchemaMaxArrayDimensions` | Fixed at one: contract arrays are `Array(T)` of scalars, so a nested list operand fails with `filter.ErrUnsupportedOperation`       |
| `WithSchemaArrayAliasPrefix`   | Not needed: element tests are lambdas (`arrayExists(_aip_e -> …)`) whose parameter is scoped to the call and cannot shadow a column |
| `WithSchemaTextCollation`      | Not expressible: ClickHouse compares `String` values bytewise and accepts `COLLATE` only in `ORDER BY` (see the order translator)   |
| `WithSchemaMaxValueDepth`      | Not needed: literals are validated as the parser produced them, without following pointers or driver decoders                       |

The scalar translator differs from its SQL siblings in the same way, also on purpose: `WithFilterTimestampPrecision` exists only here
(`DateTime64` takes its scale from the operand, while the PostgreSQL and MariaDB scalar translators render microseconds, the most their types
hold), the scalar translator has no `WithFilterDecimalFormat` (decimal operands render as exact `toDecimal128` at the literal's own scale;
the schema translator casts to `WithSchemaDecimalFormat`), and `SchemaFilterTranslator` has no `TranslateInline` (only the PostgreSQL schema
translator renders inline).

### Configuration example

```go
policy, err := filter.NewPolicy(
    filter.WithUntrustedInput(true),
    filter.WithAllowedFields("status", "createdAt", "organizationIds"),
) // build once per resource
if err != nil { return err }

trans, err := clickhouse.NewFilterTranslator(policy, clickhouse.WithFilterOptions(filter.WithFieldMapping(map[string]string{
    "organizationIds": "organization_id",
    "createdAt":       "created_at",
})))
```

## Schema filter translator

`NewFilterTranslatorWithSchema(policy, schema, opts...)` renders the listing storage contract, the semantics of the PostgreSQL and MariaDB contract
translators and of the memory listing engine, from a typed [`sqlbase.Schema`](../sqlbase/README.md#sql-field-schema) bound by `NewSchema`.
`WithSchemaContractMode(true)` is required (without it, or with a nil schema, the constructor fails with `filter.ErrInvalidOptions`);
`WithFilterOptions` maps the policy's names and must agree with the schema's columns (`Schema.ValidatePolicy`). `Translate(node)` validates the whole
tree (kinds, literals, shapes, the portable contract of a `filter.WithPortableContract` policy) before it renders `?` placeholders and the bound
arguments; there is no inline form. `ValidateAST` runs the same checks. The listing engine builds this translator itself.

| Schema field                   | ClickHouse column                      | Null, `has()` and `size()`                                                                            |
|--------------------------------|----------------------------------------|-------------------------------------------------------------------------------------------------------|
| `Scalar(col, kind)`            | NOT NULL `T`                           | an absent Optional value is the zero value: `x == null` is `` `x` = <zero> ``                         |
| `PresenceScalar(col, kind)`    | `Nullable(T)`                          | `IS [NOT] NULL`; `size()` of NULL is 0 (`COALESCE(…, 0)`)                                             |
| `Array(col, kind)`             | `Array(T)` without `Nullable` elements | `empty()` / `notEmpty()`                                                                              |
| `JSONPath(col, kind, path...)` | `String` holding JSON text             | a missing key, a JSON null, another JSON type or text that is not JSON is NULL, like `PresenceScalar` |

| Kind           | ClickHouse type | Operand                                               | JSON path                                                        |
|----------------|-----------------|-------------------------------------------------------|------------------------------------------------------------------|
| `Text`         | `String`        | bound string                                          | a JSON string: `JSONExtract(col, path…, 'Nullable(String)')`     |
| `Integer`      | `Int64`         | bound int64                                           | a JSON integer within Int64: `JSONExtract(…, 'Nullable(Int64)')` |
| `Float`        | `Float64`       | bound float64 (finite)                                | a JSON number: `JSONExtract(…, 'Nullable(Float64)')`             |
| `Boolean`      | `Bool`          | bound bool                                            | a JSON boolean: `JSONExtract(…, 'Nullable(Bool)')`               |
| `Timestamp`    | `DateTime64(p)` | `toDateTime64(?, p, 'UTC')` of the truncated UTC text | a JSON string: `parseDateTime64BestEffortOrNull(…, p, 'UTC')`    |
| `Numeric`      | `Decimal(P, S)` | `CAST(? AS Decimal(P, S))` of the exact text          | `filter.ErrUnsupportedOperation`                                 |
| `Bytes`        | `String`        | bound bytes                                           | `filter.ErrUnsupportedOperation`                                 |
| `JSON`, `UUID` | –               | `filter.ErrUnsupportedOperation`                      | –                                                                |

Column names are quoted whole, like the scalar translator (`address.city` is one Nested column). JSON path components are string keys; a
component that is an integer is an array index, zero-based like PostgreSQL's `#>>` (rendered one-based for ClickHouse) and negative from the end.
`JSONExtract` converts between JSON types (the number `5` reads as the string `"5"`, the string `"1"` and the number `1.5` as the integer `1`, the
number `1` as `true`), so every path is guarded by its JSON type: `if(JSONType(col, path…) IN ('String'), JSONExtract(…), NULL)`, with `'Int64'`
for an integer, `'Int64', 'UInt64', 'Double'` for a float and `'Bool'` for a boolean. A timestamp path reads a JSON string with ClickHouse's
best-effort parser, which also accepts spellings other than RFC 3339 (`2024-03-01 10:00:00`); a string it cannot parse is NULL.
The zero value of an Optional timestamp is the Unix epoch, the `DateTime64` default and proto3's zero `Timestamp` (Go's zero time is outside
the `DateTime64` range); a timestamp literal outside 1900-01-01 through 2299-12-31 fails with `filter.ErrFieldTypeMismatch` rather than being
clamped by the server.

| CEL                            | ClickHouse                                                                           |
|--------------------------------|--------------------------------------------------------------------------------------|
| `name == null` (Scalar text)   | `` (`name` = ?) `` with `""`                                                         |
| `note != "a"` (Presence)       | `` (`note` != ? OR `note` IS NULL) ``                                                |
| `!(note in ["a", null])`       | `` (`note` NOT IN (?) AND `note` IS NOT NULL) ``                                     |
| `note == other` (two Presence) | `` COALESCE(`note` = `other`, `note` IS NULL AND `other` IS NULL) ``                 |
| `tags == "a"`, `"a" in tags`   | `` has(`tags`, ?) ``                                                                 |
| `tags != "a"`                  | `` (NOT has(`tags`, ?)) ``                                                           |
| `tags in ["a", "b"]`           | `` hasAny(`tags`, [?, ?]) ``                                                         |
| `tags > "b"`                   | `` arrayExists(_aip_e -> _aip_e > ?, `tags`) ``                                      |
| `tags.contains("x")`           | `` arrayExists(_aip_e -> position(_aip_e, ?) > 0, `tags`) ``                         |
| `price > 1.5`                  | `` (`price` > CAST(? AS Decimal(65, 30))) ``                                         |
| `active != (name == "x")`      | the predicate operand as `(CASE WHEN … THEN true WHEN … THEN false END)`             |
| `name.substring(1, 3) == "ab"` | `` ((CASE WHEN lengthUTF8(`name`) >= 3 THEN substringUTF8(`name`, 2, 2) END) = ?) `` |

NULL and negation follow the default evaluator exactly as the [scalar translator](#null-semantics) does: `!` is pushed down to each predicate,
a NULL column or JSON path equals null and differs from every value, is in no list without a null element and contains no string, and an ordered
comparison or a bare boolean on NULL stays unknown, also under negation. Array tests are never unknown. A predicate used as a comparison operand
renders its three-valued result.

`field.substring(start[, end])` of a text column or text JSON path is a comparison or `in` operand, as in the [scalar translator](#substring), with the
same bounds (int literals, `0 <= start <= end`; otherwise `filter.ErrInvalidExpression`, non-literal bounds `filter.ErrUnsupportedOperation`) and
code-point semantics. It renders as `substringUTF8(col, start+1, end-start)` inside `CASE WHEN lengthUTF8(col) >= end THEN … END`, so it is NULL exactly
where the default evaluator finds it unknown — a NULL column or path, or a string shorter than the bound — and such a row matches neither the comparison
nor its negation; `substring(…) == null` never holds and `!(substring(…) in [])` holds only where the substring exists. A portable-contract policy (the
listing one) refuses `substring()` in `filter.Validate`.

The schema translator refuses what it cannot render with the evaluator's semantics, with the shared sentinels: an ordered comparison with `null`, a
field next to an array field or inside an `in` list, a non-literal string-function argument, a nested list, `literal == array` and `substring()` of an
array field, of a non-field or anywhere but a comparison or `in` operand (`filter.ErrUnsupportedOperation`); incompatible kinds, a float literal on an
integer field, a decimal outside `WithSchemaDecimalFormat`, an int literal beyond int64, a non-finite float and a string with NUL or invalid UTF-8
(`filter.ErrFieldTypeMismatch`); an undeclared field (`filter.ErrInvalidOptions`); a regex outside the portable subset when `filter.PortableRegex` is
enabled (`filter.ErrUnsupportedOperation`).

## List engine

`NewListEngine[T](q, codec, schema, table, columns, dest, opts...)` is `sqlbase.NewListEngine` with `ListDialect` fixed; the `sqlbase.WithList…`
options configure columns, scope keys, identity, precision and limits. `ListDialect` implements every `sqlbase.ListDialect` method: backtick
syntax, typed cursor operands, storage validation, `system.columns` schema checks and minmax skipping-index DDL, and `NewTranslator`, which
builds the schema filter translator in contract mode with the engine's decimal format and timestamp precision; embed it to override selected
behavior and pass it to `sqlbase.NewListEngine`.

| Aspect           | ClickHouse                                                                                                                                                                                                            |
|------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Syntax           | Backtick identifiers, `?` placeholders, `LIMIT n`; the table may be `database.table`                                                                                                                                  |
| Cursor           | `(a > ? OR (a = ? AND id > ?))`, decimals as `CAST(? AS Decimal(P, S))`, timestamps as `toDateTime64(?, 9, 'UTC')` of their exact text                                                                                |
| Scan             | Decimal identities and sort keys are selected with `toString` and decoded exactly                                                                                                                                     |
| Storage          | `String`, `Int64`, `Float64`, `Bool`, `DateTime64(3)`, `Decimal(P, S)` (`LowCardinality` accepted); `Nullable(T)` only for Presence; native `Array(T)`                                                                |
| Rejected options | `WithListIDType(IDUUID)` (ClickHouse orders UUIDs by their 64-bit halves), `WithListArrayStorage(ArraysJSON)`, `WithListTextCollation` (bytes compare)                                                                |
| `CheckSchema`    | Reads `system.columns`: `CheckColumn`, `CheckType`, `CheckNullability` (also `Nullable` array elements and scope columns), `CheckTimestampPrecision` (a column finer than the cursor precision), `CheckDecimalDomain` |
| `EnsureIndexes`  | `ALTER TABLE … ADD INDEX IF NOT EXISTS … (scope…, keys…) TYPE minmax GRANULARITY 1` per sort pattern, after `CheckSchema`                                                                                             |

The primary index of a MergeTree table is its sorting key, chosen when the table is created: start it with the scope keys and the dominant sort
pattern (for example `ORDER BY (tenant_id, created_at, id)`); the skipping indexes only supplement it. Stored timestamps must be aligned to the
cursor precision (`DateTime64(3)` for the default millisecond), and floats must be finite. clickhouse-go (through ch-go) converts a `DateTime64`
through Unix nanoseconds when it binds or scans one, so a value after 2262-04-11 wraps around in the driver; keep stored times before it.

```go
list, err := clickhouse.NewListEngine[Widget](db, signer, widgetSchema, "events.widgets", []string{"id", "name"},
	func(w *Widget) []any { return []any{&w.ID, &w.Name} },
	sqlbase.WithListColumns(map[string]string{"createTime": "created_at"}),
	sqlbase.WithListScopeKeys("tenant_id"), sqlbase.WithListParentKey("tenant_id"),
)
if err != nil {
	return err
}
if err := list.CheckSchema(ctx, db); err != nil {
	return err
}
page, err := list.List(ctx, "widgets.List", req, sqlbase.Scope{"tenant_id": req.Parent})
```

The listing conformance suite (`listing/listingtest`) runs against a live ClickHouse server on MergeTree tables in the test-only module
[`integration/docker`](../../integration/docker/README.md) (package `integration/docker/clickhouse`, build tag `integration_docker`, the server started
in Docker); so do the filter conformance suite (scalar translator), the contract and schema translators (every column kind and JSON path kind, compared
with the listing evaluator), the scalar NULL cross-checks and the order and projection conformance suites. The expectation table of the filter
conformance suite and the projection decoder they share with this package's unit tests live in [`clickhousetest`](clickhousetest/README.md). Without
Docker this package translates every filter of the listing suite against its widget fixture.

```sh
go -C integration/docker test -race -tags integration_docker ./clickhouse/
```

## Order translator

`NewOrderTranslator(policy, opts...)` is `sqlbase.NewOrderTranslator(policy, OrderDialect(), opts...)`. It binds the shared
`orderby.Policy` (allow-list, field access, denied API fields, default order) to the storage options passed through `sqlbase.WithOrderOptions`
(mapping, KeyResolver, denied storage fields, tie-breakers) and validates every order with the policy before mapping it. `Translate(spec)` returns the
body of `ORDER BY` without the keywords, `""` for an empty spec.

| Spec                                            | ClickHouse                    |
|-------------------------------------------------|-------------------------------|
| `name`                                          | `` `name` ASC ``              |
| `age desc, name`                                | `` `age` DESC, `name` ASC ``  |
| `address.city`                                  | `` `address`.`city` ASC ``    |
| `age` with `Key.Nulls = orderby.NullsFirst`     | `` `age` ASC NULLS FIRST ``   |
| `age desc` with `Key.Nulls = orderby.NullsLast` | `` `age` DESC NULLS LAST ``   |
| `name` with `Key.Collation = "en"`              | `` `name` ASC COLLATE 'en' `` |

Null placement and collation are programmatic `orderby.Key` attributes; the default AIP-132 parser produces neither. The collation is an ICU
locale the server must know: ClickHouse refuses `COLLATE 'C'` (UNSUPPORTED_COLLATION_LOCALE, code 186) — without a collation it already compares
strings by their bytes.

Each part of a column reference is a plain identifier (`[A-Za-z_][A-Za-z0-9_]*`, at most 255 bytes) quoted on its own; anything else after mapping
is `orderby.ErrInvalidFieldPath` — a sort column cannot be bound as a parameter. Map a nested or computed field onto a `MATERIALIZED`/`ALIAS`
column to sort by it, and add a unique column last for a deterministic order. The collation is a string literal on ClickHouse, so it must be a
plain locale (`[A-Za-z][A-Za-z0-9_-]*`, at most 64 bytes); anything else is `orderby.ErrUnsupportedOrdering`. Without `NULLS FIRST/LAST`
ClickHouse sorts NULL last in both directions, after NaN.

Unlike the filter translator, which keeps a dotted name as one Nested column (`` `address.city` ``), the order and projection translators quote
each part (`` `address`.`city` ``); map a Nested column to its own name when both are used on it. A part-by-part name may also name a column
of a joined table, so a dotted client key or read-mask path must be declared — an exact `WithAllowedFields` or `WithFieldMapping` entry — as
on PostgreSQL and MariaDB; one only a wildcard (`"a.*"`) admits fails with `orderby.ErrFieldNotAllowed` or `projection.ErrFieldNotAllowed`.

| Option                       | Default | Behavior                                                                                                                            |
|------------------------------|---------|-------------------------------------------------------------------------------------------------------------------------------------|
| `sqlbase.WithOrderOptions`   | nil     | Storage options: `orderby.WithFieldMapping`, `orderby.WithKeyResolver`, `orderby.WithDeniedStorageFields`, `orderby.WithTiebreaker` |
| `sqlbase.WithOrderCollector` | nil     | Records `orderby_clickhouse_translate_duration_seconds`; nil disables it                                                            |

```go
policy, err := orderby.NewPolicy(orderby.WithAllowedFields("createTime", "name"), orderby.WithUntrustedInput(true))
trans, err := clickhouse.NewOrderTranslator(policy,
	sqlbase.WithOrderOptions(orderby.WithFieldMapping(map[string]string{"createTime": "created_at"})))
body, err := trans.Translate(spec) // "createTime desc, name" → `created_at` DESC, `name` ASC
if body != "" {
	query += " ORDER BY " + body
}
```

## Projection translator

`NewProjectionTranslator(policy, opts...)` is `sqlbase.NewProjectionTranslator(policy, ProjectionDialect(), opts...)`. It binds
the shared `projection.Policy` to the storage options passed with `sqlbase.WithProjectionOptions` (mapping, denied and required storage fields) and
validates the columns of every view it serves. `Translate(spec)` and `TranslateView(spec, view)` return the quoted column list without the
keyword, in lexical order of the storage names; `""` means every column and is rendered as `*` by the caller.

ClickHouse is the one SQL dialect that renders an exclusion: every authorized field of a policy without an allow-list is
`` * EXCEPT (`password`, `api_key`) `` (denied storage names in lexical order). `*` never expands a subcolumn, so a denied dotted storage path is
rejected at construction with `projection.ErrAllFieldsUnresolvable` (also matching `projection.ErrUnsupportedSelection`). A `ModeNone` selection
without required fields is `projection.ErrUnsupportedSelection`.

| Option                            | Default | Behavior                                                                                            |
|-----------------------------------|---------|-----------------------------------------------------------------------------------------------------|
| `sqlbase.WithProjectionOptions`   | nil     | Storage options: `projection.TranslatorOption` values (mapping, denied and required storage fields) |
| `sqlbase.WithProjectionCollector` | nil     | Records `projection_clickhouse_translate_duration_seconds`; nil disables it                         |

```go
policy, err := projection.NewPolicy(projection.WithUntrustedInput(true), projection.WithAllowedFields("name", "createTime"))
trans, err := clickhouse.NewProjectionTranslator(policy, sqlbase.WithProjectionOptions(
	projection.WithFieldMapping(map[string]string{"createTime": "created_at"}),
	projection.WithRequiredFields("id"),
))
cols, err := trans.Translate(spec) // "createTime" → `created_at`, `id`
if cols == "" {
	cols = "*"
}
```

## Errors

| Kind       | Error                                             | When                                                                                                                                                                                                       |
|------------|---------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| all        | `ErrPolicyRequired` (filter, orderby, projection) | nil policy                                                                                                                                                                                                 |
| Filter     | `filter.ErrInvalidOptions`                        | a negative timestamp precision or an invalid decimal format (`NewSchema`); missing contract mode, a nil schema, a scalar option or a mapping conflict on the schema translator; an undeclared schema field |
| Filter     | `filter.ErrDataLimitExceeded`                     | schema translator literals beyond `WithSchemaMaxDataBudget`                                                                                                                                                |
| Filter     | `filter.ErrInvalidExpression`                     | a column name that is not a plain identifier, invalid `substring()` bounds                                                                                                                                 |
| Filter     | `filter.ErrUnsupportedOperation`                  | an operation ClickHouse cannot render with the evaluator's semantics                                                                                                                                       |
| Filter     | `filter.ErrFieldTypeMismatch`                     | incompatible declared kinds, a decimal with more than 38 significant digits (scalar) or outside the decimal format (schema), a timestamp outside the `DateTime64` range (schema)                           |
| List       | `listing.ErrInvalidSchema`                        | IDUUID, ArraysJSON, a text collation, an invalid decimal format; a failed `CheckSchema` check                                                                                                              |
| Order      | `orderby.ErrFieldNotAllowed`                      | a key the policy refuses or that resolves to a denied storage path                                                                                                                                         |
| Order      | `orderby.ErrInvalidFieldPath`                     | a key that does not map to a plain column reference                                                                                                                                                        |
| Order      | `orderby.ErrUnsupportedOrdering`                  | a collation that is not a plain locale                                                                                                                                                                     |
| Projection | `projection.ErrFieldNotAllowed`                   | a path outside the policy                                                                                                                                                                                  |
| Projection | `projection.ErrInvalidFieldPath`                  | a path that does not map to a plain column reference                                                                                                                                                       |
| Projection | `projection.ErrUnsupportedSelection`              | a `ModeNone` selection without required fields                                                                                                                                                             |
| Projection | `projection.ErrAllFieldsUnresolvable`             | an exclusion with a dotted denied storage path                                                                                                                                                             |

## API

| Symbol                          | Purpose                                                                                      |
|---------------------------------|----------------------------------------------------------------------------------------------|
| `FilterTranslator`              | Filter translator; `Translate`, `TranslateInline`, `ValidateAST`                             |
| `NewFilterTranslator`           | Builds a `FilterTranslator` from the shared `filter.Policy`                                  |
| `SchemaFilterTranslator`        | Schema filter translator; `Translate`, `ValidateAST`                                         |
| `NewFilterTranslatorWithSchema` | Builds a `SchemaFilterTranslator` from the policy and a contract-mode `Schema`               |
| `Schema`, `NewSchema`           | A `sqlbase.Schema` bound to the schema translator settings; `ContractMode`, `ValidatePolicy` |
| `SchemaOption`                  | Generated option of `NewSchema`                                                              |
| `FilterOption`                  | Generated option of both filter constructors                                                 |
| `DefaultTimestampPrecision`     | Default timestamp resolution (1ms, `DateTime64(3)`)                                          |
| `DefaultMaxDataBudget`          | Default literal validation budget of the schema translator                                   |
| `ListDialect`                   | The ClickHouse `sqlbase.ListDialect`, with the contract-mode filter translator               |
| `NewListEngine`                 | `sqlbase.NewListEngine` with `ListDialect` fixed                                             |
| `ListEngineOption`              | Alias of `sqlbase.ListEngineOption` (the `sqlbase.WithList…` options)                        |
| `OrderDialect`                  | The ClickHouse `sqlbase.OrderDialect`, for `sqlbase.NewOrderTranslator` or a resolver        |
| `NewOrderTranslator`            | `sqlbase.OrderTranslator` with the ClickHouse dialect                                        |
| `OrderOption`                   | Alias of `sqlbase.OrderOption`                                                               |
| `ProjectionDialect`             | The ClickHouse `sqlbase.ProjectionDialect`, with `* EXCEPT` exclusions                       |
| `NewProjectionTranslator`       | `sqlbase.ProjectionTranslator` with the ClickHouse dialect                                   |
| `ProjectionOption`              | Alias of `sqlbase.ProjectionOption`                                                          |

## Related packages

- [backend/sqlbase](../sqlbase/README.md) — the shared SQL walk, SQL field schema, order and projection translators and listing engine
- [listing](../../listing/README.md), [listing guide](../../docs/listing.md) — the List contract the engine serves
- [backend/postgres](../postgres/README.md), [backend/mariadb](../mariadb/README.md) — the other SQL backends
- [filter](../../filter/README.md), [orderby](../../orderby/README.md), [projection](../../projection/README.md) — the backend-neutral DSLs
- [filter guide](../../docs/filter.md), [orderby guide](../../docs/orderby.md), [projection guide](../../docs/projection.md)
