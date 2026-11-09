# functions

```go
import "github.com/altessa-s/go-aip/filter/functions"
```

Pure AST expansion helpers implementing `filter.CustomFunction`. Function registration belongs to [parser](../parser/); this package has no global
registry.

## Contract

Helpers return CustomFunction AST expansions; constructing or importing this package registers nothing globally. Install handlers explicitly with
parser.WithCustomFunctions. Expansion output is subject to the same field authorization, literal rules, AST limits, and backend compatibility checks
as ordinary syntax.

`TextSearch` expands over configured fields, escaping regex metacharacters; Option chooses operator and case sensitivity. Defaults are matches
and case-insensitive search. Range, field comparison, and soft-delete helpers compose built-in nodes rather than bypassing policy.

## Key functions

| Function                                     | Expansion                                             |
|----------------------------------------------|-------------------------------------------------------|
| `CompareField`, `Constant`                   | Comparison with a fixed field or value                |
| `Between`, `BetweenFilter`                   | Inclusive lower/upper bounds                          |
| `TextSearch`                                 | Literal case-insensitive regex across selected fields |
| `TimestampFilters`, `SelectTimestampFilters` | Created/updated/deleted time comparisons              |
| `SoftDeleteFilters`                          | Deleted/not-deleted predicates                        |

## Usage

```go
p, err := parser.New(parser.WithCustomFunctions(map[string]filter.CustomFunction{
    "search": functions.TextSearch([]string{"name", "description"}),
    "adult": functions.Constant("age", filter.OpGTE, int64(18)),
}))
```

## Presets

`TimestampFilters()` returns a ready-made set for the common `createdAt`/`updatedAt`/`deletedAt` predicates — opt in explicitly:

```go
filterparser.WithCustomFunctions(functions.TimestampFilters())
// createAfter(t)  → createdAt > t
// createBefore(t) → createdAt < t
// updateAfter(t)  → updatedAt > t
// updateBefore(t) → updatedAt < t
// deleteAfter(t)  → deletedAt > t
// deleteBefore(t) → deletedAt < t
```

Field names are fixed (camelCase). For snake_case columns map them on the translator:
`WithFilterOptions(filter.WithFieldMapping(map[string]string{"createdAt": "created_at"}))`.

For partial registration use `SelectTimestampFilters(names ...string)`:

```go
filterparser.WithCustomFunctions(functions.SelectTimestampFilters(
    functions.TimestampFuncCreateAfter,
    functions.TimestampFuncUpdateAfter,
))
```

Unknown names are silently skipped — pass the `TimestampFunc*` constants to avoid typos.

`BetweenFilter()` exposes a generic range predicate `between(field, lo, hi)` → `field >= lo && field <= hi`. The first argument must be an identifier;
the bounds can be any literal or expression the translator accepts.

```go
filterparser.WithCustomFunctions(functions.BetweenFilter())
// CEL: between(age, 18, 65)
// CEL: between(price, 100.0, 500.0)
```

`SoftDeleteFilters()` exposes the canonical soft-delete predicates keyed off `deletedAt`:

```go
filterparser.WithCustomFunctions(functions.SoftDeleteFilters())
// notDeleted()  → deletedAt == null
// onlyDeleted() → deletedAt != null
```

Equality with `null` matches both missing and explicitly null values under MongoDB and the in-memory evaluator. If your storage needs a stricter
"absent" check, use `!has(deletedAt)` directly instead.

## Construction errors

`TextSearch(nil)` returns an expander that reports `filter.ErrInvalidOptions` when called without configured fields. It does not panic.

## Text search options

Use `TextSearch([]string{"name"}, WithOperator(filter.OpContains))` for backends that support substring search without regex.
Timestamp and soft-delete helpers are opt-in presets. `CompareField` and `Constant` accept arbitrary field names and operators directly.

## Options

| Option                | Type     | Default          | Behavior                                                   |
|-----------------------|----------|------------------|------------------------------------------------------------|
| `WithCaseInsensitive` | `Option` | true             | Enable case-insensitive text-search expansion.             |
| `WithOperator`        | `Option` | filter.OpMatches | Choose the string predicate used by text-search expansion. |

## Named values

| Constant                    | Meaning        |
|-----------------------------|----------------|
| `BetweenFunc`               | "between"      |
| `SoftDeleteFuncNotDeleted`  | "notDeleted"   |
| `SoftDeleteFuncOnlyDeleted` | "onlyDeleted"  |
| `TimestampFieldCreatedAt`   | "createdAt"    |
| `TimestampFieldUpdatedAt`   | "updatedAt"    |
| `TimestampFieldDeletedAt`   | "deletedAt"    |
| `TimestampFuncCreateAfter`  | "createAfter"  |
| `TimestampFuncCreateBefore` | "createBefore" |
| `TimestampFuncUpdateAfter`  | "updateAfter"  |
| `TimestampFuncUpdateBefore` | "updateBefore" |
| `TimestampFuncDeleteAfter`  | "deleteAfter"  |
| `TimestampFuncDeleteBefore` | "deleteBefore" |

## API

| Symbol   | Purpose                                                         |
|----------|-----------------------------------------------------------------|
| `Option` | Configures `TextSearch`: `WithOperator`, `WithCaseInsensitive`. |

## Related packages

- [filter](../README.md)
- [filter/evaluator](../evaluator/README.md)
- [filter/filtertest](../filtertest/README.md)
- [filter/parser](../parser/README.md)
- [filter backends](../README.md#backends)
- [filter guide](../../docs/filter.md)
