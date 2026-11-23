# ADR 0001 — Exact List totals and case-insensitive contains

Proposed for review · 2026-11-23

## Context

Atlas Service needs `totalSize` for a filtered collection, including on subsequent pages. Its
`text.contains(q)` and `synonyms.contains(q)` filters must find mixed-case Cyrillic substrings.
The approved requirements are in the consuming repository's `docs/PLAN.md`, stage 0b, and
`docs/reviews/plan-r2-resolution.md`, finding 4. AIP-158 permits an optional
`int32 total_size` and permits documented estimates. Atlas requests a count of the matching rows. [AIP-158](https://google.aip.dev/158#guidance)

### Verified implementation at `4988f3c`

| Source | Consequence for the design |
| --- | --- |
| `listing/engine.go:17`, `pagination/page/page.go:8`, `backend/postgres/list.go:20` | The portable port serves pages. PostgreSQL already returns the concrete SQL engine. |
| `listing/filter.go:65`, `listing/filter.go:90`, `listing/filter.go:103` | Compilation validates page size, parent, order and read-mask constraints, validates the server restriction, applies filter defaults and checks the combined predicate's limits. |
| `backend/sqlbase/list_scope.go:24`, `backend/sqlbase/list_scope.go:72` | Canonical scope validates and maps the declared pins. A configured parent key must equal the request parent. |
| `backend/sqlbase/list_query.go:164`, `backend/sqlbase/list_query.go:198`, `backend/sqlbase/list_query.go:273` | List translates the compiled filter once, binds scope parameters after filter parameters and appends a cursor predicate only for a position. |
| `backend/sqlbase/list.go:25`, `backend/sqlbase/list_query.go:205`, `backend/sqlbase/list_options.go:18` | The query port needs only QueryContext. List bounds query execution and maps query errors. The default maximum is ten seconds. |
| `backend/postgres/list_dialect.go:140`, `backend/postgres/schema_options.go:12`, `backend/postgres/schema_sql_call.go:155`, `backend/postgres/schema_sql_call.go:215` | List builds a PostgreSQL contract schema. The default text collation is C. Scalar contains uses strpos, and arrays test that predicate over individual unnested elements. |
| `listing/evaluation/evaluate.go:143`, `backend/postgres/schema_evaluate.go:338`, `filter/evaluator/evaluator.go:287` | The memory listing profile, PostgreSQL evaluator program and default evaluator each have a case-sensitive contains path. |
| `backend/mongo/filter_translator.go:267`, `backend/mongo/evaluator.go:399` | MongoDB's literal contains path uses a case-sensitive regex and its evaluator uses strings.Contains. |
| `listing/engine.go:56`, `backend/sqlbase/list_query.go:249`, `integration/docker/internal/dbtest/dbtest.go:25` | SQL token bindings include the schema contract ID. The existing live fixture uses PostgreSQL 16. |

## Decision and public API

The proposed declarations below are grouped by package. The two options are generated with optgen.

```go
// backend/sqlbase
func (l *ListEngine[T]) Count(ctx context.Context, req listing.Request, scope Scope) (int64, error)

// listing
func WithCaseInsensitiveContainsFields(names ...string) ProfileOption
func (s *Schema) CaseInsensitiveContainsFields() []string

// filter/evaluator
func SimpleLower15_1(text string) string

// backend/postgres
func WithSchemaCaseInsensitiveContainsFields(names ...string) SchemaOption
func (ListDialect) NewCaseInsensitiveContainsTranslator(
    policy *filter.Policy, fields *sqlbase.Schema, opts []filter.TranslatorOption,
    config sqlbase.ListTranslatorConfig, names []string,
) (sqlbase.ListFilterTranslator, error)
```

### Count the collection before applying the cursor

The service calls List first, then Count on the same engine with the same request and authorized
scope when a total is required. Count returns zero for an empty result. A failure returns an error
and the service propagates it. The service checks the int64 result before assigning an int32 response
field and reports overflow. A narrower response field must never wrap or silently truncate.

Extract the common preparation from List through order validation, canonicalScope and filter
translation. Use the compiled predicate including defaults and `req.Restriction` (for example
`softdelete.Policy.Restriction(showDeleted)`). Use the same table, alias, mapped pin columns and
bound arguments. Count calls the existing WHERE builder with nil position and emits
`SELECT COUNT(*) FROM <quoted table> AS <quoted alias> WHERE <predicate>`. An empty predicate is TRUE.
Page size, order and read mask retain List's validation. The aggregate has no order, projection,
keyset comparison or limit. Count ignores PageToken entirely, including malformed tokens. List
continues to authenticate continuation tokens. Subject is used for token binding, so Count takes
only the authorized scope. A parent restricts rows through the configured parent pin.

Run the query on the engine's Querier with List's maxTime and the caller's earlier deadline.
Scan exactly one nonnegative int64, close rows and check iteration errors. Preserve List's
cancellation, deadline, storage-error classification and error mapper. A single RPC deadline can
bound both calls. Each statement sees its own snapshot at READ COMMITTED. Concurrent inserts or
deletes can change the total between calls. Callers needing agreement route both through the same
read-only REPEATABLE READ transaction and pass the same restriction, including its reference time.

A selective scope or parent index, possibly partial for live rows, can reduce count work.
Exact MVCC counts may still visit every qualifying entry and consult the heap. Keyset indexes
bound page work only. Inspect representative plans before adding indexes through service migrations.
PostgreSQL, MariaDB and ClickHouse constructors all expose the shared SQL method. Exercise all three.
Defer MongoDB and other concrete counters until requested. The portable Engine, bound Lister and Page keep their APIs.

### Give selected text fields one substring rule

Configure the listing profile with exact public field names, e.g.
`listing.WithCaseInsensitiveContainsFields("text", "synonyms")`. Validate at schema construction,
deduplicate, sort and freeze the names. The accessor returns a detached slice. Accept declared
plain string scalars and string arrays, including nullable scalars. Reject unknown names, enums,
nontext storage, JSON paths and UUID storage at the layer that knows the type. Field selection
does not grant filter access. An empty set selects today's behavior.

sqlbase uses the existing dialect translator factory for an empty set. For a nonempty set it
requires the optional method with the exact PostgreSQL signature above by structural interface
assertion, returning listing.ErrInvalidSchema if absent. This preserves third-party ListDialect
implementations. PostgreSQL delegates to its usual factory with the extra schema option and
validates the resolved columns. The same option on postgres.NewSchema configures standalone
schema-aware translation and evaluator.WithProfile(schema). The schema-free translator retains
its existing semantics. The Mongo list constructor rejects a nonempty set until it supports the
contract. The memory list engine reads the field set from its listing schema.

For a contains call whose receiver is a selected field, lowercase both operands using Unicode
15.1 simple lowercase mapping, then search for the resulting literal substring. The receiver
chooses the mode, including when a schema-aware call admits a field-valued needle. Preserve each
profile's existing operand validation. There is no normalization, accent removal or multi-rune
expansion. For example, `ЁЖИК` contains `ёж`, and `Straße` does not contain `STRASSE`.
Case-insensitivity applies only to contains. Equality, sorting, cursor keys, regex and other
string methods continue to use their existing rules.

Render the scalar predicate as follows, passing both operands through the existing typed
renderer. Apply the explicit collation to each input of lower. A text column may still use C.

```sql
strpos(lower((haystack) COLLATE pg_catalog.pg_c_utf8),
       lower((needle)   COLLATE pg_catalog.pg_c_utf8)) > 0
```

For text[] use the same predicate on each element inside the existing EXISTS over unnest.
Multiple matching elements count as one row. Substrings cannot span elements. An empty needle
matches every non-null string, including an empty element. Empty arrays have no matching element.
Preserve existing presence, null-argument, null-element and negation handling around the predicate
for each supported storage shape. Percent, underscore and backslash are literal characters.

PostgreSQL 17's built-in pg_c_utf8 supplies simple mapping in a UTF8 database. C applies only
ASCII case rules. Pin the Go helper's mapping to the same Unicode release, using a committed
simple-lowercase table derived from UnicodeData. Both the listing evaluator and the PostgreSQL
schema evaluator call it in their contains branches. Charge existing data budgets before allocating
folded strings and precompute literal needles at compile time. The default evaluator keeps its
portable semantics. [PostgreSQL collations](https://www.postgresql.org/docs/17/collation.html#COLLATION-MANAGING-STANDARD) · [PostgreSQL 17 Unicode version](https://github.com/postgres/postgres/blob/REL_17_STABLE/src/include/common/unicode_version.h)

Certify PostgreSQL 17 with UTF8 first. For enabled fields, CheckSchema verifies the server major
against the certified set, the encoding and the catalog-qualified collation. An unsupported
combination fails with listing.ErrInvalidSchema. Constructors remain free of database I/O.
Run this capability check before serving the profile, including in the application's deployment
preflight. Standalone SQL users have the same server prerequisite. Never fall back to C or ICU.
Certify later majors against the pinned mappings before admitting them.

Neither a B-tree on lower(text) nor a plain trigram index accelerates this strpos expression
directly. Array membership GIN indexes do not answer substring tests over unnest. Start with
scope-selective scans. If measured substring traffic warrants it, evaluate an escaped LIKE on a
matching lowered trigram expression index, or indexed child rows for repeated text, as a separate
change with literal-pattern and plan tests. [PostgreSQL trigram index support](https://www.postgresql.org/docs/17/pgtrgm.html#PGTRGM-INDEX)

## Alternatives considered

| Option | Trade-off |
| --- | --- |
| COUNT(*) OVER () in List | One statement can share a snapshot, but computing the whole total before keyset filtering can remove early termination. An empty page carries no aggregate row. |
| Capped count or planner estimate | Can bound work or avoid scanning, but needs a documented lower-bound or estimate contract. The consumer requires an exact total. |
| ILIKE on C, or global text collation | C misses Cyrillic. A global collation can change comparisons, order and token interpretation across unrelated fields. |
| ICU lowering or application-maintained folded columns | ICU requires matching provider versions and casing rules in Go. Shadow columns can support older servers but require backfill and atomic maintenance of scalar and repeated values by every writer. |

## Compatibility and migration

Count is opt-in per call. Existing List calls execute one page query. Default profiles keep their
current contract IDs and token bytes. For a nonempty field set, derive Schema.ContractID() from
an unambiguous, domain-separated encoding of the application's existing contract ID, the mapping
revision and the sorted names. Reserve the derived-ID namespace. Changing fields or folding rules
invalidates old tokens even if the application forgets to bump its ID. Count alone changes no binding.
Deploy capable binaries and validate the database first. Switch the resource profile consistently
across replicas. A client presenting a pre-switch token receives the normal invalid-page-token
error and restarts at page one. No table collation migration is required.

## Verification before implementation release

1. Script the SQL driver for first, resumed and empty pages. Compare Count's bound WHERE and args
   with List before the cursor, including mapped columns, default equalities, both soft-delete
   modes, multiple tenants and parent mismatch. Assert invalid requests issue no query, malformed
   tokens are ignored only by Count, and cancellation, scan and iteration failures propagate.
2. Exercise immutable field options, unsupported dialects and physical kinds. Check generated SQL
   and bindings for scalar, array, negation, field-valued needles where supported, and unselected
   fields. Check the evaluator work limit and concurrent use. Pin disabled-profile token goldens,
   stable IDs across field permutations, and token rejection after enabling or changing the set.
3. Share profile-specific expected IDs across the PostgreSQL program, memory List and live SQL.
   Include scalar `ПрИвЕт` and array `{"мир","ПРИВЕТИК"}` with needle `иВе`, nonmatches,
   `{"при","вет"}` against `привет`, empty and null cases, negation, Ё/ё, percent and underscore.
   Confirm default-mode differences explicitly and run existing filter and listing conformance suites.
4. Run `make test-integration-docker` with a PostgreSQL 17 UTF8 fixture using C columns. Compare
   exact totals with the IDs across all pages for the same tenant, parent and deletion mode.
   Test an intervening committed write under READ COMMITTED and a stable repeatable-read pair.
   Compare the pinned Go lowercase table with server lower over all PostgreSQL-representable
   Unicode scalars in batches. Keep the PostgreSQL 16 fixture for default success and opt-in
   preflight failure. Exercise exact counts on the MariaDB and ClickHouse fixtures.

## Open questions

- What count latency and maximum collection size does Atlas need for its widest tenant scope?
  Measure the live-row and show-deleted plans before choosing migration indexes.
- Which production server majors need certification after 17, and which endpoints need a common
  snapshot strongly enough to pay for a repeatable-read transaction?
