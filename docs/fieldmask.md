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

| Rule                                                                                | Implemented by                                                                                                                                                                                               | Behavior                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
|-------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| AIP-161: one path grammar, field names in declared or JSON spelling                 | every entry point (`ParsePaths`, `Resolve`, `Validate`, `Filter`, `ApplyMask`, `Apply`, `Plan`, `Merge`, ...)                                                                                                | A segment may use the declared, JSON or protojson snake_case spelling of its field; results are spelled by declared names.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| AIP-161: no index into a repeated field                                             | every entry point, read masks included                                                                                                                                                                       | `aliases.0` and `aliases.0.id` are a `*ValidationError` (a field error on `updateMask.paths[i]` from `Plan`), in every `PathValidationMode`. `FromMessage` and `FromSetFields` never emit them.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| AIP-161 extension: `*` addresses every element of a repeated field or every value of a map    | every entry point                                                                                                                                                                                            | A field of the elements must go through `*` (`aliases.*.display_name`); the implicit form `aliases.display_name` is rejected.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| AIP-161: map keys                                                                   | every entry point                                                                                                                                                                                            | A key must be a valid value of the map's key type in its canonical form (`ports.443`, not `ports.0443`); a protobuf map cannot address the literal key `*`; dynamic JSON can name it with `` `*` ``.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| AIP-161: a `google.protobuf.Struct` is a `map<string, Value>`                       | every entry point (`Resolve`, `Validate`, `Filter`, `Prune`, `ApplyMask`, `Apply`, `ValidateStrict`, `Plan`, `update.ResolvePath`, `MergePaths`, `Merge`); `FromMessage` and `FromSetFields`                 | `attrs` and `attrs.*` select the whole Struct; a key (`attrs.color`, ``attrs.`a.b` ``) one entry, replaced from the request or deleted when the request lacks it. `FromMessage` and `FromSetFields` emit top-level keys only.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| Extension beyond AIP-161: paths into dynamic JSON (default)                         | the same entry points                                                                                                                                                                                        | Below a Struct key, in a `Value` field and through `*` of a `ListValue` a key addresses a nested Struct entry and `*` every entry or element (`attrs.work.from`, `attrs.items.*.name`, `value_data.key`, `list_data.*.name`); no index, no `ListValue` key without `*`. Read: exactly the addressed key is kept or removed; a value of another shape (a scalar or null, a list without `*`, an element of another shape under `*`) addresses nothing, so `Filter`/`Select` drop it unless another path keeps it (the other list elements stay in order) and `Prune` leaves it as it is. Update: replaced or deleted, intermediate Structs created only for a value to write, list elements in place by index (`ErrElementCountMismatch`). AIP-203 checks are unaffected: dynamic JSON has no field behaviors. |
| AIP-161 only: `WithStrictDynamicPaths(true)`                                        | `fieldmask.WithStrictDynamicPaths` for the read entry points; `update.WithStrictDynamicPaths` for `ApplyMask`, `Apply`, `ValidateStrict`, `Merge`, `MergePaths`, `update.ResolvePath` and `Plan` (`NewSpec`) | A path below a Struct key (`attrs.color.hue`, `attrs.*.hue`) or into a `Value` or `ListValue` field is a `*ValidationError` (a field error on `updateMask.paths[i]` from `Plan`), in every `PathValidationMode`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| AIP-161: backtick-quoted segments                                                   | every entry point; `ToPaths`, `FromMessage`, `FromSetFields`, violation paths                                                                                                                                | A segment that is empty or holds a dot or a backtick is quoted, a literal backtick doubled: the key ``x`y.z`` is written ``labels.`x``y.z` ``.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| AIP-161: an invalid update path is INVALID_ARGUMENT                                 | `ApplyMask` and `Apply` (`PathValidationEnforce`, the default), `ValidateStrict`, `Plan`, `Patch`                                                                                                            | Rejected before any mutation; `PathValidationWarn` and `PathValidationDisabled` are explicit opt-outs of `ApplyMask` and `Apply`. Every raw path is checked on its own, a path another path of the mask covers included (see [Path grammar](#path-grammar)).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| AIP-134: an omitted `update_mask` updates every populated field | `Plan`, `Patch.Run`, `ApplyMask`, `Apply`, `ValidateStrict`, `update.FromSetFields`, `ImpliedPaths` | Omitted masks select populated leaves; empty mutable messages contribute no path. Lists, maps and atomic messages are replaced whole. Protected input is retained for validation, then unchanged protected paths are removed. `ImpliedPaths` returns writable leaves only; `FromSetFields` omits routing identifiers. |
| AIP-134: an empty `update_mask` updates nothing | `Plan`, `Patch.Run`, `ApplyMask`, `Apply`, `ValidateStrict` | A provided empty mask is a no-op. |
| AIP-134: `*` is a full replacement                                                  | `Plan`, `Patch.Run`, `ApplyMask`, `Apply`, `ValidateStrict`, `Merge`                                                                                                                                         | Every mutable field is replaced; stored server-managed values survive. `*` with any other path is `ErrWildcardNotAlone`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| AIP-134: `allow_missing` | `Patch.Run` with `WithAllowMissing(true)` and `Patch.Create` | A missing resource is created from the request; the mask, including invalid paths, is ignored. |
| AIP-154: the etag is optional                                                       | `Int64Precondition`, `TagPrecondition` (via [`etag.Precondition`](etag.md))                                                                                                                                  | No etag is no precondition (a nil revision, the zero `coreetag.Tag`) unless the etag is required.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| AIP-154: a stale etag is ABORTED                                                    | `Int64Precondition.Check`/`Missing`, `TagPrecondition.Check`/`Missing`                                                                                                                                       | `etag.ErrMismatch`, a `coreerrs.ErrConflict`, for an etag that does not name the stored version or tag, or any etag sent for a missing resource. `Save` enforces the revision atomically.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| AIP-163: `validate_only` runs every check and skips the write                       | `Patch.Run` with `WithValidateOnly(true)`                                                                                                                                                                    | Precondition, mask checks, field behaviors and merge run; `Save` and `Create` are not called. The merged resource (or, with `allow_missing`, the would-be created one) is returned.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| AIP-203: OUTPUT_ONLY ignored, IMMUTABLE and IDENTIFIER compared, REQUIRED non-empty | `ApplyMask`, `Apply`, `ValidateStrict`, `Plan`, `Patch`                                                                                                                                                      | See [Applying an update mask](#applying-an-update-mask) and the [AIP-203 conformance table](fieldbehavior.md#aip-203-conformance).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| AIP-193: field violations name the offending request field | `ApplyMask`, `Apply`, `Prepare`, `Complete`, `ValidateStrict`, `Spec.Plan`, `Patch.Run` | Aggregate normalization errors name every violated resource field. `Spec.Plan` returns one base-selected refusal on `updateMask.paths[i]`; `Patch.Run` wraps a selected protected change as `resource.<field>`. Implied or wildcard refusals and path-count limits use the bare mask field. `MaskPathError` retains provenance. |
| Merge of nested and collection paths                                                | `update.Merge`, `MergePaths`                                                                                                                                                                                 | Top-level and nested fields, map keys and `*` sub-paths of repeated and map fields, all-or-nothing.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |

## Applying an update mask

With `WithCurrent(stored)`, an unchanged `IMMUTABLE` or `IDENTIFIER` value is ignored; a different value is an
`INVALID_ARGUMENT` violation naming the field. Explicit clears, clears through a covering message path, and clears under `*` are changes too.
An omitted mask selects only populated fields, so omitting a field there does not request a clear.

Without current state, these values stay in the request for the caller to compare, and their paths are excluded from the effective write mask.
This applies to direct paths, covering paths, the implied mask and `*`, at every depth. `OUTPUT_ONLY` input is still cleared.
Use `Prepare` to retain a `DeferredPlan`, then complete it with stored state directly or through `Spec.Plan` / `Patch.Run`.
The plan owns the original values and clear intent. Never persist the filtered request as a replacement.

Singular messages containing protected fields expand into their other fields. Without current state, a whole collection or atomic message with
protected descendants is deferred as a whole: its covering path is omitted and its value retained. The mask format cannot express a replacement
that both changes collection membership (or crosses an atomic boundary) and excludes protected descendants. `DeferredPlan.Complete` finishes
that replacement using its captured input and current state. With current state, these descendants are compared before replacement, including removed elements; list elements
pair by index and map elements by key. New protected values without stored counterparts are changes as well.

`ApplyMask` returns the effective mask. `Apply` writes it back to a non-nil `FieldMask`, and `ValidateStrict` returns it without changing the request.
Routing identifiers and unverified protected values are preserved using a separate private filtering mask; they never become effective write paths.
`DropIdentifiers` is no longer needed after `Apply`. `Apply(nil, ...)` cannot assign its synthesized mask back to the caller; use `ApplyMask` instead.

A nil mask implies populated fields; a provided empty mask is a no-op that leaves the request untouched; `*` alone means full replacement.
A completed empty mask writes no resource fields, but normalization can still clear
OUTPUT_ONLY request input. An empty interim mask does not authorize skipping deferred completion. REQUIRED validation, path limits and strict
handling of invalid masks still apply.

## Applying a raw update mask

```go
effective, err := update.ApplyMask(req.GetUpdateMask(), req.GetWidget(), update.WithCurrent(stored))
if err != nil {
    return err
}
req.UpdateMask = effective.ToProtoFieldMask()
```

Use the raw mask so covered paths are validated before collapsing them. `ValidateStrict` checks without changing the message, and
`Spec.Plan` / `Patch` perform a read-modify-write using stored state. `Prepare` retains deferred operations for them as described below.

Stateful `StripUpdate` uses the same protected merge-effect check and retains validated protected input. A subsequent `Spec.Plan` or `Patch.Run`
can therefore compare that input even inside a mutable atomic message. Stripping has no explicit mask; use `Prepare` for masked clear intent.

## Two-stage update

`Prepare(raw, resource, opts...)` performs stateless normalization and returns a `DeferredPlan`. Its `Mask` is the initial effective mask;
`Operations` describes deferred set, clear and whole-replacement intent. `Complete(stored)` compares the captured values, restores any deferred
replacement paths, and updates the bound request to the same result as a stateful `ApplyMask`. It leaves stored state untouched and is idempotent.
A validation or behavior refusal restores the captured original request; message mismatch and conversion failures leave the current request untouched.
An explicit protected clear remains a comparison even when the initial mask is empty. The plan retains one resource snapshot and the raw mask,
subject to the existing path limits, even when `Operations()` is empty. Collection validation (including stored elements of an absent request list), expansion and retained routing-message
sanitization can need stored state without a protected comparison. An empty `Operations()` result does not authorize skipping completion.
`Patch` uses the captured resource for `allow_missing` creation, ignoring the update mask, including invalid paths.
Both the boundary's and the Spec's OUTPUT_ONLY clearing apply before `Create` or the `validate_only` return.

With a bound plan, `Spec.Plan` and `Patch.Run` behave as on the client's original request and mask after the boundary's OUTPUT_ONLY
sanitization: values are cleared and wholly OUTPUT_ONLY paths dropped, keeping keyed exclusions and empty-mask no-ops.
The Spec alone determines the paths, secrets, atomic-message handling and planned request, exactly as in a one-stage call on that input.
In addition, the update is refused if the boundary's policy refuses that sanitized input against the stored resource given now, or if the
complete write changes a value the boundary protects. Boundary refusals are recomputed on every call, even if `Prepare` received `WithCurrent`.
The final guard uses the boundary's own equality, including its ignored OUTPUT_ONLY descendants, and covers both direct merging and Patch's
sanitized merge source, plus `Plan.Secret` operations (including secrets outside the mask and their oneof side effects).
It never narrows the Spec's selection or rewrites its planned source. `Complete` itself keeps `ApplyMask`'s filtering contract.
Mask refusals retain the client's original index and spelling and the Spec's configured mask field name, even after ignored paths are
dropped or a wildcard is pinned to concrete keys. Resource-field errors and error classifications are unchanged. The Spec checks the
original raw-mask limits before sanitization. Wildcard pinning can increase the sanitized path count; existing expansion limits still
apply, and a sanitization expansion refusal identifies the originating client path.
Stateless normalization clears OUTPUT_ONLY input even inside retained protected messages and collections.
Only validated replacements restore protected values from stored state. An entirely OUTPUT_ONLY selection is ignored before merge preconditions are checked, including below a protected ancestor.
Protected values are compared against the complete merge effect of the remaining selection, including protected ancestor presence,
implicit oneof sibling clears, and requested clears. Even an unchanged selected leaf can materialize a protected ancestor or clear a protected oneof sibling.
Payload bytes are bounded by the original resource size. Release the plan with its request context.
For transactional entry points (`ApplyMask`, `Apply`, `MergePaths`, `Spec.Plan` and `Patch.Run`), every error leaves the destination and request as they were immediately before the call, including deferred refusals.
Standalone `Complete` instead restores the original request captured by `Prepare` on a behavior or validation refusal; a message-mismatch or conversion failure leaves the request as it was immediately before `Complete`.
This also covers `KeepIdentity`: cleanup and protected-value restoration complete privately before publication.
Successful mutations copy content into the caller's existing message, preserving its identity and any bound plan.
Conversion failures retain `ErrMessageMismatch` and their underlying protobuf cause. Dynamic JSON merges use reflection on each
message's own descriptor, so an unselected deep subtree does not require a wire conversion. `Patch.Run` uses a private stored copy
and publishes request restoration only after `Save` succeeds; callbacks consume the merged argument and captured `Plan.Secret` values.
Callbacks remain responsible for the atomicity of their external writes.

Attach it with `ContextWithPlan(ctx, pending)`. `PlanFromContext(ctx, resource)` only matches that exact message instance, so distinct stream
messages cannot pick up each other's plans. Use a new resource instance and context for each received message. `Patch.Run` completes the plan
from its context automatically. Direct `Spec.Plan` callers pass `WithPlanContext(ctx)` or `WithDeferredPlan(pending)`, plus `WithCurrent(stored)`.
Different policies are supported without combining their resolvers or changing their ignored-descendant equality rules.
Policy-only routing identities retain their existing rules: Spec refuses explicit identity paths, and implied or wildcard updates do not
compare or write them. Successful deferred completion restores captured input even if the normalized request was subsequently changed.

An empty effective mask is not proof that validation has finished. Interceptors must retain and attach the `Prepare` plan and defer destructive
`KeepIdentity` cleanup until completion and request consumption. `ApplyMask` followed by `KeepIdentity` alone loses comparison evidence.
Handlers consuming effective paths must first complete the retained plan, or use `Spec.Plan` / `Patch.Run` with it. Successful stateful planning
restores stored protected values inside planned whole replacements to the request, so a preceding `StripUpdate` cannot lose their ignored
OUTPUT_ONLY descendants during `MergePaths`. Preserve mask presence:
a provided empty mask remains a no-op; never convert it to nil (which implies populated fields). `DropIdentifiers` is unnecessary after `Apply`
or `ApplyMask`; their effective masks already exclude identifiers.

The following is the runnable `ExamplePrepare` (imports: `context`, `fmt`, protobuf `proto`, `descriptorpb`, `fieldmaskpb`, and this `update` package):

```go
func ExamplePrepare() {
	ctx := context.Background()
	resource := &descriptorpb.FileDescriptorProto{Name: new("example.proto"), Dependency: []string{"v2.proto"}}
	raw := &fieldmaskpb.FieldMask{Paths: []string{"name", "dependency"}}
	policy := update.WithImmutableFields("name")

	// Interceptor: normalize in place and attach the plan for this resource.
	pending, err := update.Prepare(raw, resource, policy)
	if err != nil {
		panic(err)
	}

	ctx = update.ContextWithPlan(ctx, pending)
	raw = pending.Mask().ToProtoFieldMask()

	// Handler: load stored state. Patch.Run uses its context automatically;
	// direct Spec.Plan callers pass WithPlanContext.
	stored := &descriptorpb.FileDescriptorProto{Name: new("example.proto"), Dependency: []string{"v1.proto"}}

	spec, err := update.NewSpec(resource, policy)
	if err != nil {
		panic(err)
	}

	plan, err := spec.Plan(resource, raw, update.WithCurrent(stored), update.WithPlanContext(ctx))
	if err != nil {
		panic(err)
	}

	merged := proto.Clone(stored).(*descriptorpb.FileDescriptorProto)
	if err := update.MergePaths(merged, resource, plan.Paths, policy); err != nil {
		panic(err)
	}

	fmt.Println(merged.GetName(), merged.GetDependency())
	// Output: example.proto [v2.proto]
}
```

Stateless errors still stop normalization: an early REQUIRED failure can precede additional stored-dependent violations or final expansion errors,
and the existing conservative oneof guard may reject a switch that stored state would permit. Successful normalization has exact completion
equivalence. Never store the normalized request itself; merge the final paths into a copy of stored state.

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
    return err // the first refusal, located on the mask field
}
if plan.Empty() {
    return nil
}

merged := proto.Clone(stored)
if err := update.MergePaths(merged, req.GetWidget(), plan.Paths); err != nil {
    return err
}
```

