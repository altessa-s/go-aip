# Field masks

[fieldmask](../fieldmask/) provides protobuf path selection and validation; [fieldmask/update](../fieldmask/update/) applies update masks under
the AIP-203 field behaviors, plans and merges updates and orchestrates revision-aware patches; [fieldmask/extract](../fieldmask/extract/) reads
masks off request messages. They work on `proto.Message` values and descriptors only and have no gRPC dependency.

The companion package [`fieldbehavior`](fieldbehavior.md) handles the resource *payload*; this package handles the `FieldMask` *value*. For an update
driven by a mask, `update.Apply` with `update.WithCurrent` applies every AIP-203 rule on its own; `fieldbehavior` covers Create, updates without a mask
and responses.

## Transport wiring

go-atlas provides the gRPC server interceptor that applies this package on the wire: it classifies methods by their AIP prefix (`Update*`,
`Patch*` and `BatchUpdate*` run `Apply` on the request; `Get*`, `List*`, `Search*` and `BatchGet*` run `Filter` on the response),
reads the AIP-157 `x-goog-fieldmask` metadata read mask, resolves per-method and default extractors, writes the cleaned mask back, and turns
behavior violations into `InvalidArgument` with a BadRequest detail. Its options, errors and quick start are documented in go-atlas. The
sections below cover the `fieldmask` building blocks it uses, which any caller can use directly.

## Conformance

