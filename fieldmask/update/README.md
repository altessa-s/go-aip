# update

```go
import "github.com/altessa-s/go-aip/fieldmask/update"
```

Package `update` applies a [fieldmask](../README.md) `FieldMask` to an update request: AIP-134 update masks, the AIP-203 field behaviors, AIP-154 etag
preconditions and AIP-163 validate_only. It owns the dependencies on [etag](../../etag) and [fieldbehavior](../../fieldbehavior); paths are read by
the [path grammar](../README.md#path-grammar-aip-161) of `fieldmask`. The [guide](../../docs/fieldmask.md) maps every rule to the function that
implements it.

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

A nil mask implies populated fields; a provided empty mask is a no-op; `*` alone means full replacement.
For an explicit empty mask, stateless `Apply`, `ValidateStrict` and `ApplyMask` leave the request untouched.
`Prepare` captures the original input first, then sanitizes the handler-visible request even when no write paths remain.
A completed empty mask writes no resource fields, but normalization can still clear
OUTPUT_ONLY request input. An empty interim mask does not authorize skipping deferred completion. REQUIRED validation, path limits and strict
handling of invalid masks still apply.

## Operations

| Function               | Description                                                                                                                                   |
|------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------|
| `ApplyMask`            | Apply a raw `update_mask` as sent, checking every path on its own; return the effective mask to write back (see [ApplyMask](#applymask))      |
| `Apply`                | Apply the AIP-203 rules, then clear fields outside the mask in place (mutates the mask)                                                       |
| `ValidateStrict`       | Validate an update mask under the rules of `Apply` without mutating the resource; return the cleaned mask                                     |
| `DropIdentifiers`      | Remove identifiers from a caller-built mask; no longer needed after `Apply`                                                           |
| `KeepIdentity`         | Explicitly reduce a message to routing fields; do not use before deferred comparison                               |
| `ExpandMessagePaths`   | Expand message-typed leaves into their sub-fields for a full replacement update                                                               |
| `FromSetFields`        | The update mask an omitted `update_mask` implies: `fieldmask.FromSetFields` minus identifiers and server-managed sub-fields                   |
| `ImpliedPaths`         | The implied leaf paths of an omitted `update_mask`, skipping identity, secret, immutable and server-managed fields                            |
| `ResolvePath`          | An update path spelled by declared names, or `""`                                                                                             |
| `Merge`, `MergePaths`  | Copy the selected fields from `src` to `dst` (see [Read-modify-write updates](#read-modify-write-updates))                                    |
| `NewSpec`, `Spec.Plan` | Classify a resource once and resolve one request's mask into a `Plan` (see [Update plans](#update-plans-and-patches-aip-134-aip-154-aip-163)) |
| `Patch.Run`            | The whole read-modify-write with a revision precondition, allow_missing and validate_only                                                     |

## Behavior

A field carrying several behaviors is judged by presence with a fixed precedence (`OUTPUT_ONLY` dropped, then `IDENTIFIER`, then `IMMUTABLE` compared,
then `REQUIRED`), so `[REQUIRED, IMMUTABLE]` is compared whatever the declaration order, and a violation names the governing behavior. A
regular singular replacement keeps OUTPUT_ONLY descendants by expansion. Whole collection replacements still clear unprotected OUTPUT_ONLY descendants
when the merge replaces their elements. Nested `OUTPUT_ONLY` entries are removed from the mask
before set identifiers are preserved (an `IDENTIFIER` that is also `OUTPUT_ONLY` is never preserved), so a branch that named only such fields
disappears. On a violation neither the mask nor the message is changed.

`Apply`, `ValidateStrict`, `ApplyMask` and `Spec.Plan` refuse a path that sets a oneof member whose other member is, or holds, an `IMMUTABLE` or
`IDENTIFIER` field (or a top-level identity or immutable field of the policy) when the stored resource holds that member, and without `WithCurrent`
whatever it holds: setting one member clears the others, so the write would delete the protected value unnamed (`items.k.free` beside a stored
`items.k.pinned`). Every mask is checked, implied and `*` included; a whole collection replacement also compares its protected descendants. Only a switch the merge
makes is refused: a member the request leaves unset is cleared, which switches nothing, and `items.*.free` reaches the request entries no key names,
so a stored `k.pinned` the request does not send is left alone.

`Apply` and `ValidateStrict` keep the presence of the `update_mask` (AIP-134), as `fieldmask.FromProtoFieldMask` builds it, the way `ApplyMask`,
`Spec.Plan` and `Patch.Run` do with the `*fieldmaskpb.FieldMask`:

- a nil mask (`update_mask` omitted) selects populated leaves; an empty mutable message contributes no path, while populated protected values remain available for validation;
- a non-nil empty mask (`update_mask` sent empty) is a no-op: `Apply` leaves the message and the mask untouched, `ValidateStrict` returns an empty
  mask, and no field behavior is checked; only a stored resource of another type is still `ErrMessageMismatch`;
- `*` is a full replacement.

A successfully completed mask whose every path is dropped (OUTPUT_ONLY, unchanged IMMUTABLE or IDENTIFIER) has no writes, as a completed
`Plan.Empty` does. Normalization still clears OUTPUT_ONLY input, even when no write paths remain; the request need not remain as sent.
An interim effective mask from `Prepare` can be empty while a collection replacement or protected comparison remains deferred. Always complete
that plan against stored state, directly or through `Spec.Plan` / `Patch.Run`, before deciding there are no writes. A provided empty mask is the
separate no-op case described above; `Prepare` still sanitizes its input. Never store the normalized request as the resource.

The mask policy options govern the top-level fields of `ApplyMask`, `Apply` and `ValidateStrict` as they govern `Spec.Plan`: a field named by
`WithImmutableFields` is IMMUTABLE, and a field named by `WithIdentityFields` (`id` and `etag` by default) is IDENTIFIER when the mask names it.
Unnamed, such an identity field addresses the resource and is kept, but not compared with the stored value: a stale etag is the service's concurrency
check (AIP-154).

## ApplyMask

Use `ApplyMask` on the original protobuf mask. It checks raw limits, grammar and schema before resolving covering paths, and returns the effective
mask. Thus a covered valid protected path is accepted and compared or deferred, while a covered unknown field or list index remains invalid.
`WithStrict(true)` enforces every path regardless of `WithPathValidation` and returns `StrictError` on refusal.

```go
effective, err := update.ApplyMask(req.GetUpdateMask(), req.GetWidget(), update.WithCurrent(stored))
if err != nil {
    return err
}
req.UpdateMask = effective.ToProtoFieldMask()
```

Without current state, `Prepare` carries protected-value validation to the handler through a `DeferredPlan`.

`StripUpdate` and the masked update APIs share a merge-effect evaluator: protected input is checked against the result of the actual merge,
including oneof clears and collection replacement. `StripUpdate` retains validated protected values so later implied planning, including atomic
message replacement, reaches the same decision. Its selection contains populated fields; masked pipelines use `Prepare` to retain explicit clear intent.

## Two-stage update

`Prepare(raw, resource, opts...)` performs stateless normalization and returns a `DeferredPlan`. Its `Mask` is the initial effective mask;
`Operations` describes deferred set, clear and whole-replacement intent. `Complete(stored)` compares the captured values, restores any deferred
replacement paths, and updates the bound request to the same result as a stateful `ApplyMask`.
When preparation already had `WithCurrent`, completion keeps that sanitized result. It leaves stored state untouched and is idempotent.
A validation or behavior refusal restores the captured original request; message mismatch and conversion failures leave the current request untouched.
An explicit protected clear remains a comparison even when the initial mask is empty. The plan retains one resource snapshot and the raw mask,
subject to the existing path limits, even when `Operations()` is empty. Collection validation (including stored elements of an absent request list), expansion and retained routing-message
sanitization can need stored state without a protected comparison. An empty `Operations()` result does not authorize skipping completion.
`Patch` uses the captured resource for `allow_missing` creation, ignoring the update mask, including invalid paths.
Both the boundary's and the Spec's OUTPUT_ONLY clearing apply before `Create` or the `validate_only` return.

After successful `Prepare`, the handler and downstream validators see:

| Client mask | Handler-visible resource |
|---|---|
| Omitted | Populated leaves imply selection. Unselected values are filtered even when no work remains; protected values, routing identities and containers needed for deferred comparison are retained. OUTPUT_ONLY is cleared. |
| Explicit empty | Unselected mutable input and OUTPUT_ONLY are cleared under the same filtering policy. Protected values and routing identities (including etag) are retained. The mask stays explicitly empty, with no comparisons, writes or secret changes. |
| Only OUTPUT_ONLY / dropped paths | Unselected input is filtered with the same preservation policy; OUTPUT_ONLY is cleared. The effective mask is explicitly empty. |
| Writable paths remain | Selected input is retained; unselected input is filtered except protected values, identities and containers needed for deferred comparison. OUTPUT_ONLY is cleared. |
| `*` | Replacement selection is processed; OUTPUT_ONLY is cleared and protected input is retained. |

Preservation follows the configured resolver, identity, immutable and atomic policies.
The captured original input remains the source for deferred replacements, secrets and creation.
Standalone `Complete` replays that original input, so it can restore unselected input on success
(including explicit-empty input) as stateful `ApplyMask` does; a refusal restores the original request.
A plan prepared with `WithCurrent` is already resolved and keeps its sanitized request.
The stateless APIs retain their documented empty-mask behavior and error ordering.

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
Policy-only routing identities keep their existing rules: Spec refuses explicit identity
paths, and implied or wildcard updates neither compare nor write them. Successful deferred completion restores captured input even if the normalized
request was subsequently changed.

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

## Path validation in Apply

`ApplyMask` and `Apply` reject a path the grammar or the schema rejects before any mutation, so a typo cannot clear a sibling subtree.
`WithPathValidation` opts out; such a path matches no field and both leave it out of the mask they return, so it neither covers another path nor
names a map key a `*` beside it would then skip:

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

A `*` naming every element whole (`delegates.*`) covers a key path beside it (`delegates.k.org`), as `Spec.Plan` and `MergePaths` read it: every entry
is replaced, `k` included, and the key path is checked as if sent alone. A `*` sub-path of a map (`delegates.*.email`) addresses the entries no key path
names; when the check drops such a key path (an unchanged `IMMUTABLE` value), the `*` is replaced by the keys of the request entries it addresses
(`delegates.j.email`), so the cleaned mask and a later `MergePaths` never touch the entry the `*` was checked without. Below the elements of another
collection the entries cannot be named per element: such a mask is refused as a path error (`*fieldmask.ValidationError`, `fieldmask.ErrInvalidPath`),
unless the `*` addresses no request entry and is simply dropped. The keys generated this way are held to the mask limits (`WithMaxPaths`,
`WithMaxPathLength`, `WithMaxPathDepth`) before anything is copied, and the cleaned mask as a whole after: a mask whose pinned `*` would break one is
refused with a `*fieldmask.ValidationError` matching the limit sentinel (from `Spec.Plan`, a field error on the mask path of the `*`), so the cleaned
mask and `Plan.Paths` can always be written back and merged.

A protobuf map entry whose literal key is `*` is an ordinary entry. A mask path cannot name it (AIP-161), so only the wildcard reaches it: `delegates.*` and
`delegates.*.email` check, compare and merge it like any other entry, and a `*` that would have to be pinned to name it is refused as a path error.

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
messages, or messages that do not share the exact descriptor,
are `ErrMessageMismatch`. Every check runs before the first write, so `dst` is changed all-or-nothing.
Generated and `dynamicpb` representations of the same descriptor may be mixed, at the root or within dynamic parents.
Each copied message, collection and element is allocated through the destination's representation.

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
  with `ErrProtectedField`, retained for caller comparison and excluded from paths without `WithCurrent`); a `REQUIRED` path left empty, or a `REQUIRED` field left empty in a value written whole,
  is refused with `ErrBehaviorViolation`; a `*` sub-path of a list whose request holds elements, but not as many as the stored list, is refused with
  `ErrElementCountMismatch`;
- a path setting a oneof member is refused when another member is `IMMUTABLE` or `IDENTIFIER`, holds such a field, or (top-level) is identity,
  immutable or secret, and, with `WithCurrent`, the stored resource holds it: setting one member clears the others.

`Spec.Plan` reports one refusal in a `coreerrs.FieldError` wrapping `*MaskPathError`. The field is `updateMask.paths[i]`
for an explicit path, or the bare mask field for implied masks, `*`, and mask-count limits. `WithUpdateMaskFieldName` renames it.
The cause is the original classification: changed protected fields match `ErrProtectedField`, missing REQUIRED values
match `ErrBehaviorViolation`, and invalid paths or limits retain their sentinels. All entry points use one ordered violation list:
on the error path, the frozen `a8b17ba` checking walk runs on the original request, stored resource, selection and options.
Its diagnostics that the current evaluation also found form the prefix, in the base's order, including its REQUIRED early returns
and atomic first-difference stopping rule. Remaining diagnostics follow in field declaration order, with numeric list indexes and
unescaped sorted map keys; behavior and reason break ties. Base wildcard diagnostics match the evaluation's concrete element paths
in that deterministic order. The reference never decides acceptance or adds a violation that the current evaluation did not find.
Dynamic roots, literal JSON star keys, and selections the base refuses (expansion limits or wildcard pinning) use the deterministic order alone.
A reference error or panic also falls back to that order; it cannot affect acceptance or suppress the evaluated refusal. Successful deferred
preparation retains the original selection and values, so completion follows the same rule. Plan reports the first violation. Secret oneof checks still follow ordinary mask refusals. `Patch.Run` wraps a changed protected value
with another `FieldError` on `Field + "." + path`, retaining the plan error underneath. It leaves other plan refusals unchanged.

`ApplyMask`, `Apply`, `Prepare` and completion return `*BehaviorError` with all concrete resource-relative violations.
Strict `ApplyMask`/`Prepare`/completion and `ValidateStrict` instead return `*StrictError`.
A failed stateless Prepare returns no plan and cannot report changes that require stored state; completion equivalence applies
after successful preparation. Mask defects are
`*fieldmask.ValidationError`, wrapped by `StrictError` in strict mode. Spec and Patch retain their single-field construction,
including when a deferred plan is supplied. A stricter policy retained by the normalizer can additionally refuse an operation
the Spec alone permits; it uses the same Spec/Patch error construction. `apierror/aipmap` uses its original aggregate selection and wrapper handling;
aggregate paths are not automatically prefixed by batch wrappers or changed by `coreerrs.MapFieldPath`.

`Plan.Paths` feed `MergePaths`; `Plan.Secret` tells a service whether to set, clear or keep a secret. `Patch[E, P, V]` runs the whole
read-modify-write: `Run(ctx, in, tag, req.GetUpdateMask(), opts...)` parses the revision, refuses malformed and unknown paths before the read (unless `allow_missing` may create), checks
the revision against the stored resource, plans against it (a changed IMMUTABLE or IDENTIFIER value is a `coreerrs.FieldError` on
`Field + "." + path`), clears the OUTPUT_ONLY values of the request, merges and saves.
An empty mask saves nothing and returns the stored resource
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
  stored with the resource, with revision `coreetag.Tag` (the zero Tag when no etag is sent). `Rule` sets the field, whether the etag is required and the
  comparison (exact by default); a sent etag that does not match `Etag(entity)` is `etag.ErrMismatch`.

`WithAllowMissing(true)` is AIP-134's `allow_missing`: when `Read` reports `coreerrs.ErrNotFound`, `Run` checks `Precondition.Missing` (both presets
refuse an etag sent for a missing resource as `etag.ErrMismatch`), ignores the entire mask, including invalid paths, and passes a copy of the request
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
validation is always enforced) and `Spec.Plan` (`WithCurrent`; the policy is the spec's); `WithStrict` configures `ApplyMask` and `Prepare`. Policy and per-request values share one `Option` type, so `Apply` takes
the policy options directly.

Share the same policy options across `ApplyMask`, `Apply`, `ValidateStrict`, `FromSetFields`, `ExpandMessagePaths`, `DropIdentifiers`, `ImpliedPaths`,
`ResolvePath`, `Merge` and `MergePaths`. `Patch` carries the spec's policy through the merge automatically. Atomic messages are replaceable leaves;
`WithAtomicMessage(nil)` disables atomic boundaries. `WithResolver` accepts a `fieldbehavior.Resolver` for planning and nested update traversal; nil
uses the `google.api.field_behavior` annotations. Predicates and resolvers shared between goroutines must support concurrent calls.

A whole atomic value with protected descendants is deferred without current state. With current state, every protected descendant must match,
absence included; collections inside it are also compared. Comparisons ignore OUTPUT_ONLY descendants under the shared resolver, allowing
`StripUpdate` followed by planning. Sanitized atomic and collection replacements retain the stored protected values, including their OUTPUT_ONLY
descendants. Unprotected OUTPUT_ONLY descendants of whole collection replacements still clear when their elements are replaced.

## Errors

| Error                                                                                                      | Returned by                                                                | Meaning                                                                                                |
|------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------|
| `*fieldmask.ValidationError`                                                                               | `ApplyMask`, `Apply`, `ValidateStrict` (as `Cause`), `Merge`, `MergePaths` | A path the grammar or the schema rejects; matches `fieldmask.ErrInvalidPath`                           |
| `*BehaviorError`                                                                                           | `ApplyMask`, `Apply`                                                       | One `FieldViolation` per AIP-203 violation; matches `ErrBehaviorViolation`                             |
| `*StrictError`                                                                                             | `ValidateStrict`, `ApplyMask` with `WithStrict(true)`                      | Matches `ErrInvalidUpdateMask` and, for behavior violations, `ErrBehaviorViolation`                    |
| `coreerrs.FieldError`                                                                                      | `Spec.Plan`, `Patch.Run`                                                   | Mask field for Spec; resource field for Patch protected changes; preserves `*MaskPathError`                                        |
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
| `WithPathValidation`         | `Option`      | PathValidationEnforce                   | Select PathValidationEnforce, PathValidationWarn, or PathValidationDisabled for `ApplyMask` and `Apply`.               |
| `WithPathValidationReporter` | `Option`      | nil / unset                             | Observe path validation failures in reporting mode.                                                                    |
| `WithStrict`                 | `Option`      | false                                   | `ApplyMask` / `Prepare`: check as `ValidateStrict` does (every path enforced, refusals as `*StrictError`).                    |
| `WithAllowMissing`           | `PatchOption` | false                                   | AIP-134 allow_missing for one `Patch.Run`: create a missing resource with `Patch.Create`.                              |
| `WithValidateOnly`           | `PatchOption` | false                                   | AIP-163 validate_only for one `Patch.Run`: run every check, skip `Save` and `Create`.                                  |

## Limits

The mask limits of [fieldmask](../README.md#limits) apply to every entry point a mask enters, before any parsing: `WithMaxPaths`, `WithMaxPathLength`
and `WithMaxPathDepth` (defaults `fieldmask.DefaultMaxPaths`, `DefaultMaxPathLength`, `DefaultMaxPathDepth`; zero lifts a limit, a negative value keeps
the default) are options of `ApplyMask`, `Apply` and `ValidateStrict` (in every `PathValidationMode`), `Merge`, `MergePaths` and `ResolvePath`, and of
`Spec.Plan` and `Patch` through `NewSpec`. A mask over a limit is a `*fieldmask.ValidationError`, or from `Plan` a field error on `updateMask.paths[i]`
(the bare mask field for a path-count failure, an implied mask, or `*`) wrapping a `*MaskPathError`.
The implied masks of `Plan`, `ImpliedPaths` and `FromSetFields` stop at the policy's `WithMaxPathDepth`; the deepest path then stands for the subtree below it.

With stored state, the limits bound the final effective mask, not only the mask as sent. `ApplyMask`, `Apply`, `ValidateStrict` and `Spec.Plan` check the paths the update writes
— after `*`, a message named whole that holds a server-managed field, and a pinned `*` are expanded — before the request is touched, and refuse a mask
whose expansion breaks a limit with the limit sentinel (from `Plan`, a field error located at the mask path that expands), since `MergePaths` would
refuse those paths. The default of 1,000 paths covers the expansion of ordinary resources; a service whose `*` or whole messages expand to more leaves
raises `WithMaxPaths` (on `NewSpec` for `Plan` and `Patch`). The expansion itself is bounded as it is built: it stops at the first path over
`WithMaxPaths` or `WithMaxPathDepth`, and after visiting `WithMaxPaths` × `WithMaxPathDepth` messages, so a schema that branches at every level cannot
make a small request expand exponentially before the effective mask is checked.

Without stored state, covering expansion and wildcard exclusions remain unresolved. A wildcard beside a deferred key stays with that key
in the plan instead of being pinned to temporary request keys. If an interim expansion exceeds a limit, its writes are deferred. `Complete`
replays the original mask and request with stored state and checks the final path count, length and depth. Raw-mask limits and traversal
budgets still apply immediately. A stateless effective mask alone does not describe this pending work.

The REQUIRED fields of a value an update writes whole (`*`, a message named whole, a replaced collection's elements) are checked down to
`fieldbehavior.DefaultMaxDepth` (32) nested messages, the depth `fieldbehavior.ValidateRequired` checks on create. A value nesting deeper is refused
with `fieldbehavior.ErrMaxDepthExceeded` (from `Spec.Plan`, a field error on the bare mask field), never checked in part; the walk builds a field path
only for a violation, so its cost is linear in the depth.

The complete selection's merge effect is evaluated once per direct normalization or planning call, including protected ancestor presence and implicit oneof clears.
That evaluation clones the complete stored and request resources once each; it does not copy them again for covered paths.
On successful direct calls, copying therefore costs the resource size once per evaluation, plus mask processing and the shared traversal budget.
Rejections also run the reference walk: each distinct covered sub-selection can copy its protected subtree once for the request and once for
stored state. Large byte payloads inside that subtree therefore incur copying proportional to the number of those selections. Each distinct
covered path still has its input contracts checked; duplicate paths add no repeated validation. A large unselected field contributes to the
whole-resource copies, so path limits do not bound payload bytes. Secret oneof switches compare only the affected sibling
values through sparse holders; they do not copy an unselected payload once per switch.

`Spec.Plan` with a deferred plan (also through `Patch.Run`) replays the boundary for refusals against the supplied stored resource,
then runs ordinary Spec planning on the captured input after boundary OUTPUT_ONLY sanitization. A final boundary comparison covers the
planned paths and secret operations, including their oneof effects. Direct and Patch-sanitized merge sources are checked separately when
they differ. These checks require resource-sized working copies; publishing the bound request and Patch's final merge also cost the
resource size per call. Rejected evaluations have the additional reference-selection copying cost described above.

A resource whose root is `google.protobuf.Struct`, `Value` or `ListValue` uses JSON keys, not protobuf wrapper fields.
An omitted mask selects populated object keys; a populated list or scalar `Value` implies `*`. Empty objects/lists and an unset
`Value` imply no writes. An explicit `*` replaces the complete JSON root, including removing stored-only keys, clearing an empty
replacement and changing list length. `MergePaths` consumes the returned `*` as a whole-root replacement. Generated `structpb`
and `dynamicpb` representations have the same selection and filtering behavior, at the root and inside ordinary messages.
JSON keys containing dots, backticks, or the empty string are quoted. A literal `*` JSON key is written as `` `*` `` and stays distinct
from the wildcard through implied masks, validation, filtering, planning and merging; an omitted mask never deletes unselected stored keys.

## API

| Symbol                                                                                                                                  | Purpose                                                                                                                                                                               |
|-----------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `ApplyMask`                                                                                                                             | Apply a raw update mask as sent, every path checked on its own, and return the effective mask to write back.                                                                          |
| `Apply`                                                                                                                                 | Apply an update mask to the request resource under the AIP-203 field behaviors.                                                                                                       |
| `ValidateStrict`                                                                                                                        | Validate an update mask under the rules of `Apply` without mutating the resource and return the cleaned mask.                                                                         |
| `FieldViolation`                                                                                                                        | One field-behavior violation of `ApplyMask` or `Apply`: path, behavior and reason (`Format` is always empty here).                                                                    |
| `BehaviorError`                                                                                                                         | The AIP-203 violations of `ApplyMask` and `Apply`; maps to INVALID_ARGUMENT.                                                                                                          |
| `StrictError`                                                                                                                           | The invalid paths of a strict update mask.                                                                                                                                            |
| `Spec`                                                                                                                                  | The update-mask classification of a resource message's top-level fields; immutable and safe for concurrent use.                                                                       |
| `Plan`                                                                                                                                  | The resolved update mask of one request: `Paths`, `Empty`, `Secret`.                                                                                                                  |
| `MaskPathError`                                                                                                                         | Locates a `Spec.Plan` or `Patch.Run` refusal in the mask: `Index`, `Path`, `Field`, `Err`.                                                                                            |
| `ImpliedPaths`                                                                                                                          | The implied update mask of an omitted `update_mask`: the populated leaf fields, skipping identity, secret, immutable and server-managed ones.                                         |
| `ResolvePath`                                                                                                                           | An update path spelled by declared names, or `""`: list indexes, element fields without `*` and paths into atomic values are not update paths.                                        |
| `Merge`, `MergePaths`                                                                                                                   | Copy the fields a mask or update paths name from `src` to `dst`, all-or-nothing.                                                                                                      |
| `DefaultAtomicMessage`                                                                                                                  | Treat `google.protobuf.*` and `google.type.*` values as leaves, except the dynamic `Struct`, `Value` and `ListValue`.                                                                 |
| `Patch`                                                                                                                                 | A masked read-modify-write: precondition, plan, merge, save, allow_missing create and validate_only.                                                                                  |
| `Precondition`                                                                                                                          | Revision syntax and concurrency policy: `Parse`, `Check`, `Missing`.                                                                                                                  |
| `Int64Precondition`                                                                                                                     | The AIP-154 integer-version preset with an optional etag, built on `etag.Precondition.ParseRevision`.                                                                                 |
| `TagPrecondition`                                                                                                                       | The AIP-154 opaque-etag preset: an `etag.Precondition` rule and the current `coreetag.Tag` of an entity.                                                                                  |
| `PathValidationMode`                                                                                                                    | How `ApplyMask` and `Apply` treat invalid paths: `PathValidationEnforce`, `PathValidationWarn`, `PathValidationDisabled` (see [Path validation in Apply](#path-validation-in-apply)). |
| `Option`, `PatchOption`                                                                                                                 | The policy and per-request options of the package; the per-call options of `Patch.Run`.                                                                                               |
| `DefaultUpdateMaskField`                                                                                                                | `"updateMask"`, the field update-mask errors name unless `WithUpdateMaskFieldName` renames it.                                                                                        |
| `ErrInvalidUpdateMask`, `ErrBehaviorViolation`, `ErrProtectedField`, `ErrElementCountMismatch`, `ErrMessageMismatch`, `ErrInvalidPatch` | Sentinels; see [Errors](#errors).                                                                                                                                                     |

## Related packages

- [fieldmask](../README.md)
- [fieldmask/extract](../extract/README.md)
- [fieldbehavior](../../fieldbehavior/README.md)
- [etag](../../etag/README.md)
- [validateonly](../../validateonly/README.md)
- [fieldmask guide](../../docs/fieldmask.md)
