# Batch methods

[batch](../batch/) implements the request rules and the execution semantics of the AIP batch methods: Batch Get (AIP-231), Batch Create
(AIP-233), Batch Update (AIP-234) and Batch Delete (AIP-235). It covers what a server checks and how it runs a batch; the naming, HTTP and
message-shape rules (RPC names, `:batchGet` URIs, response messages) are API design rules that `api-linter` checks on the proto files.

## Atomic or partial success

| Method             | Synchronous response | Long-running operation                               | Helper                               |
|--------------------|----------------------|------------------------------------------------------|--------------------------------------|
| Batch Get (231)    | atomic only          | — (AIP-231 defines no operation form)                | `Get`, `GetKeys`                     |
| Batch Create (233) | atomic only          | atomic or partial success (`return_partial_success`) | `Atomic`; `Partial` for an operation |
| Batch Update (234) | atomic only          | atomic or partial success (`return_partial_success`) | `Atomic`; `Partial` for an operation |
| Batch Delete (235) | atomic only          | atomic or partial success (`return_partial_success`) | `Atomic`; `Partial` for an operation |

The package does not decide which mode a method uses: the handler picks the helper. `Partial` documents that it serves only a long-running
operation that supports partial success.

## AIP-231: Batch Get

| Rule                                                                           | Implemented by                                            | Behavior                                                                                                                                     |
|--------------------------------------------------------------------------------|-----------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------|
| MUST be atomic: fail for all resources or succeed for all (no partial success) | `Get`, `GetKeys`                                          | The first resource `fetch` does not return fails the batch with an `*ItemError` on `names[i]` (or `requests[i]`); no resources are returned. |
| A missing resource fails the request                                           | `Get`, `GetKeys`, `ErrNotFound`                           | `ErrNotFound` matches `coreerrs.ErrNotFound`: NOT_FOUND, as the singular Get returns.                                                        |
| Get the resources at a consistent time point                                   | `FetchFunc` (contract)                                    | `fetch` receives every distinct key in one call, to read them in one transaction or query.                                                   |
| MUST: the order of the response is the order of the names in the request       | `Get`, `GetKeys`                                          | Results are placed by request index; a repeated name returns its resource at each of its positions.                                          |
| MUST fail if `parent` is set and the parent of a resource does not match       | `CheckParent`, `ValidateRequest`                          | `ErrParentMismatch` on `names[i]` (or `requests[i].name`). A `-` segment of `parent` matches any segment (a batch spanning parents).         |
| SHOULD error with INVALID_ARGUMENT when no names are provided                  | `CheckSize`, `ValidateRequest`                            | `ErrEmpty` in a `coreerrs.FieldError` on `names` / `requests`; `WithAllowEmpty(true)` opts out.                                              |
| The comment SHOULD document the maximum number of names (examples: 1000)       | `CheckSize`, `WithMaxItems`                               | `ErrTooManyItems` above 1000 by default, checked before any other work; 0 is unlimited.                                                      |
| Nested form: a nested `parent` must be empty or match                          | `CheckParent`, `ValidateRequest`                          | An empty nested parent inherits the batch parent.                                                                                            |
| Nested form: a hoisted field set at both levels MUST match                     | `CheckHoisted`, `ValidateRequest`                         | `ErrHoistedMismatch` on `requests[i].<field>`.                                                                                               |
| SHOULD NOT support pagination                                                  | —                                                         | No pagination helper; the request defines the scope of the response.                                                                         |
| Duplicates                                                                     | `Get`, `GetKeys`; `ValidateRequest` with `WithUniqueKeys` | AIP-231 is silent: duplicates are allowed, each distinct name fetched once. `WithUniqueKeys(true)` rejects them with `ErrDuplicate`.         |

## AIP-233: Batch Create

| Rule                                                                                                   | Implemented by                     | Behavior                                                                                                                                      |
|--------------------------------------------------------------------------------------------------------|------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------|
| Synchronous batch create MUST be atomic                                                                | `Atomic`                           | All items run in one `TxRunner` transaction; the first failure rolls it back and is returned as an `*ItemError` on `requests[i]`, no results. |
| Asynchronous batch create MAY support partial success                                                  | `Partial`                          | Failed items are collected in `Result.Failed`; the others go on.                                                                              |
| Partial success: `map<int32, google.rpc.Status> failed_requests`, key = index in `requests`            | `Result.Failed`                    | `map[int]error` keyed by request index.                                                                                                       |
| Each failed_requests value MUST mirror the error of the singular Create                                | `Result.Failed`                    | The error `do` returned, unchanged (not wrapped in an `*ItemError`).                                                                          |
| Transient errors that server retries could fix MUST NOT be reported in failed_requests                 | `WithFatal`, `DefaultFatal`        | A fatal error (context errors, `coreerrs.ErrUnavailable` by default) stops the batch and is returned instead.                                 |
| When all requests fail, `Operation.error` MUST be ABORTED with the prescribed message                  | `ErrAllFailed`, `AllFailedMessage` | `Partial` returns `ErrAllFailed`; `AllFailedMessage(metadataType)` builds the message.                                                        |
| MUST fail if `parent` is set and a nested `parent` does not match; a nested `parent` can be omitted    | `CheckParent`, `ValidateRequest`   | `ErrParentMismatch` on `requests[i].parent`; an empty nested parent passes.                                                                   |
| A hoisted field set at both levels MUST match; unique fields (customer-provided ids) cannot be hoisted | `CheckHoisted`, `ValidateRequest`  | Every same-named, same-typed singular field is compared; a unique id is never declared on the batch request, so it is never compared.         |
| The `requests` field SHOULD be REQUIRED                                                                | `CheckSize`                        | An empty batch is `ErrEmpty` by default.                                                                                                      |
| The comment SHOULD document the maximum number of requests (examples: 1000)                            | `CheckSize`, `WithMaxItems`        | 1000 by default, checked first.                                                                                                               |
| Response order                                                                                         | `Atomic`, `Partial`                | Results are in request order, whatever the order of completion under `WithConcurrency`.                                                       |

