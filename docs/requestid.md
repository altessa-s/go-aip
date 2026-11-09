# Request identification

[requestid](../requestid/) implements [AIP-155](https://google.aip.dev/155): it validates the optional `request_id` of a request and
deduplicates requests by it, so that a retried call runs once and every retry receives the first response. It works on `proto.Message`
values only and has no gRPC dependency; the storage is the `Store` interface, with an in-memory reference implementation here and database
adapters in go-atlas, all held to the [`requestidtest`](../requestid/requestidtest/) conformance suite.

## Transport wiring

A handler wraps its business logic with `requestid.Do(ctx, guard, scope, req, handler)`. The `Scope` names the RPC method and the
authenticated principal or tenant, so that IDs of different callers and methods never collide; go-atlas builds it from the gRPC method and
the authentication context. The errors carry go-atlas-core canonical classes, so the canonical `errstatus` rules map them to status codes
without knowing this package.

## Conformance

| Rule                                                                                 | Level      | Implemented by                             | Behavior                                                                                                                                                                                                                                                          |
|--------------------------------------------------------------------------------------|------------|--------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| AIP-155: providing a request ID must guarantee idempotency                           | MUST       | `Do`, `Store.Reserve`                      | The first request reserves the ID atomically and runs the handler; duplicates, concurrent ones included, never run it while the reservation or the stored outcome is live. A reservation expires after `WithLeaseTTL`, which must exceed the longest handler run. |
| AIP-155: a duplicate should return the previous successful response                  | SHOULD     | `Do`                                       | The serialized response is stored with the fingerprint of the request and decoded into a new message for every duplicate.                                                                                                                                         |
| AIP-155: any reasonable timeframe for honoring request IDs                           | MAY        | `WithRetention`                            | 24 hours by default; the entry then expires and the ID is free.                                                                                                                                                                                                   |
| AIP-155: `request_id` on the request message, never on resources                     | MUST       | `Do`, `WithField`                          | Read from the request message by its declared name (`request_id` by default); a request without a singular string field of that name is `ErrNoRequestIDField`.                                                                                                    |
| AIP-155: request IDs should be optional                                              | SHOULD     | `Do`                                       | An empty ID runs the handler without deduplication and without consulting the store.                                                                                                                                                                              |
| AIP-155: request IDs should be able to be UUIDs, may be UUIDs only                   | SHOULD/MAY | `WithFormat`, `Canonical`                  | `FormatUUID` (default: RFC 9562, any version) and `FormatUUID4` (version 4, RFC variant) accept UUIDs only; `FormatASCII` accepts 1–36 printable ASCII characters. Nil and Max UUIDs are rejected.                                                                |
| AIP-155: the format restrictions must be documented                                  | MUST       | README format table, `ErrInvalidRequestID` | The invalid-ID field violation states the restriction (never the value) on `requestId`.                                                                                                                                                                           |
| AIP-155 example: restricted to 36 ASCII characters                                   | —          | `MaxLength`                                | Checked first, in every format.                                                                                                                                                                                                                                   |
| AIP-155/AIP-202: UUID request IDs annotated `(google.api.field_info).format = UUID4` | MUST       | API schema                                 | A rule for the proto author; the guard does not read the annotation (the format is a guard option).                                                                                                                                                               |
| AIP-202: a UUID4 may be normalized to lowercase; no primitive text comparison        | MAY/MUST   | `Canonical`                                | UUIDs are compared by their lowercase canonical form: `F47AC10B-…` and `f47ac10b-…` are one request.                                                                                                                                                              |
| AIP-155: stale success response                                                      | MAY        | `ErrResponseNotRetained`                   | A response above `WithMaxResponseSize` is not stored; its duplicates fail with `ErrResponseNotRetained` (FAILED_PRECONDITION) without running the handler, and the service may return the current resource instead.                                               |
| Same ID, different parameters (open in AIP-155)                                      | —          | `Do`                                       | `ErrRequestIDReused`, matching `coreerrs.ErrAlreadyExists` (ALREADY_EXISTS): not retryable, and the ID names a request that exists.                                                                                                                               |
| Duplicate while the first request runs (open in AIP-155)                             | —          | `WithWaitTimeout`, `WithPollInterval`      | The duplicate waits (5s by default) and then receives the stored response, or fails with `ErrInProgress`, matching `coreerrs.ErrBusy` (ABORTED, retry later with the same ID).                                                                                    |
| Failed first request (open in AIP-155)                                               | —          | `WithCacheError`                           | Not stored by default: the reservation is released so a retry runs again. `WithCacheError` stores selected failures as `google.rpc.Status`, replayed as `*ReplayedError`.                                                                                         |
| Scoping by caller                                                                    | —          | `Scope`                                    | The store key is the SHA-256 of the length-prefixed method, principal and canonical ID: no cross-caller collision and no caller data in the store.                                                                                                                |

## Deliberate choices

* **Any UUID version by default.** AIP-202 names the format `UUID4` but its own normalization example has version nibble 0, and v7 UUIDs are
  common client IDs; `FormatUUID4` is available to enforce random UUIDs.
* **Fail closed.** A store failure before the handler runs is `ErrStore` (UNAVAILABLE) and the handler does not run; idempotency is never
  traded for availability. After the handler ran, its result is returned even if storing it fails, and the store error goes to
  `WithStoreErrorHandler`.
* **Live entries are never evicted.** The `MemoryStore` refuses a new reservation when full (`ErrStoreFull`) instead of dropping a live
  entry, which would silently break idempotency within the retention.
* **Deterministic, not canonical, fingerprints.** The fingerprint uses deterministic protobuf encoding, stable within a binary and runtime;
  a service upgrade that changes the encoding of a request type at most turns a pending retry into `ErrRequestIDReused` for the remainder of
  the retention.

## Limits

| Limit                         | Default  | Option                |
|-------------------------------|----------|-----------------------|
| request ID length             | 36 bytes | fixed (`MaxLength`)   |
| stored response size          | 1 MiB    | `WithMaxResponseSize` |
| live entries of `MemoryStore` | 100 000  | `WithMaxEntries`      |
| wait for a running request    | 5s       | `WithWaitTimeout`     |
| reservation lease             | 1m       | `WithLeaseTTL`        |
| retention                     | 24h      | `WithRetention`       |