| Rule                                                                                | Implemented by                                                                                                                                                                                  | Behavior                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
|-------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| AIP-161: one path grammar, field names in declared or JSON spelling                 | every entry point (`ParsePaths`, `Resolve`, `Validate`, `Filter`, `Apply`, `Plan`, `Merge`, ...)                                                                                                | A segment may use the declared, JSON or protojson snake_case spelling of its field; results are spelled by declared names.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| AIP-161: no index into a repeated field                                             | every entry point, read masks included                                                                                                                                                          | `aliases.0` and `aliases.0.id` are a `*ValidationError` (a field error on `updateMask.paths[i]` from `Plan`), in every `PathValidationMode`. `FromMessage` and `FromSetFields` never emit them.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| AIP-161: `*` addresses every element of a repeated field or every value of a map    | every entry point                                                                                                                                                                               | A field of the elements must go through `*` (`aliases.*.display_name`); the implicit form `aliases.display_name` is rejected.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| AIP-161: map keys                                                                   | every entry point                                                                                                                                                                               | A key must be a valid value of the map's key type in its canonical form (`ports.443`, not `ports.0443`); the literal key `*` cannot be addressed.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| AIP-161: a `google.protobuf.Struct` is a `map<string, Value>`                       | every entry point (`Resolve`, `Validate`, `Filter`, `Prune`, `Apply`, `ValidateStrict`, `Plan`, `update.ResolvePath`, `MergePaths`, `Merge`); `FromMessage` and `FromSetFields`                 | `attrs` and `attrs.*` select the whole Struct; a key (`attrs.color`, ``attrs.`a.b` ``) one entry, replaced from the request or deleted when the request lacks it. `FromMessage` and `FromSetFields` emit top-level keys only.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| Extension beyond AIP-161: paths into dynamic JSON (default)                         | the same entry points                                                                                                                                                                           | Below a Struct key, in a `Value` field and through `*` of a `ListValue` a key addresses a nested Struct entry and `*` every entry or element (`attrs.work.from`, `attrs.items.*.name`, `value_data.key`, `list_data.*.name`); no index, no `ListValue` key without `*`. Read: exactly the addressed key is kept or removed; a value of another shape (a scalar or null, a list without `*`, an element of another shape under `*`) addresses nothing, so `Filter`/`Select` drop it unless another path keeps it (the other list elements stay in order) and `Prune` leaves it as it is. Update: replaced or deleted, intermediate Structs created only for a value to write, list elements in place by index (`ErrElementCountMismatch`). AIP-203 checks are unaffected: dynamic JSON has no field behaviors. |
| AIP-161 only: `WithStrictDynamicPaths(true)`                                        | `fieldmask.WithStrictDynamicPaths` for the read entry points; `update.WithStrictDynamicPaths` for `Apply`, `ValidateStrict`, `Merge`, `MergePaths`, `update.ResolvePath` and `Plan` (`NewSpec`) | A path below a Struct key (`attrs.color.hue`, `attrs.*.hue`) or into a `Value` or `ListValue` field is a `*ValidationError` (a field error on `updateMask.paths[i]` from `Plan`), in every `PathValidationMode`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| AIP-161: backtick-quoted segments                                                   | every entry point; `ToPaths`, `FromMessage`, `FromSetFields`, violation paths                                                                                                                   | A segment that is empty or holds a dot or a backtick is quoted, a literal backtick doubled: the key ``x`y.z`` is written ``labels.`x``y.z` ``.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| AIP-161: an invalid update path is INVALID_ARGUMENT                                 | `Apply` (`PathValidationEnforce`, the default), `ValidateStrict`, `Plan`, `Patch`                                                                                                               | Rejected before any mutation; `PathValidationWarn` and `PathValidationDisabled` are explicit opt-outs of `Apply`. Every raw path is checked on its own, a path another path of the mask covers included (see [Path grammar](#path-grammar)).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| AIP-134: an omitted `update_mask` updates every populated field                     | `Plan` and `Patch.Run` (nil mask), `Apply` and `ValidateStrict` (nil `FieldMask`), `update.FromSetFields`, `ImpliedPaths`                                                                       | The implied mask of the populated mutable leaf fields; identity, secret, immutable and server-managed fields are left out at every depth.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| AIP-134: an empty `update_mask` updates nothing                                     | `Plan`, `Patch.Run`, `Apply`, `ValidateStrict`                                                                                                                                                  | A no-op that checks no field behavior: `Plan` returns an `Empty` plan, `Patch.Run` saves nothing after the precondition check, `Apply` leaves the resource and the mask untouched, `ValidateStrict` returns an empty mask. A mask whose every path is dropped is a no-op too: `IsEmpty` on the cleaned mask mirrors `Plan.Empty`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| AIP-134: `*` is a full replacement                                                  | `Plan`, `Patch.Run`, `Apply`, `ValidateStrict`, `Merge`                                                                                                                                         | Every mutable field is replaced; stored server-managed values survive. `*` with any other path is `ErrWildcardNotAlone`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| AIP-134: `allow_missing`                                                            | `Patch.Run` with `WithAllowMissing(true)` and `Patch.Create`                                                                                                                                    | A missing resource is created from the request (OUTPUT_ONLY values cleared), the mask ignored once its paths are checked.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| AIP-154: the etag is optional                                                       | `Int64Precondition`, `TagPrecondition` (via [`etag.Precondition`](etag.md))                                                                                                                     | No etag is no precondition (a nil revision, the zero `etag.Tag`) unless the etag is required.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| AIP-154: a stale etag is ABORTED                                                    | `Int64Precondition.Check`/`Missing`, `TagPrecondition.Check`/`Missing`                                                                                                                          | `etag.ErrMismatch`, a `coreerrs.ErrConflict`, for an etag that does not name the stored version or tag, or any etag sent for a missing resource. `Save` enforces the revision atomically.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| AIP-163: `validate_only` runs every check and skips the write                       | `Patch.Run` with `WithValidateOnly(true)`                                                                                                                                                       | Precondition, mask checks, field behaviors and merge run; `Save` and `Create` are not called. The merged resource (or, with `allow_missing`, the would-be created one) is returned.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| AIP-203: OUTPUT_ONLY ignored, IMMUTABLE and IDENTIFIER compared, REQUIRED non-empty | `Apply`, `ValidateStrict`, `Plan`, `Patch`                                                                                                                                                      | See [Applying an update mask](#applying-an-update-mask) and the [AIP-203 conformance table](fieldbehavior.md#aip-203-conformance).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| AIP-193: field violations name the offending request field                          | `Plan`, `Patch.Run`                                                                                                                                                                             | A `coreerrs.FieldError` on `updateMask.paths[i]` whose chain holds a `*MaskPathError` (index, path as sent, resource field, cause).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| Merge of nested and collection paths                                                | `update.Merge`, `MergePaths`                                                                                                                                                                    | Top-level and nested fields, map keys and `*` sub-paths of repeated and map fields, all-or-nothing.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |

## Applying an update mask

`update.Apply(FieldMask, resource, update.WithCurrent(stored))` applies the mask under the AIP-203 field behaviors. Always pass the stored
resource: only with it can an unchanged IMMUTABLE or IDENTIFIER value be told from a change.

* OUTPUT_ONLY in the mask, at any depth → silently dropped, never an error; every OUTPUT_ONLY value left in the resource (inside a collection
  replaced whole, say) is cleared.
* IMMUTABLE or IDENTIFIER in the mask → equal to the stored value: dropped (ignored); different: violation. Without `WithCurrent`: violation, so a
  change is never applied or lost silently.
* A populated IDENTIFIER the mask does not name addresses the resource and is kept; with `WithCurrent` it must equal the stored one.
* REQUIRED in the mask with an absent or falsy value (empty string or bytes, zero, false, zero enum, empty list or map, unset message) →
  violation, nested paths included. A REQUIRED field absent from the mask may be omitted from the resource.
* A singular message named whole (`profile`) → replaced. When it holds an OUTPUT_ONLY, IMMUTABLE or IDENTIFIER field, directly or in a singular
  sub-message at any depth, the entry expands to its other fields, so the stored server-managed values survive; an IMMUTABLE or IDENTIFIER field
  the resource leaves unset keeps its stored value and a set one is compared. The REQUIRED fields of the written value are checked, recursing only
  into populated sub-messages.
* A collection or element named whole (`labels`, `labels.k`, `aliases.*`) → replaced as new values: the REQUIRED fields of its elements are
  checked, their IMMUTABLE and IDENTIFIER values are not compared.
* A path into collection elements, through a map key (`labels.k.org`) or the `*` wildcard (`aliases.*.org`) → the rules above per element, a map
  entry compared with the stored entry of the same key, a list element with the stored element of the same index.
* A set IDENTIFIER the mask does not name, top-level, in a singular sub-message branch (`profile.display_name`), or in the collection elements a
  sub-path addresses, is added to the mask so the filter keeps it in the body. An IDENTIFIER that is also OUTPUT_ONLY is never kept.
* A field in the mask that the resource leaves unset is cleared; a default value is never written.
* `*` alone → a full replacement: every field but the OUTPUT_ONLY ones, with the replacement rule above applied to the whole resource.
* An empty mask → a no-op: the body and the mask are left untouched and no field behavior is checked. A mask whose every path is dropped
  (OUTPUT_ONLY, unchanged IMMUTABLE or IDENTIFIER) is a no-op too once its checks pass: the mask comes back empty and the body as sent, so
  `IsEmpty` on the cleaned mask reports that nothing is updated, as `Plan.Empty` does. Never store the body of a no-op as the resource.
* A nil mask → the omitted `update_mask`: the implied mask of `update.FromSetFields` is applied.

The policy options of `update.Apply` govern the top-level fields as they govern `Spec.Plan`: a field of `WithImmutableFields` is IMMUTABLE,
and a field of `WithIdentityFields` (`id` and `etag` by default) is IDENTIFIER when the mask names it. Unnamed, such a field addresses the
resource and is kept, but not compared: a stale etag is the service's concurrency check.

Violations are returned as `*BehaviorError` (matching `ErrBehaviorViolation`, mapping to INVALID_ARGUMENT), one `FieldViolation` per entry, each
carrying the dot-separated field path, the behavior and a reason; neither the mask nor the resource is changed. An invalid path is a `*ValidationError`;
a stored resource of another type is `ErrMessageMismatch`. On success, drop the IDENTIFIER entries added above with `DropIdentifiers` and write the
cleaned mask back with `extract.SetUpdateMask`, so the handler sees a coherent `(mask, resource)` pair and can treat its paths as a clean instruction
list.

`FromProtoFieldMask` keeps the presence of the `update_mask`: an omitted one is a nil `FieldMask` and one sent empty a non-nil empty `FieldMask`
(`Clone` and `ToProtoFieldMask` keep it too). `Apply` on a nil mask applies `update.FromSetFields(resource)`, the implied mask of an omitted one; build
it yourself to see the cleaned mask. It covers every populated editable field: running `Apply` against it compares the IMMUTABLE fields the client
populated with the stored resource, leaves IDENTIFIER and OUTPUT_ONLY fields out of the mask (the identifier stays in the body, an OUTPUT_ONLY value is
cleared), and keeps OUTPUT_ONLY fields out of the writeback mask.

`update.ValidateStrict(mask, resource, update.WithCurrent(stored))` applies the same rules without touching the resource and returns the
cleaned mask; a nil mask is validated as its implied mask, and an empty mask is a no-op that returns an empty mask without checking any field
behavior.

## Update plans and patches

`Spec.Plan(resource, req.GetUpdateMask(), update.WithCurrent(stored))` applies the same rules to a `Plan` and keeps the presence of
the mask: nil is the implied mask, an empty mask is a no-op and `*` a full replacement. Explicit paths may name nested fields, map keys and `*`
sub-paths of repeated and map fields; `Plan.Paths` feed `MergePaths`.

```go
spec, err := update.NewSpec(&pb.Widget{}, update.WithImmutableFields("code"))
if err != nil {
    return err
}

