# requestid

```go
import "github.com/altessa-s/go-aip/requestid"
```

Package `requestid` implements [AIP-155](https://google.aip.dev/155) request identification. It validates the optional `request_id` of a
request message and deduplicates requests by it: a client that retries a call with the same ID after a network failure gets the response of
the first call, and the method runs once. It works on `proto.Message` values and descriptors only and has no gRPC dependency.

## Contract

* `New(store, opts...)` returns a `Guard`; one guard serves every method of a service and is safe for concurrent use.
* `Do(ctx, guard, scope, req, handler)` runs `handler` on `req` at most once per request ID within `scope`.
* `Store` is the storage contract (reserve, complete, release with expiry). `MemoryStore` is the in-memory reference implementation, for tests
  and single-node services; database adapters live in go-atlas and run the [`requestidtest`](requestidtest/) conformance suite.
* `Canonical(raw, format)` validates and canonicalizes one request ID without deduplication.

## Usage

```go
guard, err := requestid.New(store,
	requestid.WithFormat(requestid.FormatUUID4),
	requestid.WithRetention(24*time.Hour),
)
if err != nil {
	return err
}

func (s *server) CreateBook(ctx context.Context, req *pb.CreateBookRequest) (*pb.Book, error) {
	scope := requestid.Scope{Method: pb.LibraryService_CreateBook_FullMethodName, Principal: principalOf(ctx)}
	return requestid.Do(ctx, s.guard, scope, req, s.createBook)
}
```

The request message declares the field as AIP-155 shows it:

```proto
// A unique identifier for this request. Restricted to 36 ASCII characters.
// A random UUID is recommended.
// This request is only idempotent if a `request_id` is provided.
string request_id = 4 [(google.api.field_info).format = UUID4];
```

## Semantics

`Do` reads the request ID field (`request_id`, or the field of `WithField`), then:

| Situation                                               | Result                                                                                                              |
|---------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------|
| empty request ID                                        | `handler` runs; no deduplication, the store is not consulted                                                        |
| invalid request ID                                      | `*coreerrs.FieldError` on the field's JSON name (`requestId`) holding `ErrInvalidRequestID`; `handler` does not run |
| first request with the ID                               | the ID is reserved, `handler` runs; a success is stored for the retention                                           |
| duplicate, same parameters, first request completed     | the stored response, decoded into a new message; `handler` does not run                                             |
| duplicate, same parameters, first request still running | waits up to `WithWaitTimeout`, polling every `WithPollInterval`, then `ErrInProgress`                               |
| same ID, other parameters                               | `ErrRequestIDReused`, whether the first request is running or completed                                             |
| `handler` fails                                         | the reservation is released and the error returned, so a retry runs `handler` again; see `WithCacheError`           |
| `handler` panics                                        | the reservation is released and the panic propagates                                                                |
| the store fails before `handler` runs                   | `ErrStore` wrapping the store's error; `handler` does not run (fail closed)                                         |
| the store fails after `handler` ran                     | the handler's result is returned; the store error goes to `WithStoreErrorHandler`                                   |

### Request ID format

AIP-155 says request IDs should be able to be UUIDs and may allow UUIDs to be the only valid format, and that the format must be documented.
`WithFormat` selects it:

| Format                 | Accepted                                                                                           | Canonical form    |
|------------------------|----------------------------------------------------------------------------------------------------|-------------------|
| `FormatUUID` (default) | an RFC 9562 UUID of any version and variant in the 8-4-4-4-12 hexadecimal form, either letter case | lowercase         |
| `FormatUUID4`          | the same, restricted to version 4: version nibble `4`, variant bits `10` (`8`, `9`, `a`, `b`)      | lowercase         |
| `FormatASCII`          | 1 to 36 printable ASCII characters (`!` to `~`), for services whose IDs are not UUIDs              | unchanged (exact) |

The Nil and Max UUIDs are rejected in both UUID formats: they are not unique. Braced, URN and unhyphenated forms are rejected. The UUID
formats compare IDs by their canonical lowercase form, so `F47AC10B-…` and `f47ac10b-…` are one ID, as AIP-202 requires for `UUID4` fields
("equivalence comparison must not be done via primitive text comparison").

`FormatUUID` does not check the version because AIP-202's own `UUID4` example (`F47AC10B-58CC-0372-8567-0E02B2C3D479`) has version nibble 0,
and clients commonly send v7 UUIDs; choose `FormatUUID4` to enforce random UUIDs. Every format is limited to `MaxLength` (36) bytes, checked
first.

### Fingerprint

A duplicate is a request with the same ID *and the same parameters*. The fingerprint is the SHA-256 of the request's full message name and
its deterministic protobuf encoding with the request ID field cleared, plus every top-level field named by `WithIgnoredFields` (a name the
request does not declare is skipped). Unknown fields count. Deterministic encoding is stable within one binary and protobuf runtime, which is
what a retry against the same service needs; it is not a canonical cross-language encoding.

### Scope

`Scope{Method, Principal}` isolates IDs: the same ID sent by two principals, or to two methods, names two different requests. `Method` is
required (`ErrInvalidScope`); `Principal` is the authenticated user, service account or tenant (`"tenants/t1/users/u2"`). An empty principal
puts every caller in one scope, so one caller could replay another's response: leave it empty only for an unauthenticated single-tenant API.

The store key is the hex SHA-256 of the length-prefixed method, principal and canonical ID: fixed length (64 characters), no caller data or
PII in the store, and no collision by concatenation.

### Concurrency and leases

The first request reserves the ID with a pending entry that expires after `WithLeaseTTL`. While it runs, a duplicate with the same
parameters waits and then receives the stored response, so concurrent duplicates never run the handler twice. The lease bounds a crash: if
the process dies, the ID frees itself when the lease ends. **The lease must exceed the longest handler run**: once it expires, a duplicate
takes the ID over and runs the handler again; the late first run still returns its own response, but its completion is refused
(`ErrReservationLost`, reported to `WithStoreErrorHandler`), and the takeover's response is the one stored.

### Failures

AIP-155 asks to replay "the response for the previously successful request". By default no failure is stored: the reservation is released,
so the client's retry runs the handler again (a transient failure may succeed). `WithCacheError(func(error) *status.Status)` stores the
failures it selects: it returns the `google.rpc.Status` to replay, or nil (or an OK status) to release. A duplicate of a stored failure gets a
`*ReplayedError` carrying that status. Store only deterministic failures (FAILED_PRECONDITION after a side effect, say), never transient ones.

### Retention and stale responses

A completed request is replayed for `WithRetention` (24 hours by default, the AIP-155 "reasonable timeframe"). After that the ID is free
again. A response larger than `WithMaxResponseSize` is returned to its caller but not stored; its duplicates get `ErrResponseNotRetained`
without running the handler. A service may catch that error and return the current state of the resource instead, the stale success
response AIP-155 permits.

## AIP-155 conformance

| Rule                                                                                | Level      | How it is met                                                                                                           |
|-------------------------------------------------------------------------------------|------------|-------------------------------------------------------------------------------------------------------------------------|
| Providing a request ID must guarantee idempotency                                   | MUST       | `Do` runs the handler at most once per ID, scope and retention, concurrent duplicates included (reservation with lease) |
| A duplicate should return the response of the previously successful request         | SHOULD     | The success response is stored serialized and decoded for every duplicate                                               |
| Any reasonable timeframe for honoring request IDs                                   | MAY        | `WithRetention` (default 24h)                                                                                           |
| `request_id` must be on the request message, not on resources                       | MUST       | Read from the request message passed to `Do`; `WithField` names it                                                      |
| Request IDs should be optional                                                      | SHOULD     | An empty ID runs the handler without deduplication                                                                      |
| Request IDs should be able to be UUIDs; may allow UUIDs only                        | SHOULD/MAY | `FormatUUID` (default) and `FormatUUID4` accept UUIDs only; `FormatASCII` allows other IDs                              |
| The format restrictions must be documented                                          | MUST       | The format table above; the error reason states the restriction                                                         |
| Restricted to 36 ASCII characters                                                   | (example)  | `MaxLength` = 36, checked before any other work, in every format                                                        |
| UUID request IDs must be annotated `(google.api.field_info).format = UUID4`         | MUST       | A schema rule for the API author (the fixture shows it); the guard does not read the annotation                         |
| AIP-202: a UUID4 value may be normalized to lowercase; no primitive text comparison | MAY/MUST   | UUIDs are compared by their canonical lowercase form                                                                    |
| Stale success: the current state may replace the historical response                | MAY        | `ErrResponseNotRetained` lets the service return the current resource                                                   |
| Same ID with different parameters (not specified by AIP-155)                        | —          | `ErrRequestIDReused` (ALREADY_EXISTS)                                                                                   |

## Errors

| Error                                          | Canonical class (go-atlas-core) | gRPC code (`aipmap`)         | When                                                                       |
|------------------------------------------------|---------------------------------|------------------------------|----------------------------------------------------------------------------|
| `*coreerrs.FieldError` + `ErrInvalidRequestID` | `ErrInvalidArgument`            | INVALID_ARGUMENT             | the request ID does not match the format; `Field` is `requestId`           |
| `ErrRequestIDReused`                           | `ErrAlreadyExists`              | ALREADY_EXISTS               | the ID was used in the scope by a request with other parameters            |
| `ErrInProgress`                                | `ErrBusy`                       | ABORTED                      | the first request is still running after the wait                          |
| `ErrResponseNotRetained`                       | `ErrFailedPrecondition`         | FAILED_PRECONDITION          | the original response exceeded `WithMaxResponseSize`                       |
| `ErrStore` (wraps the cause)                   | `ErrUnavailable`                | UNAVAILABLE                  | the store failed before the handler ran, or a stored entry is corrupt      |
| `*ReplayedError` / `ErrReplayedFailure`        | —                               | the stored status            | the original failure was stored by `WithCacheError`                        |
| `ErrStoreFull`                                 | (inside `ErrStore`)             | UNAVAILABLE                  | a `MemoryStore` holds `WithMaxEntries` live entries                        |
| `ErrNoRequestIDField`                          | —                               | INTERNAL                     | the request has no singular string field of the configured name            |
| `ErrInvalidScope`                              | —                               | INTERNAL                     | `Scope.Method` is empty                                                    |
| `ErrReservationLost`                           | —                               | (not returned by `Do`)       | `Store.Complete`/`Release` by a caller that no longer owns the reservation |

Why ALREADY_EXISTS for a reused ID: AIP-155 leaves the case open. The request is not malformed on its own (INVALID_ARGUMENT is for
arguments that are wrong regardless of state), and retrying cannot help (ABORTED invites a retry, AIP-194), while the ID names a request
that already exists. Why ABORTED for a request in progress: it is a concurrency conflict that a retry with the same ID resolves.

## Store contract

```go
type Store interface {
	Reserve(ctx context.Context, key string, pending Entry, now time.Time) (Entry, bool, error)
	Complete(ctx context.Context, key, token string, done Entry, now time.Time) error
	Release(ctx context.Context, key, token string, now time.Time) error
}
```

* `now` is the caller's clock, the only clock of the contract; an entry whose `ExpiresAt` is not after `now` is absent. `ExpiresAt` may be
  truncated to milliseconds.
* `Reserve` is an atomic insert-if-absent (a Mongo upsert on an expired-or-missing filter, Redis `SET NX` with the remaining TTL, an SQL
  insert with conflict handling): it returns the stored entry and true, or the live entry and false.
* `Complete` and `Release` act only on the live pending entry carrying `token`, else `ErrReservationLost`. Completed entries are never
  released.
* A store never drops a live entry; under a capacity limit it fails `Reserve` instead.
* Byte slices are not shared across the boundary; nil and empty are equivalent.

The `requestidtest.Run(t, factory)` suite checks all of it.

## Options

| Option                    | Default           | Meaning                                                                  |
|---------------------------|-------------------|--------------------------------------------------------------------------|
| `WithField`               | `request_id`      | declared name of the singular string request ID field                    |
| `WithFormat`              | `FormatUUID`      | accepted request ID format                                               |
| `WithIgnoredFields`       | none              | top-level fields left out of the fingerprint besides the request ID      |
| `WithRetention`           | 24h               | how long a completed request is replayed                                 |
| `WithLeaseTTL`            | 1m                | lifetime of a reservation; must exceed the longest handler run           |
| `WithWaitTimeout`         | 5s                | how long a duplicate waits for a running request; 0 fails at once        |
| `WithPollInterval`        | 50ms              | how often a waiting duplicate checks the store                           |
| `WithMaxResponseSize`     | 1 MiB             | largest serialized response stored; 0 is unlimited                       |
| `WithCacheError`          | nil (none cached) | selects failures to store and replay, as `google.rpc.Status`             |
| `WithStoreErrorHandler`   | nil (dropped)     | receives store errors that do not fail the call                          |
| `WithClock`               | `clock.Real()`    | time source of leases, retention and waits (go-atlas-core `clock.Clock`) |
| `WithMaxEntries` (memory) | 100 000           | live entries of a `MemoryStore`; 0 is unlimited                          |

Durations at or below zero keep their default (except `WithWaitTimeout`, where zero is meaningful); negative sizes keep their default.

## Limits

| Limit                      | Default  | Checked                                                     |
|----------------------------|----------|-------------------------------------------------------------|
| request ID length          | 36 bytes | before parsing, in every format                             |
| stored response size       | 1 MiB    | after the handler; larger responses are not stored          |
| `MemoryStore` live entries | 100 000  | on reservation of a new key, after dropping expired entries |
| wait for a running request | 5s       | per duplicate, bounded also by its context                  |

## Related packages

* [`requestidtest`](requestidtest/) — the store conformance suite.
* [`fieldbehavior`](../fieldbehavior/) — AIP-203 field behaviors of the same requests.
* go-atlas — store adapters (MongoDB, Redis, SQL) and the gRPC wiring that builds the `Scope` from the method and the authenticated principal.
