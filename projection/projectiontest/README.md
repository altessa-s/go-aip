# projectiontest

```go
import "github.com/altessa-s/go-aip/projection/projectiontest"
```

Test kit for [`projection`](../): parsing and policy helpers for tests of projection consumers, and the conformance suite every projection
translator runs.

## Contract

`MustParse` and `MustPolicy` use the public parser and policy APIs and report failures through `testing.TB`; production callers construct and
configure their own parser and policy.

`RunConformance(t, backend)` checks one translator against the behavior every backend shares. The backend supplies a constructor and adapts its
translator with `Adapt(translator, decode)`, where `decode` reads one rendered output (a column list, a BSON document, RETURN arguments, …) back into
a backend-neutral `Result`: `All`, `None`, `Include` or `Exclude` storage paths. Paths are compared as sets after dropping those an included ancestor
covers, so a backend may collapse `address` / `address.city` or keep both.

## Cases

| Case             | Checks                                                                                                                                 |
|------------------|----------------------------------------------------------------------------------------------------------------------------------------|
| `PolicyRequired` | A nil policy fails with `projection.ErrPolicyRequired`                                                                                 |
| `Include`        | A mask renders its mapped storage names plus the required fields                                                                       |
| `AllAuthorized`  | An omitted mask, `*` and FULL render the allow-list roots plus the required fields; `*` wins over a view                               |
| `EveryField`     | No policy restriction renders every field (`All`)                                                                                      |
| `Rejections`     | Unknown and denied paths (`ErrFieldNotAllowed`), indexes (`ErrUnsupportedPath`), grammar (`ErrInvalidFieldPath`)                       |
| `DeniedStorage`  | An alias of a denied storage path is refused; a required field that is denied fails with `ErrConflictingPolicy`                        |
| `Views`          | Default, BASIC and mask-over-view selections; an unknown view is `ErrInvalidView`, even under a mask                                   |
| `Nested`         | Dotted paths render as dotted storage paths                                                                                            |
| `None`           | `ModeNone` renders `None` with `Capabilities.None`, else `ErrUnsupportedSelection`; with required fields it renders them               |
| `Exclusion`      | FULL without an allow-list renders `Exclude` with `Capabilities.Exclusion`, else the constructor fails with `ErrAllFieldsUnresolvable` |
| `Concurrent`     | Concurrent `Translate` calls agree (run with `-race`)                                                                                  |

## Key types

| Symbol                         | Description                                                                      |
|--------------------------------|----------------------------------------------------------------------------------|
| `MustParse(tb, expr, opts...)` | Parses a fields expression with a no-cache parser, failing the test on error     |
| `MustPolicy(tb, opts...)`      | Builds a projection policy, failing the test on error                            |
| `RunConformance(t, backend)`   | Runs the shared translator behavior as subtests                                  |
| `Backend`                      | Constructor and `Capabilities` of the translator under test                      |
| `Capabilities`                 | `Exclusion` and `None`: the optional selections a backend renders                |
| `Adapt(tr, decode)`            | Wraps any `Translate`/`TranslateView` translator as a `Translator`               |
| `Result`                       | Backend-neutral reading of a rendered projection                                 |
| `Translator`                   | The translator under test: `Translate` and `TranslateView` returning a `Result`  |
| `Source[Out]`                  | The `Translate`/`TranslateView` shape of a backend translator that `Adapt` wraps |

## Capabilities of the go-aip translators

| Translator           | `Exclusion` | `None` |
|----------------------|-------------|--------|
| `backend/postgres`   | –           | –      |
| `backend/mariadb`    | –           | –      |
| `backend/clickhouse` | Y           | –      |
| `backend/mongo`      | Y           | –      |
| `backend/redis`      | –           | Y      |
| `backend/meili`      | –           | –      |
| `backend/memory`     | Y           | Y      |

## Related packages

- [projection](../README.md)
- [backend/sqlbase](../../backend/sqlbase/README.md#projection-translator), [backend/memory](../../backend/memory/README.md)
- [filter/filtertest](../../filter/filtertest/README.md)
- [orderby/orderbytest](../../orderby/orderbytest/README.md)