`Spec.Plan` locates its first refusal through `*update.MaskPathError` inside a `coreerrs.FieldError`:
`updateMask.paths[i]` for an explicit path, or the bare mask field for implied masks, `*`, and path-count limits.
`MaskPathError` retains the original index, mask spelling, resource field and sentinel classification.
`Patch.Run` wraps changed protected values with a resource-field `FieldError`, retaining the Spec error as its cause.

`ApplyMask`, `Apply`, `Prepare` and completion return `*BehaviorError` for resource violations; strict ApplyMask and
`ValidateStrict` return `*StrictError`. Those aggregates report all concrete collection indexes and keys.
Spec and Patch report one field using the same construction with or without a deferred plan. The mapper retains its base
aggregate selection: batch and field-path wrappers do not automatically prefix or transform aggregate violation paths.

`Patch[E, P, V]` runs the masked read-modify-write with an explicit `Precondition[E, V]`. `Run(ctx, in, tag, req.GetUpdateMask(), opts...)` parses
the revision, refuses malformed and unknown paths before the read (unless `allow_missing` may create), checks the revision against the stored resource (even for an empty plan), plans
against it (a changed IMMUTABLE or IDENTIFIER value is a `coreerrs.FieldError` on the resource field), clears the request's OUTPUT_ONLY values,
merges the planned paths and saves. The AIP-154 presets are built on [etag](etag.md): `Int64Precondition` guards an integer version whose etag
is `etag.FromRevision(version)`, with revision `*int64`, nil when the request sends no etag (no version check, unless `Required`);
`TagPrecondition` guards an opaque etag (a content hash, a stored tag) with an `etag.Precondition` rule and revision `coreetag.Tag`. A present etag
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
For a dynamic JSON resource root, an omitted mask selects populated object keys or replaces a populated scalar/list whole with `*`;
empty objects/lists and an unset `Value` imply no writes. Explicit `*` replaces the entire root, removing stored-only keys and allowing
list length changes. Generated `structpb` and `dynamicpb` roots and nested values behave alike. These masks can be passed directly to `MergePaths`.

