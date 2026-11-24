# go-aip

```go
import "github.com/altessa-s/go-aip"
```

Helpers for building APIs that follow the [Google API Improvement Proposals](https://google.aip.dev): resource names (AIP-122, AIP-123),
List methods with filtering (AIP-160), ordering (AIP-132), partial responses (AIP-157) and pagination (AIP-158), field masks and field behavior
(AIP-134, AIP-161, AIP-203), freshness, idempotency and validation (AIP-154, AIP-155, AIP-163, AIP-202), reading across collections
(AIP-159), soft delete (AIP-164, AIP-165), errors (AIP-193), states (AIP-216), partial success (AIP-217) and batch methods (AIP-231 to
AIP-235).

The packages provide request contracts for filtering, ordering, projection, listing, pagination and protobuf updates, plus the request checks
and execution helpers of the standard and batch methods. Public contracts are driver-neutral and have no gRPC dependency. The code of one
storage backend (filter, order and projection translators, schemas, evaluator profiles, listing engines and value encodings) lives in its
package under [`backend/`](#backends). The layering and its rules are described in [docs/architecture.md](docs/architecture.md).

## Installation

```sh
go get github.com/altessa-s/go-aip
```

Every storage backend is a module of its own, installed next to the root (see [Modules](#modules)):

```sh
go get github.com/altessa-s/go-aip/backend/postgres
```

Requires Go 1.27 or later.

## Packages

| Package                                                  | AIP                                | Contract                                                                                                                                                                                                                                                                  |
|----------------------------------------------------------|------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| [`filter`](filter/README.md)                             | AIP-160                            | AST, policy and the translator contracts of the backends ([guide](docs/filter.md))                                                                                                                                                                                        |
| [`filter/parser`](filter/parser/README.md)               | AIP-160                            | CEL parser: limits, LRU cache, custom functions, enum value names                                                                                                                                                                                                         |
| [`filter/evaluator`](filter/evaluator/README.md)         | AIP-160                            | In-memory evaluator with bounded work, semantic profiles and value encodings                                                                                                                                                                                              |
| [`filter/functions`](filter/functions/README.md)         | AIP-160                            | AST expansions for custom filter functions                                                                                                                                                                                                                                |
| [`filter/regexanchor`](filter/regexanchor/README.md)     | AIP-160                            | RE2 end-of-text anchors rewritten for PCRE and ICU backends (`matches()`)                                                                                                                                                                                                 |
| [`fieldpolicy`](fieldpolicy/README.md)                   | AIP-132, AIP-157, AIP-160          | Shared field access, allowlists, and exact or prefix storage mapping                                                                                                                                                                                                      |
| [`orderby`](orderby/README.md)                           | AIP-132                            | Ordering, resolution, and backend capability checks ([guide](docs/orderby.md))                                                                                                                                                                                            |
| [`projection`](projection/README.md)                     | AIP-157, AIP-161                   | Read masks and views, and the projection contract of the backends ([guide](docs/projection.md))                                                                                                                                                                           |
| [`projection/protopath`](projection/protopath/README.md) | AIP-161                            | Read mask paths validated against the resource message descriptor                                                                                                                                                                                                         |
| [`listing`](listing/README.md)                           | AIP-132, AIP-157, AIP-158, AIP-160 | Immutable schema plus profile and the list engine contract; engines live in `backend/` ([guide](docs/listing.md))                                                                                                                                                         |
| [`listing/evaluation`](listing/evaluation/README.md)     | AIP-160                            | Portable proto3 listing semantics as a driver-independent evaluator profile                                                                                                                                                                                               |
| [`pagination/pagetoken`](pagination/pagetoken/README.md) | AIP-158                            | Signers, rotation, encrypted tokens, typed position codecs, and pagers ([guide](docs/pagination.md))                                                                                                                                                                      |
| [`pagination/page`](pagination/page/README.md)           | AIP-158                            | Driver-free `Page[T]` result                                                                                                                                                                                                                                              |
| [`fieldmask`](fieldmask/README.md)                       | AIP-161                            | Field mask type, path grammar, Filter/Select/Prune/Resolve ([guide](docs/fieldmask.md))                                                                                                                                                                                   |
| [`fieldmask/update`](fieldmask/update/README.md)         | AIP-134, AIP-154, AIP-163, AIP-203 | Update masks under field behaviors, `Spec.Plan`, `Merge`, `Patch` with etag preconditions                                                                                                                                                                                 |
| [`fieldmask/extract`](fieldmask/extract/README.md)       | AIP-134, AIP-157                   | Reading `update_mask`/`read_mask` off request messages, extractor chains                                                                                                                                                                                                  |
| [`fieldbehavior`](fieldbehavior/README.md)               | AIP-202, AIP-203                   | Resolver-based payload behavior enforcement ([guide](docs/fieldbehavior.md))                                                                                                                                                                                              |
| [`fieldinfo`](fieldinfo/README.md)                       | AIP-202                            | `google.api.field_info` formats (UUID4, IPV4, IPV6, IPV4_OR_IPV6): validation, RFC normalization, value comparison, message walk with field paths, custom formats (e.g. email) ([guide](docs/fieldinfo.md))                                                               |
| [`resourcename`](resourcename/README.md)                 | AIP-122, AIP-123, AIP-159          | Compiled resource name patterns, resource types from descriptors, request name/parent validation with the `-` wildcard ([guide](docs/resourcename.md))                                                                                                                    |
| [`etag`](etag/README.md)                                 | AIP-154                            | RFC 9110 entity tags (parse, revision and content-hash tags), Update/Delete/custom method preconditions (ABORTED on mismatch), etag field read/stamp ([guide](docs/etag.md))                                                                                              |
| [`requestid`](requestid/README.md)                       | AIP-155                            | Request ID validation (UUID / UUID4 / ASCII), deduplication guard with fingerprinting, scoping, leases and retention; `Store` interface, in-memory store and the `requestidtest` conformance suite ([guide](docs/requestid.md))                                           |
| [`validateonly`](validateonly/README.md)                 | AIP-163                            | Run every check of a mutation and skip the write when `validate_only` is set ([guide](docs/validateonly.md))                                                                                                                                                              |
| [`softdelete`](softdelete/README.md)                     | AIP-164, AIP-165                   | Soft delete/undelete (`delete_time`/`purge_time` stamping, Get/List `show_deleted` rules as a listing server restriction (`Policy.Restriction`) or filter AST predicate) and Purge request validation with dry-run count/sample ([guide](docs/softdelete.md))             |
| [`apierror`](apierror/README.md)                         | AIP-193                            | `google.rpc.Status` errors with validated `ErrorInfo` and standard details, HTTP/JSON rendering, and a rule-based mapper; no gRPC dependency ([guide](docs/apierror.md))                                                                                                  |
| [`apierror/aipmap`](apierror/aipmap/README.md)           | AIP-193                            | The `apierror` mapper rules (code and reason) for the sentinels of every go-aip package and of go-atlas-core                                                                                                                                                              |
| [`state`](state/README.md)                               | AIP-216                            | Transition tables for generated state enums, state-changing method core (FAILED_PRECONDITION), state field/enum/method descriptor lint ([guide](docs/state.md))                                                                                                           |
| [`unreachable`](unreachable/README.md)                   | AIP-159, AIP-217                   | Unreachable resources: name set with limits and up-scoping, `return_partial_success`, bounded-concurrency fan-out runner, descriptor lint ([guide](docs/unreachable.md))                                                                                                  |
| [`batch`](batch/README.md)                               | AIP-231, AIP-233, AIP-234, AIP-235 | Batch request checks (size limit, parent and hoisted-field consistency, duplicates, per-item validation with indexed field errors), atomic and partial-success execution in request order, atomic Batch Get with NOT_FOUND on a missing resource ([guide](docs/batch.md)) |
| [`metrics`](metrics/README.md)                           | –                                  | The `Meter` the parsers and translators record into; `Noop` by default                                                                                                                                                                                                    |
| [`decimal128`](decimal128/README.md)                     | –                                  | IEEE 754-2008 decimal128 representability, canonical form and coefficient bounds of `decimal.Decimal`: the decimal bounds of `filter` and the MongoDB decimal codec                                                                                                       |

### Backends

Each backend is one package under `backend/` and a Go module of its own; its types and options are named by kind
(`NewFilterTranslator`/`FilterOption`/`WithFilter…`, `NewOrderTranslator`/`OrderOption`/`WithOrder…`,
`NewProjectionTranslator`/`ProjectionOption`/`WithProjection…`, `NewListEngine`/`ListEngineOption`/`WithList…`,
`NewSchema`/`SchemaOption`/`WithSchema…`).

| Package                                                        | Contents                                                                                                                                                                                                                                                                                           |
|----------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| [`backend/sqlbase`](backend/sqlbase/README.md)                 | Shared SQL: predicate builder, SQL field schema, `database/sql` value decoder, dialect-generic order and projection translators (with `ColumnResolver`), the keyset list engine and the dialect kit (`Style`, `Identifiers`) for SQL dialects in or out of this repository                         |
| [`backend/sqlbase/sqltype`](backend/sqlbase/sqltype/README.md) | Type checker and literal model of the schema-aware SQL filter translators: the dialect SPI (`Checker`, `Dialect`)                                                                                                                                                                                  |
| [`backend/postgres`](backend/postgres/README.md)               | PostgreSQL: filter translator (scalar and listing contract mode), schema and compiled evaluator profile, order and projection translators and the keyset list engine with the PostgreSQL dialect fixed                                                                                             |
| [`backend/mariadb`](backend/mariadb/README.md)                 | MariaDB/MySQL: filter translator (scalar and listing contract mode), schema, order and projection translators and the keyset list engine with the MariaDB dialect fixed                                                                                                                            |
| [`backend/clickhouse`](backend/clickhouse/README.md)           | ClickHouse: filter translator (scalar and listing contract mode, typed `clickhouse.Schema` over a `sqlbase.Schema` with `Nullable`, `Array(T)` and JSON paths), order and projection translators and the keyset list engine with the ClickHouse dialect fixed                                      |
| [`backend/mongo`](backend/mongo/README.md)                     | MongoDB: filter, order and projection translators, the schema that is also the evaluator's MongoDB profile (`evaluator.Binder`), the keyset list engine, the evaluator's BSON value encoding (`ValueEncoding`) and the codec registry (`NewRegistry`: optional values, Decimal128, calendar dates) |
| [`backend/redis`](backend/redis/README.md)                     | Redis Query Engine (RediSearch): filter query string with an index schema (`NewSchema`), single-field `SORTBY` and `RETURN` clause                                                                                                                                                                 |
| [`backend/meili`](backend/meili/README.md)                     | Meilisearch filter expression, sort argument and `attributesToRetrieve` translators                                                                                                                                                                                                                |
| [`backend/lua`](backend/lua/README.md)                         | Lua (Redis `EVAL`) filter translator                                                                                                                                                                                                                                                               |
| [`backend/memory`](backend/memory/README.md)                   | In memory: `map[string]any` order comparator, projection of maps and proto messages and the list engine; in-memory filtering is [`filter/evaluator`](filter/evaluator/README.md)                                                                                                                   |

| Backend            | Filter                                               | Order                                                                      | Projection                                                                         | Listing engine                                       |
|--------------------|------------------------------------------------------|----------------------------------------------------------------------------|------------------------------------------------------------------------------------|------------------------------------------------------|
| PostgreSQL         | [`backend/postgres`](backend/postgres/README.md)     | [`backend/postgres`](backend/postgres/README.md)                           | [`backend/postgres`](backend/postgres/README.md)                                   | [`backend/postgres`](backend/postgres/README.md)     |
| MariaDB / MySQL    | [`backend/mariadb`](backend/mariadb/README.md)       | [`backend/mariadb`](backend/mariadb/README.md)                             | [`backend/mariadb`](backend/mariadb/README.md)                                     | [`backend/mariadb`](backend/mariadb/README.md)       |
| ClickHouse         | [`backend/clickhouse`](backend/clickhouse/README.md) | [`backend/clickhouse`](backend/clickhouse/README.md)                       | [`backend/clickhouse`](backend/clickhouse/README.md)                               | [`backend/clickhouse`](backend/clickhouse/README.md) |
| MongoDB            | [`backend/mongo`](backend/mongo/README.md)           | [`backend/mongo`](backend/mongo/README.md)                                 | [`backend/mongo`](backend/mongo/README.md)                                         | [`backend/mongo`](backend/mongo/README.md)           |
| RediSearch         | [`backend/redis`](backend/redis/README.md)           | [`backend/redis`](backend/redis/README.md) (one key)                       | [`backend/redis`](backend/redis/README.md) (`FT.SEARCH RETURN`)                    | –                                                    |
| Meilisearch        | [`backend/meili`](backend/meili/README.md)           | [`backend/meili`](backend/meili/README.md)                                 | [`backend/meili`](backend/meili/README.md) (`attributesToRetrieve`)                | –                                                    |
| Lua (Redis `EVAL`) | [`backend/lua`](backend/lua/README.md)               | –                                                                          | –                                                                                  | –                                                    |
| In memory          | [`filter/evaluator`](filter/evaluator/README.md)     | [`backend/memory`](backend/memory/README.md) (`map[string]any` comparator) | [`backend/memory`](backend/memory/README.md) (`map[string]any` and proto messages) | [`backend/memory`](backend/memory/README.md)         |

Each backend README lists what the backend can express.

The SQL list engines support exact filtered totals. PostgreSQL 17 UTF8 and memory lists also support case-insensitive substring searches on selected
public fields. See the [listing guide](docs/listing.md).

### Test kits

Filter, order and projection translators run the shared conformance suites, listing engines run `listingtest`, and the SQL and MongoDB
backends share their conformance fixtures between unit and live-database tests (the test-only module `integration/docker`).

| Package                                                                              | Contents                                                                                                                                    |
|--------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------|
| [`filter/filtertest`](filter/filtertest/README.md)                                   | Conformance suite of every filter translator and the evaluator (`RunConformance`); parsing and policy helpers for tests of filter consumers |
| [`orderby/orderbytest`](orderby/orderbytest/README.md)                               | Conformance suite of every order translator (`RunConformance`); parsing and policy helpers                                                  |
| [`projection/projectiontest`](projection/projectiontest/README.md)                   | Conformance suite of every projection translator (`RunConformance`); parsing and policy helpers                                             |
| [`listing/listingtest`](listing/listingtest/README.md)                               | Conformance suite of list engines (`Run`, `RunWithoutArrays`)                                                                               |
| [`pagination/pagetoken/pagetokentest`](pagination/pagetoken/pagetokentest/README.md) | Page-token codecs with fixed keys for tests of application signers                                                                          |
| [`requestid/requestidtest`](requestid/requestidtest/README.md)                       | Conformance suite of the `requestid.Store` contract                                                                                         |
| [`backend/sqlbase/sqlbasetest`](backend/sqlbase/sqlbasetest/README.md)               | Scripted `database/sql` driver (`NewFakeDB`) for list engine, index and schema tests of SQL dialects, without a server                      |
| [`backend/postgres/postgrestest`](backend/postgres/postgrestest/README.md)           | PostgreSQL filter conformance expectations and column decoding, shared with the live suite                                                  |
| [`backend/mariadb/mariadbtest`](backend/mariadb/mariadbtest/README.md)               | MariaDB filter conformance expectations and column decoding, shared with the live suite                                                     |
| [`backend/clickhouse/clickhousetest`](backend/clickhouse/clickhousetest/README.md)   | ClickHouse filter conformance expectations and column decoding, shared with the live suite                                                  |
| [`backend/mongo/mongotest`](backend/mongo/mongotest/README.md)                       | MongoDB filter conformance expectations and explain-plan helpers proving every list order is an index scan, shared with the live suite      |

## AIP coverage

| AIP                                                          | Package                                              | Status                                                                                                                                                 |
|--------------------------------------------------------------|------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------|
| [122](https://google.aip.dev/122) Resource names             | `resourcename`                                       | Implemented; the default ID check is wider than the RFC 1034 SHOULD (`StrictID` enforces it)                                                           |
| [123](https://google.aip.dev/123) Resource types             | `resourcename`                                       | Implemented                                                                                                                                            |
| [132](https://google.aip.dev/132) Standard List / `order_by` | `orderby`, `listing`                                 | Implemented; `listing` restricts `orderBy` to declared sort patterns                                                                                   |
| [134](https://google.aip.dev/134) Standard Update            | `fieldmask/update`                                   | Implemented                                                                                                                                            |
| [154](https://google.aip.dev/154) Resource freshness         | `etag`, `fieldmask/update`                           | Implemented; a bare unquoted etag is accepted as strong unless `WithStrictQuotes(true)`                                                                |
| [155](https://google.aip.dev/155) Request identification     | `requestid`                                          | Implemented; any UUID version by default (`FormatUUID4` enforces v4)                                                                                   |
| [157](https://google.aip.dev/157) Partial responses          | `projection`, `listing`                              | Implemented                                                                                                                                            |
| [158](https://google.aip.dev/158) Pagination                 | `pagination/pagetoken`, `pagination/page`, `listing` | Implemented                                                                                                                                            |
| [159](https://google.aip.dev/159) Reading across collections | `resourcename`, `unreachable`                        | Implemented                                                                                                                                            |
| [160](https://google.aip.dev/160) Filtering                  | `filter`, `listing`                                  | Implemented; operator support varies by backend (backend READMEs)                                                                                      |
| [161](https://google.aip.dev/161) Field masks                | `fieldmask`, `projection`                            | Implemented; collection wildcards and backtick map keys in read masks are not supported (MAY); `fieldmask` extends paths into `google.protobuf.Struct` |
| [163](https://google.aip.dev/163) Change validation          | `validateonly`, `fieldmask/update`                   | Implemented                                                                                                                                            |
| [164](https://google.aip.dev/164) Soft delete                | `softdelete`                                         | Implemented; List applies `show_deleted` through the listing server restriction (`Policy.Restriction`)                                                 |
| [165](https://google.aip.dev/165) Criteria-based delete      | `softdelete`                                         | Implemented                                                                                                                                            |
| [193](https://google.aip.dev/193) Errors                     | `apierror`, `apierror/aipmap`                        | Implemented; partial errors (SHOULD avoid) are left to the API                                                                                         |
| [202](https://google.aip.dev/202) Fields                     | `fieldinfo`, `fieldbehavior`                         | Implemented; IPv4 leading zeros accepted                                                                                                               |
| [203](https://google.aip.dev/203) Field behavior             | `fieldbehavior`, `fieldmask/update`                  | Implemented                                                                                                                                            |
| [216](https://google.aip.dev/216) States                     | `state`                                              | Implemented; descriptor lint is advisory                                                                                                               |
| [217](https://google.aip.dev/217) Unreachable resources      | `unreachable`                                        | Implemented                                                                                                                                            |
| [231](https://google.aip.dev/231) Batch Get                  | `batch`                                              | Implemented (atomic only)                                                                                                                              |
| [233](https://google.aip.dev/233) Batch Create               | `batch`                                              | Implemented (atomic and partial success)                                                                                                               |
| [234](https://google.aip.dev/234) Batch Update               | `batch`                                              | Implemented (atomic and partial success)                                                                                                               |
| [235](https://google.aip.dev/235) Batch Delete               | `batch`                                              | Implemented (atomic and partial success)                                                                                                               |

Each package README and guide under [docs](docs/) maps the MUST/SHOULD rules of its AIPs to the code that implements them, with every
deliberate deviation. Backend translators cover MongoDB, ClickHouse, MariaDB/MySQL, PostgreSQL, Meilisearch, RediSearch, Lua and in-memory data
as applicable.

## Parser / policy / translator

`filter`, `orderby` and `projection` split their configuration into three layers, and every option belongs to exactly one of them (the filter
parser lives in its own package `filter/parser`, so its option is `parser.Option`). Parser options carry no kind prefix and have the same
names in [`filter/parser`](filter/parser/README.md), `orderby` and `projection`: `WithCacheSize`, `WithNoCache`, `WithCollector` and the
limits (`WithMaxExpressionLength`, …).

| Layer      | Option type        | Owns                                                                                                                                                                                |
|------------|--------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Parser     | `ParserOption`     | Syntax and resource limits of the request text: lengths, counts, caching, grammar variants (numeric path segments, direction case, duplicates)                                      |
| Policy     | `PolicyOption`     | What a client may ask for, in API names and independent of any backend: allow-list, shared `fieldpolicy.Access`, untrusted-input guard, denied API fields, defaults and views       |
| Translator | `TranslatorOption` | How one backend expresses a validated request: exact or prefix API-to-storage mapping, key resolution, storage tie-breakers, denied and required storage fields, dialect, rendering |

`NewPolicy` builds the policy once per resource and validates it at construction (for example `ErrAllowlistRequired` for untrusted input
without an allow-list or field access policy). The policy is immutable and shared by the translators of every backend that serves the
resource; each translator constructor takes it as its first argument (`mongo.NewFilterTranslator(policy, opts...)`) and fails with
`ErrPolicyRequired` for nil. Translator storage options are wrapped in the backend's own option (`mongo.WithFilterOptions(...)`,
`mongo.WithOrderOptions(...)`, `mongo.WithProjectionOptions(...)`); the dialect-generic `backend/sqlbase` constructors take their dialect
after the policy, and `backend/postgres`, `backend/mariadb` and `backend/clickhouse` fix it. `orderby.Policy.Validate`,
`projection.Policy.Validate` and, for a parsed filter AST, `filter.Policy.CheckFields` and `CheckLimits` return errors that map to
`INVALID_ARGUMENT`, and every `Translate` call validates through the bound policy, so a translator never renders a field its policy refuses.

[`listing`](listing/README.md) builds the filter and order policies from the schema (`Schema.FilterPolicy`, `Schema.OrderPolicy`) and
supplies the projection policy options it can derive (`Schema.ProjectionOptions`); engines bind those policies to their storage mapping.

## Modules

The root module holds the core packages; every storage backend is a Go module of its own, so an application requires only the backends it
uses and never the dependencies of the others (mongo-driver and go-mongox for `backend/mongo`, gopher-lua for the tests of `backend/lua`). The
root module requires no backend module.

| Module                                           | Requires (go-aip)                                 | Released                    |
|--------------------------------------------------|---------------------------------------------------|-----------------------------|
| `github.com/altessa-s/go-aip`                    | –                                                 | yes                         |
| `github.com/altessa-s/go-aip/backend/sqlbase`    | root; the SQL dialect modules, for its tests only | with the root, same version |
| `github.com/altessa-s/go-aip/backend/postgres`   | root, `backend/sqlbase`                           | with the root, same version |
| `github.com/altessa-s/go-aip/backend/mariadb`    | root, `backend/sqlbase`                           | with the root, same version |
| `github.com/altessa-s/go-aip/backend/clickhouse` | root, `backend/sqlbase`                           | with the root, same version |
| `github.com/altessa-s/go-aip/backend/mongo`      | root                                              | with the root, same version |
| `github.com/altessa-s/go-aip/backend/redis`      | root                                              | with the root, same version |
| `github.com/altessa-s/go-aip/backend/meili`      | root                                              | with the root, same version |
| `github.com/altessa-s/go-aip/backend/lua`        | root                                              | with the root, same version |
| `github.com/altessa-s/go-aip/backend/memory`     | root                                              | with the root, same version |

All modules are released together under one version: use the same version of the root and of every backend module. Backend modules import
only exported root packages, never `internal/...`.

Two test-only modules are never released: [`integration/crossbackend`](integration/crossbackend/README.md) runs the checks that need several
backends at once (one policy through several translators, the backend sentinel table), and [`integration/docker`](integration/docker/README.md)
runs the PostgreSQL, MariaDB, ClickHouse and MongoDB backends against live servers started in Docker (testcontainers-go), so no released
module requires a container library for its tests.

## Architecture

[docs/architecture.md](docs/architecture.md) describes the layers (core request DSLs, the listing contract, the SQL dialect kit and the
backends), the dependency graph generated from `go list`, the import rules `arch_test.go` (`TestLayering`) enforces, where conformance,
cross-backend and live tests live, and how to add a backend in this repository or a SQL dialect outside it.

## go-atlas

go-atlas provides the framework wiring around these packages: factories and the gRPC field mask and field behavior interceptors. They are
not part of this module.

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md) for the make targets, the CI checks and the commit rules.

## License

MIT, see [LICENSE](LICENSE).
