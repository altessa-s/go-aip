# sqlbase

```go
import "github.com/altessa-s/go-aip/backend/sqlbase"
```

Shared SQL machinery of the PostgreSQL, MariaDB and ClickHouse backends, in one package:

| Part                                                  | Main API                                                                                                                      |
|-------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------|
| [Filter predicate builder](#filter-predicate-builder) | `FilterTranslator`, `NewFilterTranslator`, `FilterDialect`, `FilterOption`                                                    |
| [SQL field schema](#sql-field-schema)                 | `Schema`, `NewSchema`, `Field`, `Kind`, `SchemaOption`                                                                        |
| [Value decoder](#value-decoder)                       | `Decoder` (database/sql values for the evaluator)                                                                             |
| [Order translator](#order-translator)                 | `OrderTranslator`, `NewOrderTranslator`, `OrderDialect`, `TranslateOrder`, `OrderOption`                                      |
| [Projection translator](#projection-translator)       | `ProjectionTranslator`, `NewProjectionTranslator`, `ProjectionDialect`, `TranslateProjection`, `ProjectionOption`             |
| [Listing engine](#listing-engine)                     | `ListEngine`, `NewListEngine`, `ListDialect`, `DecodeCursor`, `ExpandKeysetPredicate`, `CheckListColumns`, `ListEngineOption` |
| [Dialect kit](#dialect-kit)                           | `Style`, `Identifiers`, `IsPortableIdent`, `Unqualified`, `Qualifier`; subpackages `sqltype` and `sqlbasetest`                |

Every option is prefixed with its kind: `WithFilter…`, `WithSchema…`, `WithOrder…`, `WithProjection…`, `WithList…`. The dialect-generic
constructors take the dialect explicitly (`NewOrderTranslator(policy, dialect, opts...)`); backend packages own their dialects
(`postgres.OrderDialect()`, `mariadb.ListDialect`, …) and fix them. sqlbase holds no database-specific code. `ColumnResolver` lets an
application render order and projection columns through its own trusted storage expressions.

## Filter predicate builder

`FilterTranslator` is the AST walk shared by the SQL filter translators — [`clickhouse`](../clickhouse/README.md), [`mariadb`](../mariadb/README.md) and
[`postgres`](../postgres/README.md). Applications can implement the rendering interfaces.

### Contract

`FilterTranslator` shares policy validation, AST traversal, operation budgets, argument collection, and inline/bound rendering across SQL dialects.
`FilterDialect` owns identifiers, placeholders, literals, sizes, and string predicates. ValueFunc must be used once for each SQL occurrence of a bound
value.

`WithFilterPredicateRenderer` installs an application `PredicateRenderer` that runs after field and limit validation. Return handled=false for the
built-in walker. Custom renderers must quote identifiers and bind client values through the `ValueFunc`; arbitrary request text is never a trusted SQL
fragment. `WithFilterLiteralEncoder` converts every literal before it is bound or rendered inline. Schema-aware renderers may use their own storage
semantics. `WithFilterCollector` records `filter_<name>_translate_duration_seconds` for every `Translate`, `TranslateInline` and `ValidateAST`, where
the name comes from a dialect implementing `NamedFilterDialect` (`postgres`, `mariadb`, `clickhouse`) and is `sql` otherwise.

Optional dialect extensions — `ArrayDialect`, `EmptyArrayDialect`, `DecimalDialect`, `TimestampDialect`, `SubstringDialect`,
`QualifiedNamesDialect` — add array element predicates, non-nullable array columns, exact decimal operands, exact bound timestamps, `substring()`
and the qualifier rule for dotted names. A dialect without one rejects the operation with the shared sentinel (`filter.ErrUnsupportedOperation`, or
`filter.ErrUnsupportedType` at construction for decimal declarations) instead of rendering scalar SQL.

### Why it exists

The three SQL translators differ in five core places, plus the optional extensions (arrays, decimals, `substring()`):

| Difference         | ClickHouse                                      | MariaDB                                              | PostgreSQL                               |
|--------------------|-------------------------------------------------|------------------------------------------------------|------------------------------------------|
| Identifier quoting | `` `address.city` ``                            | `` `address`.`city` ``                               | `"address"."city"`                       |
| Bind marker        | `?`                                             | `?`                                                  | `$1`, `$2`, …                            |
| String predicates  | `position` / `startsWith`                       | `LOCATE` / `RIGHT` / `REGEXP`                        | `strpos` / `starts_with` / `right` / `~` |
| Inline literals    | `\`-escaped, bytes >= 0x80 as `\xNN`, `unhex()` | `''`-doubled; `_utf8mb4 X'…'` when not ASCII; `X'…'` | `E'…'` with `\uXXXX`, `decode(…, 'hex')` |
| `size()`           | `lengthUTF8` / array `length`                   | `CHAR_LENGTH`                                        | `length`                                 |
| Array elements     | `has` / `arrayExists`                           | `JSON_CONTAINS` / `JSON_TABLE`                       | `= ANY` / `unnest`                       |
| Decimal operands   | `toDecimal128(?, scale)`                        | `CAST(? AS DECIMAL(p,s))`                            | `CAST($n AS NUMERIC)`                    |
| `substring()`      | `substringUTF8`                                 | `SUBSTRING`                                          | `substr`                                 |

Everything else is identical: the traversal, the allow-list and field-type checks, depth accounting, argument collection, IN lists, null handling and
the comparison operators. That part lives here; the differences are supplied by a `FilterDialect` and its optional extensions.

### Shared semantics

The walker applies the cross-backend decisions of the filter conformance suite (`filter/filtertest`) the same way for every dialect:

| Filter                                 | SQL                                    | Why                                                                                 |
|----------------------------------------|----------------------------------------|-------------------------------------------------------------------------------------|
| `x != v`                               | `(x != v OR x IS NULL)`                | The evaluator holds `!=` for a NULL or absent `x`; plain `!=` is unknown there      |
| `!(x == v)`                            | `(x != v OR x IS NULL)`                | `!` is pushed down to the predicates (see below)                                    |
| `!(x in [a, b])`                       | `(x NOT IN (a, b) OR x IS NULL)`       | A NULL `x` is in no list without a null element                                     |
| `!x.contains(s)` (any string test)     | `(NOT (…) OR x IS NULL)`               | A string function finds nothing in a NULL target                                    |
| `!(x > v)`, `!x` (bare bool)           | `NOT (x > v)`, `NOT (x = TRUE)`        | Unknown for a NULL operand in the evaluator too, so NULL stays unmatched            |
| `!(a && b)`, `!(a \|\| b)`             | `(!a) OR (!b)`, `(!a) AND (!b)`        | De Morgan, each side negated by its own predicate                                   |
| `a == b`, `a != b` (two fields)        | null-safe equality (`NullSafeDialect`) | NULL equals NULL and differs from every value, as in the evaluator                  |
| `x in [a, null]`                       | `(x IN (a) OR x IS NULL)`              | `IN (…, NULL)` never holds for a NULL `x`; the evaluator matches null/absent        |
| `x in [null]`                          | `x IS NULL`                            | Same, without an empty `IN ()`                                                      |
| `!(x in [a, null])`                    | `x NOT IN (a)`                         | The negation of the above                                                           |
| `size(x) == null`, `size(x) in [null]` | `1 = 0`                                | `size()` and `substring()` never yield null; `!= null` is `IS NOT NULL`             |
| `name == age` (string vs int kinds)    | `filter.ErrFieldTypeMismatch`          | Field operands of incompatible declared kinds can never compare equal               |
| `age > null`                           | `filter.ErrUnsupportedOperation`       | Only `==` / `!=` have a null form (`IS [NOT] NULL`)                                 |
| `arr != x` (nullable array column)     | `(arr IS NULL OR NOT (…))`             | The evaluator finds no element in an absent array; so does every negated array test |

The kind check covers non-literal operands only: a field's declared kind, `int` for `size()` and `string` for `substring()`; numeric kinds (int,
float, decimal) compare with each other and an undeclared kind is accepted. Literal kinds are checked by `filter.Policy`. A denied field is
rejected before the shape of the operation (`secret > null` fails with `filter.ErrFieldNotAllowed`).

#### NULL and negation

A NULL column is the SQL form of a null or absent field, and the walker gives each predicate the default evaluator's outcome for it. The evaluator
applies SQL's three-valued logic: a predicate is true, false or unknown, its `&&` / `||` absorb unknown as SQL's `AND` / `OR` do, whatever the order of
the operands, its `!` keeps unknown unknown, and an unknown filter does not match ([evaluator](../../filter/evaluator/README.md#three-valued-logic)).
What differs is which predicates are unknown for a null value: the evaluator finds `x == v` false and `x != v` true, where SQL finds both unknown. So
the walker keeps one invariant, predicate by predicate: a rendered predicate is TRUE exactly where the evaluator's predicate is true, and its rendered
negation is TRUE exactly where the evaluator's negation of it is true. Where the evaluator's outcome is false or unknown the rendered one may be either
FALSE or UNKNOWN, since neither matches. SQL's `AND` / `OR` are TRUE exactly where the evaluator's `&&` / `||` are true once their operands are, so the
whole filter matches exactly where the evaluator returns true. A positive equality against a non-null literal holds the invariant as rendered (`x == v`
is unknown in SQL and false in the evaluator for a NULL `x`, unmatched either way); `x != v` gets the `IS NULL` disjunct.

SQL `NOT` around a rendered predicate would break the invariant for the negation, because it keeps unknown unknown while the evaluator's `!` turns false
into true: `NOT (x = v)` is unknown for a NULL `x`, where `!(x == v)` is true. So `!` is never rendered around a clause: it is pushed down through `&&`
and `||` (De Morgan) to each predicate, which renders its own negation: `!(x == v)` becomes `x != v`, `!(x in [...])` a `NOT IN` with the `IS NULL`
disjunct, a negated string function or array test holds for a NULL target, and `has()` flips to `IS NULL`. Where the evaluator finds a NULL operand
unknown — an ordered comparison, a bare boolean, `size()` and `substring()` of a NULL field, a NULL field argument of a string function — the negation
stays `NOT (…)`, which keeps it unknown, as the evaluator's `!` does. Such a predicate is FALSE in SQL exactly where the evaluator's is false, so its
`NOT (…)` is TRUE exactly where the evaluator's negation is.

Two field operands compare with the dialect's null-safe equality: PostgreSQL `IS [NOT] DISTINCT FROM`, MariaDB `<=>`, and for a dialect without
`NullSafeDialect` (ClickHouse) the portable `COALESCE(a = b, a IS NULL AND b IS NULL)` / `COALESCE(a != b, (a IS NULL) != (b IS NULL))`. A field
compared with `!=` against `size()` or `substring()` of another field matches a NULL field only where the other operand is defined.

An application predicate (`WithFilterPredicateRenderer`) is negated with `NOT (…)`; it should not be unknown where the evaluator's counterpart is
false.

### Key types

| Type / Function                                                                         | Description                                                                                                       |
|-----------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------|
| `FilterTranslator`                                                                      | The shared walker; its `filter.Visitor` methods are unexported (per-call state)                                   |
| `NewFilterTranslator(policy, dialect, opts ...FilterOption) (*FilterTranslator, error)` | Constructor over a bound policy; `WithFilterLiteralEncoder`, `WithFilterPredicateRenderer`, `WithFilterCollector` |
| `FilterDialect`                                                                         | The five per-backend rendering hooks                                                                              |
| `ValueFunc`                                                                             | Renders a value in the active mode; handed to the dialect so it can bind more than once                           |
| `MatchAll` / `MatchNone`                                                                | `1 = 1` / `1 = 0`                                                                                                 |
| `StringPredicates`, `RenderStringPredicate`                                             | A dialect's string-predicate templates and their renderer                                                         |

### Adding a dialect

A public translator package declares a stateless dialect and wraps the walker:

```go
type FilterTranslator struct {
    base *sqlbase.FilterTranslator
}

func (dialect) Name() string { return "mydb" } // metrics: filter_mydb_translate_duration_seconds

func NewFilterTranslator(policy *filter.Policy, meter metrics.Meter, opts ...filter.TranslatorOption) (*FilterTranslator, error) {
    bound, err := filter.NewTranslatorContext(policy, opts...) // storage names of this backend
    if err != nil {
        return nil, err
    }
    base, err := sqlbase.NewFilterTranslator(bound, dialect{}, sqlbase.WithFilterCollector(meter))
    if err != nil {
        return nil, err
    }
    return &FilterTranslator{base: base}, nil
}

func (t *FilterTranslator) Translate(n filter.Node) (string, []any, error) { return t.base.Translate(n) }
func (t *FilterTranslator) TranslateInline(n filter.Node) (string, error)  { return t.base.TranslateInline(n) }
```

`Translate` and `TranslateInline` forward to the walker, which also records the metrics, so the public packages carry no translation logic of their
own — only their dialect and its documentation. Keep the walker in an unexported field, as the PostgreSQL, MariaDB and ClickHouse translators do:
embedding it would export its field and its options. The walker's `filter.Visitor` methods are unexported, because they operate on the state of one
`Translate` run; calling them on a shared translator would race with concurrent translations and skip the field and limit checks of a run. A dialect
outside this repository is built the same way; [sqltype](sqltype/) types a schema-aware filter and [sqlbasetest](sqlbasetest/) fakes the database
for its list engine tests (see [Dialect kit](#dialect-kit)).

### Identifiers

| Function                              | Produces               | Used by             |
|---------------------------------------|------------------------|---------------------|
| `ValidateIdent(name)`                 | error or nil           | both quoters        |
| `QuoteWhole(name, quote, maxLen)`     | `` `address.city` ``   | ClickHouse          |
| `QuoteQualified(name, quote, maxLen)` | `` `address`.`city` `` | MariaDB, PostgreSQL |

`ValidateIdent` accepts only `[A-Za-z_][A-Za-z0-9_]*` segments joined by dots. That is stricter than quoting requires, on purpose: the column position
is the one part of a generated clause that no placeholder can cover, so a name that is not a plain identifier is rejected outright rather than escaped.
Because a dialect's quote character cannot survive the check, the quoters need no escaping at all.

The dot policy is the dialect's to choose. `QuoteQualified` produces the standard SQL `"table"."column"`; `QuoteWhole` produces the single quoted name
ClickHouse needs, where `address.city` is one Nested column rather than two identifiers.

Both enforce the backend's identifier length (`PostgresMaxIdentLen` 63 bytes, `MariaDBMaxIdentLen` 64, `ClickHouseMaxIdentLen` 255; 0 means no
limit) — per part for `QuoteQualified`, on the whole name for `QuoteWhole` — and `QuoteQualified` accepts at most two parts (`table.column`, never
`schema.table.column`). Both fail with `filter.ErrInvalidExpression`: PostgreSQL silently truncates a longer identifier, so `aaa…(63)_shadow` would
otherwise read the column `aaa…(63)`.

A dialect whose dot is a qualifier implements `QualifiedNamesDialect` (MariaDB and PostgreSQL). The walker then refuses, with
`filter.ErrFieldNotAllowed`, a dotted field the policy does not declare (`filter.Policy.DeclaresField`: a `WithAllowedFields` entry, a
`WithFieldTypes` declaration or a `WithFieldMapping` key): a name a client composed under a `WithFieldAccess` wildcard or `WithAnyField` could
otherwise reach a column of another joined table.

### The `ValueFunc` hook

`FilterDialect.StringPredicate` receives the needle raw plus a `ValueFunc`, rather than a pre-rendered argument, because `endsWith` has no native form
in MariaDB or PostgreSQL. It compiles to `right(col, length(x)) = x`, where the operand appears twice and must be bound twice — three times in MariaDB,
which also guards the length so PAD SPACE collations stay exact (`StringPredicates.EndsWithBinds` declares the count). Calling `value` once per
occurrence keeps the argument slice aligned with the emitted text — which matters most for PostgreSQL, where the placeholders are numbered.

### Extension contracts

| Interface / option            | Purpose                                                                                              |
|-------------------------------|------------------------------------------------------------------------------------------------------|
| `FilterDialect`               | Identifier quoting, placeholders, inline literals, size and string predicates                        |
| `ArrayDialect`                | Element predicates over fields declared by `filter.WithArrayFields`                                  |
| `EmptyArrayDialect`           | Array columns that cannot hold NULL (absent stored empty): null tests become emptiness tests         |
| `DecimalDialect`              | Exact rendering of operands of `FieldKindDecimal` fields                                             |
| `TimestampDialect`            | Bound timestamp operands for a driver whose `time.Time` binding loses precision (ClickHouse)         |
| `SubstringDialect`            | `substring()` in comparison-operand position                                                         |
| `QualifiedNamesDialect`       | A dot in a column name is a table qualifier: dotted fields must be declared by the policy            |
| `NamedFilterDialect`          | Names the dialect in the metrics of `WithFilterCollector` (`sql` without it)                         |
| `NullSafeDialect`             | Native null-safe comparison of two field operands (`IS NOT DISTINCT FROM`, `<=>`)                    |
| `WithFilterLiteralEncoder`    | Application encoding of every literal (bind and inline); takes over decimal operands                 |
| `WithFilterCollector`         | Records the translation duration as `filter_<name>_translate_duration_seconds`                       |
| `WithFilterPredicateRenderer` | Application predicates; receives a `ValueFunc` for safe binding; runs after authorization and limits |

#### Arrays

The walker normalizes every comparison on an array field to "element op literal" before it reaches `ArrayDialect.ArrayCompare`: a literal on the left
moves right with the `Converse` operator, `x in arr` becomes `arr == x`, `arr in [a, b]` an OR of equalities (`arr in []` is `MatchNone`), and
`arr != x` holds when no element equals x or the column is NULL: `(arr IS NULL OR NOT (arr == x))`, like the evaluator, which holds `!=` for an
absent array. `size(arr)` uses `ArraySize` (a NULL array has size 0); string predicates use `ArrayStringPredicate` (any element matches).
`arr == null` / `!= null` and `has(arr)` test the column itself (`IS [NOT] NULL`). Field operands on either side of an array comparison, a field
argument to an array string predicate and a bare array identifier used as a condition fail with `filter.ErrUnsupportedOperation`. Every other
comparison on a NULL array is false, and its negation true (`(arr IS NULL OR NOT (…))`), as every element test under `!`; under `!` the OR of
`arr in [a, b]` becomes an AND of negated equalities.

A dialect whose array columns cannot hold NULL (ClickHouse `Array`) implements `EmptyArrayDialect`: an absent array is stored as an empty one, so
`arr == null` renders `ArrayEmpty(col, true)` (`empty(col)`), `arr != null` and `has(arr)` render `ArrayEmpty(col, false)` (`notEmpty(col)`), and
`arr != x` needs no NULL guard. Such a backend cannot tell an empty array from an absent one; both match `arr == null`.

#### Decimals

For declared decimal fields, comparison, membership and array-element operands are converted through `Policy.DecimalOperand` (preserving the source
spelling) and rendered by `DecimalDialect.DecimalValue`, or handed to the `WithFilterLiteralEncoder` callback as `decimal.Decimal` when one is
configured. Without either, `NewFilterTranslator` rejects decimal declarations with `filter.ErrUnsupportedType`.

#### substring()

`field.substring(start[, end])` is accepted as a comparison or `in` operand with int literal bounds (0 <= start <= end; otherwise
`filter.ErrInvalidExpression`; non-literal bounds `filter.ErrUnsupportedOperation`). The evaluator finds a substring with an `end` beyond the
string unknown; SQL would compare a truncated value, so the walker guards the comparison with `size(field) >= bound`: such rows never match, also
under negation. `SubstringBounds` reads and checks the bounds and returns the start, the length and that guard; the schema translators of
`backend/postgres`, `backend/mariadb` and `backend/clickhouse` share it and render the substring as a value that is NULL where it does not exist.
Any int64 bound is accepted, as the evaluator accepts it; the bounds are ordered before `end - start` is computed, so no input overflows.
`SubstringStart(start, limit)` returns the one-based start capped at the largest position the server's function takes (PostgreSQL's int4
`substr()`, `math.MaxInt64` elsewhere): only a start whose guard can never hold reaches the cap.

Regex validation uses `Policy.CompileRegex`, including injected compilers and validators. The dialect owns backend syntax support.

### Named values

| Constant    | Meaning |
|-------------|---------|
| `MatchAll`  | "1 = 1" |
| `MatchNone` | "1 = 0" |

### API

| Symbol               | Purpose                                                                                                                       |
|----------------------|-------------------------------------------------------------------------------------------------------------------------------|
| `PredicateRenderer`  | Application predicate callback; field authorization and limits are checked before it runs.                                    |
| `ArrayDialect`       | Optional array element predicates.                                                                                            |
| `EmptyArrayDialect`  | Optional marker for non-nullable array columns; renders the emptiness tests.                                                  |
| `DecimalDialect`     | Optional exact decimal operands.                                                                                              |
| `TimestampDialect`   | Optional bound timestamp operands (the inline form keeps `FormatLiteral`).                                                    |
| `SubstringDialect`   | Optional substring() operands.                                                                                                |
| `SubstringBounds`    | The checked int literal bounds of substring(): start, length (-1 to the end) and the length guard.                            |
| `ComparisonOperator` | SQL spelling of the six comparison operators.                                                                                 |
| `Converse`           | The operator that keeps a comparison's meaning when its operands swap sides.                                                  |
| `NamedFilterDialect` | Optional metric label of a filter dialect.                                                                                    |
| `FilterOption`       | Functional option of `NewFilterTranslator`: `WithFilterLiteralEncoder`, `WithFilterPredicateRenderer`, `WithFilterCollector`. |

## SQL field schema

The schema describes immutable SQL field metadata independently of database drivers and dialects.

### Contract

`NewSchema` captures public names mapped to immutable `Field` descriptions. `Scalar`, `PresenceScalar`, `Array`, and `JSONPath` describe storage without
selecting a database, driver, evaluator, or SQL renderer. SQL kinds include text, integers, floating point, numeric, boolean, timestamps, bytes, UUID,
and JSON.

`WithSchemaColumnValidator` replaces identifier validation; `WithSchemaKindValidator` replaces logical-to-storage-kind compatibility. Consumers remain
responsible for safe quoting, value normalization, and matching storage semantics. PostgreSQL evaluation and MariaDB translation consume this same
neutral schema.

### Schema

`sqlbase` owns driver-free SQL field descriptions shared by PostgreSQL semantics, MariaDB translation, and SQL listing engines.
It contains no SQL rendering or database-specific execution behavior.

Create immutable `Field` values with `Scalar`, `PresenceScalar`, `Array`, or `JSONPath`, then pass the field map to `NewSchema`. The constructor
snapshots the map and JSON paths, validates qualified column identifiers and kinds, and rejects invalid path components. `Field` looks up the storage
metadata of a field (`Column`, `Kind`, `IsArray`, `HasPresence`, `IsJSONPath`, `Path`); `ValidatePolicy` rejects conflicting field mappings, kinds, and
array declarations. Field accessors expose the column, kind, category flags, and a copy of the JSON path.

Kinds are `Boolean`, `Integer`, `Float`, `Text`, `Bytes`, `Timestamp`, `Numeric`, `JSON`, and `UUID`. `Unknown` is invalid in a schema. Categories
retain explicit array, presence, and JSON path flags; constructors prevent incompatible combinations. Qualified columns such as `account.name` are
accepted, but expressions and prequoted identifiers are rejected. A JSON path describes storage only; PostgreSQL supports its interpretation, while the
MariaDB contract rejects JSON paths, JSON, and UUID fields.

Each SQL backend binds a neutral schema to its storage settings with `<backend>.NewSchema(fields, options...)` (`postgres`, `mariadb`, `clickhouse`):
the contract mode (`WithSchemaContractMode`, required by MariaDB and ClickHouse), the literal budget (`WithSchemaMaxDataBudget`), the timestamp
precision and the backend's collation, array alias, decimal or JSONB settings live there, and the backend's `NewFilterTranslatorWithSchema` takes that
schema with only the shared filter options.

### Identifier policy

The SQL listing engine supplies its dialect's validator. A custom validator must be non-nil; the dialect must safely quote every
accepted identifier. Default validation continues to reject expressions and prequoted names.

`WithSchemaKindValidator` validates logical field kinds against SQL storage kinds. Listing passes its configured kind resolver through this
validation. `WithSchemaColumnValidator` can admit custom column names; consuming renderers quote and escape every qualified segment.

### Options

| Option                      | Type           | Default                     | Behavior                                                                                                                   |
|-----------------------------|----------------|-----------------------------|----------------------------------------------------------------------------------------------------------------------------|
| `WithSchemaColumnValidator` | `SchemaOption` | SQL identifier validator    | WithSchemaColumnValidator replaces SQL column-name validation. The consuming dialect must quote every accepted identifier. |
| `WithSchemaKindValidator`   | `SchemaOption` | built-in kind compatibility | WithSchemaKindValidator validates logical field kinds against SQL storage kinds. It must be concurrency safe.              |

### Named values

| Constant    | Meaning                                 |
|-------------|-----------------------------------------|
| `Unknown`   | iota                                    |
| `Boolean`   | Next value in the declared enumeration. |
| `Integer`   | Next value in the declared enumeration. |
| `Float`     | Next value in the declared enumeration. |
| `Text`      | Next value in the declared enumeration. |
| `Bytes`     | Next value in the declared enumeration. |
| `Timestamp` | Next value in the declared enumeration. |
| `Numeric`   | Next value in the declared enumeration. |
| `JSON`      | Next value in the declared enumeration. |
| `UUID`      | Next value in the declared enumeration. |

### API

| Symbol           | Purpose                                                                                                                                               |
|------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------|
| `SchemaOption`   | SchemaOption configures NewSchema.                                                                                                                    |
| `Kind`           | Kind identifies a SQL value class. Numeric is independent of BSON Decimal128.                                                                         |
| `Field`          | Field describes a SQL column or an explicitly typed JSON path. Use the constructors; its zero value is invalid.                                       |
| `Scalar`         | Scalar describes a scalar column.                                                                                                                     |
| `PresenceScalar` | PresenceScalar describes a nullable scalar column with explicit presence.                                                                             |
| `Array`          | Array describes an array column.                                                                                                                      |
| `JSONPath`       | JSONPath describes a typed value within a JSON column. Interpretation of path components belongs to the consuming dialect.                            |
| `Schema`         | Schema is an immutable map from CEL field names to SQL storage descriptors. The same schema must be supplied to translation and in-memory evaluation. |
| `NewSchema`      | NewSchema validates and freezes fields. Column names are identifiers, never SQL expressions.                                                          |

## Value decoder

Converts SQL nullable wrappers and `driver.Valuer` values for [PostgreSQL evaluation](../postgres/README.md). The adapter uses the driver's public value
contract and does not add a `pgx` dependency.

### Contract

Decoder handles database/sql nullable values and driver.Valuer without importing a concrete driver. Pass it with evaluator.WithValueDecoder and a
compiled SQL profile. It adapts representation; the profile owns SQL comparison and null semantics.

### Key types

| Type      | Purpose                                                                   |
|-----------|---------------------------------------------------------------------------|
| `Decoder` | Stateless `Decode(any) (any, error)` adapter; zero value is ready for use |

### Usage

```go
policy, err := filter.NewPolicy(filter.WithAllowedFields("amount"))
if err != nil { return err }
eval, err := evaluator.New(policy,
    evaluator.WithProfile(schema),
    evaluator.WithValueDecoder(sqlbase.Decoder{}),
)
```

Invalid `sql.Null*` / `sql.Null[T]` values and nil pointers become SQL NULL. Valid wrappers and `pgtype.Numeric` / `pgtype.UUID` use `Value()`;
conversion errors propagate. Native values are preserved. Custom valuers are application code and must be concurrency-safe and bounded. Driver arrays
must be decoded into Go arrays/slices with their dimensional information preserved; this adapter does not reflect private driver structs.

## Order translator

`OrderTranslator` translates an [`orderby.Spec`](../../orderby/README.md) into the body of a SQL `ORDER BY` clause for PostgreSQL, MariaDB/MySQL or
ClickHouse.

### Contract

A `OrderDialect` describes one backend: identifier quoting and limits, and whether null placement and collation can be expressed.
`NewOrderTranslator(policy, dialect, opts...)` binds the resource's shared `orderby.Policy` (allow-list, field access, denied API fields, default order)
to the storage options passed through `WithOrderOptions` (mapping, KeyResolver, denied storage fields, tie-breakers); translation validates every order
with the policy before mapping it. `WithOrderCollector` records the translation duration as the histogram
`orderby_<dialect>_translate_duration_seconds`, named after `OrderDialect.Name` (`postgres`, `mariadb`, `clickhouse`; `sql` when empty). The three
option layers are described in the [root README](../../README.md#parser--policy--translator). Translator instances support concurrent use. No SQL
keywords are prepended to the returned body.

An attribute the dialect cannot express — a collation name the dialect cannot accept, an unknown null placement — is rejected with
`ErrUnsupportedOrdering`, never dropped. Collation text originates in the request and is validated before it reaches SQL.

`TranslateOrder(context, resolver, spec)` renders through an application `ColumnResolver` for trusted storage expressions. Never return raw client
text as a SQL expression; resolvers must be safe for concurrent calls. Null placement and collation need the resolver to implement
`KeyRenderer`; embedding an `OrderDialect` provides the dialect's rendering.

### Dialects

| Dialect                     | Quoting     | Identifiers                                      | `NULLS FIRST/LAST` | Collation                                                                               |
|-----------------------------|-------------|--------------------------------------------------|--------------------|-----------------------------------------------------------------------------------------|
| `postgres.OrderDialect()`   | `"col"`     | `col` or `table.col`, each part at most 63 bytes | Yes                | `col COLLATE "name" DIR`; `name` or `schema.name` identifier                            |
| `mariadb.OrderDialect()`    | `` `col` `` | `col` or `table.col`, each part at most 64 bytes | Emulated (below)   | `col COLLATE name DIR`; unquoted name `[A-Za-z][A-Za-z0-9_]*`, at most 64 bytes         |
| `clickhouse.OrderDialect()` | `` `col` `` | Any number of parts, each at most 255 bytes      | Yes                | `col DIR [NULLS …] COLLATE 'locale'`; locale `[A-Za-z][A-Za-z0-9_-]*`, at most 64 bytes |

Every part is a plain identifier (`[A-Za-z_][A-Za-z0-9_]*`) quoted on its own. Anything else after mapping — an expression, a JSON path, a Map
subscript, a quote — is `ErrInvalidFieldPath`: a sort column cannot be bound as a parameter. Map a nested field onto a generated
(`MATERIALIZED`/`ALIAS` on ClickHouse) column to sort by it, and add a unique column last for a deterministic order.

PostgreSQL, MariaDB and ClickHouse may read the dot as a table qualifier, so a dotted client key must be declared — an exact `orderby.WithAllowedFields`
entry or an exact `orderby.WithFieldMapping` entry (`TranslatorContext.DeclaresField`). A key only a wildcard (`"t.*"`), an open policy or a
`WithFieldPrefixMapping` rule admits fails with `orderby.ErrFieldNotAllowed`: it could otherwise name a column of another joined table. The default
order and tie-breakers are server configuration and are not checked; an application resolver qualifies only when it implements `QualifiedNamesDialect`.

Without `NULLS FIRST/LAST`, NULL placement is the backend default: PostgreSQL sorts NULL as larger than any value (last in `ASC`, first in
`DESC`), MariaDB and MySQL as smaller (first in `ASC`), ClickHouse last in both directions, after NaN.

MariaDB and MySQL have no `NULLS FIRST/LAST`. A placement equal to their native order (`ASC NULLS FIRST`, `DESC NULLS LAST`) renders as the
plain key; the opposite placement adds a leading `col IS NULL` key, which is 0 for a value and 1 for NULL:

| Key                    | MariaDB / MySQL                      |
|------------------------|--------------------------------------|
| `col asc nulls first`  | `` `col` ASC ``                      |
| `col asc nulls last`   | `` `col` IS NULL ASC, `col` ASC ``   |
| `col desc nulls first` | `` `col` IS NULL DESC, `col` DESC `` |
| `col desc nulls last`  | `` `col` DESC ``                     |

The extra key does not use an index on `col`; serve an emulated placement from a generated column when the list must stay index-ordered.

### Usage

```go
policy, err := orderby.NewPolicy(orderby.WithAllowedFields("createdAt", "slug"), orderby.WithUntrustedInput(true))
trans, err := sqlbase.NewOrderTranslator(policy, clickhouse.OrderDialect(),
	sqlbase.WithOrderOptions(orderby.WithFieldMapping(map[string]string{"createdAt": "created_at"})))
clause, err := trans.Translate(ob) // "createdAt desc, slug" → `created_at` DESC, `slug` ASC
if clause != "" {
	query += " ORDER BY " + clause
}
```

### API

| Symbol                                                                     | Purpose                                                                                                               |
|----------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------|
| `OrderDialect`                                                             | Quoting, identifier limits, null placement and collation of one backend; a `ColumnResolver` and `KeyRenderer`         |
| `postgres.OrderDialect`, `mariadb.OrderDialect`, `clickhouse.OrderDialect` | The built-in dialects, owned by the backend packages                                                                  |
| `Identifiers`                                                              | Column-reference grammar of a dialect: quote character, part length and part count                                    |
| `CollationStyle`, `NullPlacement`                                          | How a dialect writes `COLLATE` and an explicit null placement; another database sets them on its own `OrderDialect`   |
| `NewOrderTranslator(policy, dialect, opts...)`                             | Binds the shared `orderby.Policy` to a dialect; options `WithOrderOptions` (storage options) and `WithOrderCollector` |
| `OrderTranslator.Translate(ob)`                                            | Returns `col DIR, …` without the keywords; `""` for an empty spec                                                     |
| `TranslateOrder(bound, resolver, ob)`                                      | Validate with the policy bound by `orderby.NewTranslatorContext` and render through an application resolver           |
| `ColumnResolver.Table`                                                     | Resolve a storage name to quoted SQL or an application-owned expression                                               |
| `KeyRenderer.RenderKey`                                                    | Optional complete key rendering for null placement and collation                                                      |

### Errors

`orderby.ErrFieldNotAllowed` for a key the policy refuses or that resolves to a denied storage path, `orderby.ErrInvalidFieldPath` for a key that does
not map to a column name (and for the zero `OrderDialect`), `orderby.ErrUnsupportedOrdering` for an attribute the dialect cannot express, and
`orderby.ErrPolicyRequired` for a nil policy. `orderby.ErrAllowlistRequired` (`orderby.WithUntrustedInput` without `orderby.WithAllowedFields` or
`orderby.WithFieldAccess`) is raised by `orderby.NewPolicy`, before the translator is built.

## Projection translator

`ProjectionTranslator` translates a [`projection.Spec`](../../projection/README.md) into the column list of a SQL `SELECT` for PostgreSQL, MariaDB/MySQL
or ClickHouse — quoted columns joined by `, `, without the keyword.

### Contract

A `ProjectionDialect` describes one backend's identifier quoting and limits. `NewProjectionTranslator(policy, dialect, opts...)` binds the resource's
shared `projection.Policy` to storage options passed with `WithProjectionOptions` (mapping, denied and required storage fields) and validates the
columns of every view it serves; `ProjectionTranslator.Translate` renders the column list after the policy's authorization and the mapping. Translator
instances support concurrent use. The three option layers are described in the [root README](../../README.md#parser--policy--translator).

`NewProjectionContext(policy, resolver, opts...)` and `TranslateProjection(context, resolver, spec)` render through an application `ColumnResolver`,
which owns identifier quoting and application-owned expressions; it must never return unvalidated request SQL. Resolvers must be safe for concurrent
calls.

Storage columns appear in lexical order, without aliases; scan by column name when that order matters. Empty output means every field and is
rendered as `*` by the caller. SQL cannot represent a `ModeNone` selection (`projection.ErrUnsupportedSelection`; with required fields it renders
them).

### Exclusion

Every authorized field of a policy without an allow-list is "every column but the denied ones":

| Dialect                                                       | Rendering                                                                                                 |
|---------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------|
| `clickhouse.ProjectionDialect()`                              | `` * EXCEPT (`password`, `api_key`) `` — the whole select list, denied storage names in lexical order     |
| `postgres.ProjectionDialect()`, `mariadb.ProjectionDialect()` | none: `NewProjectionTranslator` fails with `projection.ErrAllFieldsUnresolvable`; list the allowed fields |
| application resolver                                          | none (`NewProjectionContext` fails the same way)                                                          |

ClickHouse excludes top-level columns only: `*` never expands a subcolumn, so `* EXCEPT (a.b)` would still return `b` inside `a`. A denied dotted
storage path is therefore rejected at construction with `projection.ErrAllFieldsUnresolvable` (also matching `projection.ErrUnsupportedSelection`).

### Dialects

| Dialect                          | Quoting     | Identifiers                                      |
|----------------------------------|-------------|--------------------------------------------------|
| `postgres.ProjectionDialect()`   | `"col"`     | `col` or `table.col`, each part at most 63 bytes |
| `mariadb.ProjectionDialect()`    | `` `col` `` | `col` or `table.col`, each part at most 64 bytes |
| `clickhouse.ProjectionDialect()` | `` `col` `` | Any number of parts, each at most 255 bytes      |

Every part is a plain identifier (`[A-Za-z_][A-Za-z0-9_]*`) quoted on its own; anything else after mapping is `ErrInvalidFieldPath`. On PostgreSQL,
MariaDB and ClickHouse a dotted path of a client read mask must be declared — an exact `projection.WithAllowedFields` or `WithFieldMapping` entry — or
the request fails with `projection.ErrFieldNotAllowed`, as for order keys; views and every authorized field are not checked.

### Usage

```go
policy, err := projection.NewPolicy(
	projection.WithUntrustedInput(true),
	projection.WithAllowedFields("name", "email", "createTime"),
)
trans, err := sqlbase.NewProjectionTranslator(policy, mariadb.ProjectionDialect(),
	sqlbase.WithProjectionOptions(
		projection.WithFieldMapping(map[string]string{"createTime": "created_at"}),
		projection.WithRequiredFields("id"),
	),
)
cols, err := trans.Translate(spec) // "name,createTime" → `created_at`, `id`, `name`
if cols == "" {
	cols = "*"
}
```

### Options

| Option                    | Default | Behavior                                                                                                                                                     |
|---------------------------|---------|--------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `WithProjectionOptions`   | nil     | Storage options: `projection.TranslatorOption` values (mapping, denied and required storage fields)                                                          |
| `WithProjectionCollector` | nil     | Records `projection_<dialect>_translate_duration_seconds` (`ProjectionDialect.Name`: `postgres`, `mariadb`, `clickhouse`; `sql` when empty); nil disables it |

### API

| Symbol                                                                                    | Purpose                                                                                     |
|-------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------|
| `ProjectionDialect`                                                                       | Quoting and identifier limits of one backend; a `ColumnResolver`                            |
| `postgres.ProjectionDialect`, `mariadb.ProjectionDialect`, `clickhouse.ProjectionDialect` | The built-in dialects, owned by the backend packages                                        |
| `NewProjectionTranslator(policy, dialect, opts...)`                                       | Binds the shared policy to a dialect; rejects policies requiring unsupported SQL exclusions |
| `ProjectionOption`, `WithProjectionOptions`, `WithProjectionCollector`                    | Package options of `NewProjectionTranslator`                                                |
| `ProjectionTranslator.Translate(spec)`                                                    | Returns `col, …` without the keyword; `""` for every column; `* EXCEPT (…)` on ClickHouse   |
| `ProjectionTranslator.TranslateView(spec, view)`                                          | Like `Translate`, with the AIP-157 view deciding when the read mask is omitted              |
| `NewProjectionContext(policy, resolver, opts...)`                                         | Binds the shared policy to storage options for an application resolver                      |
| `TranslateProjection(bound, resolver, spec)`                                              | Validate with the bound policy and render through an application resolver                   |
| `TranslateProjectionView(bound, resolver, spec, view)`                                    | `TranslateProjection` with the AIP-157 view                                                 |
| `ColumnResolver.Table`                                                                    | Resolve a storage name to quoted SQL or an application-owned expression                     |

### Errors

`projection.ErrFieldNotAllowed` for a path outside the policy, `projection.ErrInvalidFieldPath` for a path that does not map to a column name (and for
the zero `ProjectionDialect`), `projection.ErrUnsupportedSelection` for a `ModeNone` selection without required fields (and an exclusion a dialect
cannot render), `projection.ErrAllFieldsUnresolvable` when every authorized column — what an omitted read mask returns — would have to exclude denied
columns and the dialect cannot (PostgreSQL, MariaDB, application resolvers; ClickHouse for a denied subcolumn), `projection.ErrInvalidView` for a view
the policy does not serve, and `projection.ErrPolicyRequired` for a nil policy. `projection.ErrAllowlistRequired` (`projection.WithUntrustedInput`
without `projection.WithAllowedFields` or `projection.WithFieldAccess`) is raised by `projection.NewPolicy`, before the translator is built.

## Listing engine

`ListEngine` pages PostgreSQL, MariaDB or ClickHouse tables with keyset cursors and the backend-neutral [`listing`](../../listing/README.md) contract.
The application owns the table and its data; the engine emits parameterized queries and order indexes. The dialect is a required argument of
`NewListEngine`: the built-in dialects are the `ListDialect` types of [`postgres`](../postgres/README.md), [`mariadb`](../mariadb/README.md) and
[`clickhouse`](../clickhouse/README.md), which own their syntax, cursor predicates, storage validation, schema checks and index DDL.

### Contract

`NewListEngine(db, signer, schema, table, projection, scan, dialect, opts...)` creates a parameterized SQL list. The application owns tables, stored
values, and scanning. `List(ctx, subject, request, Scope)` requires every configured scope key; `WithListParentKey` explicitly binds a scope key to
`Request.Parent`. `WithListMaxTime` defaults to ten seconds; zero uses the caller deadline. `WithListErrorMapper` replaces query-failure classification.

The listing profile's `listing.WithServerOnlyFields` option reserves declared fields for server predicates. SQL translates
them in restrictions and cutoffs using the usual storage mapping. The restriction participates in the page-token binding.

For an exact total, call `Count(ctx, req, scope)` after List succeeds, passing the same request and authorized scope. Count applies the filter, default
equalities, server restriction, cutoff and pins to the whole collection, ignores the page token and returns an int64 (zero for no matches). Propagate errors and
check the total fits before assigning an int32 response field. The caller's deadline can bound both statements together. Under READ COMMITTED, an
intervening write can change the total. For agreement, run both calls through the same read-only REPEATABLE READ transaction with the same restriction
and cutoff instant. The cutoff contributes no token binding. Scope or parent indexes may reduce count work, but an exact MVCC count can visit every qualifying entry and consult the heap.

For a total that stays cheap on large collections, `BoundedCount(ctx, req, scope, opts...)` counts at most limit+1 matching rows
(`WithCountLimit`, `DefaultCountLimit` = 1000) and returns a `Total`: exact up to the limit, otherwise the larger of the counted rows and the
estimate of a `ListCountDialect` with `Exact` false (the counted rows alone when the dialect does not estimate). `WithCountProbeTime` bounds
the exact count; when it expires the total is an estimate. It requires a `ListCountDialect`: PostgreSQL runs the probe in a savepoint under a
transaction-local `statement_timeout`, so an expired probe leaves the enclosing transaction usable, and estimates with `EXPLAIN (FORMAT JSON)`.
Like `Count`, it validates the request, ignores the page token and reads the snapshot of its querier.

`WithListTableExpression` reads List, Count and BoundedCount from a trusted derived table instead of the table, such as a join adding a
column of another table: `FROM (<expression>) AS <alias>`. Filters, scope pins and cursors name its columns. `CheckSchema`, `Indexes` and
`EnsureIndexes` keep naming the table; `WithListDerivedColumns` names the columns only the expression has, which `CheckSchema` then leaves
unchecked.

Two PostgreSQL storage choices relax the column contract. `WithListNarrowIntegers` accepts `smallint` and `integer` columns (and their
arrays) for integer fields; filters and cursors still bind 64-bit values, so a literal outside the column's range fails in the database.
`WithListExplicitCollation` makes filters compare text under the expected collation explicitly (`COLLATE`, `WithListTextCollation` or
`"C"`), so filter semantics no longer depend on the column; the collation check then covers only the key columns
(`ListSchemaConfig.KeyColumn`: the identity, the scope columns and the columns of `listing.Schema.SortFields`), whose order the cursors
rely on. An explicit collation can keep a filter from using an index built under another collation. MariaDB and ClickHouse refuse both.

`MustNewListEngine` takes the same arguments for a configuration fixed in code (a package-level `var`, like `listing.MustSchema`): it returns
the engine `NewListEngine` builds and panics on a refused one with an error that starts `sqlbase.MustNewListEngine: ` and wraps the
`NewListEngine` error.

`ListDialect` composes `ListSyntax`, `ListFilterDialect`, `ListSchemaDialect`, and `ListIndexDialect`. The backend dialects implement every method;
sqlbase supplies only database-neutral building blocks (`DecodeCursor`, `ExpandKeysetPredicate`, `CheckListColumns` and the `ListSchemaConfig` helpers).
A dialect that also implements the optional `ListSortKeyDialect` renders the SELECT expression of each non-identity sort key, as `IdentityExpression`
does for the identity (ClickHouse selects decimals with `toString`). Embed a backend dialect to override selected methods or implement a new dialect.
Implementations and translators support concurrent calls. Dialects own identifier validation and quoting, table grammar, limits, placeholders, identity
expressions, cursor predicates, translation, schema checks, and index DDL.

Filter translators return native placeholders starting at parameter 1; their SQL is preserved. Scope and cursor clauses bind separately, and combined
predicates are parenthesized. `WithListTableAlias` defaults to `_listing`. `WithListColumns` maps public fields and scope keys; `WithListIDColumn` only
changes the physical identity column, so public aliases still need mapping.

Required and Optional scalars use NOT NULL columns; Optional stores its zero value. Presence uses nullable columns. A Presence field
sorts only on a dialect implementing `ListNullableSortDialect` (PostgreSQL): NULL orders before every value, first ascending and last
descending, its position key is the zero `pagetoken.SortValue` and the continuation predicate tests it with `IS NULL`. Other dialects refuse
such an order, a declared one at construction. `WithListUUIDFields` stores string fields in PostgreSQL uuid columns, as `IDUUID` does for
the identity: filters cast and validate their operands, cursors bind them as uuid and `CheckSchema` expects uuid columns. MariaDB and
ClickHouse refuse it: their UUID types do not order like the uuid text, so such fields stay in text columns. PostgreSQL arrays are NOT NULL
without null elements, and text uses COLLATE "C". MariaDB text uses utf8mb4 with utf8mb4_nopad_bin, including private scope and JSON array columns.
Indexed text must be CHAR or VARCHAR with full-value indexes. MariaDB integers are signed BIGINT, and IDUUID is rejected. Arrays are disabled by default
on MariaDB; select ArraysJSON for JSON arrays. ClickHouse columns are `String`, `Int64`, `Float64`, `Bool`, `DateTime64(p)` and `Decimal(P, S)`
(`LowCardinality` accepted), `Nullable(T)` only for Presence fields, repeated fields native `Array(T)` without `Nullable` elements (ArraysJSON and
IDUUID are rejected); text compares by bytes, so `WithListTextCollation` is rejected and there is no collation check.

Cursor identities retain their declared scalar kind; the default is string with IDNative storage. PostgreSQL IDUUID supports string identities stored as
UUID. Default timestamp precision is one millisecond; PostgreSQL uses aligned timestamptz(3), MariaDB uses DATETIME(3), ClickHouse uses DateTime64(3),
and JSON timestamp elements use UTC RFC 3339 strings at the configured precision. Floats must be finite. The default decimal domain is DECIMAL(65,30);
filters and cursors use the configured domain.

`WithListPositionCodec` replaces position normalization, ordering, and wire encoding. `WithListKindResolver` maps public kinds to neutral SQL storage
kinds for translation, scan destinations, scope normalization, identity expressions, cursor validation, and schema checks. Custom kinds need matching
dialect semantics; opaque cursor values expose SQL scalars directly or through driver.Valuer. Invalid cursor decimal domains are token errors.

Run `CheckSchema` before deployment; `EnsureIndexes` applies the same checks before DDL. `WithListTextCollation` changes translator and schema
expectations, so stored values and indexes must match the chosen comparison contract. `WithListSchemaCheckPolicy` receives passing and failing named
checks: return result.Err to retain a check, nil to disable it, or a replacement error. Missing columns have no later type/value checks. Query and scan
failures still propagate.

`WithListIndexNamer` supplies deterministic, unique, valid unquoted names for zero-based pattern numbers. The dialect owns DDL execution; PostgreSQL
uses advisory locking by default. ClickHouse renders `ALTER TABLE … ADD INDEX IF NOT EXISTS … (cols) TYPE minmax GRANULARITY 1` data skipping indexes
(directions dropped); the primary index of a MergeTree table is its sorting key, which the application chooses when it creates the table. Changing
ordering, cursor codec, precision, or identity requires new compatible token bindings and storage configuration.

### Key types

| Type      | Meaning                                                                    |
|-----------|----------------------------------------------------------------------------|
| `Querier` | Runs the parameterized SELECT; accepts `*sql.DB` and compatible test fakes |
| `Scope`   | Values pinned to the configured scope columns                              |
| `IDType`  | Selects native scalar or PostgreSQL UUID storage                           |

### Storage contract

Optional scalar columns use their kind's NOT NULL zero value, repeated fields use NOT NULL arrays without NULL elements, and `Presence` fields use
nullable columns. Timestamps are stored in UTC at millisecond precision. PostgreSQL text columns use `COLLATE "C"`; MariaDB text columns use
`utf8mb4 COLLATE utf8mb4_nopad_bin`. MariaDB supports scalar ids (`IDUUID` is rejected) and can store repeated fields as JSON arrays with
`WithListArrayStorage(ArraysJSON)`. `CheckSchema` verifies listing, scope, id, and projection columns before deployment. Float columns must exclude NaN
and both infinities; a PostgreSQL check can use `CHECK (score = score AND score <> 'Infinity'::float8 AND score <> '-Infinity'::float8)` (adapt the
column name and type).

### Usage

```go
list, err := sqlbase.NewListEngine[Widget](db, codec, widgetSchema, "widgets", []string{"id", "name"},
	func(w *Widget) []any { return []any{&w.ID, &w.Name} }, postgres.ListDialect{},
	sqlbase.WithListIDColumn("widget_id"),
	sqlbase.WithListColumns(map[string]string{"id": "widget_id"}),
	sqlbase.WithListScopeKeys("tenant_id"), sqlbase.WithListParentKey("tenant_id"),
)
if err != nil {
	return err
}
page, err := list.List(ctx, "widgets.List", req, sqlbase.Scope{"tenant_id": req.Parent})
```

MariaDB requires `utf8mb4_nopad_bin` on all text types, including scope columns outside the public schema and JSON array columns. Scope and sort text
columns must use `VARCHAR` or `CHAR` so their full values can be indexed; prefix indexes do not preserve this contract. Integer columns must be signed
`BIGINT`. Bytes accept `BINARY`, `VARBINARY`, and all BLOB widths. Timestamp precision checks apply only to scalar `DATETIME` columns. JSON timestamp
elements are RFC 3339 UTC millisecond strings (e.g. `2026-01-02T03:04:05.123Z`). JSON numbers and booleans retain their JSON types; decimal values must
fit `DECIMAL(65,30)`. Decimal filter and keyset operands are explicitly cast to that exact type.

### Scope and failure policy

A single numeric tenant key is valid without a parent binding. Changing scope still invalidates tokens. Existing child lists must pass
`WithListParentKey` explicitly. Constructors accept `pagetoken.Signer`; tokens bind the schema's contract ID. The caller's context ending is its
error and the time cap expiring is `ErrTimeout` (a field error on filter when one was sent, else wrapped with `coreerrs.ErrUnavailable`); both hide
the driver or scan error behind `apierror.Classify`, so errors.Is, errors.As and the error text never reach it and `apierror.HiddenCause` returns it
for logs. A `Scan`, a signer or a position codec failing after the query context ended reports that end alone, whatever it returned: a
FieldError, an `apierror.Error` or a sentinel never selects the status, and `WithListErrorMapper` receives the context's error classified over
the failure. A row that does not scan into the page type, or whose sort values do not normalize, fails with `ErrCorruptRecord` classifying the
scan error through `apierror.Classify`: the failure reads as and matches `ErrCorruptRecord` alone, so an `apierror.Mapper` maps it INTERNAL whatever
the scan error wraps (a destination's token, deadline or field error included), and `apierror.HiddenCause` returns the scan error for logs;
errors.Is no longer reaches it (`context.DeadlineExceeded` from a destination's `Scan` does not match).

### Errors

| Error              | Meaning                                                                                                                                         |
|--------------------|-------------------------------------------------------------------------------------------------------------------------------------------------|
| `ErrScope`         | A scope key is undeclared, not pinned or does not normalize, or the parent key differs from `Request.Parent`                                    |
| `ErrTimeout`       | The `WithListMaxTime` cap or a driver timeout expired: a field error on `filter` when one was sent, else wrapped with `coreerrs.ErrUnavailable` |
| `ErrCorruptRecord` | A stored row does not scan into the page type or its sort values do not normalize (a server-side fault)                                         |
| `ErrInvalidSchema` | `listing.ErrInvalidSchema`, re-exported: an invalid engine configuration, or a request order the engine cannot serve                            |

### Cursor configuration

Numeric identities are selected and bound as native values, without a text cast. `IDUUID` remains the PostgreSQL UUID storage choice
for string identities. Stored timestamps must align to the configured resolution; `CheckSchema` checks declared column precision.

Changing precision, codec semantics or the tie-breaker invalidates existing tokens. Database indexes and stored values must match the configuration.

### Dialect customization

`ListDialect` composes `ListSyntax`, `ListFilterDialect`, `ListSchemaDialect`, and `ListIndexDialect`. Pass an implementation to `NewListEngine`;
`postgres.ListDialect`, `mariadb.ListDialect` and `clickhouse.ListDialect` are complete implementations to embed. A dialect owns identifier quoting,
table validation, placeholder binding, identity selection, cursor predicates, filter translation, metadata queries, index names, DDL rendering, and DDL
execution. It can embed a built-in implementation to override selected methods. Implementations and filter translators must be safe for concurrent
calls. See `list_dialect_test.go` for a standalone third implementation.

A new dialect builds on the database-neutral helpers:

| Helper                                             | Purpose                                                                                                         |
|----------------------------------------------------|-----------------------------------------------------------------------------------------------------------------|
| `DecodeCursor(keys, pos, check)`                   | Decodes a page-token position into the bind values of the cursor keys; `check` vets each value (decimal domain) |
| `ExpandKeysetPredicate(keys, cols, marks, vals)`   | Nested `(c1 > m1 OR (c1 = m1 AND …))` continuation predicate over quoted columns and typed placeholders         |
| `CheckListColumns(ctx, db, config, got, checker)`  | Shared projection, scope and field column existence checks; `ListColumnChecker` adds the database checks        |
| `ListSchemaConfig.Check`                           | Reports one built-in check through the configured `ListSchemaCheckPolicy`                                       |
| `ListSchemaConfig.Column`, `Kind`, `OrderKeys`     | Mapped column, storage class and cursor keys under the engine's configuration                                   |
| `ListSchemaConfig.AlignsTimestamps`, `FitsDecimal` | Timestamp precision and decimal domain rules of the cursor contract                                             |

Collation configuration changes metadata expectations, not SQL ordering expressions. The application must ensure that its chosen
collation preserves the listing comparison contract. MariaDB's default charset expectation remains `utf8mb4`; the collation check policy
can replace that expectation too. Filter semantics, stored values, cursor ordering, and indexes must agree.

`ListSchemaCheckPolicy` receives the context, database, and a `ListSchemaCheckResult` containing a check identifier, column, and default error.
It runs for passing and failing checks. Return `result.Err` to preserve a check, nil to disable it, or an application error to replace it.
A replacement can query database metadata itself. Missing columns have no subsequent type or value checks. `EnsureIndexes` uses the same
policy before executing DDL. Database query and scan failures are always returned.

| Check identifier          | Default validation                                                                  |
|---------------------------|-------------------------------------------------------------------------------------|
| `CheckColumn`             | Listing, projection, identity, and scope columns exist                              |
| `CheckType`               | Physical column type matches the listing field                                      |
| `CheckNullability`        | Required and scope values are NOT NULL; Presence is nullable                        |
| `CheckCollation`          | Text collation and, for MariaDB, charset match expectations (not run on ClickHouse) |
| `CheckTimestampPrecision` | Stored timestamps align with cursor resolution                                      |
| `CheckDecimalDomain`      | MariaDB and ClickHouse decimal precision and scale fit the configured format        |
| `CheckSignedInteger`      | MariaDB integer columns are signed                                                  |
| `CheckIndexable`          | MariaDB indexed text uses CHAR or VARCHAR                                           |

For example, an application that enforces timestamp alignment during writes can skip only the declared precision check:

```go
sqlbase.WithListSchemaCheckPolicy(func(ctx context.Context, db *sql.DB, result sqlbase.ListSchemaCheckResult) error {
    if result.Check == sqlbase.CheckTimestampPrecision {
        return nil
    }
    return result.Err
})
```

Custom index names must be valid identifiers and unique across the configured sort patterns. The built-in default naming retains its
length-bounded hash behavior. Custom dialects own their DDL execution strategy; PostgreSQL retains advisory locking by default.

`ListSyntax.LimitSelect` owns row limiting. Filter translators return native placeholders starting at parameter 1; their SQL is preserved.
Scope and cursor predicates bind separately, and every combined predicate is parenthesized. Request orders receive storage validation.
`ListTranslatorConfig` carries the text collation, the timestamp precision and the backend-neutral `DecimalFormat`. Timestamp scope pins use the same
precision as cursors.

Custom dialects implement `ListSyntax.ValidIdentifier` for all unquoted column, alias, scope, and index names before quoting. Built-in dialects retain
the strict identifier policy. Opaque cursor scalars may implement `database/sql/driver.Valuer`; the returned scalar must match the declared field kind.
Cursor decimal-domain failures are classified as invalid page tokens, including decimal identities.

### Policy propagation

`WithListTextCollation` is passed to filter translators as well as schema checks. Constructors validate the default order even with unrestricted
ordering. Private scope keys accept the same integer, boolean, float, timestamp and decimal scalar normalization as declared fields; timestamps use the
configured precision.

`WithListKindResolver` maps public field kinds to SQL storage classes before `ListDialect.NewTranslator` runs. Custom kinds still require a dialect
that understands their filter semantics. Custom cursor values must expose a SQL driver value directly or through `driver.Valuer`; a dialect
can replace cursor rendering for other encodings. `WithListIDColumn` only selects the physical identity; map public aliases with `WithListColumns`.

The kind resolver also governs scan destinations, scope normalization, identity expressions, cursor validation, and schema checks.
`ListOrderKey.StorageKind` and `ListSyntax.IdentityExpression` expose the resolved SQL kind to dialects; `ListSchemaConfig.KindResolver` supplies
the same mapping to schema checks.

### Options

| Option                       | Type               | Default                         | Behavior                                                                                |
|------------------------------|--------------------|---------------------------------|-----------------------------------------------------------------------------------------|
| `WithListArrayStorage`       | `ListEngineOption` | ArraysUnsupported               | Select ArraysUnsupported or ArraysJSON for MariaDB repeated fields.                     |
| `WithListColumns`            | `ListEngineOption` | empty map                       | Map public field and scope names to physical SQL columns.                               |
| `WithListDecimalFormat`      | `ListEngineOption` | DefaultDecimalFormat()          | Set the exact decimal precision and scale used by filters and cursors.                  |
| `WithListDerivedColumns`     | `ListEngineOption` | nil / unset                     | Columns only the table expression has; CheckSchema leaves them unchecked.               |
| `WithListErrorMapper`        | `ListEngineOption` | nil / unset                     | Overrides query failure classification; see Scope and failure policy.                   |
| `WithListExplicitCollation`  | `ListEngineOption` | off                             | Filters collate text explicitly; only key columns must store the collation.             |
| `WithListIDColumn`           | `ListEngineOption` | DefaultIDColumn                 | Select the physical unique identity column; map public aliases separately.              |
| `WithListIDKind`             | `ListEngineOption` | filter.FieldKindString          | The scalar class of the unique tie-breaker column.                                      |
| `WithListIDType`             | `ListEngineOption` | IDNative                        | Select IDNative or PostgreSQL IDUUID storage.                                           |
| `WithListIndexNamer`         | `ListEngineOption` | nil / unset                     | Returns an unquoted index identifier for a table and zero-based pattern number.         |
| `WithListKindResolver`       | `ListEngineOption` | DefaultSQLKind                  | Maps public kinds to SQL storage classes before the dialect builds its translator.      |
| `WithListMaxTime`            | `ListEngineOption` | DefaultMaxTime                  | Cap one query; zero uses only the caller context deadline.                              |
| `WithListNarrowIntegers`     | `ListEngineOption` | off                             | PostgreSQL smallint and integer columns for integer fields.                             |
| `WithListParentKey`          | `ListEngineOption` | ""                              | Explicitly binds a declared scope key to Request.Parent. Empty disables parent binding. |
| `WithListPositionCodec`      | `ListEngineOption` | pagetoken.ScalarPositionCodec{} | Replace position normalization, comparison, and wire encoding together.                 |
| `WithListSchemaCheckPolicy`  | `ListEngineOption` | nil / unset                     | Replaces individual built-in schema checks; nil preserves strict defaults.              |
| `WithListScopeKeys`          | `ListEngineOption` | nil / unset                     | Require these equality scope keys on every query and bind them into tokens.             |
| `WithListTableAlias`         | `ListEngineOption` | DefaultTableAlias               | Qualifies sort and cursor columns.                                                      |
| `WithListTextCollation`      | `ListEngineOption` | ""                              | Overrides the dialect's expected text collation; empty uses its default.                |
| `WithListTableExpression`    | `ListEngineOption` | ""                              | A trusted derived table List and Count read instead of the table, such as a join.       |
| `WithListTimestampPrecision` | `ListEngineOption` | listing.TimestampPrecision      | The cursor timestamp resolution; stored timestamps must be aligned.                     |
| `WithListUUIDFields`         | `ListEngineOption` | nil / unset                     | String fields stored in PostgreSQL uuid columns.                                        |
| `WithCountLimit`             | `CountOption`      | DefaultCountLimit               | The largest total BoundedCount reports exactly.                                         |
| `WithCountProbeTime`         | `CountOption`      | 0                               | Bounds the exact count of BoundedCount; requires a ListCountDialect.                    |

### Defaults and limits

| Constant            | Value              |
|---------------------|--------------------|
| `DefaultCountLimit` | `1000`             |
| `DefaultIDColumn`   | `"id"`             |
| `DefaultMaxTime`    | `10 * time.Second` |
| `DefaultTableAlias` | `"_listing"`       |

### Named values

| Constant                  | Meaning                                                                             |
|---------------------------|-------------------------------------------------------------------------------------|
| `CheckColumn`             | CheckColumn requires configured columns to exist.                                   |
| `CheckType`               | CheckType compares physical and declared field types.                               |
| `CheckNullability`        | CheckNullability checks required, scope, and Presence storage.                      |
| `CheckCollation`          | CheckCollation checks text ordering and charset expectations.                       |
| `CheckTimestampPrecision` | CheckTimestampPrecision checks declared timestamp resolution.                       |
| `CheckDecimalDomain`      | CheckDecimalDomain checks MariaDB and ClickHouse decimal precision and scale.       |
| `CheckSignedInteger`      | CheckSignedInteger requires signed MariaDB integers.                                |
| `CheckIndexable`          | CheckIndexable requires fully indexable MariaDB text types.                         |
| `ArraysUnsupported`       | ArraysUnsupported rejects schemas containing array fields.                          |
| `ArraysJSON`              | ArraysJSON stores array values in JSON columns.                                     |
| `IDNative`                | IDNative uses native scalar storage selected by WithListIDKind (string by default). |
| `IDUUID`                  | IDUUID stores canonical UUID ids in a uuid column.                                  |

### API

| Symbol                    | Purpose                                                                                                                                                                         |
|---------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `ListDialect`             | ListDialect supplies SQL syntax, filter translation, schema checks, and index management. Implementations and their translators must support concurrent calls.                  |
| `ListSyntax`              | ListSyntax renders identifiers and cursor expressions. CursorPredicate returns question-mark placeholders; Bind replaces unquoted question marks starting at start.             |
| `ListFilterTranslator`    | ListFilterTranslator translates a validated filter to SQL with native bound placeholders starting at parameter 1 and ordered arguments. The engine preserves this SQL verbatim. |
| `ListFilterDialect`       | ListFilterDialect validates storage choices and builds a concurrent filter translator.                                                                                          |
| `ListSchemaDialect`       | ListSchemaDialect owns metadata queries and storage validation.                                                                                                                 |
| `ListIndexDialect`        | ListIndexDialect names, renders, and executes index DDL. Names are unquoted identifiers; IndexDDL receives a quoted table, quoted name, and quoted column/direction fragments.  |
| `ListStorageConfig`       | ListStorageConfig describes representation choices passed to a dialect.                                                                                                         |
| `ListSchemaConfig`        | ListSchemaConfig is an isolated snapshot of the engine's storage expectations. Schema is immutable.                                                                             |
| `IndexNamer`              | IndexNamer returns an unquoted index name for a table and zero-based sort pattern. It must be deterministic; duplicate or invalid names fail construction.                      |
| `ListSchemaCheck`         | ListSchemaCheck identifies an independently replaceable storage check.                                                                                                          |
| `ListSchemaCheckResult`   | ListSchemaCheckResult describes a built-in check. Err is nil when the default check passes.                                                                                     |
| `ListSchemaCheckPolicy`   | ListSchemaCheckPolicy replaces a check result. It must be safe for concurrent use.                                                                                              |
| `CursorValueCheck`        | Vets one decoded cursor value before a dialect binds it; an error makes the token invalid.                                                                                      |
| `DecodeCursor`            | Decodes a page-token position into the bind values of the cursor keys, ending with the identity.                                                                                |
| `ExpandKeysetPredicate`   | Renders the nested lexicographic continuation predicate of a dialect without row-value comparison.                                                                              |
| `ListColumnChecker`       | Database-specific scope and field column checks of `CheckListColumns`.                                                                                                          |
| `CheckListColumns`        | Runs the projection, scope and field column checks every SQL dialect shares and returns the existing scope columns.                                                             |
| `ListSortKeyDialect`      | Optional dialect extension rendering the SELECT expression of a non-identity sort key.                                                                                          |
| `ListNullableSortDialect` | Optional dialect extension that sorts on Presence fields, NULL first ascending and last descending.                                                                             |
| `ListCountDialect`        | Optional dialect extension that bounds the exact count of BoundedCount and estimates larger totals.                                                                             |
| `Total`                   | The size of a collection from BoundedCount: exact, or a lower bound or estimate when Exact is false.                                                                            |
| `CountOption`             | Functional option of BoundedCount; every option is named WithCount….                                                                                                            |
| `DecimalFormat`           | The exact DECIMAL(Precision,Scale) domain of stored decimals, shared with the MariaDB filter translator; `DefaultDecimalFormat` is DECIMAL(65,30).                              |
| `ListTranslatorConfig`    | ListTranslatorConfig carries storage precision to filter translation.                                                                                                           |
| `ArrayStorage`            | ArrayStorage selects how array fields are stored by MariaDB.                                                                                                                    |
| `ListEngineOption`        | Functional option of NewListEngine; every option is named WithList….                                                                                                            |
| `ListEngine`              | ListEngine serves one SQL keyset page at a time. Build a ListEngine once per table and share it between concurrent requests.                                                    |
| `NewListEngine`           | NewListEngine constructs a keyset list over a required ListDialect. dest returns Scan destinations in the same order as columns.                                                |
| `MustNewListEngine`       | MustNewListEngine is NewListEngine for package-level declarations with static configuration: it panics on an invalid configuration.                                             |
| `DefaultSQLKind`          | DefaultSQLKind returns the built-in SQL storage class, or Unknown for a custom kind. A custom WithListKindResolver can delegate its remaining kinds to this function.           |
| `ListOrderKey`            | ListOrderKey describes a physical cursor key, including the final unique identity.                                                                                              |

## Dialect kit

Everything a SQL dialect needs is public, so a dialect for another database can be built outside this repository exactly like the PostgreSQL,
MariaDB and ClickHouse packages: the dialect ports above (`FilterDialect`, `OrderDialect`, `ProjectionDialect`, `ListDialect` and their optional
extensions), the SQL text helpers below, the schema-aware type checker [sqltype](sqltype/) and the test driver [sqlbasetest](sqlbasetest/).

| Symbol                                        | Description                                                                                                                                      |
|-----------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------|
| `Style{Quote, Numbered}`                      | Identifier quote and placeholder form a dialect constructs: `{'"', true}` (PostgreSQL), `` {'`', false} `` (MariaDB, ClickHouse)                 |
| `Style.QuoteIdent(name)`                      | Quotes any identifier, doubling the quote character inside it                                                                                    |
| `Style.Table(name)`                           | Validates `name` or `schema.name` (plain identifiers, each at most `PortableMaxIdentLen` = 63 bytes) and quotes it; else `ErrInvalidTableName`   |
| `Style.IndexName(table, suffix)`              | `<table>_<suffix>_idx`, or a readable prefix plus an FNV-32a hash when longer than 63 bytes                                                      |
| `Style.Bind(query, start)`                    | Rewrites `?` as `$n` from `start` on a numbered style; a positional style returns the query unchanged                                            |
| `Identifiers{Quote, MaxIdentLen, MaxParts}`   | Column-reference grammar of the order and projection dialects; `Column(name)` validates and quotes each dotted part, else `ErrInvalidColumnName` |
| `IsPortableIdent(name)`                       | A plain identifier of at most 63 bytes: the unquoted grammar of the built-in `ListSyntax.ValidIdentifier`                                        |
| `Unqualified(name)`, `Qualifier(name)`        | Table and schema parts of a possibly qualified name                                                                                              |
| `PortableMaxIdentLen`                         | 63, the shorter of the PostgreSQL and MariaDB limits: table, index and listing column names fit every dialect                                    |
| `ErrInvalidTableName`, `ErrInvalidColumnName` | Configuration faults (a storage name the dialect cannot quote); `apierror/aipmap` maps them to INTERNAL                                          |

The built-in identifier grammars are `Identifiers{'"', PostgresMaxIdentLen, 2}` (PostgreSQL: `col` or `table.col`, because PostgreSQL silently
truncates a longer name), `` Identifiers{'`', MariaDBMaxIdentLen, 2} `` (MariaDB: a third part would name the database) and
`` Identifiers{'`', ClickHouseMaxIdentLen, 0} `` (ClickHouse: any number of parts, so Nested and JSON subcolumns are addressed part by part, and a
dotted client key must be declared, see `OrderDialect.Qualified`). The filter quoters (`ValidateIdent`, `QuoteWhole`, `QuoteQualified`, see
[Identifiers](#identifiers)) fail with `filter.ErrInvalidExpression` instead, because a filter field name comes from the request.

| Subpackage                  | Contents                                                                                                                                               |
|-----------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------|
| [sqltype](sqltype/)         | `Checker` and `Dialect`: types a filter against a schema and returns the typed tree a schema-aware translator renders; the exact NUMERIC literal model |
| [sqlbasetest](sqlbasetest/) | `NewFakeDB`: a scripted `database/sql` driver recording statements, arguments and transactions, for list engine and DDL tests                          |

```go
var style = sqlbase.Style{Quote: '"', Numbered: true}

table, err := style.Table("app.events") // "app"."events"
if err != nil {
    return err
}
query := style.Bind("SELECT id FROM "+table+" WHERE id = ?", 1) // ... WHERE id = $1
```

## Related packages

- [filter](../../filter/README.md)
- [filter/evaluator](../../filter/evaluator/README.md)
- [filter backends](../../filter/README.md#backends)
- [backend/postgres](../postgres/README.md), [backend/mariadb](../mariadb/README.md), [backend/clickhouse](../clickhouse/README.md)
- [orderby](../../orderby/README.md)
- [projection](../../projection/README.md)
- [listing](../../listing/README.md)
- [listing/listingtest](../../listing/listingtest/README.md)
- [backend/sqlbase/sqltype](sqltype/README.md), [backend/sqlbase/sqlbasetest](sqlbasetest/README.md)
- [filter guide](../../docs/filter.md), [orderby guide](../../docs/orderby.md), [projection guide](../../docs/projection.md)
- [listing guide](../../docs/listing.md)