## AIP-234: Batch Update

| Rule                                                                                          | Implemented by                             | Behavior                                                                                                                                                  |
|-----------------------------------------------------------------------------------------------|--------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------|
| Synchronous batch update MUST be atomic                                                       | `Atomic`                                   | As for Batch Create.                                                                                                                                      |
| Asynchronous batch update MAY support partial success; failed_requests, ABORTED when all fail | `Partial`, `Result.Failed`, `ErrAllFailed` | As for Batch Create.                                                                                                                                      |
| MUST fail if `parent` is set and the parent in the name of a resource does not match          | `CheckParent`, `ValidateRequest`           | `ValidateRequest` takes the parent from the resource's `name` (`requests[i].book.name`); `ErrParentMismatch` on that path.                                |
| A hoisted field set at both levels MUST match (`update_mask` is a good candidate)             | `CheckHoisted`, `ValidateRequest`          | A hoisted `update_mask` is compared with each nested one by value: `ErrHoistedMismatch` on `requests[i].updateMask`.                                      |
| Field errors of a nested request name its path                                                | `Each`, `ItemError`                        | A `coreerrs.FieldError` on `book.title` or `updateMask.paths[0]` (from `fieldmask`) becomes `requests[i].book.title` / `requests[i].updateMask.paths[0]`. |
| Duplicates                                                                                    | `WithUniqueKeys`, `CheckUnique`            | AIP-234 is silent; two updates of one resource are ambiguous, so reject them with `ErrDuplicate` (opt-in).                                                |

## AIP-235: Batch Delete

| Rule                                                                                          | Implemented by                             | Behavior                                                                                                                                   |
|-----------------------------------------------------------------------------------------------|--------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------|
| Synchronous batch delete MUST be atomic                                                       | `Atomic`                                   | As for Batch Create; for soft delete, `Atomic` returns the updated resources in request order.                                             |
| Asynchronous batch delete MAY support partial success; failed_requests, ABORTED when all fail | `Partial`, `Result.Failed`, `ErrAllFailed` | As for Batch Create.                                                                                                                       |
| MUST fail if `parent` is set and the parent of a resource does not match                      | `CheckParent`, `ValidateRequest`           | `ErrParentMismatch` on `names[i]` or `requests[i].name`.                                                                                   |
| A hoisted field set at both levels MUST match; unique fields (`etag`) cannot be hoisted       | `CheckHoisted`, `ValidateRequest`          | Only fields declared on both messages are compared, so a per-item `etag` is never compared.                                                |
| Filter-based matching MUST NOT be supported                                                   | —                                          | No filter helper: the items are named explicitly.                                                                                          |
| Duplicates                                                                                    | `WithUniqueKeys`, `CheckUnique`            | AIP-235 is silent; in an atomic batch the second delete of a resource would fail with NOT_FOUND, so reject duplicates with `ErrDuplicate`. |

## Error paths (AIP-193)

Every failure names the offending request field with the JSON field names of the request, as `fieldmask` does for `updateMask.paths[i]`:

| Failure                                | Field violation                                                   |
|----------------------------------------|-------------------------------------------------------------------|
| Too many or no items                   | `requests` / `names`                                              |
| Parent of a resource name              | `names[i]`                                                        |
| Parent of a nested request             | `requests[i].parent`, `requests[i].name`, `requests[i].book.name` |
| Hoisted field                          | `requests[i].updateMask`                                          |
| Duplicate                              | `names[j]`, `requests[j].name` (the later item)                   |
| Field error of a nested request        | `requests[i].<nested path>`                                       |
| Field-less INVALID_ARGUMENT of an item | `requests[i]`                                                     |

The field violation is a `coreerrs.FieldError` reachable with `errors.AsType`; the `*ItemError` around it keeps the index and the original
error. A failure that is not INVALID_ARGUMENT (NOT_FOUND, ALREADY_EXISTS, ABORTED) keeps its own classification, so an atomic batch fails with
the code its first failing item would return from the singular method. A FieldError or an INVALID_ARGUMENT sentinel behind an
`apierror.Classify` cause (a stored record that does not decode) is not in the chain of the item error and yields no field violation, so the
batch fails with the category, as the singular method does.

## Deliberate choices

* An empty batch is an error for every method: AIP-231 says so for Batch Get, and AIP-233 to AIP-235 make the repeated field REQUIRED.
* A nested request without a parent inherits the batch parent, for every method (AIP-233 states it for Create; for Get, Update and Delete the
  parent comes from a resource name the client must send).
* `ValidateRequest` does not check the parent of a resource name of fewer than three segments: a top-level resource or a bare id has no parent
  to compare.
* `Partial` with a fatal error returns the `Result` so far: the items that succeeded were applied and cannot be rolled back.
