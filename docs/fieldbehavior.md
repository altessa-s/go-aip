# Field behavior

[fieldbehavior](../fieldbehavior/) applies the `google.api.field_behavior` annotations of AIP-203 to protobuf payloads. It complements
[fieldmask](fieldmask.md), which applies the same rules to the paths of an update mask.

## Behavior

The library walks a `proto.Message` and handles every populated field whose annotation makes it invalid in the current direction of the wire:
`OUTPUT_ONLY` and `IDENTIFIER` on Create, `OUTPUT_ONLY` and `IMMUTABLE` on Update (the `IDENTIFIER` is kept there because it addresses the resource),
and `INPUT_ONLY` on responses. `ValidateRequired` checks the `REQUIRED` fields of a request or of a Create resource. The companion package
[`fieldmask`](../fieldmask/README.md) deals with the `FieldMask` *value*; `fieldbehavior` deals with the resource *payload*.

## AIP-203 conformance

The implemented AIP-203 behavior and related update-mask rules map to the functions below. "Stored value" is
the current resource passed with `WithCurrent` (fieldbehavior and fieldmask/update both have one).

| AIP-203 rule                                                                             | Implemented by                                                                                                  | Behavior                                                                                                                                                                                                                                                           |
|------------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| OUTPUT_ONLY: the server must clear the value from a request and must not return an error | `StripCreate`, `StripUpdate`, `Strip` (any set naming it); `update.Apply`, `Patch`                              | Cleared at every depth (singular, repeated, map), strict mode included; never a violation.                                                                                                                                                                         |
| OUTPUT_ONLY: ignored in update masks, at any depth                                       | `update.Apply`, `ValidateStrict`, `Spec.Plan`                                                                   | A path naming or crossing an OUTPUT_ONLY field is dropped. A message named whole that holds one is expanded to its other fields, so the stored value survives.                                                                                                     |
| IDENTIFIER: not accepted as input on create                                              | `StripCreate`                                                                                                   | Cleared by default; strict mode rejects a supplied identifier, deviating from AIP-203's instruction to ignore it on create.                                                                                                                        |
| IDENTIFIER: IMMUTABLE, and accepted as input on mutation methods                         | `StripUpdate`; `update.Apply`, `ValidateStrict`, `Spec.Plan`                                                    | Kept to address the resource. With the stored value, a different identifier is a violation and an equal one named in the mask is ignored. Without it, the value is retained for caller comparison and its path is dropped from the effective mask.                 |
| IMMUTABLE: an unchanged value is ignored, a change is INVALID_ARGUMENT                   | `StripUpdate`; `update.Apply`, `ValidateStrict`, `Spec.Plan`, `Patch`                                           | Compared with the stored value: equal is retained in the payload for subsequent planning and dropped from the write mask, different is a violation. Without stored state, IMMUTABLE values are retained for caller comparison and excluded from the effective write mask. |
| REQUIRED: present and set to a non-empty value                                           | `ValidateRequired` (create, requests)                                                                           | Absent or falsy (empty string or bytes, zero, false, zero enum, empty list or map, unset message) is a violation, at every depth of populated messages.                                                                                                            |
| REQUIRED on update: omitted when absent from the mask; a falsy value is an error         | `update.Apply`, `ValidateStrict`, `Spec.Plan`                                                                   | A REQUIRED path (nested ones too) with a falsy value is a violation; a REQUIRED field outside the mask may be omitted. A value written whole — a replaced message, a replaced collection's elements, the resource under `*` — has its REQUIRED fields checked.     |
| INPUT_ONLY: not included in output                                                       | `StripResponse`                                                                                                 | Cleared at every depth.                                                                                                                                                                                                                                            |
| Nested messages: annotations are independent of the parent's                             | every walker                                                                                                    | Each field is judged by its own annotations; REQUIRED fields of an absent sub-message are not checked.                                                                                                                                                             |
| UNORDERED_LIST, NON_EMPTY_DEFAULT, OPTIONAL                                              | none                                                                                                            | No request-handling semantics; they are not part of any default set and change nothing unless a caller passes them to `WithBehaviors`.                                                                                                                             |
| Several behaviors on one field                                                           | every walker                                                                                                    | A fixed priority, whatever the declaration order: OUTPUT_ONLY, IDENTIFIER, IMMUTABLE, REQUIRED.                                                                                                                                                                    |
| AIP-202 field formats (`google.api.field_info`), checked in the same walk                | `Strip`, `StripCreate`, `StripUpdate`, `ValidateRequired` (`WithFieldFormats`, default on); not `StripResponse` | Every kept string value — singular, repeated element, map value, at every depth — that breaks its `UUID4`/`IPV4`/`IPV6`/`IPV4_OR_IPV6` format is a violation (Behavior unspecified); cleared or rejected fields are not checked. See [fieldinfo](fieldinfo.md).    |
| AIP-154 etag: no field behavior                                                          | every walker                                                                                                    | Nothing requires the etag to carry an annotation. `Spec` treats `id` and `etag` as identity fields by name: never implied, refused when named in a mask.                                                                                                           |

