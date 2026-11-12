# fieldbehavior

```go
import "github.com/altessa-s/go-aip/fieldbehavior"
```

Package `fieldbehavior` applies the `google.api.field_behavior` annotations of AIP-203 to a `proto.Message` payload. It solves a different problem
than `fieldmask`: where fieldmask applies the rules to the paths of an update mask, fieldbehavior walks the payload itself and clears, compares or
rejects the fields whose behavior makes them invalid for the current request kind, and checks REQUIRED fields. Use it on the resource of a Create
request, on an Update whose populated fields are the update, and on a server response, so server-managed identifiers, timestamps and secrets never
cross the wire in the wrong direction. The [guide](../docs/fieldbehavior.md#aip-203-conformance) maps every AIP-203 rule to its function.

All stripping entry points publish changes only after successful validation and conversion. On any error, the message is unchanged.
Representation conversion errors match `ErrCurrentMismatch` and preserve the protobuf cause.

## Contract

`StripCreate` clears OUTPUT_ONLY and IDENTIFIER fields. `StripUpdate` clears OUTPUT_ONLY fields and compares IMMUTABLE ones with the stored resource
passed with `WithCurrent`: an unchanged value is retained for subsequent planning, a changed one is a violation. Without `WithCurrent`, IMMUTABLE values are
retained for the caller to compare with stored state. It keeps IDENTIFIER fields so the handler can locate the resource; with `WithCurrent` a
changed identifier is a violation. `StripResponse` clears INPUT_ONLY. `ValidateRequired` reports REQUIRED fields that are absent or set to an empty
value (empty string or bytes, zero, false, zero enum, empty list or map, unset message), recursing only into populated messages. The
Default*Behaviors functions return owned slices; `Strip` with `WithBehaviors` selects a custom set, and an explicitly empty set disables stripping.

OUTPUT_ONLY values are cleared at every depth and never reported, strict mode included (AIP-203 forbids an error for them). `WithStrict(true)`
reports the other matching populated fields instead of clearing them. Every function checks before it mutates: on `*BehaviorViolationError` the
message is untouched. DefaultStrict is false; DefaultMaxDepth is 32. Traversal includes populated singular messages, repeated messages, and map
values, bounded by the configured depth. BehaviorViolationError exposes violations (path, behavior, reason, format) and unwraps to
ErrBehaviorViolation.

Input walks also validate field formats (AIP-202, `google.api.field_info`) in the same pass: `Strip`, `StripCreate`, `StripUpdate` and
`ValidateRequired` report every kept string value that does not match the format of its field (`UUID4`, `IPV4`, `IPV6`, `IPV4_OR_IPV6`; a repeated
element indexed, a map value keyed) as a violation with Behavior `FIELD_BEHAVIOR_UNSPECIFIED`, `Format` the format name (the AIP-202 enum name or the
custom format name, empty for every other violation) and Reason `value <reason> (<format>)`. A field the walk clears (such as OUTPUT_ONLY) or rejects is not format-checked. Retained protected input is format-checked. `StripResponse` does not check formats (a response is not input). `WithFieldFormats(false)` turns the
check off; `WithFormatChecker` supplies a `fieldinfo.Checker` with custom formats, such as an email format AIP-202 does not define. Normalizing values
is a separate, explicit step: `fieldinfo.Normalize`. A `Strip` with an empty behavior set stays a no-op and checks no formats.

With current state, stripping uses the same merge-effect evaluator as `fieldmask/update`: it applies the implied selection to a private stored
resource and compares protected values, ignoring OUTPUT_ONLY descendants. This includes oneof sibling clears and collection element removals.
All validated protected values remain in the request, including inside mutable messages that a later planner may replace atomically.
`StripUpdate` has no mask: by AIP-134 the omitted mask implies populated fields, so an unpopulated field is not selected directly. Selected oneof siblings and whole collections can still clear protected values. Retained populated protected values are compared by `Spec.Plan` with `WithCurrent` or `Patch.Run` using that implied mask. For masked
two-stage updates, use `update.Prepare` and carry its plan in the context; the plan retains clear intent. Never replace a stored resource with the
stripped request. Protected-message equality ignores OUTPUT_ONLY descendants under the same resolver, so stripping these ignored inputs cannot
make an unchanged IMMUTABLE or IDENTIFIER message appear changed. Stateful update sanitization keeps the stored protected value when a whole
replacement is merged.

`Resolver.Resolve` supplies behavior metadata. AnnotationResolver reads google.api.field_behavior and returns owned values, decoding each field
descriptor once into a bounded, process-wide LRU (4096 descriptors); ResolverFunc adapts a function. `WithResolver` accepts the same dependency as
`update.WithResolver`, allowing stripping and update planning to share policy. Shared resolvers must support concurrent use.

## Functions

| Function           | Default behaviors           | Typical use                                                                     |
|--------------------|-----------------------------|---------------------------------------------------------------------------------|
| `StripCreate`      | `OUTPUT_ONLY`, `IDENTIFIER` | Create request payloads                                                         |
| `ValidateRequired` | `REQUIRED`                  | Create resources (after `StripCreate`) and request messages                     |
| `StripUpdate`      | `OUTPUT_ONLY`, `IMMUTABLE`  | Update payloads without a mask, with `WithCurrent(stored)` (keeps `IDENTIFIER`) |
| `StripResponse`    | `INPUT_ONLY`                | Server responses (passwords, auth tokens)                                       |

An update driven by an `update_mask` uses `update.Apply` with `update.WithCurrent(stored)`, which applies the same rules
to the masked fields only.

## Traversal semantics

| Field shape             | Behavior on field                    | Behavior absent                            |
|-------------------------|--------------------------------------|--------------------------------------------|
| Scalar, well-known type | Handled per behavior                 | No-op                                      |
| Singular nested message | Whole subtree handled, not descended | Recursive descent into populated subtree   |
| Repeated of messages    | Whole list handled                   | Recursive descent into each element        |
| Map with message values | Whole map handled                    | Recursive descent into each value          |
| Repeated/map of scalars | Handled per behavior                 | No-op                                      |
| Oneof case              | Case handled if set                  | Recursive descent if the case is a message |

Fields carrying multiple behaviors (for example `REQUIRED` + `IMMUTABLE`) match when at least one of their behaviors intersects the configured set,
and the governing behavior follows a fixed priority: OUTPUT_ONLY, IDENTIFIER, IMMUTABLE, REQUIRED, then the configured order. `UNORDERED_LIST`,
`NON_EMPTY_DEFAULT` and `OPTIONAL` have no request-handling semantics and are not part of any default set.

## Example

```go
func (s *server) CreateBucket(ctx context.Context, req *pb.CreateBucketRequest) (*pb.Bucket, error) {
    if err := fieldbehavior.StripCreate(req.GetBucket()); err != nil {
        return nil, err
    }
    if err := fieldbehavior.ValidateRequired(req.GetBucket()); err != nil {
        return nil, err // INVALID_ARGUMENT, one violation per field
    }
    // server-set fields (name, create_time) are now zero, safe to mint.
    return req.GetBucket(), nil
}

func (s *server) ReplaceBucket(ctx context.Context, req *pb.ReplaceBucketRequest) (*pb.Bucket, error) {
    stored, err := s.repo.Get(ctx, req.GetBucket().GetName())
    if err != nil {
        return nil, err
    }
    // OUTPUT_ONLY cleared; an unchanged IMMUTABLE value ignored, a changed one rejected.
    if err := fieldbehavior.StripUpdate(req.GetBucket(), fieldbehavior.WithCurrent(stored)); err != nil {
        return nil, err
    }
    // Persist only validated mutable fields, preserving stored protected values.
    return s.repo.UpdateMutable(ctx, stored, req.GetBucket())
}

func (s *server) GetBucket(ctx context.Context, req *pb.GetBucketRequest) (*pb.Bucket, error) {
    b, err := s.repo.Get(ctx, req.GetName())
    if err != nil {
        return nil, err
    }
    // strip any INPUT_ONLY field the storage layer may have read back.
    if err := fieldbehavior.StripResponse(b); err != nil {
        return nil, err
    }
    return b, nil
}
```

## See also

- [`fieldmask`](../fieldmask/README.md) — hierarchical field mask filter, prune, and update-mask validation.
- [`fieldinfo`](../fieldinfo/README.md) — AIP-202 field formats: validation, normalization, comparison, custom formats.
- AIP-203, AIP-133, AIP-134 — Google API Improvement Proposals defining `field_behavior` and the Create/Update conventions.

## Resolution and defaults

| API                          | Contract                                               |
|------------------------------|--------------------------------------------------------|
| `Resolver.Resolve(fd)`       | Supply annotations from an application metadata source |
| `AnnotationResolver`         | Read protobuf annotations through a bounded cache      |
| `DefaultCreateBehaviors()`   | Return a fresh OUTPUT_ONLY, IDENTIFIER slice           |
| `DefaultUpdateBehaviors()`   | Return a fresh OUTPUT_ONLY, IMMUTABLE slice            |
| `DefaultResponseBehaviors()` | Return a fresh INPUT_ONLY slice                        |

Custom resolvers must support concurrent calls when shared. They may own a bounded cache; the default lookup and the subtree-relevance check use
bounded process-wide LRUs, and the format check uses fieldinfo's bounded per-descriptor caches.
Unknown numeric behavior values participate in matching. An empty `WithBehaviors()` disables stripping, including convenience defaults.
`WithStrict(false)` reverses strict mode. Strict violations prefer IDENTIFIER, IMMUTABLE, REQUIRED, then the configured order; OUTPUT_ONLY is never
reported.
Paths use dots for repeated indices and map keys; a map key that is empty or holds a dot or a backtick is backtick-quoted with every backtick
doubled, matching fieldmask paths.

## Consistency contracts

BehaviorViolationError unwraps to ErrBehaviorViolation and maps to INVALID_ARGUMENT. Inspect Violations for paths, annotations and reasons;
diagnostic text is not a classification API. ErrCurrentMismatch reports a `WithCurrent` message of another type; ErrMaxDepthExceeded a walk deeper
than `WithMaxDepth`. The limit fails closed: `ValidateRequired` refuses a payload nesting more messages than `WithMaxDepth` (DefaultMaxDepth, 32) with
ErrMaxDepthExceeded rather than checking it in part, and builds a field path only for a violation, so its cost is linear in the depth. The update
package refuses the values an update writes whole with the same sentinel past DefaultMaxDepth.

`ResolverFunc` adapts a function to `Resolver`. The same resolver instance can be supplied to `fieldbehavior.WithResolver` and
`update.WithResolver`. Implementations shared across requests must support concurrent calls.

## Errors

| Error                     | Meaning                                                                     |
|---------------------------|-----------------------------------------------------------------------------|
| `*BehaviorViolationError` | One violation per offending field; matches `ErrBehaviorViolation`           |
| `ErrBehaviorViolation`    | Every field behavior or format violation (INVALID_ARGUMENT)                 |
| `ErrCurrentMismatch`      | A `WithCurrent` message that is invalid or of another type than the payload |
| `ErrMaxDepthExceeded`     | A walk deeper than `WithMaxDepth`                                           |

## Options

| Option              | Type     | Default                    | Behavior                                                                                      |
|---------------------|----------|----------------------------|-----------------------------------------------------------------------------------------------|
| `WithBehaviors`     | `Option` | nil / unset                | Replace the behavior set; an explicitly empty set disables stripping.                         |
| `WithCurrent`       | `Option` | nil (unknown)              | The stored resource an update payload is compared with (IMMUTABLE and IDENTIFIER).            |
| `WithFieldFormats`  | `Option` | DefaultFieldFormats        | Also report kept string values that break their AIP-202 format; `StripResponse` turns it off. |
| `WithFormatChecker` | `Option` | nil (AIP-202 formats only) | The `fieldinfo.Checker` used for formats, e.g. one with a custom email format.                |
| `WithMaxDepth`      | `Option` | DefaultMaxDepth            | Bound traversal depth.                                                                        |
| `WithResolver`      | `Option` | AnnotationResolver{}       | Sets resolver.                                                                                |
| `WithStrict`        | `Option` | DefaultStrict              | Report matching populated fields instead of clearing them; OUTPUT_ONLY is always cleared.     |

## Defaults and limits

| Constant              | Value   |
|-----------------------|---------|
| `DefaultFieldFormats` | `true`  |
| `DefaultMaxDepth`     | `32`    |
| `DefaultStrict`       | `false` |

## API

| Symbol                   | Purpose                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
|--------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `BehaviorViolation`      | BehaviorViolation describes a single field a request cannot carry: a field strict mode reports, an IMMUTABLE or IDENTIFIER value an update would change, a REQUIRED field left empty, or a value that breaks its AIP-202 format (Behavior unspecified). It aliases `behavior.Violation` so the go-atlas gRPC interceptor can render fieldbehavior and fieldmask violations through a single io.altessa.badrequest.v1.BadRequest FieldViolation mapping. |
| `Strip`                  | Apply the behavior set of `WithBehaviors` (none passed is a no-op); the `Strip*` functions preset their default sets.                                                                                                                                                                                                                                                                                                                                   |
| `BehaviorViolationError` | The violations of a call (`Violations`); unwraps to `ErrBehaviorViolation`, INVALID_ARGUMENT.                                                                                                                                                                                                                                                                                                                                                           |
| `AnnotationResolver`     | The default `Resolver`: reads `google.api.field_behavior` through a bounded process-wide cache.                                                                                                                                                                                                                                                                                                                                                         |
| `Option`                 | Option is a functional option for configuring options.                                                                                                                                                                                                                                                                                                                                                                                                  |
| `Resolver`               | Resolver supplies field annotations. Implementations must support concurrent calls when shared; callers may inject a bounded, instance-owned cache here.                                                                                                                                                                                                                                                                                                |
| `ResolverFunc`           | ResolverFunc adapts a function to Resolver.                                                                                                                                                                                                                                                                                                                                                                                                             |

## Related packages

- [go-aip](../README.md)
- [fieldinfo](../fieldinfo/README.md)
- [fieldmask](../fieldmask/README.md)
- [fieldpolicy](../fieldpolicy/README.md)
- [filter](../filter/README.md)
- [listing](../listing/README.md)
- [orderby](../orderby/README.md)
- [projection](../projection/README.md)
- [fieldbehavior guide](../docs/fieldbehavior.md)
