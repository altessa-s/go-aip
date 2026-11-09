# softdelete

```go
import "github.com/altessa-s/go-aip/softdelete"
```

Package `softdelete` implements [AIP-164](https://google.aip.dev/164) soft delete and undelete and [AIP-165](https://google.aip.dev/165)
criteria-based delete (Purge) for protobuf resources. It has no gRPC or storage dependency: the handler loads and saves the resource, the
package decides what the stored resource looks like and which error the request gets. The [guide](../docs/softdelete.md) maps every AIP rule
to the function that implements it.

## Soft delete (AIP-164)

A `Policy` reads two `google.protobuf.Timestamp` fields of a resource through its descriptor, by their AIP-148 names:

| Field         | Option                | Meaning                                                                        |
|---------------|-----------------------|--------------------------------------------------------------------------------|
| `delete_time` | `WithDeleteTimeField` | Set: the resource is soft-deleted. Required.                                   |
| `purge_time`  | `WithPurgeTimeField`  | When the soft-deleted resource is purged. Optional; `expire_time` if named so. |

Both must be singular `google.protobuf.Timestamp` fields marked `OUTPUT_ONLY` (AIP-148); `WithOutputOnlyNotRequired(true)` waives the field
behavior. A field is found by its proto name or its JSON name. `Policy.Validate(md)` checks a resource type at startup; every other method returns
the same `ErrUnsupportedResource` for an unsupported type. Generated and dynamic (`dynamicpb`) messages are both supported.

```go
policy := softdelete.New(softdelete.WithRetention(7 * 24 * time.Hour))

// DeleteBook
stored, err := repo.Get(ctx, req.GetName())            // nil when missing
changed, err := policy.Delete(stored,
	softdelete.WithAllowMissing(req.GetAllowMissing()),
	softdelete.WithPrecondition(etagCheck(req.GetEtag())))
if err != nil { return nil, err }
if changed { err = repo.Save(ctx, stored) }

// UndeleteBook
err = policy.Undelete(stored, etagCheck(req.GetEtag()))
if err != nil { return nil, err }
err = repo.Save(ctx, stored)
return stored, err

// GetBook
if err := policy.CheckGet(stored); err != nil { return nil, err }

// ListBooks
lreq := listing.RequestFromProto(req)
lreq.Restriction = policy.Restriction(req.GetShowDeleted()) // any listing engine
q.Filter = policy.Restrict(q.Filter, req.GetShowDeleted())  // an AST in hand
ok, err := policy.Visible(item, req.GetShowDeleted())       // an item in memory
```

| Method                     | Behavior                                                                                                                            |
|----------------------------|-------------------------------------------------------------------------------------------------------------------------------------|
| `Delete(current, opts...)` | Sets `delete_time` to now and `purge_time` to now + retention (cleared when the retention is 0); reports whether `current` changed. |
| `Undelete(current, check)` | Clears `delete_time` and `purge_time`.                                                                                              |
| `CheckGet(current)`        | nil: Get returns the resource, soft-deleted or not. `WithHideDeletedOnGet(true)` makes a soft-deleted resource `ErrNotFound`.       |
| `Visible(current, show)`   | Whether a List returns an item: a soft-deleted one only with `show`.                                                                |
| `Restriction(show)`        | The listing server restriction: `deleteTime == null`, or nil with `show`; see [show_deleted](#show_deleted-in-list).                |
| `Restrict(node, show)`     | The List filter AST: `node && deleteTime == null` unless `show`, for code holding the AST itself.                                   |
| `IsDeleted(current)`       | Whether `delete_time` is set.                                                                                                       |
| `PurgeDue(current)`        | Whether the resource is soft-deleted and its purge time has passed: an expunge job may remove it.                                   |

A nil `current` is a missing resource. A resource whose purge time has passed is treated as missing (purged) by every method, even before an
expunge job removes it; `Visible` hides it with `show` too.

### Order of the checks

| Method     | 1                                | 2                                                             | 3                            | 4     |
|------------|----------------------------------|---------------------------------------------------------------|------------------------------|-------|
| `Delete`   | missing or purged: `ErrNotFound` | soft-deleted: `ErrAlreadyDeleted` (with allow missing: no-op) | precondition                 | stamp |
| `Undelete` | missing or purged: `ErrNotFound` | precondition                                                  | not deleted: `ErrNotDeleted` | clear |

With `WithAllowMissing(true)` a missing, purged or soft-deleted resource is a no-op (`false, nil`) and the precondition is not checked: for
Delete such a resource does not exist. The precondition (`Precondition`, typically an AIP-154 etag comparison) gets the stored resource; its error
is returned unchanged (an etag package returns `coreerrs.ErrConflict`, ABORTED).

### show_deleted in List

`Restriction(show)` returns the AIP-164 rule as a server restriction for the listing engines: set it on `listing.Request.Restriction` and the schema
ANDs it onto the client's AIP-160 filter after the default equalities (a blank filter included); every list engine of `backend/` applies it, and the
page token binds it, so a token of one `show_deleted` value is refused with the other (AIP-158). The field is `WithFilterField` (default `deleteTime`,
the JSON name). Declare it in the list schema as a nullable timestamp (`listing.Field{Name: "deleteTime", Kind: filter.FieldKindTimestamp, Presence:
true}`, or `Optional`); a required field makes the null test a refused restriction (`listing.ErrInvalidRestriction`, INTERNAL). See
`Example_softDeleteRestriction` in [integration/crossbackend](../integration/crossbackend/softdelete_list_example_test.go).

`Restrict(node, show)` composes the same rule with an AST the caller holds (its own translator or evaluator, a store adapter), never on
strings: it returns a new root `node && deleteTime == null` (or the predicate alone for a nil node) and leaves `node` untouched; bind the page
tokens to the restricted filter. With the memory engine the items may also be filtered with `Visible` before they are passed. Without a
`show_deleted` field, `listing.WithDefaultEqualities` on a boolean `deleted` field is the alternative the listing package documents.

The restriction tests only `delete_time`, so with `show` a resource past its purge time that the expunge job has not removed yet still
appears: a clock-dependent predicate would change between pages and invalidate their tokens. `Visible` hides such a resource in memory.

## Purge (AIP-165)

```go
purger, err := softdelete.NewPurger(filterPolicy, filterParser, store,
	softdelete.WithParentRequired(true), softdelete.WithMaxPurgeCount(1000))

resp, err := purger.Purge(ctx, softdelete.RequestFromProto(req))
// resp.PurgeCount, resp.PurgeSample → PurgeBooksResponse
```

`Purge` validates, in order, `parent` (non-blank under `WithParentRequired`) and `filter` (required and non-blank, within
`WithMaxFilterLength`, `"*"` only under `WithWildcard`, parsed by the `Parser` and checked with `filter.Validate` under the `*filter.Policy`:
allowed fields, kinds, depth and operation limits), and returns the first violation as a `coreerrs.FieldError` on `parent` or `filter`
(INVALID_ARGUMENT). The reason of a parse or validation failure is generic; the parser's diagnostic, which may echo literals, stays in the chain.

| `force` | Store calls                                                                   | Response                                            |
|---------|-------------------------------------------------------------------------------|-----------------------------------------------------|
| false   | `Preview(parent, f, sampleSize)`                                              | `PurgeCount`, `PurgeSample` cut to `WithSampleSize` |
| true    | `Preview(parent, f, 0)` when `WithMaxPurgeCount` > 0, then `Purge(parent, f)` | `PurgeCount`; more than the limit is a field error  |

The `Store` does the counting, sampling and deletion; the filter it receives is validated, and nil is the wildcard. Counts are clamped to
`math.MaxInt32` (`purge_count` is `int32`); a negative count is `ErrInvalidStoreResult`. The pre-count and the deletion are two calls: a store
that must enforce the limit atomically does it inside `Purge`. Store errors are returned unchanged; wrap the operation in a long-running
operation (AIP-151) in the handler.

## Options

| Option                      | Type   | Default       | Meaning                                                                |
|-----------------------------|--------|---------------|------------------------------------------------------------------------|
| `WithDeleteTimeField`       | Policy | `delete_time` | Delete time field (proto or JSON name).                                |
| `WithPurgeTimeField`        | Policy | `purge_time`  | Purge time field; absent from a resource means no purge time.          |
| `WithFilterField`           | Policy | `deleteTime`  | Filter field `Restriction` and `Restrict` test.                        |
| `WithRetention`             | Policy | 30 days       | Delete to purge; 0 keeps forever (no purge time), negative = default.  |
| `WithClock`                 | Policy | system clock  | Time source; nil keeps the system clock.                               |
| `WithHideDeletedOnGet`      | Policy | false         | Get of a soft-deleted resource is NOT_FOUND.                           |
| `WithOutputOnlyNotRequired` | Policy | false         | Accept fields without OUTPUT_ONLY.                                     |
| `WithAllowMissing`          | Delete | false         | Missing or soft-deleted resource is a no-op.                           |
| `WithPrecondition`          | Delete | none          | Check of the stored resource before the change.                        |
| `WithSampleSize`            | Purger | 100           | Names in `PurgeSample`; 0 none, negative = default.                    |
| `WithMaxPurgeCount`         | Purger | 10000         | Largest forced purge; 0 unlimited, negative = default.                 |
| `WithMaxFilterLength`       | Purger | 4096          | Filter bytes, checked before parsing; 0 unlimited, negative = default. |
| `WithParentRequired`        | Purger | false         | `parent` is required.                                                  |
| `WithWildcard`              | Purger | false         | Accept the filter `*`.                                                 |

## Errors

| Error                    | Matches                               | Code             | When                                                                                                                    |
|--------------------------|---------------------------------------|------------------|-------------------------------------------------------------------------------------------------------------------------|
| `ErrNotFound`            | `coreerrs.ErrNotFound`                | NOT_FOUND        | Missing (nil) or purged resource; Get of a deleted one when hidden.                                                     |
| `ErrAlreadyDeleted`      | `ErrNotFound`, `coreerrs.ErrNotFound` | NOT_FOUND        | Delete of a soft-deleted resource without allow missing.                                                                |
| `ErrNotDeleted`          | `coreerrs.ErrAlreadyExists`           | ALREADY_EXISTS   | Undelete of a resource that is not deleted.                                                                             |
| `coreerrs.FieldError`    | `coreerrs.ErrInvalidArgument`         | INVALID_ARGUMENT | Invalid `parent` or `filter` of a Purge; cause `ErrFilterTooLong`, `ErrTooManyResources` or the parser/validator error. |
| `ErrUnsupportedResource` | —                                     | INTERNAL         | The resource type lacks valid soft-delete fields (server configuration).                                                |
| `ErrInvalidPurger`       | —                                     | INTERNAL         | `NewPurger` without a policy, parser or store.                                                                          |
| `ErrInvalidStoreResult`  | —                                     | INTERNAL         | The store returned a negative count.                                                                                    |

Precondition and store errors are returned unchanged.

## Limits

| Limit                 | Default | Checked                                                                |
|-----------------------|---------|------------------------------------------------------------------------|
| `WithMaxFilterLength` | 4096 B  | Before parsing the purge filter.                                       |
| Filter depth and size | policy  | `filter.Validate` (`WithMaxDepth`, `WithMaxOperations` of the policy). |
| `WithMaxPurgeCount`   | 10000   | Before a forced purge deletes.                                         |
| `WithSampleSize`      | 100     | Passed to the store; longer samples are cut.                           |

The descriptor cache of a `Policy` holds one entry per message descriptor it has seen.