Collection elements are compared by list index or map key when current state is supplied, including elements named whole in an update mask.
Without current state, masked replacements of whole collections or atomic messages containing protected fields are deferred: the covering path is
omitted while the request value is retained. `update.Prepare` captures these replacements in a `DeferredPlan`, which completes with stored state. A protected value whose stored parent is absent differs from its stored value.

Without stored state, `update.Prepare` retains comparisons and explicit clear intent in a plan. Attach it with `update.ContextWithPlan` for
`Patch.Run`, or pass `update.WithPlanContext` and `WithCurrent` to `Spec.Plan`. `StripUpdate` has no mask: AIP-134's omitted mask implies populated
fields, so an unpopulated field is outside the update and requests no clear. Its retained protected values are compared by `Spec.Plan` / `Patch.Run`
with the implied mask and stored state. Never store the stripped request as the resource.

## When to reach for `fieldbehavior`

| Scenario                                                                             | Use                                                     |
|--------------------------------------------------------------------------------------|---------------------------------------------------------|
| Strip server-managed identifiers and timestamps from a Create resource               | `StripCreate(req.GetResource())`                        |
| Reject absent or empty REQUIRED fields on Create (or on any request message)         | `ValidateRequired(req.GetResource())`                   |
| Sanitize an Update that selects its populated fields (no mask) | `StripUpdate(req.GetResource(), WithCurrent(stored))`   |
| Update driven by an `update_mask`                                                    | `update.Apply` with `WithCurrent` (no strip)            |
| Strip secrets (`INPUT_ONLY` — passwords, tokens) from a response before returning it | `StripResponse(resp)`                                   |
| Reject identifiers or secrets instead of silently clearing them                      | Add `WithStrict(true)` → `*BehaviorViolationError`      |
| Cap traversal depth on adversarial/fuzzed input                                      | `WithMaxDepth(n)`                                       |
| Skip AIP-202 format checks, or add a custom format (email)                           | `WithFieldFormats(false)`, `WithFormatChecker(checker)` |
| Normalize formatted values (lowercase UUID, RFC 5952 IPv6) before persisting         | `fieldinfo.Normalize(res)` after the strip              |

## Quick start

### Create — clear server-managed fields, check required ones

```go
func (s *server) CreateBucket(ctx context.Context, req *pb.CreateBucketRequest) (*pb.Bucket, error) {
    if err := fieldbehavior.StripCreate(req.GetBucket()); err != nil {
        return nil, err
    }
    if err := fieldbehavior.ValidateRequired(req.GetBucket()); err != nil {
        return nil, err // *BehaviorViolationError: INVALID_ARGUMENT with one violation per field
    }
    // ...
}
```

`DefaultCreateBehaviors() = [OUTPUT_ONLY, IDENTIFIER]`. `REQUIRED`, `IMMUTABLE` and `INPUT_ONLY` survive: the client is allowed (and often expected)
to set them at creation. `ValidateRequired` skips OUTPUT_ONLY and IDENTIFIER fields, which are not input on create.

