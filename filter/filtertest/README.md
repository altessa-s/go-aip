# filtertest

```go
import "github.com/altessa-s/go-aip/filter/filtertest"
```

Test kit for [`filter`](../): filter-parsing and policy helpers for tests of filter consumers (translators, storages, listing engines), and
the conformance suite every filter translator and the evaluator run.

`AnyFieldPolicy(t, opts...)` builds a translation-test policy with `WithAnyField(true)` first, then the supplied policy options,
and returns storage options separately for a backend's `WithFilterOptions`. Explicit allow-lists and untrusted-input guards still apply.
It accepts only `filter.PolicyOption` and `filter.TranslatorOption`; unexpected options fail the test.

## Contract

Test helpers parse expressions, build policies and report failures through testing.TB. They use the public parser and policy APIs; production callers
should construct and configure their own parser and policy.

## Key types

| Symbol                                                | Description                                                                                           |
|-------------------------------------------------------|-------------------------------------------------------------------------------------------------------|
| `MustParseFilter(tb, expr)`                           | Parses a filter expression, failing the test on error                                                 |
| `AnyFieldPolicy(tb, opts...)`                         | A translation-test policy (`WithAnyField(true)` first) and the storage options of `opts`              |
| `MustPolicy(tb, opts...)`                             | Builds a filter policy, failing the test on error                                                     |
| `RunConformance(t, backend)`                          | Runs the filter conformance suite against one translator or evaluator                                 |
| `Backend`                                             | A backend under the suite: constructor, optional in-process `Match` and its `Reference`, expectations |
| `TranslateFunc`, `MatchFunc`                          | A rendered translation; a per-record match of a filter                                                |
| `Outcome`                                             | Expected outcome of one case: `Want` (golden rendering) or `Err` (sentinel), and `Known` for a gap    |
| `ConformanceCases()`, `SharedConformanceCases()`      | Names of the backend-specific cases `Backend.Expect` must hold; names of the shared policy cases      |
| `ConformanceRecords()`                                | The five records the cases select from, for backends that run their output in-process                 |
| `ConformanceFieldKinds()`, `ConformanceArrayFields()` | Declared kinds and array fields of the suite's policy, for backends that need a schema (RediSearch)   |
| `FormatSQL(query, args)`, `FormatValue(v)`            | Renders a parameterized query with its typed arguments as one golden string                           |
| `FormatMatches(match, node)`                          | Renders the records a filter selects (`"1 3"`, `"none"`, `"!2"` for a record whose evaluation fails)  |

## Conformance suite

