# mariadb

```go
import "github.com/altessa-s/go-aip/backend/mariadb"
```

Everything go-aip renders for MariaDB lives in this one package. Each kind of request clause has its own constructor:

| Kind                       | Constructor                                                                          | Output                                            |
|----------------------------|--------------------------------------------------------------------------------------|---------------------------------------------------|
| Filter (AIP-160), scalar   | `NewFilterTranslator(policy, opts ...FilterOption)`                                  | `WHERE` body with `?` placeholders, or inline SQL |
| Filter, listing contract   | `NewFilterTranslatorWithSchema(policy, schema *Schema, opts ...FilterOption)`        | `WHERE` body over a `Schema` in contract mode     |
| Order (AIP-132 `order_by`) | `NewOrderTranslator(policy, opts ...sqlbase.OrderOption)`                                    | `ORDER BY` body                                   |
| Projection (read mask)     | `NewProjectionTranslator(policy, opts ...sqlbase.ProjectionOption)`                          | `SELECT` column list                              |
| Listing engine             | `NewListEngine[T](q, codec, schema, table, columns, dest, opts ...sqlbase.ListEngineOption)` | keyset-paginated `listing` engine                 |

The order, projection and listing constructors are the dialect-generic ones of [`sqlbase`](../sqlbase/README.md) with the MariaDB dialect fixed
(`OrderDialect`, `ProjectionDialect`, `ListDialect`, all owned by this package). Their option types come from `sqlbase`, so the
options are `sqlbase.WithOrderOptions` / `sqlbase.WithOrderCollector`, `sqlbase.WithProjectionOptions` / `sqlbase.WithProjectionCollector` and the
`sqlbase.WithList…` options. Use the generic `sqlbase` constructors directly for an application `sqlbase.ColumnResolver` or a customized
`ListDialect`. Each kind's semantics, limits and errors are documented in its `sqlbase` section; the rest of this guide covers the filter
translator, which is MariaDB-specific.

## Order, projection and listing

```go
order, err := mariadb.NewOrderTranslator(orderPolicy,
    sqlbase.WithOrderOptions(orderby.WithFieldMapping(map[string]string{"createTime": "created_at"})))
sort, err := order.Translate(spec) // `created_at` DESC, `name` ASC

proj, err := mariadb.NewProjectionTranslator(projectionPolicy)
cols, err := proj.Translate(mask) // `created_at`, `name`

list, err := mariadb.NewListEngine[Widget](db, signer, schema, "widgets", []string{"id", "name"},
    func(w *Widget) []any { return []any{&w.ID, &w.Name} },
    sqlbase.WithListArrayStorage(sqlbase.ArraysJSON))
```