### Update — compare immutable fields with the stored resource

```go
func (s *server) ReplaceBucket(ctx context.Context, req *pb.ReplaceBucketRequest) (*pb.Bucket, error) {
    stored, err := s.repo.Get(ctx, req.GetBucket().GetName())
    if err != nil {
        return nil, err
    }
    if err := fieldbehavior.StripUpdate(req.GetBucket(), fieldbehavior.WithCurrent(stored)); err != nil {
        return nil, err // a changed IMMUTABLE or IDENTIFIER value
    }
    // ...
}
```

`DefaultUpdateBehaviors() = [OUTPUT_ONLY, IMMUTABLE]`. OUTPUT_ONLY values are cleared; an IMMUTABLE value equal to the stored one is retained for subsequent
planning, a different one is a violation. `IDENTIFIER` is kept: it addresses the resource, and with `WithCurrent` a different one is a
violation. `StripUpdate` treats every populated field as part of the update, which suits an update without a field mask whose populated fields are selected. For full replacement, use `update.ApplyMask` with `*` and stored state.
An update driven by an `update_mask` uses `update.Apply` with `update.WithCurrent(stored)` instead: it applies the same
rules to the masked fields only and clears the OUTPUT_ONLY values itself.

Without `WithCurrent`, `StripUpdate` retains protected values for the caller to compare; it neither rejects them as unverified nor discards them. A stored resource of another message type is `ErrCurrentMismatch`.

#### Migrating from the earlier Update defaults

`StripUpdate` used to clear `IDENTIFIER` fields, and later used to clear `IMMUTABLE` fields silently, so a requested change was lost without an error.
It now keeps both unverified and validated immutable and identifier values, and compares them when supplied with stored state. This preserves comparison evidence inside mutable atomic messages. Pass `WithCurrent(stored)`; a caller that persists the stripped
payload must merge only validated mutable fields and preserve stored protected values.

### Response — strip INPUT_ONLY secrets

```go
func (s *server) GetBucket(ctx context.Context, req *pb.GetBucketRequest) (*pb.Bucket, error) {
    b, err := s.repo.Get(ctx, req.GetName())
    if err != nil {
        return nil, err
    }
    if err := fieldbehavior.StripResponse(b); err != nil {
        return nil, err
    }
    return b, nil
}
```

### Custom behavior set

```go
// Report populated INPUT_ONLY fields under a custom strict policy.
err := fieldbehavior.Strip(msg,
    fieldbehavior.WithBehaviors(annotations.FieldBehavior_INPUT_ONLY),
    fieldbehavior.WithStrict(true),
)
```

## API surface

| Function                         | Default set                  | Contract                                                                                 |
|----------------------------------|------------------------------|------------------------------------------------------------------------------------------|
| `Strip(msg, opts...)`            | none (no-op)                 | Handle the populated fields of the `WithBehaviors` set.                                  |
| `StripCreate(msg, opts...)`      | `DefaultCreateBehaviors()`   | Clear OUTPUT_ONLY and IDENTIFIER.                                                        |
| `StripUpdate(msg, opts...)`      | `DefaultUpdateBehaviors()`   | Clear OUTPUT_ONLY, compare IMMUTABLE and IDENTIFIER with `WithCurrent`, keep IDENTIFIER. |
| `StripResponse(msg, opts...)`    | `DefaultResponseBehaviors()` | Clear INPUT_ONLY.                                                                        |
| `ValidateRequired(msg, opts...)` | —                            | Report absent or empty REQUIRED fields.                                                  |

All return `error`: `nil`, `*BehaviorViolationError` (matching `ErrBehaviorViolation`, the message left untouched), `ErrMaxDepthExceeded`, or
`ErrCurrentMismatch`.

### Options

