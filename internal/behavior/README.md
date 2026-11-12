# behavior

```go
import "github.com/altessa-s/go-aip/internal/behavior"
```

Shared annotation resolution and field paths for AIP packages. Decoded annotations are cached per field descriptor in a bounded, process-wide
LRU (`AnnotationCacheSize`, 4096 descriptors), so dynamic descriptors are never retained without limit. Subtree checks handle recursive schemas
and arbitrary numeric annotation values; results for sets of values below 64 (every standard behavior) are cached per message type
(`SubtreeCacheSize`, 4096 entries), other sets are searched on every call.

## Contract

This is an internal implementation package. Its exported helpers are available only to packages permitted by Go internal import rules. Mutable work
state belongs to the caller or an explicitly constructed instance; consumers should use the public parent APIs.

## Functions

| Function                    | Contract                                                                       |
|-----------------------------|--------------------------------------------------------------------------------|
| `Get`                       | Read an owned annotation slice                                                 |
| `Shared`                    | Read the cached annotation slice without a copy; callers must not modify it    |
| `Has`, `HasAny`             | Match annotations                                                              |
| `SubtreeHasAny`             | Search the reachable schema (call-local visited set), cached per type and set  |
| `FirstMatch`                | Prefer OUTPUT_ONLY, IDENTIFIER, IMMUTABLE, REQUIRED, then the configured order |
| `JoinPath`, `QuoteSegment`  | Build dot-separated paths, quoting map keys by the AIP-161 grammar             |
| `IsEmpty`, `RequiredUnmet`  | Tell an absent or falsy value, the AIP-203 REQUIRED test                       |
| `RequiredViolations`        | Walk populated messages for absent or empty REQUIRED fields                    |
| `CheckedRequiredViolations` | `RequiredViolations` plus a `FieldCheck` per visited populated field (formats) |
| `SameValue`, `Child`        | Compare a field with its stored counterpart; reach a singular sub-message      |
| `ProtectedViolation`        | Build the violation of a changed IMMUTABLE/IDENTIFIER value    |
| `SortedMapKeys`             | Visit map entries in a deterministic order                                     |

Public applications inject `fieldbehavior.Resolver` for alternative metadata or an instance-owned bounded cache.

## API

| Symbol                                    | Purpose                                                                                                                                                                                                                                                                                                                                                                                                    |
|-------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `ForEachMessageInList`                    | ForEachMessageInList invokes fn for each message element of list. Iteration stops at the first non-nil error from fn; lists with no elements are a no-op.                                                                                                                                                                                                                                                  |
| `Violation`                               | Violation describes a single field-behavior constraint that was violated during update-mask application or payload sanitization. It is the shared shape used by both [github.com/altessa-s/go-aip/fieldmask/update] and [github.com/altessa-s/go-aip/fieldbehavior] so the go-atlas gRPC interceptor can render either source through the same io.altessa.badrequest.v1.BadRequest FieldViolation mapping. |
| `Resolve`, `FieldCheck`                   | The field-behavior source of a REQUIRED walk, and an extra per-field check (fieldbehavior's AIP-202 formats) run in the same walk.                                                                                                                                                                                                                                                                         |
| `ReasonRequired`                          | The violation reason of an absent or empty REQUIRED field.                                                                                                                                                                                                                                                                                                                                                 |
| `ReasonImmutable*`, `ReasonIdentifier*`   | The violation reasons of an IMMUTABLE or IDENTIFIER value an update cannot accept: `…Changed` for a changed value, `…Unverified` for one written while the stored value is unknown (AIP-203).                                                                                                                                                                                                              |
| `AnnotationCacheSize`, `SubtreeCacheSize` | The bounds of the process-wide annotation and subtree caches (4096 entries each).                                                                                                                                                                                                                                                                                                                          |

## Related packages

- [go-aip](../../README.md)
- [internal/literalvalue](../literalvalue/README.md)