MariaDB has no `NULLS FIRST/LAST`: an order key whose null placement differs from the native one (nulls smallest) gets a leading
`` `col` IS NULL `` key. The listing engine filters through `NewFilterTranslatorWithSchema` over a contract-mode `Schema` carrying the engine's text
collation, decimal format and timestamp precision; it rejects UUID ids, and array fields need `sqlbase.WithListArrayStorage(sqlbase.ArraysJSON)`. See
the [listing section of sqlbase](../sqlbase/README.md#listing-engine) for the engine contract.

`MustNewListEngine` takes the same arguments for a configuration fixed in code (a package-level `var`, like `listing.MustSchema`): it returns
the engine `NewListEngine` builds and panics on a refused one with an error that starts `sqlbase.MustNewListEngine: ` (it is
`sqlbase.MustNewListEngine` with `ListDialect` fixed) and wraps the `NewListEngine` error.

The concrete SQL list engine provides an exact filtered total through `Count(ctx, req, scope)` on every page. See [exact SQL
totals](../../docs/listing.md#exact-sql-totals).

## Filter

Translates a `filter.Node` AST into a MariaDB SQL `WHERE` clause. Two output modes share one AST walk: a parameterized clause with `?` placeholders plus
a bound argument slice, and a self-contained clause with literals rendered in place. The output is equally valid MySQL.

### Contract

`NewFilterTranslator` takes the shared `*filter.Policy` (field authorization, types, limits, decimal conversion, value codecs and regex rules, in API
names, built once per resource) and this package's Option values; `WithFilterOptions` binds the policy to storage names (`filter.WithFieldMapping`).
A nil policy fails with `filter.ErrPolicyRequired`; `filter.NewPolicy` requires an allowlist or an explicit trusted `WithAnyField` opt-out.

`ValidateAST` uses the configured renderer to check operand shapes and values without contacting a server. Unsupported operations remain explicit
errors. Translators support concurrent calls with per-call traversal state; injected callbacks must also support concurrent use.

`WithFilterLiteralEncoder` converts values before backend encoding; it does not return executable query fragments. Without an encoder, decimal operands
are normalized through the shared policy, including membership, and rendered exactly as `CAST(? AS DECIMAL(p,s))` using `WithFilterDecimalFormat` (see
Decimals). `WithFilterPredicateRenderer` installs a trusted application predicate renderer that binds client values through its `ValueFunc`; field
authorization and limits run first.

`Translate` returns SQL and ordered arguments; `TranslateInline` renders literals in place. Bind parameters for runtime queries. Placeholder numbering
and repeated operand bindings follow the selected dialect. Both constructors give NULL the default evaluator's semantics rather than SQL
three-valued logic (see Null semantics).

`NewFilterTranslatorWithSchema` consumes a `Schema` (`NewSchema(*sqlbase.Schema, ...SchemaOption)`). `WithSchemaContractMode(true)` enables portable
listing semantics; arrays use JSON. Contract storage uses utf8mb4_nopad_bin, the configured decimal precision/scale, and the configured timestamp
precision. Value depth, array dimensions, data work, and array alias prefixes are bounded schema options. The scalar SQL translator uses ? placeholders;
listing support targets MariaDB and is not a MySQL listing engine.

Under the contract only presence columns are nullable, and a NULL one follows the default evaluator, as the memory listing engine and the PostgreSQL
contract translator do: `!` is pushed down to the predicates, `note != "a"` is `` (`note` <> ? OR `note` IS NULL) ``, `!(note in ["a"])` is
`` (`note` NOT IN (?) OR `note` IS NULL) ``, `!note.contains("a")` is `` (NOT (…) OR `note` IS NULL) ``, two presence columns compare with `<=>`, and a
negated ordered comparison stays `NOT (…)`, which never matches NULL. A `null` element of an `in` list matches a NULL presence column. An array is
compared with a literal and an `in` list holds literals; other operands fail with `filter.ErrUnsupportedOperation`.

### Constructor

| Function                                                                                                       | Description                                                                               |
|----------------------------------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------|
| `NewFilterTranslator(policy, opts ...FilterOption) (*FilterTranslator, error)`                                 | Requires a policy (`filter.ErrPolicyRequired`).                                           |
| `NewSchema(fields *sqlbase.Schema, opts ...SchemaOption) (*Schema, error)`                                     | Binds the SQL fields to the storage settings of the schema translator.                    |
| `NewFilterTranslatorWithSchema(policy, schema *Schema, opts ...FilterOption) (*SchemaFilterTranslator, error)` | Listing contract translator; requires a schema built with `WithSchemaContractMode(true)`. |

`WithUntrustedInput` without `WithAllowedFields` makes `filter.NewPolicy` return `filter.ErrAllowlistRequired`. The listing contract settings
(contract mode, text collation, array aliases, timestamp precision, decimal domain and the literal validation bounds) live on the `Schema`;
`NewFilterTranslatorWithSchema` takes only the shared `WithFilterOptions` and `WithFilterCollector`, and a scalar option (`WithFilterLiteralEncoder`,
`WithFilterPredicateRenderer`, `WithFilterDecimalFormat`) fails it with `filter.ErrInvalidOptions` instead of being ignored.

### Methods

| Method                                   | Description                                                                 |
|------------------------------------------|-----------------------------------------------------------------------------|
| `Translate(node) (string, []any, error)` | Parameterized clause; every literal becomes a `?` and is returned in `args` |
| `TranslateInline(node) (string, error)`  | Self-contained clause with literals rendered in place                       |

A `nil` node translates to `1 = 1` (match all) in both modes, so `"... WHERE " + where` stays valid without a special case.

### Usage

```go
parser, _ := filterparser.New()
ast, _ := parser.Parse(ctx, `status == 2 && type in [1, 2]`)

policy, err := filter.NewPolicy(
    filter.WithUntrustedInput(true),
    filter.WithAllowedFields("status", "type"),
) // build once per resource
if err != nil { return err }

trans, err := mariadb.NewFilterTranslator(policy)
if err != nil {
    return err
}

where, args, err := trans.Translate(ast)
// where: (`status` = ?) AND (`type` IN (?, ?))
// args:  []any{int64(2), int64(1), int64(2)}

rows, err := db.QueryContext(ctx, "SELECT * FROM events WHERE "+where, args...)
```

### Operation mapping

| CEL                        | MariaDB                                                                                                                      |
|----------------------------|------------------------------------------------------------------------------------------------------------------------------|
| `==` `<` `>` `<=` `>=`     | `=` `<` `>` `<=` `>=`                                                                                                        |
| `field != v`               | `` (`field` != ? OR `field` IS NULL) ``                                                                                      |
| `field == other`           | `` `field` <=> `other` ``                                                                                                    |
| `field != other`           | `` NOT (`field` <=> `other`) ``                                                                                              |
| `&&` `\|\|`                | `AND` `OR`                                                                                                                   |
| `!`                        | pushed down to each predicate (see Null semantics)                                                                           |
| `!(field == v)`            | `` (`field` != ? OR `field` IS NULL) ``                                                                                      |
| `!(field in [a, b])`       | `` (`field` NOT IN (?, ?) OR `field` IS NULL) ``                                                                             |
| `!(field > v)`             | `` NOT (`field` > ?) ``                                                                                                      |
| `field`                    | `` `field` = TRUE ``                                                                                                         |
| `field in [a, b]`          | `` `field` IN (?, ?) ``                                                                                                      |
| `field in []`              | `1 = 0`                                                                                                                      |
| `field in [a, null]`       | `` (`field` IN (?) OR `field` IS NULL) ``                                                                                    |
| `field == null`            | `` `field` IS NULL ``                                                                                                        |
| `field != null`            | `` `field` IS NOT NULL ``                                                                                                    |
| `has(field)`               | `` `field` IS NOT NULL ``                                                                                                    |
| `field.contains(s)`        | `` LOCATE(?, `field`) > 0 ``                                                                                                 |
| `field.startsWith(s)`      | `` LOCATE(?, `field`) = 1 ``                                                                                                 |
| `field.endsWith(s)`        | `` (CHAR_LENGTH(`field`) >= CHAR_LENGTH(?) AND RIGHT(`field`, CHAR_LENGTH(?)) = ?) ``                                        |
| `field.matches(re)`        | `` `field` REGEXP ? ``                                                                                                       |
| `field.size()` / `size(f)` | `` CHAR_LENGTH(`field`) ``                                                                                                   |
| `arr == x` (JSON array)    | `` JSON_CONTAINS(`arr`, JSON_ARRAY(?)) ``                                                                                    |
| `arr != x`                 | `` (`arr` IS NULL OR NOT (JSON_CONTAINS(`arr`, JSON_ARRAY(?)))) ``                                                           |
| `arr < x` (any element)    | `` EXISTS (SELECT 1 FROM JSON_TABLE(`arr`, '$[*]' COLUMNS (v BIGINT PATH '$')) AS _aip_e WHERE _aip_e.v < ?) ``              |
| `x in arr`                 | `` JSON_CONTAINS(`arr`, JSON_ARRAY(?)) ``                                                                                    |
| `arr.contains(s)`          | `` EXISTS (SELECT 1 FROM JSON_TABLE(`arr`, '$[*]' COLUMNS (v LONGTEXT PATH '$')) AS _aip_e WHERE LOCATE(?, _aip_e.v) > 0) `` |
| `arr.size()`               | `` COALESCE(JSON_LENGTH(`arr`), 0) ``                                                                                        |
| decimal `field > 1.5`      | `` `field` > CAST(? AS DECIMAL(65,30)) ``                                                                                    |
| `f.substring(1, 3) == s`   | `` (CHAR_LENGTH(`f`) >= 3 AND SUBSTRING(`f`, 2, 2) = ?) ``                                                                   |

`contains` and `startsWith` go through `LOCATE`, which takes the needle as a plain string — there is no LIKE pattern, so a `%` or `_` in the operand
stays literal. `startsWith` is `LOCATE = 1` because MariaDB has no dedicated function; the two are equivalent, including for an empty needle. `size()`
is rejected outside a comparison: `CHAR_LENGTH` is an integer expression, and on its own there is no predicate for `WHERE` to test.

**`endsWith` binds its operand three times.** MariaDB has no `endsWith`, so the needle guards the length, sizes the suffix and is compared to it. One
CEL argument consumes three placeholders — `args` reflects that, and any clause following it is numbered accordingly. The length guard is what keeps the
predicate exact under a PAD SPACE collation: without it a column shorter than the needle comes back whole from `RIGHT` and is padded with spaces before
the comparison, so `"a".endsWith("a ")` would be true.

### Arrays

MariaDB has no SQL array type: fields declared by `filter.WithArrayFields` are JSON array columns (MariaDB 10.6+ for `JSON_TABLE`) and get element
semantics, matching the evaluator and the MongoDB translator. `==` is `JSON_CONTAINS(col, JSON_ARRAY(?))`, which keeps the bound value a JSON string or
number and compares strings exactly (not by collation); booleans compare with the JSON constants `true` / `false` because MariaDB binds a boolean as 1
or 0. `!=` holds when no element equals the operand or the column is NULL (absent), like the evaluator; `x in arr` is `arr == x`; `arr in [a, b]` is an
OR of equalities. Ordered comparisons and string predicates expand the array with `JSON_TABLE` into a column typed by the declared element kind
(`BIGINT` int, `DOUBLE` float, `LONGTEXT` string, the `WithFilterDecimalFormat` DECIMAL for decimals; undeclared: `DOUBLE` for numbers, `LONGTEXT` for
strings); the `LONGTEXT` element column uses the connection's default collation, like the scalar predicates. `size()` is
`COALESCE(JSON_LENGTH(col), 0)`. `arr == null`, `arr != null` and `has(arr)` test the column itself. Timestamp and bytes elements have no JSON
representation and fail with `filter.ErrUnsupportedType`; ordered boolean comparisons, field operands, field arguments to array string predicates and a
bare array identifier fail with `filter.ErrUnsupportedOperation`. A NULL column satisfies no other comparison, and every negated element test holds for
it: `!(arr > x)` is `` (`arr` IS NULL OR NOT (…)) ``, and `!(arr in [a, b])` an AND of negated equalities.

### Decimals

Fields declared `filter.FieldKindDecimal` need no encoder: operands keep their source spelling (`Policy.DecimalOperand`), are bound as strings and cast
to the `WithFilterDecimalFormat` domain (default `DECIMAL(65,30)`). A value the cast would round or overflow fails with `filter.ErrFieldTypeMismatch`.
The schema translator reads `WithSchemaDecimalFormat` instead; with `WithFilterLiteralEncoder` the encoder receives the `decimal.Decimal` instead.

### substring()

`field.substring(start[, end])` is supported as a comparison or `in` operand with int literal bounds and renders as `SUBSTRING(col, start+1, end-start)`
(characters). Invalid bounds fail with `filter.ErrInvalidExpression`, non-literal bounds with `filter.ErrUnsupportedOperation`. The comparison is
guarded with `CHAR_LENGTH(col) >= end`, so a row shorter than the bound never matches — the evaluator finds it unknown rather than comparing a truncated
value. Any int64 bound is accepted, as in the evaluator: the bounds are ordered before `end - start` is computed, so
`substring(1, -9223372036854775808)` fails with `filter.ErrInvalidExpression` instead of wrapping, and the start of `substring(9223372036854775807)` is
capped rather than overflowed (its guard never holds).

### Column names

Field names are checked against `WithAllowedFields`, run through `WithFieldMapping`, validated as plain SQL identifiers, and emitted backtick-quoted per
dot-separated segment: `address.city` becomes `` `address`.`city` ``.

A dot is a **table qualifier** here, not nesting: `address.city` is the column `city` of the table or alias `address`. So a dotted field must be one
the policy declares (`filter.Policy.DeclaresField`: a `WithAllowedFields` entry, a `WithFieldTypes` declaration or a `WithFieldMapping` key); one
admitted only by a `WithFieldAccess` wildcard (`a.*`) or `WithAnyField` fails with `filter.ErrFieldNotAllowed`, since a client could otherwise name a
column of another joined table. A column name has at most two parts (`table.column`, never `db.table.column`) and each part at most 64 characters
(`sqlbase.MariaDBMaxIdentLen`); anything longer fails with `filter.ErrInvalidExpression` rather than reaching the server.

The validation is stricter than backtick quoting requires: the column position is the one part of the generated SQL that no placeholder can cover, so
anything that is not `[A-Za-z_][A-Za-z0-9_]*` (dot-separated) is rejected with `filter.ErrInvalidExpression`. To filter on a JSON path or a computed
value, map the CEL name onto a generated column.

### Collation

String comparison follows the column's collation, and MariaDB's defaults (`utf8mb4_general_ci` and friends) are **case-insensitive**. Every string
predicate here inherits that: `contains`, `startsWith`, `endsWith`, `REGEXP` and plain equality all match case-insensitively unless the column or the
connection says otherwise. This is a real semantic difference from the ClickHouse and PostgreSQL translators, which are case-sensitive. Use an explicit
`_bin` or `_cs` collation where the distinction matters. The translator keeps this behavior deliberately — it follows the schema rather than overriding
it with a `COLLATE` clause that would defeat column indexes — and the filter conformance suite pins it as a backend limit (`contains("dave")` matches
`Dave`). Array equality (`JSON_CONTAINS`) is the exception and compares strings exactly; the schema translator declares its own collation
(`WithSchemaTextCollation`, NO PAD binary by default).

Most MariaDB and MySQL collations — `utf8mb4_bin` included — are **PAD SPACE**: strings compare as if the shorter one were padded with spaces.
Comparisons and membership inherit that, so under such a collation `name == "a "` matches `a`, and `!=`, `<`, `<=`, `>`, `>=` and `in` likewise ignore
trailing spaces. The string predicates do not: `contains` and `startsWith` (`LOCATE`) and `size()` (`CHAR_LENGTH`) see every character, and `endsWith`
is length-guarded, so `a` does not end with `a `. Use a NO PAD collation (`_nopad_`, `utf8mb4_0900_*`) where trailing spaces in comparisons matter.

### Null semantics

A SQL row always carries every column of its table, so there is no "missing field" for `has()` to detect — it compiles to `IS NOT NULL` and is
meaningful only against a nullable column. A NULL column otherwise behaves like a null or absent field in the default evaluator, not under SQL
three-valued logic: `col != "x"` is `` (`col` != ? OR `col` IS NULL) `` and matches the NULL row. Two field operands compare with the null-safe `<=>`
(negated: `NOT (a <=> b)`), so NULL equals NULL and differs from every value.

`!` is not rendered as `NOT` around a clause, since `NOT` keeps a NULL comparison unknown where the evaluator's `!` holds. It is pushed down through
`&&` and `||` (De Morgan) to each predicate, which renders its own negation: `!(col == "x")` is the `!=` form above, `!(col in ["a", "b"])` is
`` (`col` NOT IN (?, ?) OR `col` IS NULL) ``, `!col.contains("a")` is `` (NOT (…) OR `col` IS NULL) `` and `!has(col)` is `IS NULL`, so each matches
the NULL row. An ordered comparison, a bare boolean, `size()` / `substring()` of a NULL column and a NULL column as a string function argument are
errors in the evaluator; their negation stays `NOT (…)` and does not match NULL either. Array fields carry the NULL test themselves (see Arrays).

A `null` element of an `in` list matches a NULL column, as in the evaluator: `x in ["a", null]` is ``(`x` IN (?) OR `x` IS NULL)``, a list of only
nulls is `` `x` IS NULL ``, and its negation is `` `x` NOT IN (?) `` (`` `x` IS NOT NULL `` for only nulls). An ordered comparison with `null`
(`age > null`) fails with `filter.ErrUnsupportedOperation`, and two field operands of incompatible declared kinds (`name == age` with a string `name`
and an int `age`) with `filter.ErrFieldTypeMismatch`; see [sqlbase](../sqlbase/README.md#shared-semantics).

### Regular expressions

`matches()` compiles to `REGEXP`, which MariaDB 10.0.5+ evaluates with PCRE. Unlike RE2 it can backtrack, so a crafted pattern can be made expensive
server-side. `filter.WithMaxRegexLength` bounds the blast radius but does not eliminate it — expose `matches()` to trusted callers, or tighten the
cap.

When a match exhausts PCRE2's match or depth limit, MariaDB does not fail the statement: it raises **warning 1139**
(`Got error '... match limit exceeded' from regexp`) and the `REGEXP` evaluates to false for that row, so the row is silently dropped from `matches()`
and silently kept by `!matches()`. The server has no variable to raise or to escalate that limit, `database/sql` does not surface warnings, and the
translators and the list engine cannot detect it from the result set. Where exact results matter: keep `filter.WithSafeRegex(true)` (it refuses the
backtracking shapes), bound the stored value length, run the query on a dedicated `*sql.Conn` and check `SHOW WARNINGS` (or `@@warning_count`) right
after it, and set `max_statement_time` so a pathological pattern cannot hold a server thread.

The pattern is RE2, as CEL defines `matches()`, and one difference in anchors is corrected before it is sent: outside multi-line mode RE2's `$` matches
only at the very end, while PCRE (MariaDB) and ICU (MySQL 8) also match it before a final newline. Every such `$` is rewritten to `\z` (`^Jo$` is sent
as `^Jo\z`); a `$` under `(?m)`, an escaped `\$` and a `$` in a character class are left alone. In inline mode the `\z` backslash is doubled like any
other, so the caveat under [Inline rendering](#inline-rendering) applies.

### Timestamps

`timestamp(...)` literals bind as `time.Time` in the parameterized form and render as a UTC `DATETIME` literal at microsecond precision inline. Store
the corresponding columns in UTC.

### Inline rendering

`TranslateInline` output is pure ASCII, so it reads the same in every connection character set. A string of ASCII bytes is quoted with the quote
doubled, which closes the break-out in **every** `sql_mode`, and the backslash doubled, which is correct under the default mode where the backslash is
an escape character. Under `NO_BACKSLASH_ESCAPES` it is not, and such a value containing a backslash comes out with it doubled — a fidelity bug, never
an injection.

A string with any byte >= 0x80 is rendered as the hex literal `` _utf8mb4 X'...' `` instead: in a multibyte connection character set (`gbk`, `big5`,
`sjis`, `cp932`, `gb18030`) a raw byte >= 0x80 can pair with the escaping backslash after it into one character and end the literal early (the filter
`name == "丿\\' OR 1=1 -- "` would otherwise select every row over a `gbk` connection). The hex form has nothing to escape, is exact under every
`sql_mode`, and the introducer keeps the value `utf8mb4` text whatever the connection's character set. Use the parameterized form where possible.

### Concurrency

A `FilterTranslator` is safe for concurrent `Translate` and `TranslateInline` calls. Each call owns its traversal state.

### Field operands

Comparisons, `in` list elements, and `contains` / `startsWith` / `endsWith` arguments can reference other fields. Field mappings and allowlists apply to
every reference. Lists containing dynamic operands render as equality disjunctions; constant lists retain `IN`. Literal values remain bound parameters.

`matches` requires a literal pattern so regex validation and length limits apply before execution.

### Listing storage contract

`NewFilterTranslatorWithSchema(policy, schema)` creates a concurrent-safe `SchemaFilterTranslator` over a `Schema` built with
`NewSchema(fields, WithSchemaContractMode(true), …)`. The immutable [`sqlbase.Schema`](../sqlbase/README.md#sql-field-schema) supplies public-name
mappings, kinds, array flags, and presence declarations; the `Schema` adds the storage settings. Without `WithSchemaContractMode`, the constructor
returns `filter.ErrInvalidOptions`; use `NewFilterTranslator` for the original MariaDB translation API.

The contract renderer validates and walks the filter tree directly, binding `?` arguments in SQL text order. It never rewrites SQL. Every text column
(including ids, scope keys, and JSON array storage) must use `utf8mb4 COLLATE utf8mb4_nopad_bin`; `utf8mb4_bin` uses PAD SPACE and cannot distinguish
trailing spaces. A MariaDB `JSON` column is `LONGTEXT` with the collation `utf8mb4_bin` whatever the table or database default, so a listing array
column must spell its collation: `tags JSON COLLATE utf8mb4_nopad_bin NOT NULL`. The engine's `CheckSchema` (and `EnsureIndexes`, which runs it first)
rejects a JSON array column without it with `listing.ErrInvalidSchema`. Optional scalars store their zero value; Presence scalars use nullable
columns.

```sql
CREATE TABLE widgets (
    id VARCHAR(16) NOT NULL PRIMARY KEY,           -- utf8mb4_nopad_bin from the database default
    name VARCHAR(64) NOT NULL,
    created_at DATETIME(3) NOT NULL,
    note VARCHAR(64) NOT NULL,                     -- Optional: the zero value when absent
    rating BIGINT,                                 -- Presence: NULL when absent
    tags JSON COLLATE utf8mb4_nopad_bin NOT NULL   -- JSON defaults to utf8mb4_bin
) DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_nopad_bin;
```

JSON arrays contain scalars of the declared kind, with numbers and booleans stored as JSON numbers and booleans, not strings. Timestamp elements use RFC
3339 UTC strings with exactly three fractional digits, for example `2026-01-02T03:04:05.123Z`. Equality and membership bind serialized JSON; ordered
predicates use typed `JSON_TABLE` columns. String elements use `LONGTEXT` with explicit NO PAD binary collation, independent of connection collation.
Decimal operands use `CAST(? AS DECIMAL(p,s))` in the `WithSchemaDecimalFormat` domain (default `DECIMAL(65,30)`); values must fit that precision and
scale. Regex predicates use the same end-of-text anchor adaptation as `NewFilterTranslator`. The contract API needs `JSON_TABLE` (MariaDB 10.6+) and
`JSON_OVERLAPS` (MariaDB 10.9+); the live suite runs `mariadb:11` (the latest 11.x).

The translator validates every expression branch and literal before rendering. JSON paths, whole JSON values, and UUID storage remain unsupported. The
translator has no PostgreSQL semantics dependency.

`field.substring(start[, end])` of a text column is a comparison or `in` operand, as in the [scalar translator](#substring), with the same bounds
(int literals, `0 <= start <= end`; otherwise `filter.ErrInvalidExpression`, non-literal bounds `filter.ErrUnsupportedOperation`) and character
semantics. It renders as `SUBSTRING(col, start+1, end-start)` inside `CASE WHEN CHAR_LENGTH(col) >= end THEN … END`, so it is NULL exactly where the
default evaluator finds it unknown — a NULL presence column or a string shorter than the bound — and such a row matches neither the comparison nor
its negation; `substring(…) == null` never holds and `!(substring(…) in [])` holds only where the substring exists. `substring()` of an array field,
of a non-field, as an `in` list element or anywhere but a comparison or `in` operand fails with `filter.ErrUnsupportedOperation`, as does any
`substring()` under a portable-contract policy (the listing one), refused by `filter.Validate`.

### Configuration and compatibility

| API           | Purpose                                                       |
|---------------|---------------------------------------------------------------|
| `ValidateAST` | Check the configured renderer without contacting the database |

Both filter constructors accept this package's generated `FilterOption` values; the schema translator takes the `Schema` and only the shared
`WithFilterOptions` and `WithFilterCollector`. Shared policies and callbacks must be safe for concurrent use. The translator exposes no `filter.Visitor`
methods; call `Translate`. `ValidateAST` checks actual expression shapes and values, including field-to-field operands, rather than promising support
for an operator regardless of its operands. Unsupported expressions return filter sentinel errors. Backend-native differences remain explicit.

Literal encoders return values, never executable query text. They can choose timestamp units or stored enum representations. Schema-aware SQL renderers
use their declared storage semantics instead; configure those through the `SchemaOption` values.

### Schema rendering policy

`sqlbase.DecimalFormat.Check` rejects rounding and overflow. Use the same format for cursor operands; SQL listing exposes `WithListDecimalFormat` and
checks that decimal column domains fit it. A format applies to the whole translator schema; use separate instances for different domains. Changing
precision or collation requires stored data and any in-memory comparator to use the same contract.

Schema translation applies policy value codecs to array elements and decimal operands. Regex length and compiler checks also apply
when unsafe syntax is enabled. Backend regex syntax and numeric storage limits remain additional requirements.

### Policy propagation

SchemaFilterTranslator validation budgets are configurable through `WithSchemaMaxDataBudget`, `WithSchemaMaxValueDepth` and
`WithSchemaMaxArrayDimensions` (defaults: 4194304, 64 and 6). `WithSchemaTextCollation` controls text array element declarations. Size of a bytes
literal uses OCTET_LENGTH.

Schema translation applies the portable regex subset only when the policy enables `PortableRegex`. A custom compiler and validator can
admit backend regex syntax when that rule is disabled. Schema-approved column names are escaped when quoted.

## Options

Filter options (`FilterOption`, generated). The schema translator takes only `WithFilterOptions` and `WithFilterCollector`; the scalar options fail
it with `filter.ErrInvalidOptions`:

| Option                        | Default                          | Behavior                                                                                                                                                                                                         |
|-------------------------------|----------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `WithFilterCollector`         | nil / unset                      | A `metrics.Meter` to record into; nil disables it.                                                                                                                                                               |
| `WithFilterOptions`           | nil / unset                      | Binds the shared policy to the storage names of this backend (`filter.WithFieldMapping`).                                                                                                                        |
| `WithFilterDecimalFormat`     | `sqlbase.DefaultDecimalFormat()` | Exact decimal precision and scale of decimal casts (scalar translator).                                                                                                                                          |
| `WithFilterLiteralEncoder`    | nil / unset                      | Converts values before backend quoting (scalar translator). It must be concurrency safe.                                                                                                                         |
| `WithFilterPredicateRenderer` | nil / unset                      | Trusted predicate override (`sqlbase.PredicateRenderer`) that binds client values through its `ValueFunc`; return handled=false to use built-ins. Runs after field authorization and limits (scalar translator). |

Schema options (`SchemaOption`, generated, read by `NewSchema`):

| Option                         | Default                          | Behavior                                                                                                           |
|--------------------------------|----------------------------------|--------------------------------------------------------------------------------------------------------------------|
| `WithSchemaContractMode`       | false                            | Enables the listing contract; `NewFilterTranslatorWithSchema` requires it.                                         |
| `WithSchemaArrayAliasPrefix`   | `DefaultArrayAliasPrefix`        | Prefix of the generated array element aliases, followed by a counter.                                              |
| `WithSchemaDecimalFormat`      | `sqlbase.DefaultDecimalFormat()` | Exact decimal precision and scale of decimal casts; use the same format for cursors.                               |
| `WithSchemaMaxArrayDimensions` | `DefaultMaxArrayDimensions`      | Bounds nested arrays in a filter literal.                                                                          |
| `WithSchemaMaxDataBudget`      | `DefaultMaxDataBudget`           | Bounds literal validation work, in bytes and values.                                                               |
| `WithSchemaMaxValueDepth`      | `DefaultMaxValueDepth`           | Bounds pointer unwrapping during literal validation.                                                               |
| `WithSchemaTextCollation`      | `DefaultTextCollation`           | Collation of text array element declarations.                                                                      |
| `WithSchemaTimestampPrecision` | `DefaultTimestampPrecision`      | Timestamp truncation; `DATETIME(n)` with the digits it needs (at most 6), zero keeps microseconds, negative fails. |

Order, projection and listing options are the `sqlbase` ones (`sqlbase.OrderOption`, `sqlbase.ProjectionOption` and `sqlbase.ListEngineOption`):
`sqlbase.WithOrderOptions`, `sqlbase.WithOrderCollector`, `sqlbase.WithProjectionOptions`, `sqlbase.WithProjectionCollector` and the
`sqlbase.WithList…` options. Metric names are those of `sqlbase` (`backend` label `mariadb` for the filter translator).

## Defaults and limits

| Constant                    | Value                 |
|-----------------------------|-----------------------|
| `DefaultArrayAliasPrefix`   | `"_aip_element_"`     |
| `DefaultMaxDataBudget`      | `1 << 22`             |
| `DefaultMaxArrayDimensions` | `6`                   |
| `DefaultMaxValueDepth`      | `64`                  |
| `DefaultTextCollation`      | `"utf8mb4_nopad_bin"` |
| `DefaultTimestampPrecision` | `time.Millisecond`    |

## API

| Symbol                          | Purpose                                                                                                                                                                                  |
|---------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `FilterTranslator`              | Scalar filter translator: `Translate` (`?` placeholders + args), `TranslateInline`, `ValidateAST`.                                                                                       |
| `NewFilterTranslator`           | Builds a `FilterTranslator` over a shared `*filter.Policy`.                                                                                                                              |
| `Schema`, `NewSchema`           | A `sqlbase.Schema` bound to the storage settings of the schema translator; `ContractMode`, `ValidatePolicy`.                                                                             |
| `SchemaOption`                  | Functional option of `NewSchema`.                                                                                                                                                        |
| `SchemaFilterTranslator`        | Listing contract filter translator over a `Schema`: `Translate`, `ValidateAST`.                                                                                                          |
| `NewFilterTranslatorWithSchema` | Builds a `SchemaFilterTranslator`; without contract mode it returns `filter.ErrInvalidOptions`.                                                                                          |
| `FilterOption`                  | Functional option of both filter constructors.                                                                                                                                           |
| `OrderDialect`                  | The MariaDB `sqlbase.OrderDialect` (backticks, emulated null placement, bare `COLLATE`).                                                                                                 |
| `NewOrderTranslator`            | `sqlbase.NewOrderTranslator` with `OrderDialect()`; returns `*sqlbase.OrderTranslator`.                                                                                                  |
| `ProjectionDialect`             | The MariaDB `sqlbase.ProjectionDialect` (backticks, no exclusion projection).                                                                                                            |
| `NewProjectionTranslator`       | `sqlbase.NewProjectionTranslator` with `ProjectionDialect()`; returns `*sqlbase.ProjectionTranslator`.                                                                                   |
| `ListDialect`                   | The MariaDB dialect of `sqlbase.NewListEngine`: syntax, storage validation, schema checks, index DDL and the filter translator in contract mode. Embed it to override selected behavior. |
| `NewListEngine`                 | `sqlbase.NewListEngine` with `ListDialect{}`; returns `*sqlbase.ListEngine[T]`.                                                                                                          |
| `MustNewListEngine`             | Panicking `NewListEngine` for package-level declarations fixed in code; panics with an error that wraps the `NewListEngine` error.                                                       |

## Related packages

- [backend/sqlbase](../sqlbase/README.md) — shared SQL predicate builder, schema, order, projection and listing engine
- [backend/postgres](../postgres/README.md), [backend/clickhouse](../clickhouse/README.md) — the other SQL dialects
- [filter](../../filter/README.md) — parser, AST, policy and the shared translator options
- [orderby](../../orderby/README.md), [projection](../../projection/README.md), [listing](../../listing/README.md) — the backend-neutral contracts
- [filter guide](../../docs/filter.md)
- [mariadbtest](mariadbtest/README.md) — the filter conformance expectations shared by the unit tests and the live tests
- [integration/docker](../../integration/docker/README.md) — the test-only module running the filter, order, projection and listing conformance
  suites against a live MariaDB 11 server (`go -C integration/docker test -race -tags integration_docker ./mariadb/`, needs Docker)