plan, err := spec.Plan(req.GetWidget(), req.GetUpdateMask(), update.WithCurrent(stored))
if err != nil {
    return err // coreerrs.FieldError on updateMask.paths[i], holding a *MaskPathError
}
if plan.Empty() {
    return nil
}

merged := proto.Clone(stored)
if err := update.MergePaths(merged, req.GetWidget(), plan.Paths); err != nil {
    return err
}
```

A refusal names the offending path by its index in the mask. `errors.AsType[*update.MaskPathError](err)` returns its `Index`, `Path` (as the
request sent it), `Field` (the resource field concerned, `aliases.*.id` say) and `Err` (`fieldmask.ErrInvalidPath`, `fieldmask.ErrWildcardNotAlone`,
`ErrProtectedField`, `ErrBehaviorViolation` or `ErrElementCountMismatch`); a refusal of the implied or `*` mask has index -1 and the bare mask
field.

`Patch[E, P, V]` runs the masked read-modify-write with an explicit `Precondition[E, V]`. `Run(ctx, in, tag, req.GetUpdateMask(), opts...)` parses
the revision, refuses malformed and unknown paths before the read, checks the revision against the stored resource (even for an empty plan), plans
against it (a changed IMMUTABLE or IDENTIFIER value is a `coreerrs.FieldError` on the resource field), clears the request's OUTPUT_ONLY values,
merges the planned paths and saves. The AIP-154 presets are built on [etag](etag.md): `Int64Precondition` guards an integer version whose etag
is `etag.FromRevision(version)`, with revision `*int64`, nil when the request sends no etag (no version check, unless `Required`);
`TagPrecondition` guards an opaque etag (a content hash, a stored tag) with an `etag.Precondition` rule and revision `etag.Tag`. A present etag
that does not name the stored state is `etag.ErrMismatch` (`coreerrs.ErrConflict`, ABORTED), a malformed one a `coreerrs.FieldError` on the etag
field. `WithAllowMissing(true)` creates a missing resource with `Patch.Create` after `Precondition.Missing` accepts it. `WithValidateOnly(true)`
(AIP-163, see [validateonly](validateonly.md)) runs every step but calls neither `Save` nor `Create` and returns the resource the write would
store; values the store assigns on save (a new etag, an update time) are not computed.

## Path grammar

The path parser used by `ParsePaths`, `FromProtoFieldMask` and every operation on a `fieldmask.FieldMask` follows the AIP-161 grammar:

| Construct                            | Example                                 | Semantics                                                                                                  |
|--------------------------------------|-----------------------------------------|------------------------------------------------------------------------------------------------------------|
| Plain dotted path                    | `profile.display_name`                  | Descend into a singular message field.                                                                     |
| Backtick-quoted segment              | ``reviews.`John Smith`.score``          | One segment; quoting is optional when the key holds no dot or backtick.                                    |
| Backtick-quoted segment with a dot   | ``metadata.`google.com/project` ``      | Required for a key holding a dot; `ToPaths` re-quotes it, so the round trip is lossless.                   |
| Doubled backtick                     | ``metadata.`x``y.z` ``                  | A literal backtick inside a quoted segment: the key ``x`y.z``.                                             |
| Wildcard segment on a repeated field | `aliases.*.display_name`                | Apply the nested mask to every element. `aliases.*` (leaf) keeps the whole list (or clears it on `Prune`). |
| Wildcard segment on a map field      | `labels.*.display_name`                 | Same shape over a map. A specific-key entry (`labels.admin.id`) wins over `*` for matched keys.            |
| Typed map key                        | `ports.443`, `flags.true`               | The key in the canonical form of the map's key type; `ports.0443` and `flags.yes` are rejected.            |
| Indexed access on a repeated field   | `authors.0` / `authors.0.given_name`    | **Rejected**, on read and on update.                                                                       |
| Struct entry                         | `attrs.color`, `attrs.*`                | A `google.protobuf.Struct` is a `map<string, Value>`.                                                      |
| Dynamic JSON (default)               | `attrs.work.from`, `attrs.items.*.name` | Beyond AIP-161: keys and `*` walk the JSON; **rejected** with `WithStrictDynamicPaths(true)`.              |
| Element field without `*`            | `authors.given_name`                    | **Rejected**: write `authors.*.given_name`.                                                                |

