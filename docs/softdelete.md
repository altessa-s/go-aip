# Soft delete and purge

[softdelete](../softdelete/) implements AIP-164 (soft delete, undelete) and AIP-165 (criteria-based delete) on `proto.Message` values and
descriptors. It has no gRPC or storage dependency: a handler loads the stored resource, lets a `Policy` decide and change it, and saves it; a
`Purger` validates a Purge request and hands the validated filter to a `Store`. Errors match the go-atlas-core canonical errors, so the
transport maps them to the status codes below.

## AIP-164 conformance

| Rule                                                                            | Level         | Implemented by                                            | Behavior                                                                                                                                                                         |
|---------------------------------------------------------------------------------|---------------|-----------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Delete marks the resource deleted instead of removing it                        | MUST          | `Policy.Delete`                                           | Sets `delete_time` on the stored resource; the caller saves it.                                                                                                                  |
| The resource has `delete_time` and `purge_time` (AIP-148)                       | SHOULD        | `Policy.Validate`, every method                           | `delete_time` is required, `purge_time` optional (or another name via `WithPurgeTimeField`, e.g. `expire_time`); both singular `google.protobuf.Timestamp`.                      |
| `delete_time` and `purge_time` are OUTPUT_ONLY (AIP-148)                        | MUST          | `Policy.Validate`                                         | A field without OUTPUT_ONLY is `ErrUnsupportedResource` unless `WithOutputOnlyNotRequired(true)`.                                                                                |
| Document when soft-deleted resources are purged                                 | SHOULD        | `WithRetention`                                           | `purge_time = delete_time + retention` (30 days by default); 0 keeps the resource indefinitely and stamps no purge time.                                                         |
| Delete of a soft-deleted resource: success with `allow_missing`, else NOT_FOUND | MUST / SHOULD | `Policy.Delete`, `WithAllowMissing`                       | `ErrAlreadyDeleted` (NOT_FOUND); with allow missing a no-op `(false, nil)`.                                                                                                      |
| Get of a soft-deleted resource returns it                                       | SHOULD        | `Policy.CheckGet`                                         | nil error. `WithHideDeletedOnGet(true)` is a deliberate opt-out (NOT_FOUND).                                                                                                     |
| List hides soft-deleted resources unless `show_deleted`                         | SHOULD        | `Policy.Restriction`, `Policy.Restrict`, `Policy.Visible` | `Restriction` is `deleteTime == null` for `listing.Request.Restriction`; `Restrict` ANDs it onto an AST in hand; `Visible` decides per item. See [List](#list-and-show_deleted). |
| Undelete returns the resource                                                   | MUST          | `Policy.Undelete`                                         | Clears `delete_time` and `purge_time` in place; the handler saves and returns the resource.                                                                                      |
| Undelete of a resource that never existed or was expunged: NOT_FOUND            | MUST          | `Policy.Undelete`                                         | A nil resource or one past its purge time is `ErrNotFound`.                                                                                                                      |
| Undelete of a resource that is not deleted: ALREADY_EXISTS                      | MUST          | `Policy.Undelete`                                         | `ErrNotDeleted`, matching `coreerrs.ErrAlreadyExists`.                                                                                                                           |
| Without permission: PERMISSION_DENIED regardless of existence                   | MUST          | caller                                                    | Authorization runs before the policy; the package never reports permission errors.                                                                                               |
| `DELETED` state value when the resource has a `state` field (AIP-216)           | SHOULD        | caller                                                    | Not set by the policy: restoring the previous state is resource specific.                                                                                                        |
| Undelete name REQUIRED, POST `:undelete`, long-running variant                  | MUST          | API definition                                            | Proto and transport concerns outside the package.                                                                                                                                |
| Expunge: `:expunge`, separate permission                                        | MUST          | caller                                                    | `Policy.PurgeDue` tells an expunge job which resources are past their purge time.                                                                                                |
| Etag on Delete and Undelete (AIP-154)                                           | MAY           | `WithPrecondition`, `Undelete(…, check)`                  | The precondition gets the stored resource after the existence check and before the state check; its error is returned unchanged.                                                 |

A resource whose purge time has passed is treated as purged by every method even before an expunge job removes it.

## List and show_deleted

With the listing engines, the handler passes the rule as the request's server restriction:

```go
req := listing.RequestFromProto(msg)
req.Restriction = policy.Restriction(msg.GetShowDeleted()) // deleteTime == null, or nil with show_deleted
page, err := books.List(ctx, req)
```

`listing` ANDs the restriction onto the client's filter after the default equalities (a blank filter included), validates it under the
schema's filter policy, and every engine (`backend/memory`, `backend/mongo` and the `backend/sqlbase` engine of `backend/postgres`,
`backend/mariadb` and `backend/clickhouse`) applies it. The canonical filter bound to the page token includes it, so a token of one
`show_deleted` value is refused with the other (AIP-158, a field error on `pageToken`). The field (`WithFilterField`, default `deleteTime`)
must be declared nullable in the list schema (`Presence` or `Optional`); on a required field the null test is refused with
`listing.ErrInvalidRestriction` (INTERNAL: a server configuration fault, not the client's). `Example_softDeleteRestriction` in
[integration/crossbackend](../integration/crossbackend/softdelete_list_example_test.go) pages a `backend/memory` list with `show_deleted`
toggled.

`Restrict(node, showDeleted)` is the same rule for code that holds the AST itself (its own translator or evaluator, a store adapter): unless
`showDeleted` it returns `&BinaryOpNode{OpAnd, node, deleteTime == null}` (the predicate alone for a nil node) and does not modify `node`; the
caller then binds its page tokens to the restricted filter. `Visible` decides per item for a list read from memory without the listing
engines.

For a finite restore window, also set `req.Cutoff = policy.Cutoff(at)`. The predicate is
`deleteTime == null || purgeTime > at`. A tombstone disappears at its purge instant, even before physical deletion.
Declare both fields as timestamps. The delete field uses `WithFilterField`; the purge field uses the lower camel case
of `WithPurgeTimeField` (`expire_time` becomes `expireTime`). A tombstone without a purge time fails this predicate.
Omit the cutoff for indefinite retention. Capture one instant for List and Count and refresh it on the next request.
SQL and memory support the cutoff. MongoDB refuses it with `listing.ErrInvalidRestriction`.

## AIP-165 conformance

| Rule                                                  | Level  | Implemented by                                        | Behavior                                                                                                |
|-------------------------------------------------------|--------|-------------------------------------------------------|---------------------------------------------------------------------------------------------------------|
| `filter` is required                                  | MUST   | `Purger.Purge`                                        | Empty or blank: field error on `filter` (INVALID_ARGUMENT).                                             |
| `filter` follows AIP-160 List semantics               | MUST   | `Purger.Purge` with the `Parser` and `*filter.Policy` | Parsed by the same parser and validated by `filter.Validate` under the same policy a List uses.         |
| `filter` may be `*` (all resources)                   | MAY    | `WithWildcard`                                        | Off by default (field error); on, the store gets a nil filter.                                          |
| `parent` is required unless the resource is top-level | SHOULD | `WithParentRequired`                                  | Blank: field error on `parent`, checked before `filter`.                                                |
| `force` false: count and sample, nothing deleted      | MUST   | `Purger.Purge`                                        | `Store.Preview`; `purgeCount` and `purgeSample`.                                                        |
| `force` true: delete                                  | MUST   | `Purger.Purge`                                        | `Store.Purge`; `purgeSample` empty.                                                                     |
| `purge_count` may be an estimate                      | MAY    | `Store`                                               | The store's count, clamped to `math.MaxInt32`.                                                          |
| `purge_sample` only without `force`, about 100 names  | SHOULD | `WithSampleSize`                                      | 100 by default; a longer store sample is cut.                                                           |
| Response is a long-running operation                  | MUST   | caller                                                | The handler wraps `Purge` in an AIP-151 operation.                                                      |
| Caution: irreversible                                 | —      | `WithMaxPurgeCount`                                   | A forced purge counts first and refuses more than 10000 resources by default (field error on `filter`). |

## Errors

| Error                                         | Code             | When                                                                                                  |
|-----------------------------------------------|------------------|-------------------------------------------------------------------------------------------------------|
| `ErrNotFound` (`coreerrs.ErrNotFound`)        | NOT_FOUND        | Missing or purged resource; Get of a deleted one when hidden.                                         |
| `ErrAlreadyDeleted` (`ErrNotFound`)           | NOT_FOUND        | Delete of a soft-deleted resource without allow missing.                                              |
| `ErrNotDeleted` (`coreerrs.ErrAlreadyExists`) | ALREADY_EXISTS   | Undelete of a resource that is not deleted.                                                           |
| `coreerrs.FieldError` on `parent`/`filter`    | INVALID_ARGUMENT | Invalid Purge request; causes `ErrFilterTooLong`, `ErrTooManyResources`, parser and validator errors. |
| `ErrUnsupportedResource`                      | INTERNAL         | Resource type without valid soft-delete fields.                                                       |
| `ErrInvalidStoreResult`                       | INTERNAL         | Negative store count.                                                                                 |
| `ErrInvalidPurger`                            | INTERNAL         | `NewPurger` without a policy, parser or store.                                                        |
