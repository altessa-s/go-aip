# projection

```go
import "github.com/altessa-s/go-aip/projection"
```

Package `projection` parses a client-supplied field list — the AIP-157 partial-response `fields` parameter — and translates it into a
database-specific projection, so unrequested fields are never read from storage instead of being trimmed off the response afterwards. It is the
field-selection counterpart of [`orderby`](../orderby): a cached parser produces a `Spec`, and per-backend translators turn it into a MongoDB
projection document or a SQL column list. One shared `Policy` states what a client may select; each translator adds the mapping, storage denies and
required fields of its backend.

## Contract

`NewParser` parses a field list into `Spec`; ParserOption configures grammar, duplicate handling, limits, and cache. PolicyOption (`NewPolicy`)
controls, in API names, field access, allowlists, denied fields, resource views, the API path grammar and the untrusted-input guard.
TranslatorOption controls, per backend, mapping, denied storage fields, required storage fields and the storage path grammar. Parser, policy and
storage path validators are separate injection points.

`Policy.Validate` authorizes a read mask alone and returns the API paths to read as `Fields` (`Mode()`, `Paths()`): an omitted mask and `ModeAll`
(`*`) return every authorized field, and `ModeNone` selects nothing. `Policy.Select(spec, view)` adds the AIP-157 view: a present mask decides, an
omitted one defers to the view. `TranslatorContext.Resolve` and `ResolveView` validate through the bound policy and return a detached `Selection`
in storage names. Exact mappings precede longest-prefix mappings. Denies cover ancestors and descendants, including storage aliases; required
fields cannot overlap denied paths. Shared fieldpolicy.Access narrows authorization.

"Every authorized field" is derived once, at construction, from the allow-list minus the denied fields. A field access policy therefore needs an
allow-list to enumerate. SQL cannot express an exclusion projection, so a SQL backend with denied fields needs a restrictive allowlist. MongoDB
supports inclusion/exclusion and configurable identity handling. Custom SQL expressions belong in sqlbase.ColumnResolver, never raw request paths.

See the [projection guide](../docs/projection.md) for the AIP-157/AIP-161 conformance table.

### Parser / policy / translator