`RunConformance` builds the backend once per case through `Backend.New` (the case's policy plus `filter.TranslatorOption` storage options passed
through the translator's `WithFilterOptions`), translates the case's filter and compares the rendered output with the expectation. For every case
it also checks that `Translate` is deterministic (two calls, same output) and leaves the caller's node unchanged, and once that a nil policy
fails with `filter.ErrPolicyRequired`. A constructor error is the outcome of the case, so a backend that rejects a policy (a decimal declaration
it cannot bind exactly) declares that sentinel.

### Policy and fields

Every case starts from one policy: untrusted input with an allow-list of the record fields, declared kinds (`ConformanceFieldKinds`), the array
field `tags`, the enum `status` (0, 1, 2), safe regular expressions, and a field access policy that denies `secret` although the allow-list
names it. Decimal cases also declare `price` as `filter.FieldKindDecimal`; limit and regex cases add one option. Two cases add a field mapping
(`alias` to `nickname`, `address.city` to `location.city`).

| Field           | Kind                    | Records                                                              |
|-----------------|-------------------------|----------------------------------------------------------------------|
| `name`          | string                  | alice, bob, carol, Dave (capital), erin                              |
| `age`, `status` | int (status is an enum) | 30 25 41 19 30; 1 0 2 1 2                                            |
| `score`         | float                   | 1.5 2.5 0.5 3.0 1.5                                                  |
| `active`        | bool                    | true false true false true                                           |
| `created`       | timestamp               | 2024-03-01, 2023-06-15, 2025-01-01, 2022-12-31T23:59:59Z, 2024-03-01 |
| `tags`          | string array            | [red blue], [green], [] , [red], absent                              |
| `address.city`  | string (nested)         | Oslo, Bern, carol (equal to name), Lima, Kyiv                        |
| `data`          | bytes                   | "ab", "x", "", "dave", "ab"                                          |
| `nickname`      | string                  | al, bobby, carol, D, e                                               |
| `note`          | string, nullable        | "x", null, absent, "", "y"                                           |
| `price`         | decimal (decimal cases) | 10.50, 3.1, 7, 0.1, 10.5                                             |
| `secret`        | string, denied          | s1 … s5                                                              |

### Cases

Backend-specific rows (`ConformanceCases()`), whose outcome each backend declares in `Backend.Expect` — a missing or unknown name fails the
suite, so a new case makes every backend declare its behavior:

| Group               | Cases                                                                                                                                                                                                                                                     |
|---------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Comparisons         | `==`, `!=`, `<`, `<=`, `>`, `>=` on strings, ints and floats; a float literal on an int field; an int literal on a float field; a negative literal; ordered strings; bools (`== true`, bare, negated bare); an enum value; a nested field; field mappings |
| Presence and null   | `has()` on a nested field; `== null`, `!= null`; `> null`; an ordered comparison of a nullable field; `== ""`                                                                                                                                             |
| Membership          | `in` with ints, strings, an empty list, a list holding `null`; `!(… in …)`; `"x" in` a non-array field                                                                                                                                                    |
| String functions    | `contains`, `startsWith`, `endsWith` with literals (including LIKE and glob wildcard characters and the empty string) and with field arguments; case sensitivity; `functions.TextSearch` (plain and case-insensitive)                                     |
| Regular expressions | `matches()` with a character class and with an end anchor                                                                                                                                                                                                 |
| size(), substring() | `x.size()` and `size(x)` on a string and on an array (one element, empty or absent); `substring(i, j)`, `substring(i)`, ordered, reversed range                                                                                                           |
| Typed literals      | timestamps (greater, equal, a fractional second), a `time.Duration` literal (built node), bytes (equal, ordered), decimals (greater, equal, trailing zeros, a decimal string, `in`, not a number)                                                         |
| Array fields        | `tags == x`, `!=`, `"x" in tags`, `tags in [...]`, `tags.startsWith()`, `tags < x`, `tags == null`                                                                                                                                                        |
| Logic               | `&&`, `\|\|`, precedence of `&&` over `\|\|`, parentheses, `!` over a comparison, over `&&` and over `\|\|`, double negation, a bare literal, the nil filter                                                                                              |
| Three-valued logic  | an unknown predicate on a null or absent `note` (`note < "m"`) under `\|\|` and `&&` in either order, under `!` (alone, over `\|\|`, over `&&`); `!` over `size()` of null, a null string-function argument and a `substring()` out of range              |
| Field operands      | `a == b`, `a < b`, `a in [b, "x"]`, operands of incompatible declared kinds                                                                                                                                                                               |

Shared rows (`SharedConformanceCases()`), with one outcome for every backend: a field outside the allow-list, a denied field (as an operand and as
a function argument) — `ErrFieldNotAllowed`; an enum value outside the declared set — `ErrEnumValueNotAllowed`; a literal of the wrong kind and
an element literal of the wrong kind for an array — `ErrFieldTypeMismatch`; an invalid, an unsafe and a too long regular expression —
`ErrInvalidRegex`; `matches()` without `WithSafeRegex` under untrusted input — `ErrFunctionNotAllowed`; depth and operation limits —
`ErrMaxDepthExceeded`, `ErrMaxOperationsExceeded`. A backend lists a shared case in `Expect` only with `Known` set, to pin a deviation.

### Golden renderings and semantic checks

Each backend renders its output as one string, compared exactly:

| Backend                       | Rendering                                                           | Semantic check                                                           |
|-------------------------------|---------------------------------------------------------------------|--------------------------------------------------------------------------|
| postgres, mariadb, clickhouse | `FormatSQL(query, args)`                                            | golden only                                                              |
| mongo                         | `TranslateD` as relaxed Extended JSON (`Translate` must fail alike) | golden only                                                              |
| redis, meili                  | the query string                                                    | golden only                                                              |
| lua                           | the Lua expression                                                  | run with gopher-lua on every record, compared with the default evaluator |
| evaluator (default)           | `FormatMatches`: the records it selects                             | the golden selection is the hand-checked reference semantics             |
| evaluator (MongoDB profile)   | `FormatMatches`                                                     | compared with the default evaluator                                      |

When `Backend.Match` is set (it runs the backend's own output against each record, converted to the backend's storage form), the suite compares
the records it selects with `Backend.Reference` for every case that translates; a record whose evaluation fails counts as no match on both sides.

### Known gaps

`Outcome.Known` marks an outcome the backend produces today although it is wrong — a silent wrong result, a sentinel that differs from the other
backends, a lazily reported error. The suite still pins the outcome (`Err` or `Want`), tolerates a disagreement with the reference and logs the gap
(`go test -v` prints `known gap: …`), so it stays visible until the backend is fixed and the entry updated. A backend names its gaps as `known…`
constants next to its expectations (its `filter_conformance_test.go` or the `FilterConformanceBackend` of its `<backend>test` package). One is
deliberate: the evaluator's MongoDB profile mirrors the MongoDB translator, whose `$nor` matches a missing field where the reference keeps a negated
unknown predicate unknown (`knownMongoNegation`).

### Adding a case

Add a row to `conformanceCases` (a name, an expression or a built node, optional policy, parser and translator options). Every backend then fails
with `Expect["…"]: missing outcome` and a ready-to-paste `observed: "…": {Want: …}` line; check each observed outcome against the reference
selection of the evaluator before pasting it, and mark disagreements with `Known`.

```go
filtertest.RunConformance(t, filtertest.Backend{
	New: func(policy *filter.Policy, opts ...filter.TranslatorOption) (filtertest.TranslateFunc, error) {
		tr, err := postgres.NewFilterTranslator(policy, postgres.WithFilterOptions(opts...))
		if err != nil {
			return nil, err
		}

		return func(node filter.Node) (string, error) {
			query, args, err := tr.Translate(node)
			return filtertest.FormatSQL(query, args), err
		}, nil
	},
	Expect: map[string]filtertest.Outcome{
		"equal string": {Want: `"name" = $1 | ["alice"]`},
		"matches":      {Want: `"name" ~ $1 | ["^[ab]"]`},
		"in with null": {Want: `("note" IN ($1) OR "note" IS NULL) | ["x"]`},
		// ... every name of filtertest.ConformanceCases()
	},
})
```

## Related packages

- [filter](../README.md)
- [filter/evaluator](../evaluator/README.md)
- [filter/functions](../functions/README.md)
- [filter/parser](../parser/README.md)
- [filter backends](../README.md#backends)
- [orderby/orderbytest](../../orderby/orderbytest/README.md), [projection/projectiontest](../../projection/projectiontest/README.md)
- [filter guide](../../docs/filter.md)