| Option                                    | Default                     | Effect                                                                                                     |
|-------------------------------------------|-----------------------------|------------------------------------------------------------------------------------------------------------|
| `WithBehaviors(b ...)`                    | per function                | Replace the behavior set; an explicitly empty set disables stripping.                                      |
| `WithCurrent(stored proto.Message)`       | nil (unknown)               | The stored resource an update is compared with. Must be of the payload's type.                             |
| `WithStrict(enabled bool)`                | off                         | Report matching populated fields instead of clearing them; OUTPUT_ONLY and IMMUTABLE keep their own rules. |
| `WithMaxDepth(n int)`                     | `32`                        | Bound traversal depth.                                                                                     |
| `WithResolver(r Resolver)`                | `AnnotationResolver{}`      | Supply the annotations.                                                                                    |
| `WithFieldFormats(enabled bool)`          | on (off in `StripResponse`) | Also report kept string values that break their AIP-202 format.                                            |
| `WithFormatChecker(c *fieldinfo.Checker)` | AIP-202 formats only        | The checker of the formats, e.g. with a custom email format from a `fieldinfo.Registry`.                   |

`ValidateRequired` uses only `WithResolver`, `WithMaxDepth`, `WithFieldFormats` and `WithFormatChecker`.

### Errors

| Error                     | When                                                                                                                                                                                                                                                                                                               |
|---------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `*BehaviorViolationError` | One or more violations; `Violations []BehaviorViolation` carries `Path`, `Behavior`, `Reason` and `Format` (a format violation has Behavior `FIELD_BEHAVIOR_UNSPECIFIED`, `Format` the format name and Reason `value <reason> (<format>)`; `Format` is empty for every other violation). Maps to INVALID_ARGUMENT. |
| `ErrMaxDepthExceeded`     | The walk went deeper than `WithMaxDepth`; the payload is refused, never checked in part.                                                                                                                                                                                                                           |
| `ErrCurrentMismatch`      | `WithCurrent` holds a message of another type.                                                                                                                                                                                                                                                                     |

## Traversal semantics

| Field shape             | Behavior on field                    | Behavior absent                            |
|-------------------------|--------------------------------------|--------------------------------------------|
| Scalar, well-known type | Handled per behavior                 | No-op                                      |
| Singular nested message | Whole subtree handled, not descended | Recursive descent into populated subtree   |
| Repeated of messages    | Whole list handled                   | Recursive descent into each element        |
| Map with message values | Whole map handled                    | Recursive descent into each value          |
| Repeated/map of scalars | Handled per behavior                 | No-op                                      |
| Oneof case              | Case handled if set                  | Recursive descent if the case is a message |

"Handled" means: OUTPUT_ONLY cleared; IMMUTABLE compared by the shared merge-effect evaluator (retained when equal, a violation when changed),
retained without current state for later comparison; any other behavior cleared, or reported under `WithStrict`. The check runs before any mutation, so a payload with a
violation is never partly cleared. Paths use dots for repeated indices and map keys (`aliases.2.id`, `labels.k.id`); a map key that is empty or
holds a dot or a backtick is backtick-quoted with every backtick doubled, matching fieldmask paths. Map entries are visited in key order, so
violations are deterministic.

### Multiple behaviors on the same field

A field carrying several behaviors (`REQUIRED + IMMUTABLE` for a slug) matches the set when one of them is in it, and the behavior that governs is
chosen by a fixed priority, whatever the declaration order: OUTPUT_ONLY, IDENTIFIER, IMMUTABLE, REQUIRED, then the order of the set. `StripUpdate`
compares such a slug because `IMMUTABLE` is in `DefaultUpdateBehaviors()`; `StripCreate` leaves it alone and `ValidateRequired` checks it.

### What is **not** traversed

* Unpopulated fields outside collections: an absent field is not selected by `StripUpdate`. Inside a supplied collection replacement,
  an absent protected field is compared with its stored counterpart. `ValidateRequired` separately checks absent REQUIRED fields.
* Message types whose descriptor tree carries no relevant annotation — no behavior of the set and, when formats are checked, no field format: the
  walk is skipped (fast path).

## Strict mode