A `FieldMask` is a set of paths none of which covers another, so a path below another path of the same mask (`profile.id` with `profile`)
collapses into it: `["description", "description.no_such"]`, `["aliases", "aliases.0.id"]` or `["profile", "profile.id"]` become `description`,
`aliases` or `profile`. The update entry points check every raw path on its own before the mask collapses:

* `ParsePaths` and `FromProtoFieldMask`, which know no schema, check the grammar of every raw path and collapse a covered one, as `FromPaths` does;
  a redundant read mask selects the same fields either way. The collapsed path reaches no schema or field behavior check, so an update caller
  that wants every raw path checked passes the raw mask to `ApplyMask` or `Spec.Plan`. `Apply` on such a mask still enforces the covering path
  over its whole subtree: `profile` replaced keeps the stored IDENTIFIER and IMMUTABLE values and refuses changed ones.
* `Spec.Plan` and `Patch.Run` resolve every raw path and check its field behaviors on its own, a covered path as if sent alone, and plan the
  collapsed mask: `["profile", "profile.id"]` accepts and drops the protected path without `WithCurrent`; with current state a changed
  identifier is refused on its path.
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
the usual structured error: a `*ValidationError`, or a `*StrictError` in strict update validation.
`Spec.Plan` and `Patch.Run` locate mask defects through `*MaskPathError`; a path-count limit uses the bare mask field.
`FromMessage` and `FromSetFields` stop a recursive message at the default depth, the implied masks at the
policy's depth.