Clients send paths with `ParsePaths` or `FromProtoFieldMask`, which reject malformed paths (`a.`, `.a`, `a..b`, an unclosed backtick) up front.
`FromPaths` is for trusted paths written in code: it keeps a malformed path verbatim, so it never widens to its parent and every schema check rejects
it. A `google.protobuf.Struct` is addressed by its keys as a `map<string, Value>`, and by default a path walks the dynamic JSON below by key and `*`;
`WithStrictDynamicPaths(true)` keeps to AIP-161, where an entry's value, a `Value` and a `ListValue` are selected whole only.

A `FieldMask` is a set of paths none of which covers another, so a path below another path of the same mask (`profile.id` with `profile`) would
collapse into it and escape every later check: `["description", "description.no_such"]`, `["aliases", "aliases.0.id"]` or `["profile",
"profile.id"]` (naming an IDENTIFIER) would pass as `description`, `aliases` or `profile`. Every raw path is therefore checked on its own before the
mask collapses:

* `ParsePaths` and `FromProtoFieldMask`, which know no schema, refuse a covered raw path as a `*ValidationError` naming it (`path is covered by
  path "profile" of the same mask`); an exact duplicate is accepted. `FromPaths` trusts its paths and still collapses them.
* `Spec.Plan` and `Patch.Run` resolve every raw path and check its field behaviors on its own, a covered path as if sent alone, and plan the
  collapsed mask: `["profile", "profile.id"]` without `WithCurrent` is refused on `updateMask.paths[1]`.