The three option layers are described once in the [root README](../README.md#parser--policy--translator). Build one `Policy` per resource
with `NewPolicy` and pass it as the first argument of every translator constructor; a nil policy fails with `ErrPolicyRequired`. Required fields
are storage paths the backend needs (the ID, a tenant key, a version, keyset sort keys), so they are translator options, not policy options.

## Grammar

```
fields := "" | "*" | path ("," path)*
path   := ident ("." ident)*
ident  := [A-Za-z_][A-Za-z0-9_]*
```

A restricted subset of the [AIP-161](https://google.aip.dev/161) field mask grammar: a value from `x-goog-fieldmask`, `$fields` or
`fieldmaskpb.FieldMask.Paths` parses as long as it stays inside the subset. Whitespace around commas is insignificant; identifiers are case-sensitive.
Empty or whitespace-only input is an omitted mask: every authorized field, or the requested view under `Select`; `*` explicitly selects all
authorized fields, whatever the view. Duplicates are dropped and the paths sorted; a parent and its child are both kept, because collapsing them is
only safe after field mapping.

| Input                                              | Result                                        |
|----------------------------------------------------|-----------------------------------------------|
| `name, address.city`                               | `Spec{Paths: [address.city name]}`            |
| `""`                                               | `Spec{}` — omitted: all fields or the view    |
| `"*"`                                              | `Spec{Mode: ModeAll}` — all authorized fields |
| `a,,b`                                             | `ErrEmptyClause`                              |
| `$where`, `a..b`, `1a`                             | `ErrInvalidFieldPath`                         |
| `items.*.name`, `a,*`, `items.0`, `` labels.`k` `` | `ErrUnsupportedPath` (INVALID_ARGUMENT)       |

## Key types

| Type                | Description                                                                                                                                               |
|---------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Parser`            | Fields parser with LRU caching: `Parse(ctx, string)`, `ParsePaths(ctx, []string)`, `MustParse(string)` for fixed masks                                    |
| `Spec`              | Parsed read mask: sorted, unique API paths; empty is an omitted mask. `Relative(prefix)` re-roots an envelope mask; `Contains`, `IsEmpty`, `Clone`        |
| `Selection`         | Resolved projection in storage names: `Include`, `Exclude`, or an explicit all/none mode; `IsAll()`                                                       |
| `Policy`            | Backend-independent field policy of one resource; `Validate(Spec)` and `Select(Spec, View)` return the authorized `Fields`                                |
| `View`              | AIP-157 resource view: `ViewUnspecified`, `ViewBasic`, `ViewFull`, custom values above `ViewFull`; `Or(def)`                                              |
| `Fields`            | Authorized API paths: `Mode()` and `Paths()`                                                                                                              |
| `TranslatorContext` | A `Policy` bound to one backend; `Resolve(Spec)`, `ResolveView(Spec, View)`, `ViewSelection(View)`, `RequiredFields()`, `DeclaresField(path)`, `Policy()` |
| `metrics.TranslateTimer`         | `NewTelemetry(meter, backend)`: the translation duration histogram `projection_<backend>_translate_duration_seconds`                                      |

## Parser options

See the [options reference](#options) and contract above.

## Policy and translator options

See the [options reference](#options). Every request path is checked by the policy against the allow-list and `WithDeniedFields` by API name, then
mapped by the translator and checked against the denied storage names —
`WithDeniedStorageFields` plus every storage path a denied API field reaches through the mapping (its own name, the targets its descendants are mapped
to, the names an exact ancestor entry implies) — so an allowed alias cannot reach a protected column.

A path selects its whole subtree in storage: `address` with prefix mapping `"address." → "addr."` selects `addr`, and a descendant mapped elsewhere
(`"address.city" → "city_col"`) is selected alongside it. The same rule turns the wildcard root `address.*` into part of every authorized field.

### Omitted masks and views

AIP-157: "If the field mask is not provided, all fields must be returned." An omitted mask (`Spec{}`) and `*` (`ModeAll`) therefore select every
authorized field, derived once by `NewPolicy` and once by each translator constructor; "all fields" never bypasses the policy:

| Policy                                | Every authorized field                                                                 |
|---------------------------------------|----------------------------------------------------------------------------------------|
| Restrictive allow-list                | the allow-list roots (`address.*` → `address`) minus denied ones, plus required fields |
| No allow-list (or `*`), denied fields | every field except the denied ones (exclusion; MongoDB only)                           |
| No allow-list, nothing denied         | every field                                                                            |

A root equal to or inside a denied field (API or storage) is dropped. A policy whose fields cannot be enumerated safely — an empty allow-list or
one with embedded wildcards, a root that carries a denied descendant or is refused by the field access policy, a field access policy without an
allow-list, nothing left after the denies — fails at `NewPolicy` (or at the translator constructor, for storage denies) with
`ErrAllFieldsUnresolvable`. On SQL an exclusion fails the same way.

Views are the AIP-157 alternative to a mask. `WithView(ViewBasic, paths...)` configures BASIC (values above `ViewFull` name custom views);
`ViewFull` is always every authorized field and cannot be configured. `WithDefaultView` picks the view of a request with `ViewUnspecified`
(default `ViewFull`); AIP-157 recommends BASIC for List methods and BASIC or FULL for the others — a method whose default differs passes
`view.Or(projection.ViewBasic)`. `NewPolicy` checks every view path against the grammar, the allow-list, the field access policy and the
deny-list (`ErrConflictingPolicy`), and rejects an unconfigurable or empty view and an unserved default view (`ErrInvalidView`); each translator
constructor fails with `ErrConflictingPolicy` when a view field reaches a denied storage path. A published view must not lose a field: that is
the API's contract to keep.

`Select(spec, view)` / `ResolveView` / `TranslateView`:

| Read mask | View                 | Fields                              |
|-----------|----------------------|-------------------------------------|
| paths     | any served view      | the paths (within the policy)       |
| `*`       | any served view      | every authorized field              |
| omitted   | `ViewUnspecified`    | the default view                    |
| omitted   | `ViewBasic` / custom | the configured paths                |
| omitted   | `ViewFull`           | every authorized field              |
| any       | not served           | `ErrInvalidView` (INVALID_ARGUMENT) |

`Validate`, `Resolve` and `Translate` take a mask alone and behave as `Select` with `ViewFull`.

## Usage

```go
parser, _ := projection.NewParser()
spec, err := parser.Parse(ctx, req.GetFields())
if err != nil {
    return status.Error(codes.InvalidArgument, err.Error())
}

policy, err := projection.NewPolicy(
    projection.WithUntrustedInput(true),
    projection.WithAllowedFields("name", "email", "createTime", "address.*"),
)
if err != nil {
    return err
}

trans, err := mongo.NewProjectionTranslator(policy,
    mongo.WithProjectionOptions(
        projection.WithFieldMapping(map[string]string{"createTime": "created_at"}),
        projection.WithDeniedStorageFields(deniedFromBehaviorTags...),
        projection.WithRequiredFields("_id", "created_at"),
    ),
)
proj, err := trans.TranslateView(spec, projection.View(req.GetView())) // or Translate(spec) without views

cur, err := col.Find(ctx, filter, options.Find().SetProjection(proj))
```

The `fields` value can also come from an AIP-157 read mask — for example one read off the request with `extract.ReadMask`, or
the one the go-atlas gRPC `fieldmask` interceptor captures from the `x-goog-fieldmask` metadata:

```go
parser, _ := projection.NewParser() // spec and err are the ones declared above
if mask, ok := extract.ReadMask(req); ok {
    spec, err = parser.ParsePaths(ctx, mask.GetPaths())
    // A List* mask addresses the response envelope ("items.name, next_page_token"):
    spec, _ = spec.Relative("items")
}
```

For a behavior-tagged model, go-atlas can derive the policy (`mapping/behavior/translators/mongo.NewFieldPathsTranslator`): its `Denied`
paths feed `WithDeniedStorageFields` (translator), its `Selectable` paths an allow-list (policy).

## Pagination

Keyset pagination reads the cursor and sort values back from the last returned row, so a projection must keep the cursor ID field and the primary
sort field; list them with `WithRequiredFields` on the translator. The MongoDB list engine (`backend/mongo`) refuses a projection that
computes a sort field and fails a page whose document lacks one.

## Backends

The projection translators live in the backend packages:

| Package                                                                                                                   | Output                                                                        | Every field | Exclusion                       | `ModeNone` |
|---------------------------------------------------------------------------------------------------------------------------|-------------------------------------------------------------------------------|-------------|---------------------------------|------------|
| [backend/mongo](../backend/mongo/README.md)                                                                               | `bson.M` (`{p: 1}` / `{p: 0}`)                                                | `nil`       | `{denied: 0}`                   | rejected   |
| [backend/sqlbase](../backend/sqlbase/README.md#projection-translator) (fixed by backend/postgres, mariadb and clickhouse) | `string` (`"a", "b"` on PostgreSQL, `` `a`, `b` `` on MariaDB and ClickHouse) | `""` → `*`  | ClickHouse only: `* EXCEPT (…)` | rejected   |
| [backend/redis](../backend/redis/README.md)                                                                               | `[]string` FT.SEARCH arguments `RETURN n field …`                             | `nil`       | construction error              | `RETURN 0` |
| [backend/meili](../backend/meili/README.md)                                                                               | `[]string` `attributesToRetrieve`                                             | `nil`       | construction error              | rejected   |
| [backend/memory](../backend/memory/README.md)                                                                             | `*memory.Projection` applied to `map[string]any` or a proto message           | copy        | applied                         | empty map  |

Every constructor takes the policy first, then package options: `NewProjectionTranslator(policy, opts...)`, and for the generic SQL one
`sqlbase.NewProjectionTranslator(policy, dialect, opts...)`; [backend/postgres](../backend/postgres/README.md),
[backend/mariadb](../backend/mariadb/README.md) and [backend/clickhouse](../backend/clickhouse/README.md) fix the dialect. Storage options go
through `WithProjectionOptions(...projection.TranslatorOption)`, metrics through `WithProjectionCollector`.
`sqlbase.NewProjectionContext(policy, resolver, opts ...projection.TranslatorOption)` binds an application `ColumnResolver`. "Rejected" means
`ErrUnsupportedSelection`; required fields turn `ModeNone` into an inclusion on every backend.
[projectiontest](projectiontest) holds the conformance suite every translator runs.

## Errors

| Error                           | Meaning                                                                                          |
|---------------------------------|--------------------------------------------------------------------------------------------------|
| `ErrEmptyClause`                | Empty path between commas                                                                        |
| `ErrInvalidFieldPath`           | Path outside the grammar, or a storage name a backend cannot address                             |
| `ErrUnsupportedPath`            | Valid AIP-161 syntax this package does not serve (wildcards, keys, indexes)                      |
| `ErrDuplicatePath`              | Repeated path under `WithRejectDuplicates(true)`                                                 |
| `ErrExpressionTooLong`          | Input longer than `WithMaxExpressionLength`                                                      |
| `ErrMaxPathsExceeded`           | More than `WithMaxPaths` paths (counted before deduplication)                                    |
| `ErrMaxFieldPathDepthExceeded`  | Path deeper than `WithMaxFieldPathDepth`                                                         |
| `ErrMaxFieldNameLengthExceeded` | Path longer than `WithMaxFieldNameLength`                                                        |
| `ErrFieldNotAllowed`            | Path outside the allow-list or overlapping a denied field                                        |
| `ErrAllowlistRequired`          | `WithUntrustedInput` without a restrictive allow-list (`NewPolicy`)                              |
| `ErrPolicyRequired`             | Nil policy passed to a translator constructor or `NewTranslatorContext`                          |
| `ErrConflictingPolicy`          | Required or view field contradicts the deny-list or allow-list                                   |
| `ErrAllFieldsUnresolvable`      | Every authorized field cannot be enumerated safely; list the allowed fields                      |
| `ErrInvalidView`                | View not served (request), or an unconfigurable view or default (policy)                         |
| `ErrUnsupportedSelection`       | A selection mode the backend cannot render (`ModeNone` without required fields on most backends) |

Request-time errors (parser, `Validate`, `Select`, `Translate*`) map to INVALID_ARGUMENT; the others are construction errors.

## Selection and path policy

| API                      | Meaning                                                   |
|--------------------------|-----------------------------------------------------------|
| `Spec{}` / `ModeDefault` | Omitted mask: every authorized field, or the view         |
| `ModeAll` / parsed `*`   | All authorized fields, whatever the view                  |
| `ModeNone`               | No selectable fields; required storage fields still apply |

Use the same path validator in the parser (`WithPathGrammar`) and the policy (`WithPathValidator`) — for example the proto descriptor validator of
[`projection/protopath`](protopath), `projection.WithPathValidator(v.Validate)`, which enforces the AIP-161 rules against the resource message
(repeated and map fields only last, no sub-paths into well-known types, unknown fields). It applies to API paths only: the denied and required storage
paths of a translator follow `WithStoragePathValidator` (default: the dotted grammar), since storage names need not be API names. Backend resolvers
still own safe storage encoding. SQL and Mongo presets reject a truly empty selection with `ErrUnsupportedSelection`, because their existing output
representations cannot express it safely. Mongo's constructor accepts local options: wrap storage options with `mongo.WithProjectionOptions` and
configure identity behavior with `WithProjectionIDField` and `WithProjectionIncludeID(bool)`. Existing required fields and deny rules still apply.

## Consistency contracts

WithNoCache and WithUntrustedInput take an explicit bool. Later options can disable an earlier preset. Wrapped path-validator failures retain
their causes.

Nil path validators use the default grammar. The allow-list roots that make up every authorized field must pass the shared field access policy.

## Options

| Option                     | Type               | Default                       | Behavior                                                                                                                                                                                                                                                                           |
|----------------------------|--------------------|-------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `WithMaxExpressionLength`  | `ParserOption`     | DefaultMaxExpressionLength    | Limit input expression length in bytes.                                                                                                                                                                                                                                            |
| `WithMaxFieldNameLength`   | `ParserOption`     | DefaultMaxFieldNameLength     | Limit a field path length in bytes, including dots.                                                                                                                                                                                                                                |
| `WithMaxFieldPathDepth`    | `ParserOption`     | DefaultMaxFieldPathDepth      | Limit the number of dotted path segments.                                                                                                                                                                                                                                          |
| `WithMaxPaths`             | `ParserOption`     | DefaultMaxPaths               | Limit parsed projection paths.                                                                                                                                                                                                                                                     |
| `WithCacheSize`            | `ParserOption`     | DefaultParserCacheSize        | Set the parsed-specification cache capacity.                                                                                                                                                                                                                                       |
| `WithCollector`            | `ParserOption`     | nil / unset                   | Parser `metrics.Meter`.                                                                                                                                                                                                                                                            |
| `WithNoCache`              | `ParserOption`     | false                         | Disable parsed-specification caching when true.                                                                                                                                                                                                                                    |
| `WithPathGrammar`          | `ParserOption`     | nil / unset                   | Replaces the parser path grammar.                                                                                                                                                                                                                                                  |
| `WithRejectDuplicates`     | `ParserOption`     | false                         | Reject duplicate projection paths instead of deduplicating them.                                                                                                                                                                                                                   |
| `WithAllowedFields`        | `PolicyOption`     | nil / unset                   | Restrict public field names; matched before storage mapping. A restrictive list also defines every authorized field: its roots minus the denied fields.                                                                                                                            |
| `WithDefaultView`          | `PolicyOption`     | `ViewFull`                    | View of a request with no mask and `ViewUnspecified`; must be `ViewFull` or configured. AIP-157: BASIC for List, BASIC or FULL otherwise.                                                                                                                                          |
| `WithDeniedFields`         | `PolicyOption`     | nil / unset                   | Reject denied public paths, including overlaps; translators also deny the storage paths they reach through the mapping.                                                                                                                                                            |
| `WithFieldAccess`          | `PolicyOption`     | nil / unset                   | Add a shared field authorization policy. Existing allow-lists remain restrictive.                                                                                                                                                                                                  |
| `WithPathValidator`        | `PolicyOption`     | default dotted-path validator | Replace the grammar of API paths: requested, denied and view paths. Storage paths use `WithStoragePathValidator`. Authorization still applies.                                                                                                                                     |
| `WithUntrustedInput`       | `PolicyOption`     | false                         | Mark the request as client input; `NewPolicy` then requires an allow-list that restricts the fields.                                                                                                                                                                               |
| `WithView`                 | `PolicyOption`     | nil / unset                   | API paths of a view (`ViewBasic` or a custom value above `ViewFull`) returned for a request with no mask; validated against the policy by `NewPolicy`.                                                                                                                             |
| `WithDeniedStorageFields`  | `TranslatorOption` | nil / unset                   | Reject storage paths regardless of their public alias.                                                                                                                                                                                                                             |
| `WithFieldMapping`         | `TranslatorOption` | identity mapping              | Exact mapping from API names to storage names.                                                                                                                                                                                                                                     |
| `WithFieldPrefixMapping`   | `TranslatorOption` | nil / unset                   | Rewrite the leading segments of dotted API paths ("address." → "addr."). A missing trailing dot is added to keys and values.                                                                                                                                                       |
| `WithRequiredFields`       | `TranslatorOption` | nil / unset                   | Storage paths every inclusion projection carries regardless of the request: the document ID, a tenant key, a version used for optimistic locking, or the sort keys a keyset paginator reads from the last row. A required path that overlaps a denied one is a construction error. |
| `WithStoragePathValidator` | `TranslatorOption` | default dotted-path validator | Grammar of the denied and required storage paths; the policy's API path validator never applies to them.                                                                                                                                                                           |

## Defaults and limits

| Constant                     | Value  |
|------------------------------|--------|
| `DefaultMaxExpressionLength` | `4096` |
| `DefaultMaxFieldNameLength`  | `128`  |
| `DefaultMaxFieldPathDepth`   | `8`    |
| `DefaultMaxPaths`            | `64`   |
| `DefaultParserCacheSize`     | `1000` |

## Named values

| Constant          | Meaning                                                |
|-------------------|--------------------------------------------------------|
| `ModeDefault`     | Omitted mask without paths; the listed paths with them |
| `ModeAll`         | Every authorized field (`*`), whatever the view        |
| `ModeNone`        | No field                                               |
| `ViewUnspecified` | The policy's default view (`WithDefaultView`)          |
| `ViewBasic`       | The BASIC view, configured with `WithView`             |
| `ViewFull`        | Every authorized field                                 |

## API

| Symbol                                   | Purpose                                                                                                                                                                                                                    |
|------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Spec`                                   | Spec is the parsed result of a fields expression: the requested API field paths, deduplicated and sorted. The zero value is an omitted mask; ModeAll explicitly requests all authorized fields, and ModeNone selects none. |
| `Mode`, `Fields`, `Selection`            | The selection mode; the authorized API paths (`Mode()`, `Paths()`); the resolved storage projection.                                                                                                                       |
| `View`, `View.Or`, `View.String`         | AIP-157 resource view; `Or(def)` replaces `ViewUnspecified` with a method's default.                                                                                                                                       |
| `PathValidator`                          | PathValidator validates an API or configured policy path. It must be safe for concurrent use.                                                                                                                              |
| `NewParser`                              | NewParser creates a new fields parser.                                                                                                                                                                                     |
| `ParserOption`                           | ParserOption is a functional option for configuring parserOptions.                                                                                                                                                         |
| `NewPolicy`                              | Build and validate the shared `Policy`: `ErrAllowlistRequired`, `ErrInvalidFieldPath`/`ErrUnsupportedPath`, `ErrAllFieldsUnresolvable`, `ErrInvalidView`, `ErrConflictingPolicy`.                                          |
| `PolicyOption`                           | Functional option of `NewPolicy`.                                                                                                                                                                                          |
| `Policy.Validate`, `Policy.DeniedFields` | Authorize a spec into `Fields`; inspect the denied API paths.                                                                                                                                                              |
| `Policy.DeclaresField`                   | Whether a path is an exact `WithAllowedFields` entry.                                                                                                                                                                      |
| `Policy.Select`, `Policy.ViewFields`     | Apply the mask-or-view rule; return the fields of one view.                                                                                                                                                                |
| `Policy.Views`, `Policy.DefaultView`     | The served views (`ViewFull` and the configured ones) and the default view.                                                                                                                                                |
| `NewTranslatorContext`                   | Bind a policy to the storage options of one backend; `ErrPolicyRequired` for nil.                                                                                                                                          |
| `metrics.TranslateTimer`, `NewTelemetry`              | Per-backend translation duration histogram `projection_<backend>_translate_duration_seconds`, used by the translators' `WithProjectionCollector`.                                                                          |
| `TranslatorOption`                       | TranslatorOption is a functional option for configuring translatorOptions.                                                                                                                                                 |

## Related packages

- [go-aip](../README.md)
- [fieldbehavior](../fieldbehavior/README.md)
- [fieldmask](../fieldmask/README.md)
- [fieldpolicy](../fieldpolicy/README.md)
- [filter](../filter/README.md)
- [listing](../listing/README.md)
- [orderby](../orderby/README.md)