`WithStrict(true)` reports, instead of clearing, matching populated fields: IDENTIFIER on create, INPUT_ONLY
in a response (a server bug worth surfacing), or any custom behavior. It never reports OUTPUT_ONLY — AIP-203 forbids an error for it, so those values
are cleared in every mode — and it does not change the IMMUTABLE rule, which already reports a change and ignores an unchanged value. When strict mode
reports nothing, OUTPUT_ONLY values are cleared and validated IMMUTABLE values remain available for planning. Rejecting a create identifier under strict mode
remains a behavior difference from AIP-203's instruction to ignore the supplied identifier on create.

## Wiring into gRPC

go-atlas provides a gRPC server interceptor that classifies methods by their AIP-133/AIP-134 prefixes and strips requests and responses. Calling
`StripCreate`, `ValidateRequired`, `StripUpdate` (with the stored resource) and `StripResponse` from a handler is equally valid; an update handler
already reads the stored resource, which is what `WithCurrent` needs.

## Performance

The default resolver caches decoded annotations per field descriptor, and the "does this subtree carry a relevant behavior" answer per message
type, in bounded process-wide LRU caches (4096 entries each), so dynamic descriptors are never retained without limit. Payloads whose message
type carries no relevant annotation cost one cache lookup. Violation paths are rendered only when a violation is recorded, and the clearing pass
is skipped when the checking pass found nothing to clear. Run `go test -bench . ./fieldbehavior` on the deployment toolchain for current
figures.

## `fieldbehavior` vs `fieldmask`

| Concern                                      | `fieldmask`                                           | `fieldbehavior`                                           |
|----------------------------------------------|-------------------------------------------------------|-----------------------------------------------------------|
| What is the input?                           | A `FieldMask` value (paths) and the resource          | A `proto.Message` payload                                 |
| What does it produce?                        | The cleaned mask and filtered resource, or violations | A sanitized `proto.Message`, or `*BehaviorViolationError` |
| When does it run on the wire path?           | Update with an `update_mask`                          | Create, Update without a mask, Response                   |
| How does it use `google.api.field_behavior`? | Applies the AIP-203 rules to the masked paths         | Applies them to the populated payload fields              |

For a masked update, `fieldmask/update` alone applies every rule; for Create, run `StripCreate` then `ValidateRequired`.

## Edge cases & FAQ

**Q: Can I add new behaviors when Google extends the proto?**
A: Yes — `WithBehaviors` takes any `annotations.FieldBehavior` value; unknown values are cleared, or reported under strict mode.

**Q: My `oneof` has an INPUT_ONLY case and a non-secret case. Does `StripResponse` collapse the oneof?**
A: Only if the **active** case is the annotated one; then the oneof is cleared. A REQUIRED oneof member is satisfied by any member of its oneof.

**Q: Is `Strip` thread-safe?**
A: Its state lives for one call. The input message must not be shared between concurrent calls that may mutate it.

**Q: How do I integrate with `protovalidate`?**
A: Run `StripCreate` / `StripUpdate` first, then validation, so the validator does not flag OUTPUT_ONLY values that were about to be cleared.

## Resolver dependency

`Resolver` has `Resolve(protoreflect.FieldDescriptor) []annotations.FieldBehavior`. `AnnotationResolver` reads `google.api.field_behavior` through a
bounded, process-wide descriptor cache; `ResolverFunc` adapts an application callback. Inject the same concurrency-safe resolver with
`fieldbehavior.WithResolver` and `update.WithResolver` so stripping and planning share metadata.

## See also

* [`fieldmask`](fieldmask.md) — update masks under the same AIP-203 rules.
* [`fieldinfo`](fieldinfo.md) — AIP-202 field formats.
* [`internal/behavior`](../internal/behavior) — the shared annotation reader, emptiness and comparison helpers.
* **AIP-203** — `field_behavior` definitions ([aip.dev/203](https://google.aip.dev/203)).
* **AIP-133 / AIP-134** — Create / Update method conventions.
* **AIP-154** — etags ([aip.dev/154](https://google.aip.dev/154)).
* [Package options](../fieldbehavior/README.md#options).