With stored state, the limits bound the final effective mask: the update entry points (`ApplyMask`, `Apply`, `ValidateStrict`, `Plan`) check the paths an update writes,
after `*`, whole messages holding server-managed fields and pinned wildcards are expanded, and refuse an expansion over a limit before the request is
touched, since `MergePaths` would refuse those paths. A resource whose expansion exceeds the default 1,000 paths needs a higher `WithMaxPaths`.

Without stored state, covering expansion and wildcard exclusions remain unresolved. A wildcard beside a deferred key stays with that key
in the plan instead of being pinned to temporary request keys. If an interim expansion exceeds a limit, its writes are deferred. `Complete`
replays the original mask and request with stored state and checks the final path count, length and depth. Raw-mask limits and traversal
budgets still apply immediately. A stateless effective mask alone does not describe this pending work.

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

Use the same policy with `update.FromSetFields`, `ExpandMessagePaths`, `DropIdentifiers`, `ImpliedPaths`, `update.ResolvePath` and `MergePaths`.
`ApplyMask`, `Apply` and `ValidateStrict` take the same options next to the per-request ones (`WithCurrent`, `WithPathValidation`;
`WithStrict` for `ApplyMask` and `Prepare`). This keeps implied masks, expansion, protected fields, identity preservation, and atomic
boundaries consistent. A nil resolver selects annotation lookup. A shared resolver must be concurrency-safe and may also be passed to
`fieldbehavior.WithResolver` so payload stripping and path planning use the same metadata.

