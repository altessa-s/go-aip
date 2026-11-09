# Projection

[projection](../projection/) turns an AIP-157 partial response request — a `fields` parameter, an AIP-161 read mask or a resource view — into a
backend projection, so unrequested fields are never read from storage. A `Parser` produces a `Spec`, one `Policy` per resource authorizes it in API
names, and the projection translators of [backend/mongo](../backend/mongo/README.md),
[backend/sqlbase](../backend/sqlbase/README.md#projection-translator) (with the dialect fixed by [backend/postgres](../backend/postgres/README.md),
[backend/mariadb](../backend/mariadb/README.md) and [backend/clickhouse](../backend/clickhouse/README.md)), [backend/redis](../backend/redis/README.md),
[backend/meili](../backend/meili/README.md) and [backend/memory](../backend/memory/README.md) render it in storage names.

## AIP-157 and AIP-161 conformance

| Rule                                                                                       | Implemented by                                              | Behavior                                                                                                                                                                                   |
|--------------------------------------------------------------------------------------------|-------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| AIP-157: if the field mask is not provided, all fields must be returned                    | `Policy.Validate`, `TranslatorContext.Resolve`, `Translate` | An omitted mask (`Spec{}`) selects every authorized field: the allow-list roots minus the denied fields, or every field but the denied ones without an allow-list. No option narrows it.   |
| AIP-157: `*` selects all fields                                                            | `Parser.Parse`, `Policy.Validate`, `Policy.Select`          | `*` parses to `ModeAll`: every authorized field, whatever the view.                                                                                                                        |
| AIP-157: a view enum ends in -View and has at least BASIC and FULL                         | `View`, `ViewBasic`, `ViewFull`, `WithView`                 | `ViewFull` is always every authorized field; `ViewBasic` is the subset set with `WithView(ViewBasic, ...)`; values above `ViewFull` are custom views. The API maps its enum onto `View`.   |
| AIP-157: the UNSPECIFIED value must be valid, not an error                                 | `Policy.Select`, `WithDefaultView`                          | `ViewUnspecified` selects the default view (`ViewFull` unless set). `NewPolicy` rejects a default view it cannot serve.                                                                    |
| AIP-157: the default for List methods should be BASIC; for the others BASIC or FULL        | `WithDefaultView`, `View.Or`                                | The policy-wide default is `WithDefaultView`; a method with another default resolves it with `view.Or(projection.ViewBasic)` before `Select`.                                              |
| AIP-157: APIs must not remove a field from a given view                                    | `WithView` (contract)                                       | A stability rule of the published API: keep every path of a view. `NewPolicy` validates each path against the allow-list and the deny-list, so a view never exceeds FULL.                  |
| AIP-157: a request may carry both a mask and a view                                        | `Policy.Select`, `ResolveView`, `TranslateView`             | A present mask (paths or `*`) decides the fields; an omitted one defers to the view. An unknown view is `ErrInvalidView` even under a mask.                                                |
| AIP-161: paths are dot-separated proto field names relative to the resource                | `WithPathGrammar`, `WithPathValidator`                      | The default grammar is `ident("." ident)*`. A proto descriptor validator can be passed to the parser and the policy; it checks API paths only, never storage names.                        |
| AIP-161: a mask cannot index a repeated field; the request must fail with INVALID_ARGUMENT | default grammar, `ErrUnsupportedPath`                       | `items.0` fails with `ErrUnsupportedPath` in the parser, in `Validate` for a hand-built `Spec` and in `WithView`. Map it to INVALID_ARGUMENT like every request-time error of the package. |
| AIP-161: wildcards on collections (`items.*.name`) and backtick map keys may be supported  | `ErrUnsupportedPath`                                        | Not supported: both fail with `ErrUnsupportedPath` (INVALID_ARGUMENT). A dotted path into a repeated message (`items.name`) selects that field in every element.                           |
| AIP-161: a read mask may ignore paths to values that cannot exist                          | `Policy.Validate`, `ErrFieldNotAllowed`                     | Stricter, as allowed: a path outside the allow-list or overlapping a denied field fails with `ErrFieldNotAllowed` (INVALID_ARGUMENT) rather than being ignored.                            |

## Selection rule

| Read mask | View                 | Fields                               |
|-----------|----------------------|--------------------------------------|
| paths     | any served view      | the paths, within the policy         |
| `*`       | any served view      | every authorized field               |
| omitted   | `ViewUnspecified`    | the default view                     |
| omitted   | `ViewBasic` / custom | the paths configured with `WithView` |
| omitted   | `ViewFull`           | every authorized field               |
| any       | not served           | `ErrInvalidView`                     |

`Validate`, `Resolve` and `Translate` take a mask alone and behave as `Select` with `ViewFull`. Required storage fields (`WithRequiredFields`) join
every inclusion projection whatever the mask or view: they are what the backend needs, not what the client sees.

