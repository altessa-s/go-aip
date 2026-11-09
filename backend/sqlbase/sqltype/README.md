# sqltype

```go
import "github.com/altessa-s/go-aip/backend/sqlbase/sqltype"
```

The type checker and literal value model of the schema-aware SQL filter translators: the dialect SPI of
[backend/sqlbase](../README.md). backend/postgres, backend/mariadb and backend/clickhouse type a filter with one `Checker` and render
SQL from the typed tree it returns, so a typing rule is written once and the renderers never re-resolve a field or re-check a kind. A
SQL dialect outside this repository implements `Dialect` the same way.

## Contract

- `Checker.Check` walks every branch before anything is rendered, so a malformed branch cannot bypass the checks of another.
  It fails with the dialect's `TypeError` for incompatible operands, its `Unsupported` for an expression the dialect cannot
  render, and the policy's and `sqlbase.SubstringBounds`' errors; a root that is not boolean fails with
  `filter.ErrInvalidExpression`.
- The typed tree mirrors the AST: a unary node has its operand as the only child, a binary node its two operands, a list its
  elements, a call its target followed by the argument of a string function. `Expr.Node` keeps the source node.
- Literal functions charge their work to a `Budget`; a spent budget fails with `filter.ErrDataLimitExceeded` before more work
  is done, and a value outside the operand type fails with the budget's `TypeError`, so each dialect keeps its own error text.

## Dialect hooks

| Hook / switch        | PostgreSQL                               | MariaDB                        | ClickHouse                              |
|----------------------|------------------------------------------|--------------------------------|-----------------------------------------|
| `Dialect.Field`      | every declared field                     | refuses JSON, UUID, JSON paths | refuses JSON, UUID, decimal/bytes paths |
| `Dialect.Hint`       | the declared kind                        | the declared kind              | the declared kind                       |
| `Dialect.InferKind`  | `InferKind`                              | `InferKind`                    | `InferKind` without `json.Number`       |
| `Dialect.Literal`    | normalizes to the evaluator value        | validates within the budget    | validates DateTime64 and the budget     |
| `Dialect.Comparison` | JSONB ordering needs a comparator        | none                           | none                                    |
| `Dialect.Regex`      | `CheckPortableRegex`, `(?s)` native mode | `CheckPortableRegex`           | `CheckPortableRegex`                    |
| `Contract`           | listing contract only                    | yes                            | yes                                     |
| `MaxArrayDimensions` | configured                               | configured                     | 0: flat lists                           |
| `NormalizeDecimals`  | yes                                      | no                             | no                                      |
| `SubstringArgFirst`  | yes                                      | no                             | no                                      |
| `LiteralStringArgs`  | no                                       | no                             | yes                                     |

## API

| Symbol                                        | Purpose                                                                             |
|-----------------------------------------------|-------------------------------------------------------------------------------------|
| `Checker`, `Dialect`                          | The type checker and its per-dialect hooks.                                         |
| `Expr`, `Dimension`, `Span`                   | A typed node, the dimensions of a list and the bounds of substring().               |
| `IsNumber`, `Compatible`                      | Kind predicates of the comparison rules.                                            |
| `ComparisonKind`, `SetListKind`               | The kind two operands compare as; propagation of a list kind.                       |
| `CheckPortableRegex`                          | The `filter.PortableRegex` rule (`filter.CheckPortableRegex`) after an RE2 compile. |
| `Budget`, `Decoder`                           | Charges literal work; converts driver values.                                       |
| `Number`, `ParseNumber`, `CompareNumber`      | An exact NUMERIC: a finite decimal or NaN, ±Infinity.                               |
| `NegativeInfinity`, `PositiveInfinity`, `NaN` | The special values of a `Number`.                                                   |
| `MaxIntegerDigits`, `MaxFractionDigits`       | PostgreSQL's unconstrained NUMERIC digit bounds.                                    |
| `InferKind`                                   | The operand kind of a Go literal value.                                             |
| `Integer`, `Float`                            | Narrow a value to an int64 BIGINT or a float64 DOUBLE.                              |
| `Unwrap`                                      | Follows pointers and an optional decoder to the held value, bounded by a depth.     |

## Limits

The checker visits each node once and allocates one `Expr` per node; the filter policy bounds the tree. Nested lists are bounded
by `MaxArrayDimensions`. `ParseNumber` checks a decimal's exponent before formatting it and the text length before parsing it,
so an operand such as `1e1000000000` is refused without allocating its digits. `Unwrap` takes at most the given number of
steps.

## Related packages

- [go-aip](../../../README.md)
- [backend/sqlbase](../README.md): operand kinds, the dialect ports, identifier and placeholder styles
- [backend/postgres](../../postgres/README.md)
- [backend/mariadb](../../mariadb/README.md)
- [backend/clickhouse](../../clickhouse/README.md)
