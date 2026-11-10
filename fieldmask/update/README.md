# update

```go
import "github.com/altessa-s/go-aip/fieldmask/update"
```

Package `update` applies a [fieldmask](../README.md) `FieldMask` to an update request: AIP-134 update masks, the AIP-203 field behaviors, AIP-154 etag
preconditions and AIP-163 validate_only. It owns the dependencies on [etag](../../etag) and [fieldbehavior](../../fieldbehavior); paths are read by
the [path grammar](../README.md#path-grammar-aip-161) of `fieldmask`. The [guide](../../docs/fieldmask.md) maps every rule to the function that
implements it.

Pass the stored resource with `WithCurrent` to `ApplyMask`, `Apply`, `ValidateStrict` and `Spec.Plan`:

* `OUTPUT_ONLY` paths are dropped from the mask at any depth, never an error, and their client values are cleared.
* `IMMUTABLE` and `IDENTIFIER` paths are compared with the stored value: an unchanged value is ignored, a changed one is a violation
  (`INVALID_ARGUMENT`). Without `WithCurrent` every such path is a violation, so a change is never applied or lost silently. An `IDENTIFIER` the mask
  does not name addresses the resource; with `WithCurrent` it must equal the stored one.
* A `REQUIRED` path with an absent or falsy value is a violation; a `REQUIRED` field absent from the mask may be omitted.
* A singular message named whole (`profile`) is replaced through its other fields when it holds an `OUTPUT_ONLY`, `IDENTIFIER` or `IMMUTABLE` field
  (directly or in a singular sub-message): the stored server-managed values survive, an unset `IMMUTABLE` or `IDENTIFIER` keeps its stored value and a
  set one is compared. The `REQUIRED` fields of the written value are checked, recursing only into populated sub-messages.
* Repeated and map fields, and their elements named whole (`labels.k`, `aliases.*`), are replaced as a whole: their elements are new values whose
  `IMMUTABLE` and `IDENTIFIER` values are not compared. A sub-path into collection elements, through a map key (`labels.k.id`) or the `*` wildcard
  (`aliases.*.id`), updates the elements in place and follows the rules above per element (a map entry compared with the stored entry of the same key,
  a list element with the stored element of the same index). Each element such a sub-path addresses keeps its set `IDENTIFIER` fields:
  `labels.k.display_name` adds `labels.k.id` to the mask so the filter keeps it, and `DropIdentifiers` removes it again.

## Operations

| Function               | Description                                                                                                                                   |
|------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------|
| `ApplyMask`            | Apply a raw `update_mask` as sent, checking every path on its own; return the effective mask to write back (see [ApplyMask](#applymask))      |
| `Apply`                | Apply the AIP-203 rules, then clear fields outside the mask in place (mutates the mask)                                                       |
| `ValidateStrict`       | Validate an update mask under the rules of `Apply` without mutating the resource; return the cleaned mask                                     |
| `DropIdentifiers`      | Remove the `IDENTIFIER` entries `Apply` added, before handing the mask on (mutates)                                                           |
| `ExpandMessagePaths`   | Expand message-typed leaves into their sub-fields for a full replacement update                                                               |
| `FromSetFields`        | The update mask an omitted `update_mask` implies: `fieldmask.FromSetFields` minus identifiers and server-managed sub-fields                   |
| `ImpliedPaths`         | The implied leaf paths of an omitted `update_mask`, skipping identity, secret, immutable and server-managed fields                            |
| `ResolvePath`          | An update path spelled by declared names, or `""`                                                                                             |
| `Merge`, `MergePaths`  | Copy the selected fields from `src` to `dst` (see [Read-modify-write updates](#read-modify-write-updates))                                    |
| `NewSpec`, `Spec.Plan` | Classify a resource once and resolve one request's mask into a `Plan` (see [Update plans](#update-plans-and-patches-aip-134-aip-154-aip-163)) |
| `Patch.Run`            | The whole read-modify-write with a revision precondition, allow_missing and validate_only                                                     |

## Behavior

A field carrying several behaviors is judged by presence with a fixed precedence (`OUTPUT_ONLY` dropped, then `IDENTIFIER`, then `IMMUTABLE` compared,
then `REQUIRED`), so `[REQUIRED, IMMUTABLE]` is compared whatever the declaration order, and a violation names the governing behavior. An
`OUTPUT_ONLY` descendant never causes an error: a replaced message keeps it by expansion. Nested `OUTPUT_ONLY` entries are removed from the mask
before set identifiers are preserved (an `IDENTIFIER` that is also `OUTPUT_ONLY` is never preserved), so a branch that named only such fields
disappears. On a violation neither the mask nor the message is changed.

`Apply`, `ValidateStrict`, `ApplyMask` and `Spec.Plan` refuse a path that sets a oneof member whose other member is, or holds, an `IMMUTABLE` or
`IDENTIFIER` field (or a top-level identity or immutable field of the policy) when the stored resource holds that member, and without `WithCurrent`
whatever it holds: setting one member clears the others, so the write would delete the protected value unnamed (`items.k.free` beside a stored
`items.k.pinned`). Every mask is checked, implied and `*` included; a collection replaced whole holds new elements and is not.

`Apply` and `ValidateStrict` keep the presence of the `update_mask` (AIP-134), as `fieldmask.FromProtoFieldMask` builds it, the way `Spec.Plan` and
`Patch.Run` do with the `*fieldmaskpb.FieldMask`:

- a nil mask (`update_mask` omitted) is the implied mask of `FromSetFields`;
- a non-nil empty mask (`update_mask` sent empty) is a no-op: `Apply` leaves the message and the mask untouched, `ValidateStrict` returns an empty
  mask, and no field behavior is checked; only a stored resource of another type is still `ErrMessageMismatch`;
- `*` is a full replacement.

A mask whose every path is dropped (OUTPUT_ONLY, unchanged IMMUTABLE or IDENTIFIER) is a no-op too: the cleaned mask comes back empty and the message
as sent. So `FieldMask.IsEmpty` on the cleaned mask reports that the update changes nothing, as `Plan.Empty` does; never store the message of a no-op
as the resource.

The mask policy options govern the top-level fields of `Apply` and `ValidateStrict` as they govern `Spec.Plan`: a field named by `WithImmutableFields`
is IMMUTABLE, and a field named by `WithIdentityFields` (`id` and `etag` by default) is IDENTIFIER when the mask names it. Unnamed, such an identity
field addresses the resource and is kept, but not compared with the stored value: a stale etag is the service's concurrency check (AIP-154).

## ApplyMask

`ApplyMask(req.GetUpdateMask(), resource, update.WithCurrent(stored))` applies the mask exactly as the client sent it and returns the effective mask
to write back to the request (`effective.ToProtoFieldMask()`). It is the entry point for a raw client mask:

| Use                  | When                                                                                                                                       |
|----------------------|--------------------------------------------------------------------------------------------------------------------------------------------|
| `ApplyMask`          | A handler that receives the request resource and its raw `update_mask` and stores the filtered resource; the write-back mask comes with it |
| `Apply`              | A `FieldMask` built or transformed in code; it returns the cleaned mask in place, IDENTIFIER entries included (`DropIdentifiers`)          |
| `ValidateStrict`     | Checking a mask without touching the resource                                                                                              |
| `Spec.Plan`, `Patch` | A read-modify-write of the stored resource: `Plan.Paths` for `MergePaths`, write-only secrets, AIP-193 errors on `updateMask.paths[i]`     |

`fieldmask.FromProtoFieldMask` then `Apply` sees a `FieldMask`, a set of paths none of which covers another: `FromProtoFieldMask` collapses a raw path
another one covers (`profile.id` with `profile`) into the covering path, so `Apply` checks only the latter. `ApplyMask` reads the raw paths and checks
each one, a covered path included, as if sent alone.

The presence of the mask decides the mode (AIP-134): nil is the implied mask of `FromSetFields`, applied and returned; a mask with no paths is a no-op
returning a non-nil empty mask, or the implied mask with `WithEmptyMaskImplied(true)`; a mask that names paths is never implied, even when every path
is dropped (it is then a no-op too); `*` alone is a full replacement. The checks run before the resource is touched, in this order:

1. The limits (`WithMaxPaths`, `WithMaxPathLength`, `WithMaxPathDepth`) on the raw paths, duplicates included, in every `PathValidationMode`.
2. The grammar of each raw path: `""` and malformed paths, and `*` with any other path (a second `*` included, `fieldmask.ErrWildcardNotAlone`).
   `PathValidationEnforce` refuses, `PathValidationWarn` reports to `WithPathValidationReporter` and drops, `PathValidationDisabled` drops.
3. The schema and the field behaviors of each raw path on its own, before the mask collapses. An unknown path follows the mode of step 2; an index
   into a repeated field, a sub-path into an atomic message and, with `WithStrictDynamicPaths`, a path below a `Struct` key are refused in every mode.

The resource is then mutated exactly as `Apply` mutates it. The effective mask is spelled by declared names; `*` and every message named whole that
holds an `OUTPUT_ONLY`, `IMMUTABLE` or `IDENTIFIER` field are expanded so that no path covers one; `OUTPUT_ONLY` paths, unchanged `IMMUTABLE` and
`IDENTIFIER` paths and the `IDENTIFIER` entries `Apply` adds are left out; `INPUT_ONLY` fields stay. A collection or map entry named whole stays one
path: its elements are new values whose `IMMUTABLE` and `IDENTIFIER` values are kept as sent.

An omitted mask or `*` whose request sets an `IMMUTABLE` value is refused without `WithCurrent`, as with `Apply`: the stored value is unknown, so a
change cannot be told from an unchanged value (AIP-203). Pass the stored resource, or name the paths to update.

Errors are those of `Apply`: `*fieldmask.ValidationError` (`fieldmask.ErrInvalidPath`, a limit sentinel, `fieldmask.ErrWildcardNotAlone`),
`*BehaviorError` (`ErrBehaviorViolation`) and `ErrMessageMismatch` for a nil resource or a stored one of another type. `WithStrict(true)` checks as
`ValidateStrict` does: every path is enforced whatever `WithPathValidation` says, and every refusal is a `*StrictError` (`ErrInvalidUpdateMask`)
naming the offending path, or the mask field of `WithUpdateMaskFieldName` for a mask-level one.

```go
effective, err := update.ApplyMask(req.GetUpdateMask(), req.GetWidget(), update.WithCurrent(stored))
if err != nil {
    return nil, err // *fieldmask.ValidationError, *BehaviorError or ErrMessageMismatch
}
if effective.IsEmpty() {
    return toProto(stored), nil // the update changes nothing
}
req.UpdateMask = effective.ToProtoFieldMask()
```

## Path validation in Apply

`Apply` rejects a path the grammar or the schema rejects before any mutation, so a typo cannot clear a sibling subtree. `WithPathValidation` opts out:

| Mode                     | Behavior                                                                                                                    |
|--------------------------|-----------------------------------------------------------------------------------------------------------------------------|
| `PathValidationEnforce`  | Default. Reject with `*fieldmask.ValidationError` before any mutation (AIP-161: an invalid update path is INVALID_ARGUMENT) |
| `PathValidationWarn`     | Report every invalid path to `WithPathValidationReporter` and apply the mask; such a path matches no field                  |
| `PathValidationDisabled` | Ignore invalid paths: they match no field, so a mistyped path clears the field it was meant to keep                         |

An index into a repeated field and a path into an atomic message (`delete_time.seconds`, and `delete_time.typo`, whose tail is no field) are rejected
in every mode. Every path is checked on its own before the mask collapses: `Spec.Plan` checks a raw path
another one covers (`profile.id` with `profile`) as if sent alone, and `Apply` and `ValidateStrict` do so for a path of their `FieldMask` that another
one covers once both are resolved (`backupOwner` with `backup_owner.org`). `fieldmask.ParsePaths` and `fieldmask.FromProtoFieldMask` collapse a raw
path another one covers before `Apply` sees it; the covering path still enforces the rules over its subtree.

```go
// Surface bad paths without breaking callers while a fix rolls out.
err := update.Apply(mask, msg,
    update.WithCurrent(stored),
    update.WithPathValidation(update.PathValidationWarn),
    update.WithPathValidationReporter(func(ve *fieldmask.ValidationError) { slog.Warn("bad mask path", "path", ve.Path) }),
)
```

## Wildcards

`Apply` drops OUTPUT_ONLY fields transitively through element sub-masks: `aliases.*.updated_at` and `labels.k.updated_at` (where `updated_at` is
OUTPUT_ONLY on `Profile`) are removed from the writeback mask before the handler sees it. When an element sub-mask becomes empty after the strip, it
is dropped, and so is a parent collection left without entries.

A `*` naming every element whole (`delegates.*`) covers a key path beside it (`delegates.k.org`), as `Spec.Plan` and `MergePaths` read it: every
entry is replaced, `k` included, and the key path is checked as if sent alone. A `*` sub-path of a map (`delegates.*.email`) addresses the entries no
key path names; when the check drops such a key path (an unchanged `IMMUTABLE` value), the `*` is replaced by the keys of the request entries it
addresses (`delegates.j.email`), so the cleaned mask and a later `MergePaths` never touch the entry the `*` was checked without. Below the elements
of another collection the entries cannot be named per element, and such a mask is refused.

## Read-modify-write updates

For a handler that applies an update mask itself, typically a read-modify-write of a stored resource:

| Symbol                             | Description                                                                                                 |
|------------------------------------|-------------------------------------------------------------------------------------------------------------|
| `Merge(mask, dst, src, ...)`       | Copy the selected fields from `src` (deep copy; unset in `src` clears `dst`); an empty mask changes nothing |
| `MergePaths(dst, src, paths, ...)` | The same merge from raw update paths, with the policy options: the merge of a `Plan`                        |
| `ResolvePath(md, path, ...)`       | The update path spelled by declared names, or `""` when `path` is not an update path                        |

`Merge` and `MergePaths` accept every update path: top-level and nested fields, map keys and `*` on repeated and map fields, in any spelling of their
field segments. A field path replaces the field (a trailing `*` replaces the whole collection) and a nested path only that leaf, keeping its siblings.
A map key path (`labels.k`) replaces that entry, or deletes it when `src` does not hold it. A sub-path through a map key (`labels.k.display_name`)
updates that entry in place; through `*` on a map, every entry `src` holds that no other path names by key. A sub-path through `*` on a repeated field
(`aliases.*.display_name`) updates every element of `dst` from the `src` element of the same index, so `src` must hold as many elements as `dst` or
none (`ErrElementCountMismatch`). `*` alone replaces every top-level field; `*` with other paths is `fieldmask.ErrWildcardNotAlone`; a path that is
not an update path is a `*fieldmask.ValidationError`.

A mask computed with `fieldmask.WithSkipFields` is empty when every populated field is skipped; `Merge` then changes nothing. Nil or typed-nil
messages, or messages that are not of one Go type and one descriptor (a `dynamicpb` message does not mix with the generated type of its descriptor),
are `ErrMessageMismatch`. Every check runs before the first write, so `dst` is changed all-or-nothing.

```go
mask, err := fieldmask.FromProtoFieldMask(req.GetUpdateMask())
if err != nil {
    return nil, err // *ValidationError
}
if req.GetUpdateMask() == nil { // omitted: the implied mask of the populated fields
    mask = fieldmask.FromSetFields[fieldmask.FieldMask](req.GetBook(), fieldmask.WithSkipFields("name", "etag"))
}
merged := proto.Clone(current)
if err := update.Merge(mask, merged, req.GetBook()); err != nil {
    return nil, err // ErrWildcardNotAlone, *ValidationError, ErrElementCountMismatch or ErrMessageMismatch
}
```

## Update plans and patches (AIP-134, AIP-154, AIP-163)

`Spec` classifies the top-level fields of a resource once (`spec, err := update.NewSpec(&pb.Widget{}, update.WithImmutableFields("code"))`): `id`,
`etag` and `IDENTIFIER` fields are identity, `OUTPUT_ONLY` fields server-managed, `INPUT_ONLY` fields write-only secrets, `IMMUTABLE` fields and the
names passed are immutable, every other field is mutable. `spec.Plan(in, req.GetUpdateMask(), update.WithCurrent(stored))` resolves the update mask
into a `Plan` by its presence:

- a nil mask (`update_mask` omitted) is `ImpliedPaths`: the populated leaf fields of the request, descending into singular messages, skipping
  identity, secrets, immutable fields and server-managed fields at every depth (repeated, map and `google.protobuf.*`/`google.type.*` fields are
  leaves replaced whole); the IMMUTABLE and IDENTIFIER values the request sets are compared with the stored resource;
- a mask with no paths (`update_mask` sent empty) is a no-op: the plan is `Empty`, nothing is checked or written and no secret changes;
- `"*"` alone is a full replacement: every mutable field; a mutable message holding a server-managed field at any depth expands to its other leaves,
  so the replacement keeps those stored values; `"*"` sets a secret the request supplies and never clears an unset one;
- explicit paths, in any spelling of their field segments, must be update paths (`ResolvePath`: singular messages, map keys and `*` on repeated and
  map fields) or a top-level secret; unknown paths, paths below a secret and the `id`/`etag` identity fields are refused;
- in every mode `OUTPUT_ONLY` paths are dropped; `IMMUTABLE` and `IDENTIFIER` paths are compared with `WithCurrent` (equal dropped, changed refused
  with `ErrProtectedField`, refused without `WithCurrent`); a `REQUIRED` path left empty, or a `REQUIRED` field left empty in a value written whole,
  is refused with `ErrBehaviorViolation`; a `*` sub-path of a list whose request holds elements, but not as many as the stored list, is refused with
  `ErrElementCountMismatch`;
- a path setting a oneof member is refused when another member is `IMMUTABLE` or `IDENTIFIER`, holds such a field, or (top-level) is identity,
  immutable or secret, and, with `WithCurrent`, the stored resource holds it: setting one member clears the others.

Every refusal is a `coreerrs.FieldError` whose `Field` is the update-mask field followed by the offending path's index, `updateMask.paths[2]` (the
AIP-193 field violation of that path; `WithUpdateMaskFieldName` renames the mask field), or the bare mask field when the refusal comes from the
implied or `*` mask. Its `Err` chain holds a `*MaskPathError` with the index, the path as sent, the resource field concerned and the classification
(`fieldmask.ErrInvalidPath`, `fieldmask.ErrWildcardNotAlone`, `ErrProtectedField`, `ErrBehaviorViolation`, `ErrElementCountMismatch`).

`Plan.Paths` feed `MergePaths`; `Plan.Secret` tells a service whether to set, clear or keep a secret. `Patch[E, P, V]` runs the whole
read-modify-write: `Run(ctx, in, tag, req.GetUpdateMask(), opts...)` parses the revision, refuses malformed and unknown paths before the read, checks
the revision against the stored resource, plans against it (a changed IMMUTABLE or IDENTIFIER value is a `coreerrs.FieldError` on
`Field + "." + path`), clears the OUTPUT_ONLY values of the request, merges and saves. An empty mask saves nothing and returns the stored resource
once the precondition passes.

`Precondition[E, V]` owns revision syntax and concurrency policy: `Parse` decodes the request's etag, `Check` compares it with the stored entity (even
for an empty plan), and `Missing` judges it against a resource that does not exist. `Save` receives the parsed revision and must enforce it
atomically. Both presets are built on the [etag](../../etag) package (AIP-154):

* `Int64Precondition[E]{Field: "resource.etag", Version: version}` guards an integer version whose etag is `etag.FromRevision(version)`,
  `W/"<version>"`, with revision `*int64` (`etag.Precondition.ParseRevision`): the etag is optional, so a request without one carries no precondition
  (a nil revision) unless `Required` is set; a malformed etag is a `coreerrs.FieldError` on `Field` (`etag.ReasonInvalid`, or `etag.ReasonRequired`
  for a missing required one); a present etag that does not name the stored version is `etag.ErrMismatch`, a `coreerrs.ErrConflict` (ABORTED);
  `Singleton` permits version zero.
* `TagPrecondition[E]{Rule: etag.NewPrecondition(...), Etag: current}` guards an opaque etag, such as a content tag from `etag.FromMessage` or a tag
  stored with the resource, with revision `etag.Tag` (the zero Tag when no etag is sent). `Rule` sets the field, whether the etag is required and the
  comparison (exact by default); a sent etag that does not match `Etag(entity)` is `etag.ErrMismatch`.

`WithAllowMissing(true)` is AIP-134's `allow_missing`: when `Read` reports `coreerrs.ErrNotFound`, `Run` checks `Precondition.Missing` (both presets
refuse an etag sent for a missing resource as `etag.ErrMismatch`), ignores the mask once its paths are checked, and passes a copy of the request
resource without its OUTPUT_ONLY values to `Patch.Create`. Without `Create`, such a request is `ErrInvalidPatch`.

`WithValidateOnly(true)` is AIP-163's `validate_only` (see [validateonly](../../validateonly)): `Run` performs every step, the precondition, the mask
checks, the field behaviors and the merge, but calls neither `Save` nor `Create`. It returns the merged resource an update would save, the stored
resource for an empty mask, or, with `allow_missing`, the request resource without its OUTPUT_ONLY values that `Create` would receive. A validation
request therefore fails exactly when the live one would fail before its write. Values the store assigns on save (a new etag, an update time, a
generated identifier) are not computed: an update returns the stored ones.

```go
patch := update.Patch[*Widget, *pb.Widget, *int64]{
    Field:        "widget",
    Spec:         spec,
    Precondition: update.Int64Precondition[*Widget]{Field: "widget.etag", Version: func(w *Widget) int64 { return w.Version }},
    Read:         func(ctx context.Context) (*Widget, error) { return repo.Get(ctx, req.GetWidget().GetId()) },
    ToProto:      toProto,
    Save: func(ctx context.Context, merged *pb.Widget, _ update.Plan, expected *int64) (*Widget, error) {
        return repo.Update(ctx, merged, expected) // nil expected: no version check
    },
    Create: func(ctx context.Context, in *pb.Widget) (*Widget, error) { return repo.Create(ctx, in) },
}
out, err := patch.Run(ctx, req.GetWidget(), req.GetWidget().GetEtag(), req.GetUpdateMask(),
    update.WithAllowMissing(req.GetAllowMissing()), update.WithValidateOnly(req.GetValidateOnly()))
```

## Shared policy

`NewSpec` captures descriptor-bound update policy. `Option` configures identity fields (`id` and `etag` by default), immutable and implied-skip
fields, atomic messages, the error field name, and behavior resolution. `DefaultAtomicMessage` recognizes well-known atomic values. The per-request
options (`WithCurrent`, `WithPathValidation`, `WithPathValidationReporter`) configure `ApplyMask`, `Apply`, `ValidateStrict` (`WithCurrent`; path
validation is always enforced) and `Spec.Plan` (`WithCurrent`; the policy is the spec's); `WithEmptyMaskImplied` and `WithStrict` configure
`ApplyMask` only. Policy and per-request values share one `Option` type, so `Apply` takes
the policy options directly.

Share the same policy options across `Apply`, `ValidateStrict`, `FromSetFields`, `ExpandMessagePaths`, `DropIdentifiers`, `ImpliedPaths`,
`ResolvePath`, `Merge` and `MergePaths`. `Patch` carries the spec's policy through the merge automatically. Atomic messages are replaceable leaves;
`WithAtomicMessage(nil)` disables atomic boundaries. `WithResolver` accepts a `fieldbehavior.Resolver` for planning and nested update traversal; nil
uses the `google.api.field_behavior` annotations. Predicates and resolvers shared between goroutines must support concurrent calls.

## Errors

| Error                                                                                                      | Returned by                                                                | Meaning                                                                                                |
|------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------|
| `*fieldmask.ValidationError`                                                                               | `ApplyMask`, `Apply`, `ValidateStrict` (as `Cause`), `Merge`, `MergePaths` | A path the grammar or the schema rejects; matches `fieldmask.ErrInvalidPath`                           |
| `*BehaviorError`                                                                                           | `ApplyMask`, `Apply`                                                       | One `FieldViolation` per AIP-203 violation; matches `ErrBehaviorViolation`                             |
| `*StrictError`                                                                                             | `ValidateStrict`, `ApplyMask` with `WithStrict(true)`                      | Matches `ErrInvalidUpdateMask` and, for behavior violations, `ErrBehaviorViolation`                    |
| `coreerrs.FieldError`                                                                                      | `Spec.Plan`, `Patch.Run`                                                   | Field `updateMask.paths[i]`; its chain holds a `*MaskPathError`                                        |
| `ErrProtectedField`                                                                                        | `Spec.Plan`, `Patch.Run`                                                   | A changed IMMUTABLE or IDENTIFIER value, an identity path, a oneof switch away from a protected member |
| `fieldmask.ErrMaxPathsExceeded`, `fieldmask.ErrMaxPathDepthExceeded`, `fieldmask.ErrMaxPathLengthExceeded` | every entry point a mask enters                                            | A mask over a limit (see [Limits](#limits))                                                            |
| `ErrElementCountMismatch`                                                                                  | `Spec.Plan`, `Merge`, `MergePaths`                                         | A `*` sub-path of a list whose source holds elements, but not as many as the destination               |
| `ErrMessageMismatch`                                                                                       | `ApplyMask`, `Merge`, `MergePaths`, `NewSpec`, `Spec.Plan`, `WithCurrent`  | Nil messages, or messages of different types                                                           |
| `etag.ErrMismatch` (`coreerrs.ErrConflict`)                                                                | `Int64Precondition`, `TagPrecondition`                                     | A present etag that does not name the stored version, or any etag for a missing resource               |
| `coreerrs.FieldError` on the etag field                                                                    | `Int64Precondition.Parse`, `TagPrecondition.Parse`                         | A malformed etag (`etag.ReasonInvalid`) or a missing required one (`etag.ReasonRequired`)              |
| `ErrInvalidPatch`                                                                                          | `Patch.Run`                                                                | A missing dependency, or allow_missing without `Create`                                                |

## Options

| Option                       | Type          | Default                                 | Behavior                                                                                                               |
|------------------------------|---------------|-----------------------------------------|------------------------------------------------------------------------------------------------------------------------|
| `WithAtomicMessage`          | `Option`      | DefaultAtomicMessage                    | Choose message descriptors that must be updated as whole values.                                                       |
| `WithIdentityFields`         | `Option`      | `"id"`, `"etag"`                        | Replace conventional id/etag paths used by update planning; an empty set disables name-based identities.               |
| `WithImmutableFields`        | `Option`      | nil / unset                             | Add application paths protected as immutable during planning.                                                          |
| `WithImpliedSkipFields`      | `Option`      | nil / unset                             | Exclude configured paths when synthesizing an implied update mask.                                                     |
| `WithResolver`               | `Option`      | fieldbehavior.AnnotationResolver{}      | Replace annotation lookup for update planning, including nested fields. Nil uses annotations.                          |
| `WithUpdateMaskFieldName`    | `Option`      | DefaultUpdateMaskField (`"updateMask"`) | Set the field name used to attribute update-mask errors.                                                               |
| `WithStrictDynamicPaths`     | `Option`      | false                                   | Keep paths into `Struct`, `Value` and `ListValue` to AIP-161; deeper paths are rejected in every `PathValidationMode`. |
| `WithMaxPathDepth`           | `Option`      | fieldmask.DefaultMaxPathDepth (32)      | Bound the segments of a path, `*` and quoted keys included; 0 is unlimited.                                            |
| `WithMaxPaths`               | `Option`      | fieldmask.DefaultMaxPaths (1000)        | Bound the paths of a mask; 0 is unlimited.                                                                             |
| `WithMaxPathLength`          | `Option`      | fieldmask.DefaultMaxPathLength (1024)   | Bound the bytes of a raw path; 0 is unlimited.                                                                         |
| `WithCurrent`                | `Option`      | nil (unknown)                           | The stored resource IMMUTABLE and IDENTIFIER values are compared with (AIP-203).                                       |
| `WithPathValidation`         | `Option`      | PathValidationEnforce                   | Select PathValidationEnforce, PathValidationWarn, or PathValidationDisabled for `Apply`.                               |
| `WithPathValidationReporter` | `Option`      | nil / unset                             | Observe path validation failures in reporting mode.                                                                    |
| `WithEmptyMaskImplied`       | `Option`      | false                                   | `ApplyMask` only: apply the implied mask to an `update_mask` sent with no paths, as to an omitted one.                 |
| `WithStrict`                 | `Option`      | false                                   | `ApplyMask` only: check as `ValidateStrict` does (every path enforced, refusals as `*StrictError`).                    |
| `WithAllowMissing`           | `PatchOption` | false                                   | AIP-134 allow_missing for one `Patch.Run`: create a missing resource with `Patch.Create`.                              |
| `WithValidateOnly`           | `PatchOption` | false                                   | AIP-163 validate_only for one `Patch.Run`: run every check, skip `Save` and `Create`.                                  |

## Limits

The mask limits of [fieldmask](../README.md#limits) apply to every entry point a mask enters, before any parsing: `WithMaxPaths`, `WithMaxPathLength`
and `WithMaxPathDepth` (defaults `fieldmask.DefaultMaxPaths`, `DefaultMaxPathLength`, `DefaultMaxPathDepth`; zero lifts a limit, a negative value
keeps the default) are options of `Apply` and `ValidateStrict` (in every `PathValidationMode`), `Merge`, `MergePaths` and `ResolvePath`, and of
`Spec.Plan` and `Patch` through `NewSpec`. A mask over a limit is a `*fieldmask.ValidationError`, or from `Plan` a field error on
`updateMask.paths[i]` (the bare `updateMask` for the path count) wrapping a `*MaskPathError`. The implied masks of `Plan`, `ImpliedPaths` and
`FromSetFields` stop at the policy's `WithMaxPathDepth`; the deepest path then stands for the subtree below it.

## API

| Symbol                                                                                                                                  | Purpose                                                                                                                                                                |
|-----------------------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `ApplyMask`                                                                                                                             | Apply a raw update mask as sent, every path checked on its own, and return the effective mask to write back.                                                           |
| `Apply`                                                                                                                                 | Apply an update mask to the request resource under the AIP-203 field behaviors.                                                                                        |
| `ValidateStrict`                                                                                                                        | Validate an update mask under the rules of `Apply` without mutating the resource and return the cleaned mask.                                                          |
| `FieldViolation`                                                                                                                        | One field-behavior violation of `Apply`: path, behavior and reason (`Format` is always empty here).                                                                    |
| `BehaviorError`                                                                                                                         | The AIP-203 violations of `Apply`; maps to INVALID_ARGUMENT.                                                                                                           |
| `StrictError`                                                                                                                           | The invalid paths of a strict update mask.                                                                                                                             |
| `Spec`                                                                                                                                  | The update-mask classification of a resource message's top-level fields; immutable and safe for concurrent use.                                                        |
| `Plan`                                                                                                                                  | The resolved update mask of one request: `Paths`, `Empty`, `Secret`.                                                                                                   |
| `MaskPathError`                                                                                                                         | Locates a `Spec.Plan` or `Patch.Run` refusal in the mask: `Index`, `Path`, `Field`, `Err`.                                                                             |
| `ImpliedPaths`                                                                                                                          | The implied update mask of an omitted `update_mask`: the populated leaf fields, skipping identity, secret, immutable and server-managed ones.                          |
| `ResolvePath`                                                                                                                           | An update path spelled by declared names, or `""`: list indexes, element fields without `*` and paths into atomic values are not update paths.                         |
| `Merge`, `MergePaths`                                                                                                                   | Copy the fields a mask or update paths name from `src` to `dst`, all-or-nothing.                                                                                       |
| `DefaultAtomicMessage`                                                                                                                  | Treat `google.protobuf.*` and `google.type.*` values as leaves, except the dynamic `Struct`, `Value` and `ListValue`.                                                  |
| `Patch`                                                                                                                                 | A masked read-modify-write: precondition, plan, merge, save, allow_missing create and validate_only.                                                                   |
| `Precondition`                                                                                                                          | Revision syntax and concurrency policy: `Parse`, `Check`, `Missing`.                                                                                                   |
| `Int64Precondition`                                                                                                                     | The AIP-154 integer-version preset with an optional etag, built on `etag.Precondition.ParseRevision`.                                                                  |
| `TagPrecondition`                                                                                                                       | The AIP-154 opaque-etag preset: an `etag.Precondition` rule and the current `etag.Tag` of an entity.                                                                   |
| `PathValidationMode`                                                                                                                    | How `Apply` treats invalid paths: `PathValidationEnforce`, `PathValidationWarn`, `PathValidationDisabled` (see [Path validation in Apply](#path-validation-in-apply)). |
| `Option`, `PatchOption`                                                                                                                 | The policy and per-request options of the package; the per-call options of `Patch.Run`.                                                                                |
| `DefaultUpdateMaskField`                                                                                                                | `"updateMask"`, the field update-mask errors name unless `WithUpdateMaskFieldName` renames it.                                                                         |
| `ErrInvalidUpdateMask`, `ErrBehaviorViolation`, `ErrProtectedField`, `ErrElementCountMismatch`, `ErrMessageMismatch`, `ErrInvalidPatch` | Sentinels; see [Errors](#errors).                                                                                                                                      |

## Related packages

- [fieldmask](../README.md)
- [fieldmask/extract](../extract/README.md)
- [fieldbehavior](../../fieldbehavior/README.md)
- [etag](../../etag/README.md)
- [validateonly](../../validateonly/README.md)
- [fieldmask guide](../../docs/fieldmask.md)
