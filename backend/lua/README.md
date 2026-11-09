# lua

```go
import "github.com/altessa-s/go-aip/backend/lua"
```

Package `lua` is the Lua backend of go-aip. It holds one kind, the filter translator: `FilterTranslator` turns filter AST nodes into
Lua boolean expressions suitable for Redis `EVAL` scripts, so filters run server-side on plain Redis keys. Ordering and projection have no
Lua form; use the [redis](../redis/README.md) backend for those.

## Contract

`NewFilterTranslator(policy, ...FilterOption)` takes the shared `*filter.Policy` (field authorization, types, array fields, limits, decimal
conversion, value codecs and regex rules, in API names, built once per resource) and this package's `FilterOption` values, the same shape as
every other filter translator. `WithFilterOptions` binds the policy to storage names (`filter.WithFieldMapping`); `WithFilterTableVar` names
the Lua variable that holds the record (default `d`, `DefaultTableVar`). A nil policy fails with `filter.ErrPolicyRequired`; `filter.NewPolicy`
requires an allowlist or an explicit trusted `WithAnyField` opt-out.

`ValidateAST` uses the configured renderer to check operand shapes and values without contacting a server. Unsupported operations remain explicit
errors.

`WithFilterLiteralEncoder` converts values before backend encoding; it does not return executable query fragments. Decimal operands are
normalized through the shared policy, including membership.

`WithFilterPredicateRenderer` supplies an application renderer after policy validation; unhandled nodes use built-in rendering. The callback remains
responsible for safe backend encoding.

## Usage

```go
policy, err := filter.NewPolicy(filter.WithAllowedFields("active", "name")) // build once per resource
if err != nil { return err }

translator, err := lua.NewFilterTranslator(policy)
if err != nil { return err }
expression, err := translator.Translate(node)
// Bind the record to local d in the Lua script before evaluating expression.
```

## Operation mapping

`ref` is the field's read: `d["a"]` for `a`; for `a.b` a call of a path helper,
`(function(v, ...) for _, k in ipairs({...}) do if type(v) ~= "table" then return nil end v = v[k] end return v end)(d, "a", "b")`,
which yields nil as soon as a parent is not a table (absent, null, a number, a string, a boolean) instead of raising a Lua
"attempt to index" error or reading the string library.

| Filter                              | Lua                                                                                                          |
|-------------------------------------|--------------------------------------------------------------------------------------------------------------|
| `a == v` / `a != v`                 | `(ref == v)` / `(ref ~= v)`, `~=` behind a type guard (see Run-time safety); `v` a literal or a field        |
| `a < n`, `<=`, `>`, `>=` (literal)  | `(type(ref) == "number" and ref < n)`; a string literal guards with `"string"`                               |
| `a < b`, … (field operand)          | `(type(ref) == type(b) and (type(ref) == "number" or type(ref) == "string") and ref < b)`                    |
| `a == null` / `a != null`           | `(ref == nil)` / `(ref ~= nil)`                                                                              |
| `a < null`, `a < true`, …           | `filter.ErrUnsupportedOperation` (nothing orders against null; booleans are unordered, as in the evaluator)  |
| bare `a` / `!a`                     | `(ref == true)` / `(ref == false)`; a nil (unknown) or non-bool value matches neither                        |
| `a in [x, y]`                       | `(ref == x or ref == y)`; an empty list is `false`                                                           |
| `has(a.b)`                          | `(ref ~= nil)`                                                                                               |
| `a.contains(s)`                     | `(type(ref) == "string" and string.find(ref, s, 1, true) ~= nil)` (plain find, no patterns)                  |
| `a.startsWith(s)` / `a.endsWith(s)` | `string.sub` prefix / suffix test behind the same string guard                                               |
| `a.size() op n`                     | UTF-8 code points of a string, `#` of a table; `0` for an absent array field, unknown for any other value    |
| `a.substring(i[, j]) op v`          | code points `[i, j)` through a Lua 5.1 helper, compared with `v`                                             |
| `a.matches(re)`                     | `filter.ErrUnsupportedOperation` (Lua patterns are not RE2)                                                  |
| `a && b`, `a \|\| b`, `!(…)`        | `and`, `or`; `!` pushed down to each predicate, which renders its own negation (see Negation)                |

String functions take a string literal or a field reference; a field argument must hold a string at run time. A target that is not a string
(absent, null, a number) does not match, as in the evaluator, instead of raising a Lua error or coercing a number to a string.

## Run-time safety

A Lua runtime error aborts the whole `EVAL` script, not one record, so no rendered expression raises one for any record:

- An ordered comparison is guarded by the operand's Lua type: a value that is null, absent, a boolean, a table, or of another type
  than the literal (or than the other field) does not match, where the evaluator finds the comparison unknown or fails that record.
- A nested field under a parent that is not a table (absent, null, number, string, boolean) is nil: every step of the path is
  guarded by `type(parent) == "table"`, so the predicate does not match.
