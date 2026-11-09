# clickhousetest

```go
import "github.com/altessa-s/go-aip/backend/clickhouse/clickhousetest"
```

Test fixtures of the [ClickHouse backend](../), shared by its unit tests and by the live ClickHouse suite of the test-only module
[`integration/docker`](../../../integration/docker/), so the expectation tables exist once. The package imports no database driver.

| Symbol                       | Returns                                                                                                                                                                 |
|------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `FilterConformanceBackend()` | `filtertest.Backend` with `New` (the scalar `clickhouse.NewFilterTranslator`, rendered with `filtertest.FormatSQL`) and `Expect` (the outcome of every capability case) |
| `DecodeColumns(cols)`        | Reads a column list rendered by the ClickHouse projection translator back into a `projectiontest.Result` (`""` is `All`)                                                |

The unit test `TestConformance` runs `FilterConformanceBackend()` as is. The live run adds a `Reference` (the default evaluator) and a `Match`
executing the translated SQL on the server over `filtertest.ConformanceRecords`, so every case also checks that ClickHouse selects the rows the
evaluator selects.

```go
filtertest.RunConformance(t, clickhousetest.FilterConformanceBackend())
```
