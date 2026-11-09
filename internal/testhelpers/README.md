# testhelpers

```go
import "github.com/altessa-s/go-aip/internal/testhelpers"
```

Package `testhelpers` provides the fixtures shared by the tests of several packages of the root module. Internal to the root module: the
backend and test-only modules never import it (`TestLayering` rule 5) and keep the few helpers they need in their own test files. Helpers
accepting `testing.TB` register cleanup automatically.

## Key helpers

| Helper                                | Purpose                                                                                          |
|---------------------------------------|--------------------------------------------------------------------------------------------------|
| `ApplyLeafPaths`                      | Copies update-mask leaf paths between protobuf messages, as a storage update by leaf paths would |
| `NewRecordingMeter`, `RecordingMeter` | `metrics.Meter` counting completed timings and increments, and help texts, by full metric name   |
| `ExportedErrors`                      | Lists the exported `Err*` variables and error types of a source tree, as `<package dir>.<name>`  |

Fixtures tied to a capability live with it: `filter/filtertest`, `orderby/orderbytest`, `pagination/pagetoken/pagetokentest`,
`listing/listingtest` and `backend/sqlbase/sqlbasetest` (the scripted `database/sql` driver); the protobuf messages the field mask and field
behavior tests use are generated in `internal/proto`. Keep fixtures used only within one package tree in that tree's test files.