* `Apply` and `ValidateStrict` resolve every path of their `FieldMask` on its own and check a path that another one covers once both are
  resolved (`backupOwner` with `backup_owner.org`) the same way, an atomic sub-path included.

`update.Apply` strips `OUTPUT_ONLY` fields transitively through wildcard sub-masks before writing back: `aliases.*.updated_at` (where
`updated_at` is `OUTPUT_ONLY` on `Profile`) is removed from the cleaned mask, and the parent collection entry is dropped too if the wildcard
sub-mask collapses to empty.

## Limits

The cost of a client mask is bounded in every entry point before any parsing or descriptor work: at most `WithMaxPaths` paths (default 1000),
`WithMaxPathLength` bytes per raw path (default 1024) and `WithMaxPathDepth` segments per path, `*` and quoted keys included (default 32). Zero
lifts a limit. The limits are `fieldmask.Option` values of the read entry points and `update.Option` values of the update entry points
(through `NewSpec` for `Plan` and `Patch`; `ParsePaths` uses the defaults), and they hold in every `PathValidationMode`. A violation is
`ErrMaxPathsExceeded`, `ErrMaxPathLengthExceeded` or `ErrMaxPathDepthExceeded`, each matching `ErrLimitExceeded` and `ErrInvalidPath`, inside
the usual structured error: a `*ValidationError`, or from `Plan` a field error on `updateMask.paths[i]` (the bare `updateMask` for too many
paths) wrapping a `*MaskPathError`. `FromMessage` and `FromSetFields` stop a recursive message at the default depth, the implied masks at the
policy's depth.

## Request extraction

The [extract](../fieldmask/extract/) package reads two request-message shapes, with one return type (`*fieldmaskpb.FieldMask`):

| AIP                          | Transport                                                          | Helper                         |
|------------------------------|--------------------------------------------------------------------|--------------------------------|
| AIP-134 (Standard Update)    | `update_mask` field on the request message                         | `UpdateMask` / `DefaultUpdate` |
| AIP-157 via AIP-161 — legacy | `read_mask` field on the request message — AIP-161 deprecates this | `ReadMask` / `DefaultRead`     |

The modern AIP-157 transport, the `x-goog-fieldmask` gRPC metadata key (or the HTTP `$fields` parameter grpc-gateway maps onto it), is read
from `metadata.FromIncomingContext`, so its extractor is part of the go-atlas gRPC interceptor. The reflection helpers here stay gRPC-unaware
(descriptors only) and work anywhere you have a `proto.Message`.

