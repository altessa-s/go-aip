# batch

```go
import "github.com/altessa-s/go-aip/batch"
```

Package `batch` provides the request checks and execution helpers of the AIP batch methods: Batch Get ([AIP-231](https://google.aip.dev/231)),
Batch Create ([AIP-233](https://google.aip.dev/233)), Batch Update ([AIP-234](https://google.aip.dev/234)) and Batch Delete
([AIP-235](https://google.aip.dev/235)). It works on `proto.Message` values and plain Go values, with no gRPC dependency. The
[guide](../docs/batch.md) maps every rule of the four AIPs to the function that implements it.

* The request checks bound the batch size first, then match the parent and the other hoisted fields of every item with the batch request, reject
  duplicate keys where asked to and run the validation of the singular method on each nested request.
* `Atomic` runs a batch all or nothing inside a transaction, as every synchronous batch method must; `Get` reads a Batch Get in one call and fails
  it with NOT_FOUND when a resource is missing; `Partial` runs the partial-success batches that AIP-233 to AIP-235 allow for long-running
  operations, with bounded concurrency.
* The results are always in request order, whatever the order of completion.
* Every item failure is an `*ItemError` on its request path (`requests[2]`, `names[0]`); a field error of a nested request is reported under that
  path (`requests[2].book.title`) as a `coreerrs.FieldError`, as `fieldmask` reports `updateMask.paths[i]`. A field error behind the hidden cause
  of an `apierror.Classify` error is not in the chain and is never re-rooted.

## Usage

```go
func (s *Server) BatchUpdateBooks(ctx context.Context, req *pb.BatchUpdateBooksRequest) (*pb.BatchUpdateBooksResponse, error) {
	if err := batch.ValidateRequest(req, batch.WithUniqueKeys(true)); err != nil {
		return nil, err // INVALID_ARGUMENT on "requests", "requests[i].book.name", ...
	}

	if err := batch.Each(batch.FieldRequests, req.GetRequests(), func(_ int, r *pb.UpdateBookRequest) error {
		return s.validateUpdate(r) // a field error on "book.title" becomes "requests[i].book.title"
	}); err != nil {
		return nil, err
	}

	books, err := batch.Atomic(ctx, batch.FieldRequests, req.GetRequests(), s.store.InTx,
		func(ctx context.Context, _ int, r *pb.UpdateBookRequest) (*pb.Book, error) {
			return s.update(ctx, r) // writes through ctx, which carries the transaction
		})
	if err != nil {
		return nil, err // the first failure, on "requests[i]"; nothing was written
	}

	return &pb.BatchUpdateBooksResponse{Books: books}, nil
}

func (s *Server) BatchGetBooks(ctx context.Context, req *pb.BatchGetBooksRequest) (*pb.BatchGetBooksResponse, error) {
	if err := batch.ValidateRequest(req); err != nil {
		return nil, err
	}

	books, err := batch.GetKeys(ctx, batch.FieldNames, req.GetNames(), s.store.GetMany)
	if err != nil {
		return nil, err // NOT_FOUND on "names[i]" for the first missing book
	}

	return &pb.BatchGetBooksResponse{Books: books}, nil
}
```

A service that addresses resources by id rather than by resource name uses the generic checks with its own accessors:

```go
err := batch.CheckSize(batch.FieldRequests, len(reqs))
err = batch.CheckParent(batch.FieldRequests, reqs, req.GetParent(),
	func(r *pb.CreateBotRequest) (string, string) { return r.GetParent(), "parent" })
err = batch.CheckUnique(batch.FieldRequests, reqs,
	func(r *pb.CreateBotRequest) (string, string) { return r.GetBotId(), "botId" })
```

## Request checks

Run them before any work, in this order; each returns the first failure.

| Function          | Checks                                                                                                                                              | Failure                                                                                       |
|-------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------|
| `CheckSize`       | At most `WithMaxItems` items (1000 by default, 0 unlimited); at least one unless `WithAllowEmpty(true)`                                             | `ErrTooManyItems` or `ErrEmpty` in a `coreerrs.FieldError` on the repeated field (`requests`) |
| `CheckParent`     | With a batch `parent`, the parent each item names matches it; an item without a parent inherits it; a `-` segment of the batch parent is a wildcard | `ErrParentMismatch` in an `*ItemError`, field error on `names[i]` or `requests[i].<path>`     |
| `CheckHoisted`    | A hoisted field set on both the batch and an item holds the same value                                                                              | `ErrHoistedMismatch` in an `*ItemError`, field error on `requests[i].<path>`                  |
| `CheckUnique`     | No two items share a key (resource name or id); a zero key is skipped                                                                               | `ErrDuplicate` in an `*ItemError` at the later index, its reason naming the earlier one       |
| `Each`            | The validation of the singular method on every nested request                                                                                       | The item error in an `*ItemError`; its field error re-rooted under `requests[i]`              |
| `ParentMatches`   | The parent comparison of `CheckParent`, segment by segment with `-` as a wildcard                                                                   | —                                                                                             |
| `ValidateRequest` | All of the above but `Each` on a message of the standard shape, read by its descriptor (below)                                                      | As above; `ErrNotBatchRequest` for a message without `requests` or `names`                    |

The accessors return the value of an item together with the JSON path of its field relative to the item: `""` when the item is the resource name
itself (`names`), `"parent"` for a nested Create request, `"book.name"` for a nested Update request. The path completes the error path, so a
mismatch is reported on the field that holds the wrong value.

### ValidateRequest

`ValidateRequest(req, opts...)` reads the standard fields of a batch request by its descriptor:

1. The size of the repeated message field `requests` or, without one, the repeated string field `names`.
2. The parent, when the request has a string `parent` field that is set. The parent of a nested request is its `parent` field, else the parent of
   the resource name in its `name` field, else the parent of the `name` of its first singular message field that has one (the resource of an
   Update request). The parent of a resource name is the name without its last two segments (collection and id); a name of fewer than three
   segments (a top-level resource or a bare id) has none and is not checked.
3. The hoisted fields of `requests`: every other singular field of the batch request that the nested request declares with the same name, kind
   and message type (`update_mask`, `validate_only`, ...). When both are set, the values must be equal (`protoreflect.Value.Equal`).
4. With `WithUniqueKeys(true)`, duplicate keys: the names of `names`, or the `name` (or resource `name`) of each nested request.

Error paths use the JSON field names of the message (`requests[1].updateMask`).

### Duplicates

AIP-231 to AIP-235 do not forbid duplicate items, so neither `ValidateRequest` nor the execution helpers reject them by default:

* Batch Get: `Get` and `GetKeys` fetch each distinct key once and return the resource once per occurrence, in request order.
* Batch Update and Batch Delete: turn on `WithUniqueKeys(true)` (or call `CheckUnique`): two updates of one resource are ambiguous, and in an
  atomic batch the second delete of a resource fails with NOT_FOUND.
* Batch Create: a create request has no resource name. Check the user-specified ids (AIP-133) with `CheckUnique` if the method takes them.

## Execution

| Function         | Mode                        | Order                    | On failure                                                                                                                             |
|------------------|-----------------------------|--------------------------|----------------------------------------------------------------------------------------------------------------------------------------|
| `Atomic`         | all or nothing, sequential  | results in request order | The first item error as an `*ItemError`; the transaction rolls back; no results                                                        |
| `Get`, `GetKeys` | all or nothing, one read    | results in request order | The first missing resource as an `*ItemError` wrapping `ErrNotFound` (matches `coreerrs.ErrNotFound`); a fetch error as is; no results |
| `Partial`        | partial success, concurrent | results in request order | Failed items in `Result.Failed` by index; a fatal item error stops the batch; every item failed is `ErrAllFailed`                      |

### Atomic

`Atomic(ctx, field, items, inTx, do)` calls `inTx` once and runs `do` on every item inside it, in request order. `TxRunner` is the unit of work of
a store: it commits when its function returns nil and rolls back otherwise. It may run the function again to retry a transaction; every attempt
starts afresh, so a retried batch never returns results of an earlier attempt. The items run one after the other because a transaction is rarely
safe for concurrent use, and `do` must write through the context it receives. A nil runner, or one that returns nil without running the batch,
is `ErrNoTransaction`.

### Get

`Get(ctx, field, items, keyOf, fetch)` serves the nested-request form, `GetKeys(ctx, field, keys, fetch)` the `names` form. `fetch` receives the
distinct keys in first-seen order and must read them at one consistent point in time (a read transaction or a single query, as AIP-231
requires); a key missing from its map does not exist. Batch Get is always atomic: there is no partial mode.

### Partial

`Partial(ctx, field, items, do, opts...)` runs `do` on every item and returns a `*Result`:

* `Values` holds each result at its request index (the zero value where the request did not succeed).
* `Failed` maps the index of each failed request to its error as the singular method returned it: convert each error to a `google.rpc.Status`
  for the `map<int32, google.rpc.Status> failed_requests` of the operation metadata.
* `Succeeded(i)` and `SucceededCount()` tell the requests that succeeded; a request neither succeeded nor failed was not run, or was
  interrupted, because the batch stopped.

Up to `WithConcurrency(n)` items run at once (one by default). An item error that `WithFatal` matches stops the batch: transient errors must
not be reported in `failed_requests` (AIP-233 to AIP-235), so by default context errors and `coreerrs.ErrUnavailable` are fatal. The items not
yet started are not run, the running ones see their context canceled, and `Partial` returns the error that stopped the batch as its
`*ItemError`, along with the `Result` so far. The cancellation of `ctx` stops the batch the same way and returns `ctx.Err()`. When every request
failed the error is `ErrAllFailed`: set the operation error to ABORTED with the message of `AllFailedMessage(metadataType)`.

Use `Partial` only for a method that returns a long-running operation and supports partial success (opted in with `return_partial_success`
when the method was atomic before); retry transient failures inside `do`.

## Errors

| Error                | Raised by                         | Matches                                                    | gRPC code (AIP-193)                                 |
|----------------------|-----------------------------------|------------------------------------------------------------|-----------------------------------------------------|
| `ErrTooManyItems`    | `CheckSize`, `ValidateRequest`    | `coreerrs.ErrInvalidArgument` (in a `coreerrs.FieldError`) | INVALID_ARGUMENT, violation on `requests` / `names` |
| `ErrEmpty`           | `CheckSize`, `ValidateRequest`    | `coreerrs.ErrInvalidArgument` (in a `coreerrs.FieldError`) | INVALID_ARGUMENT, violation on `requests` / `names` |
| `ErrParentMismatch`  | `CheckParent`, `ValidateRequest`  | `coreerrs.ErrInvalidArgument`, `*ItemError`                | INVALID_ARGUMENT, violation on the item's field     |
| `ErrHoistedMismatch` | `CheckHoisted`, `ValidateRequest` | `coreerrs.ErrInvalidArgument`, `*ItemError`                | INVALID_ARGUMENT, violation on the item's field     |
| `ErrDuplicate`       | `CheckUnique`, `ValidateRequest`  | `coreerrs.ErrInvalidArgument`, `*ItemError`                | INVALID_ARGUMENT, violation on the later item       |
| `ErrNotFound`        | `Get`, `GetKeys`                  | `coreerrs.ErrNotFound`, `*ItemError`                       | NOT_FOUND                                           |
| `ErrAllFailed`       | `Partial`                         | —                                                          | ABORTED, message `AllFailedMessage(metadataType)`   |
| `ErrNoTransaction`   | `Atomic`                          | —                                                          | INTERNAL (a wiring bug)                             |
| `ErrNotBatchRequest` | `ValidateRequest`                 | —                                                          | INTERNAL (a wiring bug)                             |
| `*ItemError`         | every item failure                | every error its `Err` matches                              | that of `Err`                                       |

`*ItemError{Index, Field, Err}` names the item by `Path()` (`requests[2]`). It unwraps to `Err` and, when `Err` holds a `coreerrs.FieldError`,
to a copy of it re-rooted under the item path (`book.title` becomes `requests[2].book.title`), found first by `errors.AsType`. An `Err` that
matches `coreerrs.ErrInvalidArgument` without naming a field yields a field error on the item path itself. Any other `Err` (NOT_FOUND, a
conflict) keeps its classification, so a batch fails with the code the singular method would return. `NewItemError(field, index, err)` builds
one (nil for a nil `err`).

## Options

| Option                 | Applies to                     | Default                  | Description                                                                  |
|------------------------|--------------------------------|--------------------------|------------------------------------------------------------------------------|
| `WithMaxItems(n)`      | `CheckSize`, `ValidateRequest` | `DefaultMaxItems` (1000) | Maximum items of a batch; 0 is unlimited, a negative value keeps the default |
| `WithAllowEmpty(bool)` | `CheckSize`, `ValidateRequest` | false                    | Accept a batch without items                                                 |
| `WithUniqueKeys(bool)` | `ValidateRequest`              | false                    | Reject duplicate names (see [Duplicates](#duplicates))                       |
| `WithConcurrency(n)`   | `Partial`                      | `DefaultConcurrency` (1) | Items run at once; zero or a negative value keeps the default                |
| `WithFatal(func)`      | `Partial`                      | `DefaultFatal`           | Item errors that stop the batch; nil makes every item error a failed request |

## Limits

The item count is the only client-controlled size: `CheckSize` (and `ValidateRequest`) bound it before any other work, 1000 items by default
as in the AIP examples; document the limit in the comment of the repeated field, as the AIPs ask. `CheckUnique` and `Get` allocate one map entry
per item, so they rely on that bound. `WithConcurrency` is a server setting that bounds the goroutines of `Partial`.

## API

| Symbol                                                                                                    | Kind      |
|-----------------------------------------------------------------------------------------------------------|-----------|
| `CheckSize`, `CheckParent`, `CheckHoisted`, `CheckUnique`, `Each`, `ParentMatches`                        | checks    |
| `ValidateRequest`                                                                                         | checks    |
| `Atomic`, `TxRunner`, `ItemFunc`                                                                          | execute   |
| `Get`, `GetKeys`, `FetchFunc`                                                                             | execute   |
| `Partial`, `Result`, `DefaultFatal`                                                                       | execute   |
| `ItemError`, `NewItemError`, `AllFailedMessage`, sentinel errors                                          | errors    |
| `Option`, `RunOption`, `WithMaxItems`, `WithAllowEmpty`, `WithUniqueKeys`, `WithConcurrency`, `WithFatal` | options   |
| `FieldRequests`, `FieldNames`, `FieldParent`, `DefaultMaxItems`, `DefaultConcurrency`                     | constants |

## Related packages

* [`fieldmask`](../fieldmask/): the update mask of each nested Update request, and its `updateMask.paths[i]` field errors.
* [`fieldbehavior`](../fieldbehavior/): the field behaviors of each nested Create or Update request.
