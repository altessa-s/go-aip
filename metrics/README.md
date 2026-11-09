# metrics

```go
import "github.com/altessa-s/go-aip/metrics"
```

The consumer-side metrics API of go-aip: the few instruments the parsers and translators record, declared here so the library depends on no
metrics backend. A component that records metrics accepts a `Meter` through its `WithCollector` option (a kind-prefixed
`With<Kind>Collector` in the backends); without one it uses `Noop`, whose instruments do nothing.

## Recorded metrics

| Component                                                                             | Instrument                                    |
|---------------------------------------------------------------------------------------|-----------------------------------------------|
| [filter/parser](../filter/parser), [orderby](../orderby), [projection](../projection) | Parse duration `Timer`, parse error `Counter` |
| [backend](../filter/README.md#backends) filter, order and projection translators      | Translation duration `TranslateTimer`         |

Every metric is created through `Meter.WithSubsystem`, so its full name is `{namespace}_{subsystem}_{name}` with the namespace chosen by the
backend: the filter parser records `parse_duration_seconds` and `parse_errors_total` under subsystem `filter` (`orderby` and `projection`
for the others), a translator `translate_duration_seconds` under `filter_<backend>`, `orderby_<backend>` or `projection_<backend>`. The SQL
order and projection translators use the dialect name as the backend (`postgres`, `mariadb`, `clickhouse`), as the filter translators do.

## Key symbols

| Symbol           | Purpose                                                                                                         |
|------------------|-----------------------------------------------------------------------------------------------------------------|
| `Meter`          | Creates instruments: `Counter`, `Timer`, `MustCounter`, `MustTimer`, and `WithSubsystem`; carries no lifecycle  |
| `Counter`        | Monotonic counter: `Inc`, `Add` (a negative delta is ignored)                                                   |
| `Timer`          | Histogram of seconds: `Start` returns the function that records the elapsed time; `ObserveDuration` records one |
| `MetricOpts`     | `Name` (required, e.g. `parse_errors_total`) and `Help` of a counter                                            |
| `HistogramOpts`  | Options of a timer; embeds `MetricOpts`, bucket boundaries are left to the backend                              |
| `Noop`           | The shared no-op `Meter`; its `WithSubsystem` returns itself                                                    |
| `TranslateTimer` | `translate_duration_seconds` of one translator (`NewTranslateTimer`); a nil one records nothing                 |

An implementation must be safe for concurrent use. `Must*` methods panic when an instrument cannot be created; the others return a usable
(possibly no-op) instrument.

## Adapting a backend

The method names, option structs and semantics match the subset of the go-atlas `observability/metrics` API the packages use, so a go-atlas
collector (or any Prometheus or OpenTelemetry wrapper) is adapted with a thin forwarding type: `Counter` and `Timer` convert the options field by
field, `WithSubsystem` wraps the returned meter again, and the instruments forward `Inc`, `Add`, `Start` and `ObserveDuration` unchanged.

From `Example_adapter` ([adapter_example_test.go](adapter_example_test.go)), where the `backend*` types stand for the metrics package being adapted
(its own option structs, and instruments that already have `Inc`/`Add` and `Start`/`ObserveDuration`):

```go
type meter struct{ m backendMeter }

func counterOpts(o metrics.MetricOpts) backendOpts { return backendOpts{Name: o.Name, Help: o.Help} }

func timerOpts(o metrics.HistogramOpts) backendHistogramOpts {
    return backendHistogramOpts{backendOpts: counterOpts(o.MetricOpts)}
}

func (a meter) Counter(o metrics.MetricOpts) metrics.Counter     { return a.m.Counter(counterOpts(o)) }
func (a meter) Timer(o metrics.HistogramOpts) metrics.Timer      { return a.m.Timer(timerOpts(o)) }
func (a meter) MustCounter(o metrics.MetricOpts) metrics.Counter { return a.m.Counter(counterOpts(o)) }
func (a meter) MustTimer(o metrics.HistogramOpts) metrics.Timer  { return a.m.Timer(timerOpts(o)) }
func (a meter) WithSubsystem(s string) metrics.Meter             { return meter{a.m.WithSubsystem(s)} }

p, err := parser.New(parser.WithCollector(meter{m: backend}))
```

The example records `app_filter_parse_duration_seconds` for every parse and `app_filter_parse_errors_total` for a failed one. A go-atlas
collector's `MustCounter` and `MustTimer` map to its own `Must*` methods.