| Function                                 | Purpose                                                                                        |
|------------------------------------------|------------------------------------------------------------------------------------------------|
| `UpdateMask(req, opts...)`               | Locate the `update_mask` field and the sibling resource sub-message via descriptor reflection. |
| `ReadMask(req, opts...)`                 | Locate the `read_mask` field on a Get/List/Search request.                                     |
| `SetUpdateMask(req, mask, opts...)`      | Write the cleaned mask back onto the request. Returns `ErrFieldNotSettable` on shape mismatch. |
| `IsCanonicalUpdateRequest(req, opts...)` | Report whether the request has the canonical AIP-134 shape the reflection helpers expect.      |

`WithMaskField` and `WithResourceField` select other field names than `update_mask`/`read_mask` and the inferred resource field.

## Custom extractors

The built-in reflection extractor assumes the canonical AIP-134 shape — `UpdateXxxRequest{Xxx resource, FieldMask update_mask}` — and locates the
mask on the top level of the request. Requests that nest the mask inside an `Options` sub-message (`req.Options.UpdateMask`) are not reachable from
there. Build a typed extractor instead. The function types and the generic builders have no gRPC dependency, so HTTP handlers and batch jobs
reuse them as well as the go-atlas interceptor.

| Symbol                                                                   | Purpose                                                                                         |
|--------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------|
| `UpdateFunc`                                                             | `func(ctx, req) (mask, resource, writeback, ok)`. Returning `ok=false` is a silent passthrough. |
| `ReadFunc`                                                               | `func(ctx, req) (mask, ok)`.                                                                    |
| `NewUpdateFunc[ReqT, ResT proto.Message](getMask, getResource, setMask)` | Generic builder over typed getters. `setMask` may be nil to skip writeback.                     |
| `NewReadFunc[ReqT proto.Message](getMask)`                               | Generic builder for read masks.                                                                 |
| `DefaultUpdate(opts ...Option)` / `DefaultRead(...)`                     | Wrap the reflection helpers above in the `UpdateFunc` / `ReadFunc` shape.                       |
| `ChainRead(fns ...)` / `ChainUpdate(fns ...)`                            | First-ok-wins composition.                                                                      |

The writeback closure an `UpdateFunc` returns is called by the caller with the cleaned mask after `Apply` removes OUTPUT_ONLY entries; for the nested
shape below it updates `req.Options.UpdateMask`. A nil writeback skips the step.

From `ExampleChainUpdate` ([fieldmask/extract/extractor_example_test.go](../fieldmask/extract/extractor_example_test.go)), where `pb` holds the
request messages:

```go
extractor := extract.ChainUpdate(
    extract.NewUpdateFunc(
        func(r *pb.NestedUpdateResourceRequest) *fieldmaskpb.FieldMask { return r.GetOptions().GetUpdateMask() },
        func(r *pb.NestedUpdateResourceRequest) *pb.Resource { return r.GetResource() },
        func(r *pb.NestedUpdateResourceRequest, m *fieldmaskpb.FieldMask) { r.Options.UpdateMask = m },
    ),
    extract.DefaultUpdate(),
)

raw, resource, writeback, ok := extractor(ctx, req)
if !ok {
    return nil // no update mask on this request
}

stored, err := s.repo.Get(ctx, resourceName) // the resource being updated
if err != nil {
    return err
}

mask, err := fieldmask.FromProtoFieldMask(raw)
if err != nil {
    return err // a malformed path
}
if err := update.Apply(mask, resource, update.WithCurrent(stored)); err != nil {
    return err
}

if writeback != nil {
    update.DropIdentifiers(mask, resource)
    writeback(mask.ToProtoFieldMask())
}
```

## Selection

FieldMask supports Filter, Select, Prune, Union, Intersection, Difference and Resolve; the update package adds Merge. `Filter`, `Select` and `Prune`
validate and resolve the mask first and leave the message untouched on an invalid path. An empty mask selects no paths (`Filter` keeps everything,
`Select` clears everything); a standalone wildcard selects everything; a wildcard mixed with other paths is `ErrWildcardNotAlone`.

`update.Merge(mask, dst, src)` and `update.MergePaths(dst, src, paths)` copy the selected fields: a field path replaces the field, a nested path one
leaf, a map key path one entry, and a sub-path through a map key or `*` the fields of the elements it addresses in place.

