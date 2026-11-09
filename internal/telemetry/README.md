# telemetry

```go
import "github.com/altessa-s/go-aip/internal/telemetry"
```

The parse metrics shared by the [filter/parser](../../filter/parser), [orderby](../../orderby) and [projection](../../projection)
parsers. Each parser records the same two instruments under its own subsystem; this package builds them once.

## Contract

This is an internal implementation package. Its exported helpers are available only to packages permitted by Go internal import
rules. Translation duration is public instead: [metrics.TranslateTimer](../../metrics) through `filter.NewTelemetry`,
`orderby.NewTelemetry` and `projection.NewTelemetry`, so custom translators record it under the same names.

## API

| Symbol      | Purpose                                                                                                |
|-------------|--------------------------------------------------------------------------------------------------------|
| `Parser`    | `Duration` (`parse_duration_seconds` timer) and `Errors` (`parse_errors_total` counter) of one parser. |
| `NewParser` | Creates them under a subsystem with help texts naming what is parsed; a nil meter uses `metrics.Noop`. |

| Parser          | Subsystem    | Help noun        |
|-----------------|--------------|------------------|
| `filter/parser` | `filter`     | `CEL expression` |
| `orderby`       | `orderby`    | `order_by`       |
| `projection`    | `projection` | `fields`         |

## Related packages

- [go-aip](../../README.md)
- [metrics](../../metrics/README.md)