```go
policy, err := projection.NewPolicy(
    projection.WithUntrustedInput(true),
    projection.WithAllowedFields("name", "email", "createTime", "address.*"),
    projection.WithView(projection.ViewBasic, "name", "email"),
    projection.WithDefaultView(projection.ViewBasic), // a List method
)

fields, err := policy.Select(spec, projection.View(req.GetView()))
if err != nil {
    return status.Error(codes.InvalidArgument, err.Error())
}
```

## Every authorized field

FULL, `*` and an omitted mask resolve to the same selection, derived once by `NewPolicy` and once by each translator constructor:

* a restrictive allow-list contributes its roots (`address.*` → `address`); a root equal to or inside a denied field — in API or storage names —
  is dropped;
* without an allow-list the selection is every field except the denied ones, an exclusion that MongoDB renders as `{denied: 0}`, ClickHouse
  as `* EXCEPT (denied, …)` (top-level columns only), and the memory translator applies; PostgreSQL, MariaDB, RediSearch and Meilisearch reject
  it at construction with `ErrAllFieldsUnresolvable`.

The derivation fails with `ErrAllFieldsUnresolvable` when it cannot be done safely: an empty allow-list or one with embedded wildcards, a root that
carries a denied descendant or that the field access policy refuses, a field access policy without an allow-list, nothing left after the denies,
or a backend that cannot express an exclusion with denied fields and no allow-list. List the allowed fields so that their roots carry nothing denied.

## Backend capabilities

| Backend                  | Exclusion (no allow-list, some fields denied) | ModeNone without required fields      |
|--------------------------|-----------------------------------------------|---------------------------------------|
| sql PostgreSQL / MariaDB | `ErrAllFieldsUnresolvable` at construction    | `ErrUnsupportedSelection`             |
| sql ClickHouse           | `* EXCEPT (…)`, top-level columns only        | `ErrUnsupportedSelection`             |
| mongo                    | `{p: 0}`                                      | `ErrUnsupportedSelection`             |
| redis                    | `ErrAllFieldsUnresolvable` at construction    | `RETURN 0`                            |
| meili                    | `ErrAllFieldsUnresolvable` at construction    | `ErrUnsupportedSelection`             |
| memory                   | applied                                       | empty map, or a cleared proto message |

Every `NewProjectionTranslator` takes the policy first and then the backend's `ProjectionOption` values (`WithProjectionOptions`,
`WithProjectionCollector`); the dialect-generic `sqlbase.NewProjectionTranslator` takes its `ProjectionDialect` between them, which
`backend/postgres`, `backend/mariadb` and `backend/clickhouse` fix. Every translator runs `projectiontest.RunConformance`; a backend that cannot
express a selection rejects it with the same sentinel as the others and never widens or narrows it silently. On every backend, required fields
turn ModeNone into an inclusion of those fields.

### Backend behavior

| Feature                 | All backends                                                                                                                         |
|-------------------------|--------------------------------------------------------------------------------------------------------------------------------------|
| Include list            | Mapped storage names plus required fields                                                                                            |
| Every authorized field  | An omitted mask, `*` and FULL: the allow-list roots plus required fields, or every field / the exclusion above without an allow-list |
| Views (`TranslateView`) | AIP-157 rule of `projection.Policy.Select`; an unknown view is `ErrInvalidView`, even under a mask                                   |
| Required fields         | Join every inclusion; with `ModeNone` they are the whole selection                                                                   |
| Nested paths            | Dotted storage paths: nested documents (Mongo, Meili, memory), qualified or subcolumns (SQL), flat attributes or JSONPath (Redis)    |
| Overlapping paths       | Dropped under a selected ancestor where the backend nests (Mongo, Meili, memory); kept where names are flat (SQL, Redis)             |
| Concurrency             | Translators are safe for concurrent use after construction                                                                           |

MongoDB also takes `mongo.WithProjectionIDField` (the identity field, `_id` by default) and `mongo.WithProjectionIncludeID` (include it
through the required fields). `sqlbase.NewProjectionContext(policy, resolver, opts ...projection.TranslatorOption)` with
`sqlbase.TranslateProjection` / `sqlbase.TranslateProjectionView` renders through an application `ColumnResolver`.

## Path validators

`WithPathGrammar` and `WithPathValidator` replace the API path grammar of the parser and the policy — use the same validator for both, for example
[`projection/protopath`](../projection/protopath/README.md) built from the resource message descriptor. The translator's denied and required storage
paths follow `WithStoragePathValidator` (default: the dotted grammar), because storage names — SQL columns, MongoDB keys — need not be API field
names.

## Errors

Request-time errors — the parser's, `Validate`, `Select` and every `Translate*` — map to INVALID_ARGUMENT: `ErrEmptyClause`,
`ErrInvalidFieldPath`, `ErrUnsupportedPath`, `ErrDuplicatePath` (with `WithRejectDuplicates`), the length and count limits, `ErrFieldNotAllowed`,
`ErrInvalidView` and `ErrUnsupportedSelection`. Construction errors are configuration faults: `ErrAllowlistRequired`, `ErrPolicyRequired`,
`ErrConflictingPolicy`, `ErrAllFieldsUnresolvable` and `ErrInvalidView` from `NewPolicy`.