```go
package example

import (
    "google.golang.org/protobuf/proto"

    "github.com/altessa-s/go-aip/fieldmask"
    "github.com/altessa-s/go-aip/fieldmask/update"
)

func ValidateUpdate(resource, stored proto.Message, paths []string) (fieldmask.FieldMask, error) {
    mask, err := fieldmask.ParsePaths(paths...)
    if err != nil {
        return nil, err
    }
    return update.ValidateStrict(mask, resource,
        update.WithIdentityFields("id", "etag"),
        update.WithUpdateMaskFieldName("update_mask"),
        update.WithCurrent(stored))
}
```

## Shared update policy

`update.NewSpec(resource, opts...)` captures the resource descriptor and the policy options. Plan validates selected paths and protects
identifiers, immutable fields, and server-managed fields. Descriptor mismatches fail before mutation. Default identity fields are id and etag;
`DefaultAtomicMessage` defines the well-known values that must be selected as a whole.

| Option                    | Responsibility                                                                       |
|---------------------------|--------------------------------------------------------------------------------------|
| `WithIdentityFields`      | Replace conventional identity paths; an empty set disables name-based identity rules |
| `WithImmutableFields`     | Add application immutable paths                                                      |
| `WithImpliedSkipFields`   | Exclude paths from implied masks                                                     |
| `WithAtomicMessage`       | Define message boundaries that cannot be partially updated                           |
| `WithUpdateMaskFieldName` | Attribute update errors to the configured mask field                                 |
| `WithResolver`            | Supply fieldbehavior.Resolver for nested behavior lookup                             |

Use the same policy with `update.FromSetFields`, `ExpandMessagePaths`, `DropIdentifiers`, `ImpliedPaths`, `update.ResolvePath` and `MergePaths`. `Apply`
and `ValidateStrict` take the same options next to the per-request ones (`WithCurrent`, `WithPathValidation`). This keeps implied masks, expansion,
protected fields, identity preservation, and atomic boundaries consistent. A nil resolver selects annotation lookup. A shared resolver must be
concurrency-safe and may also be passed to `fieldbehavior.WithResolver` so payload stripping and path planning use the same metadata.

## Errors

`*fieldmask.ValidationError` matches `fieldmask.ErrInvalidPath`; its `Cause` may refine it (`ErrWildcardNotAlone`, a limit error matching
`ErrLimitExceeded`). `*update.StrictError` retains its cause and matches `ErrInvalidUpdateMask`. Behavior failures match `ErrBehaviorViolation`. `Plan`
and `Patch.Run` return a `coreerrs.FieldError` on `updateMask.paths[i]` wrapping a `*MaskPathError`, so `ErrInvalidPath`, `ErrProtectedField` and the
others stay distinguishable. `Int64Precondition` and `TagPrecondition` report a stale or misplaced etag as `etag.ErrMismatch` (a
`coreerrs.ErrConflict`). `WithPathValidation` and `WithPathValidationReporter` control the path-validation mode and reporting of `update.Apply`.

See the option references of [fieldmask](../fieldmask/README.md#options) and [update](../fieldmask/update/README.md#options), [field
behavior](fieldbehavior.md), and [projection](../projection/) for payload stripping and storage read selections.

## See also

* [`fieldmask`](../fieldmask/README.md) — the underlying `FieldMask` operations; [`update`](../fieldmask/update/README.md) and
  [`extract`](../fieldmask/extract/README.md).
* [`fieldbehavior.md`](fieldbehavior.md) — sibling AIP-203 payload stripper; pair with `fieldmask` for full AIP-133/134/157 wire semantics.
* **AIP-134** — Standard Update method ([aip.dev/134](https://google.aip.dev/134)).
* **AIP-154** — Resource freshness validation with etags ([aip.dev/154](https://google.aip.dev/154)).
* [`etag.md`](etag.md) and [`validateonly.md`](validateonly.md) — the AIP-154 and AIP-163 packages `Patch` builds on.
* **AIP-163** — Change validation with `validate_only` ([aip.dev/163](https://google.aip.dev/163)).
* **AIP-157** — Partial responses with `read_mask` ([aip.dev/157](https://google.aip.dev/157)).
* **AIP-161** — Field masks: path grammar (backtick-quoted segments, `*` wildcards, indexed-access
  restriction) ([aip.dev/161](https://google.aip.dev/161)).
* **AIP-203** — `field_behavior` annotations ([aip.dev/203](https://google.aip.dev/203)).
