# orderbytest

```go
import "github.com/altessa-s/go-aip/orderby/orderbytest"
```

Test kit for [`orderby`](../): order_by parsing and policy helpers for tests of orderby consumers (translators, listing engines), and the
conformance suite every orderby translator runs.

## Contract

Test helpers parse expressions, build policies and report failures through testing.TB. They use the public parser and policy APIs; production
callers should construct and configure their own parser and policy.

## Key types

| Symbol                                | Description                                                                                   |
|---------------------------------------|-----------------------------------------------------------------------------------------------|
| `MustParseOrderBy(tb, expr)`          | Parses an AIP-132 order_by expression, failing the test on error                              |
| `MustPolicy(tb, opts...)`             | Builds an `orderby.Policy` with `orderby.NewPolicy`, failing the test on a construction error |
| `RunConformance(t, backend)`          | Runs the orderby conformance suite against one translator                                     |
| `Backend`, `TranslateFunc`, `Outcome` | A translator under the suite: constructor, rendered output, expected outcome per case         |
| `ConformanceCases()`, `Case*`         | Names of the backend-specific cases `Backend.Expect` must hold                                |
| `ConformanceRecords()`                | The records the case field names refer to, for comparators that render by sorting data        |

## Conformance suite

`RunConformance` builds the translator once per case through `Backend.New` (policy plus `orderby.TranslatorOption` storage options passed
through the translator's `WithOrderOptions`) and compares the rendered `Translate` output with an expectation.

Shared rows, with one outcome for every backend: a field the allow-list refuses, a denied API field and a denied storage path
(`ErrFieldNotAllowed`), two keys mapping to one storage name (`ErrDuplicateKey`), an unknown null placement and a collation that is not a
plain name (`ErrUnsupportedOrdering`), an invalid direction (`ErrInvalidDirection`). It also checks that a nil policy fails with
`ErrPolicyRequired` and that `Translate` leaves the caller's spec unchanged.

Backend rows, whose outcome each backend declares in `Backend.Expect` (a missing or unknown name fails the suite, so a new case makes every
backend declare its behavior): empty spec, ascending, descending, multiple keys, nested path, exact and prefix mapping, default order,
tie-breaker, tie-breaker already present, the four `asc|desc nulls first|last` combinations, and a collation. A capability a backend
cannot express must fail with `orderby.ErrUnsupportedOrdering` or an error wrapping it (`orderby.ErrTooManySortKeys`).

```go
orderbytest.RunConformance(t, orderbytest.Backend{
	New: func(policy *orderby.Policy, opts ...orderby.TranslatorOption) (orderbytest.TranslateFunc, error) {
		tr, err := meili.NewOrderTranslator(policy, meili.WithOrderOptions(opts...))
		if err != nil {
			return nil, err
		}

		return func(spec orderby.Spec) (string, error) {
			out, err := tr.Translate(spec)
			return strings.Join(out, ","), err
		}, nil
	},
	Expect: map[string]orderbytest.Outcome{
		orderbytest.CaseAscNullsLast:  {Want: "age:asc"},
		orderbytest.CaseAscNullsFirst: {Err: orderby.ErrUnsupportedOrdering},
		// ... every name of orderbytest.ConformanceCases()
	},
})
```

## Related packages

- [orderby](../README.md)
- [orderby guide](../../docs/orderby.md)