- String functions, `size()` and `substring()` test the value's type before reading it.
- `!=` (and a negated `==`) is guarded by type as well: Lua's `~=` holds for any two values of different types, but the evaluator
  finds such a comparison unknown, so `x != 1` does not match a string `x`. Against a literal it renders
  `(ref == nil or (type(ref) == "<literal type>" and ref ~= v))`; against a field,
  `((ref == nil) ~= (b == nil) or (type(ref) == type(b) and type(ref) ~= "table" and ref ~= b))`. Null equals only null, so a nil
  operand still makes `!=` hold unless both are nil; two tables never compare.

The conformance suite runs every case in gopher-lua on every record of `filtertest.ConformanceRecords` and fails on a runtime error.

## Negation

The evaluator and the SQL translators give a null or absent value SQL's three-valued logic: an ordering comparison, a bare boolean, `size()` of
a value that is no array field, a `substring()` that does not exist and a null string-function argument are unknown, `&&` and `||` absorb
unknown, and `!` keeps it unknown. A Lua expression is only true or false, so `!` is never rendered as `not` around a clause, which would turn
unknown into a match: it is pushed down through `&&` and `||` (De Morgan) to each predicate, which renders its negation behind the same guard.

| Filter                   | Lua                                                                                             |
|--------------------------|-------------------------------------------------------------------------------------------------|
| `!(a > 1)`               | `(type(ref) == "number" and not (ref > 1))`: false for a nil or non-number `a`                  |
| `!a`                     | `(ref == false)`                                                                                |
| `!(a == v)`              | `a != v`: false where `a` holds a value of another type (unknown in the evaluator)              |
| `!(a > 1 \|\| b == 2)`   | `((type(a) == "number" and not (a > 1)) and (b == nil or (type(b) == "number" and b ~= 2)))`    |
| `!a.contains(b)`         | holds for a non-string `a`; for a string `a` only when `b` is a string that it does not contain |
| `!(a.size() == 0)`       | holds only for a string or table `a` (or any value of an array field) whose size is not 0       |
| `!(a.substring(1) == v)` | false where the substring does not exist                                                        |

`x > 1 || y` therefore matches a record with a nil `x` where `y` holds, `!(x > 1)` and `!(x > 1 || y)` never match it, and `!(x > 1 && y)`
matches it where `y` is false — as the evaluator and SQL decide.

## Type checks

Operands of statically incompatible declared kinds (`filter.WithFieldTypes`) fail with `filter.ErrFieldTypeMismatch` at translate time, as
in the MongoDB translator: two fields (`name == age`), a field in an `in` list (`age in [1, name]`), a field compared with `size()` (an
int) or with `substring()` (a string), and `substring()` of a field not declared a string. Numeric kinds (int, float, decimal) compare with
each other; an undeclared kind is not checked. A literal compared with `size()` must be a number (or null for `==`/`!=`).

## Array fields

Fields declared with `filter.WithArrayFields` are tested element by element, with the semantics of the evaluator and the MongoDB translator:

| Filter on array field `tags` | Holds when                                       |
|------------------------------|--------------------------------------------------|
| `tags == v`, `<`, `<=`, …    | one element satisfies the comparison             |
| `tags != v`                  | no element is equal to `v`                       |
| `"v" in tags`                | one element is equal to `"v"` (membership)       |
| `tags in [x, y]`             | one element is in the list                       |
| `tags.contains(s)` …         | one string element satisfies the string function |
| `tags.size() op n`           | the element count (`0` when absent)              |
| `tags == null`               | the field is absent or null                      |

The element loop is an inline `ipairs` function call (Lua 5.1). An ordered comparison skips an element it cannot order against the operand
(another type, a boolean, a table) instead of raising a Lua error. A value that is not a table is tested itself. Dotted array paths are read as nested
tables; lists of documents on the way are not traversed.

## Literals

