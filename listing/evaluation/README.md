# evaluation

```go
import "github.com/altessa-s/go-aip/listing/evaluation"
```

Portable proto3 listing semantics for `filter/evaluator`. Required values, optional scalar zero values, empty arrays, and nullable presence fields
retain the listing contract. Construct a profile with explicit schema metadata and a `FieldReader`, then supply it through `evaluator.WithProfile`.
Prepared programs snapshot the AST. Unbound profiles and programs are safe for concurrent use. This profile does not use `evaluator.ValueDecoder`;
adapt storage through the reader.

## Contract

`New(schema, reader)` builds a filter/evaluator profile for the listing contract without a database driver. `FieldReader` resolves stored values;
`NewReader(encoding, maxWork)` supplies bounded dotted traversal with optional storage conversion.

Required scalars, implicit Optional zeros, empty arrays, and nullable Presence fields keep their distinct semantics. Timestamp normalization follows
`Schema.TimestampPrecision`; regex matchers come from the filter policy. Compilation snapshots the AST. This profile does not accept an
`evaluator.ValueDecoder` (`evaluator.WithValueDecoder`): evaluation fails with `filter.ErrInvalidOptions`.

`Profile.BindBudget` binds predicate execution to a request-owned budget. A custom `BudgetedReader` binds nested field work to the same budget. Bound
instances follow the budget's concurrency guarantees; unbound compiled programs can be shared.

## Compilation

`CompileEvaluator` does everything that depends only on the filter and the schema once: it resolves every field, without copying
the enum map that `Schema.Field` copies, and computes each field's zero value. It converts literal operands, list elements and
decimal operands, and compiles `matches()` patterns. Evaluating a record then only reads and compares values. Each evaluation keeps
its work budget on the stack, so it allocates nothing beyond the values it reads. Errors that depend on the shape of the AST are
reported when the record is evaluated, at the same point and after the same reads and budget charges as before compilation.

Comparisons avoid exact rational arithmetic when it is not needed. Two int64 values, two finite float64 values, or an int64 within
±2^53 and a finite float64 are compared directly. Larger integers, uint64 values and decimals take the exact path. Values that are
already in their normal form (string, bool, int64, uint64, float64, decimal, UTC time, bytes) are passed through without reflection
and without being boxed again. `Reader.Lookup` walks a dotted path without splitting it into a slice. It indexes `map[string]any`
and `map[string]string` directly, and without a storage encoding its work budget never leaves the stack.

## Limits

The reader bounds each lookup. The compiled program bounds predicate visits and existential array comparisons using the shared evaluator's data
budget. Memory listing additionally bounds scanned and materialized records and sorting work. Storage encodings must charge their supplied budget.

## Shared budgets

| API                          | Purpose                                                                  |
|------------------------------|--------------------------------------------------------------------------|
| `Profile.BindBudget(budget)` | Bind predicate evaluation and supporting field reads to a request budget |
| `Reader.BindBudget(budget)`  | Charge both the per-lookup limit and the supplied request budget         |
| `BudgetedReader`             | Let a custom reader bind its nested work to the supplied budget          |

Binding returns a copy; it does not mutate a reusable profile or reader. Bound instances have the concurrency guarantees of the supplied budget.
The memory engine binds one budget per request. A custom reader must implement `BudgetedReader` to account for its own nested traversal.

Timestamp comparisons and stored timestamp arrays use `Schema.TimestampPrecision()`. Regex matchers are compiled once per prepared program through
the filter policy, including custom regex compilers. Decimal comparisons preserve exact values without expanding extreme exponents.

Decimal comparisons and membership use the filter policy's decimal parser, including custom literal spellings. String predicates charge
both input lengths to the work budget. Array evaluation reuses the normalized element slice within each field read.

Array normalization charges one unit per element before allocation, plus pointer-unwrapping work. Predicate scans charge their own work;
field access does not charge the same array length again. An unbound evaluator can normalize values without a request budget.

## API

| Symbol              | Purpose                                                                                                                                                                                                                 |
|---------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `FieldReader`       | FieldReader resolves stored values. Reusable implementations must be concurrency safe; a request-bound reader follows its budget's concurrency guarantees.                                                              |
| `Profile`           | Profile supplies the portable proto3 listing contract to filter/evaluator. Required fields, zero-valued optional fields, arrays and nullable presence fields retain the same semantics regardless of the backing store. |
| `New`               | New constructs an explicit listing evaluation profile from metadata and a reader.                                                                                                                                       |
| `Reader`            | Reader resolves dotted paths through Go maps, pointers and optional storage encodings. It does not choose predicate semantics or a database profile.                                                                    |
| `NewReader`         | NewReader constructs a bounded reader. A non-positive limit uses the evaluator default.                                                                                                                                 |
| `BudgetedReader`    | BudgetedReader is a FieldReader that binds its nested work to a request budget (`BindBudget`).                                                                                                                          |
| `ErrNonFiniteFloat` | ErrNonFiniteFloat reports a stored NaN or infinity in a float field.                                                                                                                                                    |

| API                            | Purpose                                                              |
|--------------------------------|----------------------------------------------------------------------|
| `New(schema, reader)`          | Create a reusable evaluation profile                                 |
| `FieldReader`                  | Inject concurrency-safe stored-field lookup                          |
| `NewReader(encoding, maxWork)` | Read Go maps, pointers, and arrays with an optional storage encoding |
| `Profile.CompileEvaluator`     | Compile an AST for the shared evaluator                              |
| `Profile.ValidatePolicy`       | Accept any filter policy: the schema owns contract validation        |
| `Reader.Lookup(data, path)`    | Resolve a dotted path within the reader's work limit                 |

## Errors

| Error                                       | Cause                                                                                                                  |
|---------------------------------------------|------------------------------------------------------------------------------------------------------------------------|
| `ErrNonFiniteFloat`                         | A float field (scalar or array element) stores NaN or an infinity; the message names the field. It is a storage fault. |
| `filter.ErrDataLimitExceeded`               | Predicate evaluation or a field read exhausted the work budget.                                                        |
| `filter.ErrInvalidExpression`               | An AST shape `filter.Validate` rejects; `CompileEvaluator` validates first, so a compiled program never returns it.    |
| `filter.ErrInvalidOptions`                  | The evaluator was given an `evaluator.ValueDecoder`; adapt storage through the reader instead.                         |
| decimal parser errors, `FieldReader` errors | Returned unchanged from the filter policy's decimal parser and the reader.                                             |

## Related packages

- [listing](../README.md)
- [listing guide](../../docs/listing.md)
