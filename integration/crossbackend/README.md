# integration/crossbackend

```text
module github.com/altessa-s/go-aip/integration/crossbackend
```

Checks that need several storage backends in one test. This is a test-only nested Go module: it is never released or tagged, and it requires
the root module and every backend module except `backend/mongo` through `replace` directives to the checkout, so neither the root module nor
any backend module requires another backend for its tests.

## Running

```sh
go -C integration/crossbackend test -race ./...
```

`make test` runs it with every other module of `MODULES`.

## Contents

The package `crossbackend` holds no code; its test files are named after the package whose behavior they pin:

| Files                                       | Checks                                                                                                                                                                                               |
|---------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `aipmap_test.go`, `exported_errors_test.go` | The code and reason of every backend sentinel, and that the table lists every exported `Err*` of `backend/` (`TestEveryBackendSentinelIsListed`); `backend/mongo` checks its own in `errors_test.go` |
| `filter_*_test.go`                          | One policy shared by every filter translator and the evaluator: allow-lists, untrusted input, the safe-regex subset, budgets, literal and decimal encoders, concurrency                              |
| `listing_*_test.go`                         | Listing profiles, schemas and policies checked by the memory and SQL list engines                                                                                                                    |
| `orderby_*_test.go`, `projection_*_test.go` | One order or projection policy shared by several translators                                                                                                                                         |
| `softdelete_*_test.go`                      | A soft-delete restriction applied by the memory list engine                                                                                                                                          |
| `memory_*_test.go`, `postgres_*_test.go`    | Backend tests that compare with or borrow from another backend                                                                                                                                       |

Behavior shared by every backend belongs in the conformance suites (`filter/filtertest`, `orderby/orderbytest`, `projection/projectiontest`,
`listing/listingtest`), which each backend runs itself; this module is for checks that need several concrete backends in one test. A new
backend module that a check here needs gets a `require` and a `replace` in `go.mod`.
