# parser

```go
import "github.com/altessa-s/go-aip/filter/parser"
```

Parses CEL into the shared [filter AST](../). Owns parsing limits, caching, enum-name rewriting and an explicitly configured instance function
registry.

## Contract

`New(opts...)` builds a parser with its own CEL environment and function handlers; there is no global registration step or implicit preset registry.
Supply expansions through `WithCustomFunctions`, including helpers from the sibling functions package. Named builtins can only be replaced with
`WithAllowBuiltinOverride(true)`.

`Parse(ctx, expression)` returns caller-owned nodes. `ParseShared` explicitly shares a read-only cached AST; callers must not mutate it. Extensions
are lowered before snapshots enter the cache. `WithNoCache(true)` disables caching; the default cache holds at most 1,000 expressions and about 8 MiB
of ASTs (`WithCacheBytes`), with a 4,096-byte input limit.

The parser refuses a field that is not a dotted path of plain identifiers with `filter.ErrInvalidExpression`: a CEL backtick escape whose content
is not a plain identifier (``a.`b c` ``, ``a.`b.c` ``) and a root-scoped name (`.a`).

## Cache

The cache is an approximate LRU (CLOCK eviction) bounded by entry count (`WithCacheSize`) and by the approximate bytes of the cached ASTs
(`WithCacheBytes`, estimated per node plus names and literal payloads, on the high side of the real heap). A 4,096-byte filter can parse to an AST
of about 100 KB, so the byte bound is what caps the memory a client can pin with distinct filters. An AST above a sixteenth of the byte budget is
returned but not cached, so one huge filter cannot flush the cache. A lookup takes no lock, so cache hits scale with cores.

On a hit, `Parse` copies the cached AST into one backing array per node kind (a handful of allocations instead of one per node); the copy shares
no node, child slice or byte literal with the cache. `ParseShared` skips the copy and is the fast path for callers that only read the tree.

Deterministic parse failures (syntax errors, refused fields or functions, failing custom functions) are remembered in a separate negative cache of
128 entries and 64 KiB of error text, keyed by a per-parser seeded hash and the length of the expression, so a repeated invalid filter returns the
same error without being parsed again; an error longer than 1 KiB is returned but not cached, and a failed parse never enters the AST cache. A
filter the parser accepts but a policy later rejects is cached like any other parse: the parser does not know the policy, and the byte bound limits
what such filters can hold. Custom functions and the timestamp parser (`WithTimestampParser`) must be deterministic, since their results and
failures are both cached.

`WithAllowedFunctions` restricts named functions, including custom handlers. Operators and the `has` macro remain grammar. `WithEnvironmentOptions`
extends the instance environment, and `WithTimestampParser` replaces RFC 3339 timestamp decoding.

`WithEnumNames` maps public enum names to immutable stored values. Integers that fit normalize to int64; mutable references are rejected. Ordered enum
comparisons require `WithOrderedEnums(true)`. Parsing does not replace translator authorization or backend compatibility checks.

## Key types

| Type     | Purpose                                 |
|----------|-----------------------------------------|
| `Parser` | Concurrent parser; construct with `New` |
| `Option` | Parser configuration                    |

## Ownership

`Parse` returns a caller-owned deep copy, including byte literals; custom-function results are copied before caching. `ParseShared` returns the cached
AST: callers must not mutate it.
`MustParse` and `MustParseShared` panic on invalid expressions and are intended for fixed application expressions.
A parser snapshots explicitly supplied custom functions at construction. No global registry is consulted.

## Usage

```go
p, err := parser.New(parser.WithNoCache(true))
if err != nil {
    return err
}
node, err := p.Parse(ctx, `name.startsWith("A")`)
```

See [functions](../functions/) for expansion helpers and the [guide](../../docs/filter.md) for CEL syntax and enum names.

## Error causes

A syntax error matches `filter.ErrParseFailed` and reads `line:column: message near "snippet" (and N more)`: the first error CEL reports, its
message cut to 160 bytes, about 24 characters of the expression on each side of it, quoted, and the number of further errors. cel-go itself
renders each of up to 100 errors with the whole source line, so a 4,096-byte filter of mismatched parentheses used to produce about 443 KB of
error text for the API status and the logs; the compact form stays under 1 KiB.

Custom-function and timestamp-parser failures match `filter.ErrInvalidExpression` and retain the injected error for `errors.Is` and `errors.AsType`.

Enum values must be immutable scalars or reference-free arrays and structs. Integers fitting int64 normalize to int64.
Mutable references are rejected at construction so cached ASTs never alias caller-owned state.

## Options

| Option                     | Type     | Default                    | Behavior                                                                                                                                                                                                                               |
|----------------------------|----------|----------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `WithAllowBuiltinOverride` | `Option` | false                      | WithAllowBuiltinOverride allows explicit custom handlers to replace built-in named functions.                                                                                                                                          |
| `WithAllowedFunctions`     | `Option` | nil / unset (all allowed)  | WithAllowedFunctions restricts which CEL functions the parser accepts. The whitelist covers built-in named functions (contains, startsWith, endsWith, matches, size, timestamp) and any handlers registered via `WithCustomFunctions`. |
| `WithCacheBytes`           | `Option` | DefaultCacheBytes          | Bound the approximate bytes of the cached ASTs; an AST above a sixteenth of it is not cached.                                                                                                                                          |
| `WithCacheSize`            | `Option` | DefaultCacheSize           | Set the parsed-expression cache capacity.                                                                                                                                                                                              |
| `WithCollector`            | `Option` | nil / unset                | A `metrics.Meter` to record into; nil disables it.                                                                                                                                                                                     |
| `WithCustomFunctions`      | `Option` | empty map                  | Install per-parser named AST expansions; no global registration is performed.                                                                                                                                                          |
| `WithEnumNames`            | `Option` | empty map                  | Map public enum names to immutable stored values.                                                                                                                                                                                      |
| `WithEnvironmentOptions`   | `Option` | nil / unset                | WithEnvironmentOptions extends the instance CEL environment, including macros.                                                                                                                                                         |
| `WithMaxExpressionLength`  | `Option` | DefaultMaxExpressionLength | Limit input expression length in bytes.                                                                                                                                                                                                |
| `WithNoCache`              | `Option` | false                      | Disable parsed-expression caching when true.                                                                                                                                                                                           |
| `WithOrderedEnums`         | `Option` | false                      | WithOrderedEnums permits ordered comparisons of enum stored values.                                                                                                                                                                    |
| `WithTimestampParser`      | `Option` | RFC 3339 parser            | WithTimestampParser replaces timestamp literal decoding.                                                                                                                                                                               |

`WithCollector` takes a [`metrics.Meter`](../../metrics/README.md) (`github.com/altessa-s/go-aip/metrics`); a go-atlas
`observability/metrics` collector is adapted to it.

## Defaults and limits

| Constant                     | Value   |
|------------------------------|---------|
| `DefaultCacheSize`           | `1000`  |
| `DefaultCacheBytes`          | `8 MiB` |
| `DefaultMaxExpressionLength` | `4096`  |

## API

| Symbol | Purpose                                  |
|--------|------------------------------------------|
| `New`  | New creates a new CEL expression parser. |

## Related packages

- [filter](../README.md)
- [filter/evaluator](../evaluator/README.md)
- [filter/filtertest](../filtertest/README.md)
- [filter/functions](../functions/README.md)
- [filter backends](../README.md#backends)
- [filter guide](../../docs/filter.md)