| Go literal                   | Lua                                                                                                      |
|------------------------------|----------------------------------------------------------------------------------------------------------|
| `string`                     | double-quoted, `\`, `"`, newline, CR, tab and NUL escaped (NUL as `\000`)                                |
| `[]byte` (`b"…"`)            | double-quoted Lua byte string; every byte outside printable ASCII, `"` and `\` is a three-digit `\ddd`   |
| `time.Time` (`timestamp()`)  | Unix epoch seconds: an integer for a whole second (as the Meilisearch translator), else seconds.fraction |
| `int64`, `uint64`, `float64` | number                                                                                                   |
| `bool`, `null`               | `true` / `false`, `nil`                                                                                  |
| `decimal.Decimal`            | `filter.ErrUnsupportedType` unless `WithFilterLiteralEncoder` converts it (Lua numbers are doubles)      |

Store timestamps as epoch seconds, or use `WithFilterLiteralEncoder` to emit another unit (for example `ts.UnixMilli()`).

## Not supported

| Expression                               | Result                                                                 |
|------------------------------------------|------------------------------------------------------------------------|
| `matches()`                              | `filter.ErrUnsupportedOperation`                                       |
| ordered comparison with `null` or a bool | `filter.ErrUnsupportedOperation`                                       |
| `substring()` outside a comparison       | `filter.ErrUnsupportedOperation`                                       |
| `size()` other than a comparison operand | `filter.ErrUnsupportedOperation` (`size(a) in [1]`, a function target) |
| a bare `size(a)` predicate               | `filter.ErrInvalidExpression`                                          |
| a nested call as a function argument     | `filter.ErrUnsupportedOperation`                                       |
| decimal field without a literal encoder  | `filter.ErrUnsupportedType` at construction                            |

## Behavior

The output supports Lua 5.1, including Redis `EVAL`; Unicode size and substring need no `utf8` library. Field mappings are escaped as table
keys. The table variable name is trusted application configuration, emitted verbatim.

A Lua script cannot fail a single record, so where the evaluator finds a predicate unknown or returns an error for one record the Lua
predicate does not match, nor does its negation: a `substring()` out of range or on a non-string is `false` for every operator, a string
function on a non-string value is `false` (its negation `true`), and an ordered comparison of a null, absent or differently typed value is
`false`.

Field references are authorized before the expression's shape is checked: a denied field fails with `filter.ErrFieldNotAllowed` wherever it
appears, also as the argument of a function call the translator does not support. A bare boolean that holds a non-bool or nil is neither true
nor false: neither it nor its negation matches, as in the evaluator and the SQL translators (the MongoDB translator's `$ne: true` matches it).

## Concurrency

A `FilterTranslator` is immutable after construction and safe for concurrent `Translate` calls: each call copies the traversal state (depth guard)
before visiting. Injected callbacks (`WithFilterPredicateRenderer`, `WithFilterLiteralEncoder`) and the shared policy must also support concurrent
use. Limits and field policies are shared with [filter](../../filter/README.md).

## Configuration and compatibility

| API           | Purpose                                                       |
|---------------|---------------------------------------------------------------|
| `ValidateAST` | Check the configured renderer without contacting the database |

Constructors accept this package's generated `FilterOption` values. Shared policies and callbacks must be safe for concurrent use. The translator
exposes no `filter.Visitor` methods; call `Translate`. `ValidateAST` checks actual expression shapes and values, including field-to-field operands,
rather than promising support for an operator regardless of its operands. Unsupported expressions return filter sentinel errors. Backend-native
differences remain explicit.

Literal encoders return values, never executable query text. They can choose timestamp units or stored enum representations.

`WithFilterPredicateRenderer` installs a trusted, concurrent-safe callback for application predicates. Field authorization and limits run first.
Return `handled=false` to delegate to built-ins; encode every client value safely before returning query text.

## Options

| Option                        | Type           | Default     | Behavior                                                                                  |
|-------------------------------|----------------|-------------|-------------------------------------------------------------------------------------------|
| `WithFilterCollector`         | `FilterOption` | nil / unset | A `metrics.Meter` to record into; nil disables it.                                        |
| `WithFilterOptions`           | `FilterOption` | nil / unset | Binds the shared policy to the storage names of this backend (`filter.WithFieldMapping`). |
| `WithFilterLiteralEncoder`    | `FilterOption` | nil / unset | Converts values before backend quoting. It must be concurrency safe.                      |
| `WithFilterPredicateRenderer` | `FilterOption` | nil / unset | Supplies a trusted predicate override. Return handled=false to use built-ins.             |
| `WithFilterTableVar`          | `FilterOption` | `"d"`       | Names the Lua variable that holds the record; trimmed, empty keeps the default.           |

## API

| Symbol                | Purpose                                                                                                                 |
|-----------------------|-------------------------------------------------------------------------------------------------------------------------|
| `FilterTranslator`    | Converts filter AST nodes to Lua boolean expressions.                                                                   |
| `NewFilterTranslator` | Creates a Lua filter translator over policy with the given options; a nil policy fails with `filter.ErrPolicyRequired`. |
| `FilterOption`        | Functional option for `NewFilterTranslator`.                                                                            |
| `DefaultTableVar`     | The record variable used unless `WithFilterTableVar` names another (`"d"`).                                             |

## Configuration example

```go
policy, err := filter.NewPolicy(
    filter.WithAllowedFields("name", "age", "status", "userName"),
    filter.WithMaxDepth(10),
) // build once per resource
if err != nil { return err }

trans, err := lua.NewFilterTranslator(policy, lua.WithFilterTableVar("item"), lua.WithFilterOptions(filter.WithFieldMapping(map[string]string{
    "userName": "user_name",
})))
```

## Related packages

- [filter](../../filter/README.md)
- [filter/evaluator](../../filter/evaluator/README.md) (reference semantics the parity tests compare against)
- [backend/redis](../redis/README.md)
- [backend/mongo](../mongo/README.md)
- [backend/meili](../meili/README.md)
- [backend/postgres](../postgres/README.md)
- [backend/mariadb](../mariadb/README.md)
- [backend/clickhouse](../clickhouse/README.md)
- [backend/sqlbase](../sqlbase/README.md)
- [filter guide](../../docs/filter.md)