## Errors

Update violations use the frozen `a8b17ba` checking walk for order, only after the current evaluation finds a violation.
Diagnostics found by both walks form the prefix in the base's order, including its REQUIRED early returns and first-difference
atomic comparisons. The evaluation's remaining diagnostics follow in field declaration order, numeric list-index order and
unescaped map-key order, then behavior and reason. Base wildcard diagnostics match concrete element paths in that order.
The reference never decides acceptance. Dynamic roots, literal JSON star keys, selections the base refuses (expansion limits or wildcard
pinning), and non-conversion reference errors or recovered panics use the deterministic order alone. Conversion errors are returned to the caller. The evaluator's refusal is still returned.
`ApplyMask`, `Apply`, `ValidateStrict` and deferred completion report this ordered list; `Spec.Plan` and `Patch.Run` select its first
violation. Deferred completion uses the original selection and values retained by its plan.

`*fieldmask.ValidationError` matches `fieldmask.ErrInvalidPath`; its `Cause` may refine it (`ErrWildcardNotAlone`, a limit error matching
`ErrLimitExceeded`). `*update.StrictError` retains its cause and matches `ErrInvalidUpdateMask`. Apply behavior failures match `ErrBehaviorViolation`. `Spec.Plan` reports its first violation through a `FieldError` on
`updateMask.paths[i]` (the bare mask field for implied masks, `*`, and mask-count limits). `Patch.Run` adds a resource-field
`FieldError` for protected changes and retains the Spec error beneath it. Deferred plans use these same entry-point error shapes. `MaskPathError` provenance and sentinel identities remain available through `errors.As` and `errors.Is`. `Int64Precondition` and `TagPrecondition` report a stale or misplaced etag as `etag.ErrMismatch` (a
`coreerrs.ErrConflict`). `WithPathValidation` and `WithPathValidationReporter` control the path-validation mode and reporting of `update.ApplyMask` and
`update.Apply`.

See the option references of [fieldmask](../fieldmask/README.md#options) and [update](../fieldmask/update/README.md#options), [field
behavior](fieldbehavior.md), and [projection](../projection/) for payload stripping and storage read selections.

An omitted update mask for a JSON object selects each populated key literally, including `*`, dots, backticks, empty keys,
and keys named `fields` or `values`. The returned paths quote these keys as needed; `` `*` `` selects only that key, while
`*` replaces the complete root. Direct planning and `Prepare` followed by `Complete` preserve unselected stored keys.

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
