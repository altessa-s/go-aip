# apierror

```go
import "github.com/altessa-s/go-aip/apierror"
```

Package `apierror` implements the [AIP-193](https://google.aip.dev/193) error model: a `google.rpc.Status` with a `google.rpc.Code`, a
developer-facing English message and the standard details of `google/rpc/error_details.proto`, `ErrorInfo` always among them. It has no gRPC
dependency — codes are the genproto `google.golang.org/genproto/googleapis/rpc/code` enum and the status is the genproto `status.Status`
message — and depends on no go-atlas package other than `go-atlas-core/errors`, whose canonical sentinels it maps.

The subpackage [`aipmap`](aipmap/) maps the sentinels of the other go-aip packages (fieldmask, fieldbehavior, fieldinfo, etag, validateonly,
filter, orderby, projection, listing, pagetoken, batch, softdelete, requestid, unreachable, state, resourcename).

## Contract

* An `*Error` is immutable. Build it with `New` (or `MustNew` for package-level errors) and return it — or wrap it — like any error.
* `New` validates AIP-193: the code is an error code of `google.rpc.Code` (not `OK`), the message is non-empty UTF-8, the `ErrorInfo` reason is
  UPPER_SNAKE_CASE of at most 63 characters (default: the code's name), every metadata key is lowerCamelCase of at most 64 characters, a
  `LocalizedMessage` has a locale and a message, a field violation has a field, and each detail type appears once. Every detail must marshal
  (strings must be UTF-8). A failure is `ErrInvalidError`.
* `ErrorInfo` is always present and always the first detail.
* `DebugInfo` is stored but rendered only with `WithIncludeDebugInfo(true)`.
* `FromError` / `Mapper.Map` never return an error without `ErrorInfo`; an unclassified error becomes `INTERNAL` with the fixed message
  `"internal error"` unless `WithExposeUnknown(true)`.

## Building an error

```go
var errNoStock = apierror.MustNew(code.Code_FAILED_PRECONDITION, "the item is out of stock",
	apierror.WithReason("NO_STOCK"), apierror.WithDomain("shop.example.com"))

e, err := apierror.New(code.Code_INVALID_ARGUMENT, "the shelf name is invalid",
	apierror.WithReason("INVALID_SHELF"),
	apierror.WithDomain("library.example.com"),
	apierror.WithMetadata(map[string]string{"shelf": name}),
	apierror.WithFieldViolations(&errdetails.BadRequest_FieldViolation{
		Field: "shelf.name", Description: "must not be empty", Reason: "EMPTY_NAME",
	}),
	apierror.WithLocalizedMessage("en-US", "Enter a shelf name."),
	apierror.WithCause(storageErr))
```

| Option                       | Detail                           | Notes                                                    |
|------------------------------|----------------------------------|----------------------------------------------------------|
| `WithReason`                 | `ErrorInfo.reason`               | Default: the code's name (`NOT_FOUND`).                  |
| `WithDomain`                 | `ErrorInfo.domain`               | Default: empty; a `Mapper` fills in `WithDefaultDomain`. |
| `WithMetadata`               | `ErrorInfo.metadata`             | Copied; keys validated.                                  |
| `WithFieldViolations`        | `BadRequest.field_violations`    | Appends; field, description, reason, localized message.  |
| `WithLocalizedMessage`       | `LocalizedMessage`               | `(locale, message)`, both required.                      |
| `WithHelpLinks`              | `Help.links`                     | Appends; a link needs a URL.                             |
| `WithRetryDelay`             | `RetryInfo.retry_delay`          | A delay that is not positive is ignored.                 |
| `WithQuotaViolations`        | `QuotaFailure.violations`        | Appends.                                                 |
| `WithPreconditionViolations` | `PreconditionFailure.violations` | Appends.                                                 |
| `WithResourceInfo`           | `ResourceInfo`                   |                                                          |
| `WithRequestInfo`            | `RequestInfo`                    |                                                          |
| `WithDebugInfo`              | `DebugInfo`                      | Rendered only with `WithIncludeDebugInfo(true)`.         |
| `WithDetails`                | any other message                | One per type; a standard type here fills its slot.       |
| `WithCause`                  | —                                | Reachable with `errors.Is`/`errors.As`; never rendered.  |

`errors.Is(err, target)` holds when `target` is an `*Error` with the same code and reason, so a package-level `*Error` works as a sentinel.
Accessors: `Code`, `Message`, `Reason`, `Domain`, `Metadata`, `FieldViolations`, `Details` (all copies).

## Rendering

| Method / function                  | Output                                                                                                   |
|------------------------------------|----------------------------------------------------------------------------------------------------------|
| `Proto(opts...)`                   | `*status.Status`, details packed in `Any`, `ErrorInfo` first. gRPC: `status.FromProto(e.Proto()).Err()`. |
| `Status()`                         | `Proto()` with the default rendering (no `DebugInfo`).                                                   |
| `JSON(opts...)`                    | The AIP-193 HTTP/JSON body (below).                                                                      |
| `MarshalJSON()`                    | `JSON()`, so `json.Marshal(e)` writes the body.                                                          |
| `WriteHTTP(w, opts...)`            | The body with the HTTP status and `application/json; charset=utf-8`.                                     |
| `HTTPStatus()`, `HTTPStatus(code)` | The HTTP status of the code, per `google/rpc/code.proto`.                                                |
| `FromProto(status)`                | Reads a status back (client or proxy); unknown detail types stay packed as `Any`.                        |

```json
{
  "error": {
    "code": 404,
    "message": "shelf not found",
    "status": "NOT_FOUND",
    "details": [
      {"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": "SHELF_NOT_FOUND", "domain": "library.example.com"}
    ]
  }
}
```

`code` is the HTTP status, `status` the `google.rpc.Code` name, and each detail the protojson form of its `Any` (lowerCamelCase field names).

| google.rpc.Code   | HTTP | google.rpc.Code     | HTTP |
|-------------------|------|---------------------|------|
| OK                | 200  | RESOURCE_EXHAUSTED  | 429  |
| CANCELLED         | 499  | FAILED_PRECONDITION | 400  |
| UNKNOWN           | 500  | ABORTED             | 409  |
| INVALID_ARGUMENT  | 400  | OUT_OF_RANGE        | 400  |
| DEADLINE_EXCEEDED | 504  | UNIMPLEMENTED       | 501  |
| NOT_FOUND         | 404  | INTERNAL            | 500  |
| ALREADY_EXISTS    | 409  | UNAVAILABLE         | 503  |
| PERMISSION_DENIED | 403  | DATA_LOSS           | 500  |
| UNAUTHENTICATED   | 401  | (other)             | 500  |

## Mapping errors

`FromError(err)` maps with the default rules; `NewMapper` adds service rules, a default domain and the unknown-error policy:

```go
mapper, merr := apierror.NewMapper(
	apierror.WithRules(myRules...),        // first: they win
	apierror.WithRules(aipmap.Rules()...), // go-aip packages
	apierror.WithDefaultDomain("library.example.com"))
if merr != nil {
	return merr
}

// err is the error a handler failed with.
return status.FromProto(mapper.Map(err).Proto()).Err()
```

`Map` returns an `*Error` it is given as it is, and one found deeper in the chain as a copy that keeps the original error as its cause (the
default domain filled in when it has none). Otherwise the first rule whose `Target` matches (`errors.Is`) decides the code and reason — the
mapper's own rules in order, then `DefaultRules()`:

| Sentinel                         | Code                | Message                                             | Reason                  |
|----------------------------------|---------------------|-----------------------------------------------------|-------------------------|
| `coreerrs.ErrNotFound`           | NOT_FOUND           | `not found`                                         | `NOT_FOUND`             |
| `coreerrs.ErrAlreadyExists`      | ALREADY_EXISTS      | `already exists`                                    | `ALREADY_EXISTS`        |
| `coreerrs.ErrConflict`           | ABORTED             | `changed concurrently, re-read and retry`           | `VERSION_CONFLICT`      |
| `coreerrs.ErrInvalidArgument`    | INVALID_ARGUMENT    | `<field>: <reason>` for a FieldError, else its text | `INVALID_ARGUMENT`      |
| `coreerrs.ErrFailedPrecondition` | FAILED_PRECONDITION | `failed precondition`                               | `FAILED_PRECONDITION`   |
| `coreerrs.ErrUnavailable`        | UNAVAILABLE         | `temporarily unavailable, retry later`              | `TEMPORARY_UNAVAILABLE` |
| `coreerrs.ErrBusy`               | ABORTED             | `the same work is in progress, retry later`         | `BUSY`                  |
| `context.Canceled`               | CANCELLED           | `the request was canceled`                          | `CANCELLED`             |
| `context.DeadlineExceeded`       | DEADLINE_EXCEEDED   | `the deadline expired before ...`                   | `DEADLINE_EXCEEDED`     |
| anything else                    | INTERNAL            | `internal error`                                    | `INTERNAL`              |

The rows and texts match the canonical rules of the go-atlas errstatus interceptor, so a service moving between the two keeps its contract.

* **Convert.** A rule with `Convert` builds the `*Error` itself from the matched error (aipmap replays a stored requestid status this way,
  and turns the fieldinfo, fieldbehavior and fieldmask violation lists into one field violation per field);
  a nil result falls back to the rule's code, reason and message.
* **Messages are fixed** unless the rule sets `Expose`: wrapped text may name stored data. Only errors that describe the client's own input
  expose their text (`ErrInvalidArgument`, the input errors of `aipmap`). An `INTERNAL` rule never exposes.
* **FieldError.** When the code is `INVALID_ARGUMENT` and the chain holds a `coreerrs.FieldError`, the message is `<field>: <reason>` and a
  `BadRequest` field violation carries the field exactly as produced (`updateMask.paths[2]`, `requests[0].book.title`, `pageToken`), the
  description and the rule's reason. Only the first FieldError of the chain is reported; for several violations build the `*Error` yourself
  or use a `Convert` rule, as `aipmap` does.
  Re-root domain paths with `coreerrs.MapFieldPath` before mapping.
* **Order.** A sentinel ranked before `ErrInvalidArgument` wins over a FieldError joined with it (`ErrNotFound` joined with a FieldError is
  NOT_FOUND). In `aipmap`, configuration faults are ranked first, so a FieldError around one is INTERNAL, never blamed on a request field.
* **Classified errors.** `Classify(category, cause)` returns a `Classified` error that reads as `category`, unwraps to `category` alone and hides
  `cause`: errors.Is, errors.As and its text never reach the cause, so neither the mapper nor any code that walks the chain — `coreerrs.MapFieldPath`,
  `batch.ItemError`, a `Convert` function, a wrapper formatting it with any verb and flag, `%#v` included — sees a FieldError, sentinel or text
  the cause holds: the error implements `fmt.Formatter` and `fmt.GoStringer` and formats its category text alone.
  `HiddenCause(err)` returns the cause of the first classified error in a tree, the mapped `*Error` included (it keeps the original error as its
  cause, also when it is a copy of an `*Error` found in the chain — a classified error whose category is an `*Error` — or built by a `Convert`
  function that kept no cause), and the error is a `slog.LogValuer` rendering `<category>: <cause>` for logs. A backend uses it for a server-side
  fault whose cause may wrap a client-facing sentinel — a stored record whose decoder returned a page-token or deadline error stays INTERNAL,
  and its stored values never reach the client; errors.Is(err, context.DeadlineExceeded) is false for it, which is the point of the
  classification. Only the classified branch is cut: a sibling joined with it still classifies, and a FieldError above it (built by the caller,
  with no access to the cause) is an ordinary client error.
  Another type implements `Classified` with a `HiddenCause() error` method; it must not unwrap to its cause.
* **Etag mismatch vs. collision.** `ErrConflict` is the AIP-154 stale etag (ABORTED); `ErrAlreadyExists` a create that collides (ALREADY_EXISTS).
* **Permission.** go-atlas-core has no permission sentinel: return `apierror.New(code.Code_PERMISSION_DENIED, ...)` from the
  authorization check — before the existence check, as AIP-193 requires — or add a rule for the service's own sentinel.
* A message that would not be UTF-8 (an exposed text, a field reason) falls back to the rule's fixed message.
* **Bounded text.** Error text the mapper copies into the status — an exposed text, the `<field>: <reason>` message and the field violation
  description of a FieldError, and the texts `aipmap` copies — is cut to `MaxExposedMessageLength` (1024) runes on a rune boundary, ending
  with `TruncatedSuffix` (`…`), so an error that echoes a large client input cannot inflate the response. Shorter texts are unchanged; fixed
  messages and an `*Error` already in the chain are not cut. `BoundMessage` applies the same bound in a `Convert` rule.

### Why a registry and a subpackage

`apierror` imports only go-atlas-core/errors. The go-aip sentinels are mapped by rules (`Rule` values), not by imports, and the rules for the existing
packages live in `aipmap`, which imports those packages. This keeps the error model light (no CEL from filter in a service that does not filter), lets
any package or service contribute rules without touching `apierror`, and rules out an import cycle: none of the packages `aipmap` maps imports
`apierror` or `aipmap`. The SQL and MongoDB list engines (`backend/sqlbase`, `backend/mongo`) import `apierror` only to classify their errors
(`Classify`, `Classified`), and no package imports `aipmap`.

## Errors

| Error             | Returned by                     | Meaning                                                            |
|-------------------|---------------------------------|--------------------------------------------------------------------|
| `ErrInvalidError` | `New`, `FromProto`, `NewMapper` | The error, status or rule breaks AIP-193 (see the contract above). |

`MustNew` panics with it. `JSON` and `WriteHTTP` fail for a detail kept packed whose type is not linked into the program; `WriteHTTP` writes
nothing then.

## Limits

| Constant                  | Value | Rule                                                                                                   |
|---------------------------|-------|--------------------------------------------------------------------------------------------------------|
| `MaxReasonLength`         | 63    | `ErrorInfo.reason` length                                                                              |
| `MaxMetadataKeyLength`    | 64    | `ErrorInfo.metadata` key length                                                                        |
| `MaxExposedMessageLength` | 1024  | Runes of error text a `Mapper` copies into a message or a field violation description (`BoundMessage`) |

`ValidReason` and `ValidMetadataKey` expose the checks.

## AIP-193 conformance

| Rule                                                                               | Level  | How it is met                                                                                                                    |
|------------------------------------------------------------------------------------|--------|----------------------------------------------------------------------------------------------------------------------------------|
| Return `google.rpc.Status` with a `google.rpc.Code`                                | MUST   | `Proto()`; `New` refuses `OK` and values outside the enum.                                                                       |
| Message is a developer-facing, human-readable English debug message                | SHOULD | Caller-supplied; fixed English messages for mapped errors; non-empty UTF-8 enforced.                                             |
| Do not expose internals in messages                                                | SHOULD | Mapped messages are fixed unless the rule exposes client input; unknown errors are `internal error`; `DebugInfo` off by default. |
| Every error includes `ErrorInfo`                                                   | MUST   | Always present (reason defaults to the code name); `FromProto` refuses a status without one.                                     |
| `ErrorInfo.reason` UPPER_SNAKE_CASE, ≤ 63 chars, `[A-Z][A-Z0-9_]+[A-Z0-9]`         | MUST   | `New`, `NewMapper` (rule reasons), field-violation reasons.                                                                      |
| `ErrorInfo.domain` identifies the service                                          | MUST   | `WithDomain`, `WithDefaultDomain`; left to the service (no global default).                                                      |
| Metadata keys lowerCamelCase, ≤ 64 chars, `[a-z][a-zA-Z0-9-_]+`                    | MUST   | `New`.                                                                                                                           |
| Dynamic parts of the message are in metadata                                       | MUST   | `WithMetadata`; mapped messages carry no dynamic values except exposed client input.                                             |
| Existing metadata keys keep appearing                                              | MUST   | Service responsibility (documented).                                                                                             |
| Each detail type appears at most once                                              | MUST   | One slot per standard type; `WithDetails` and `FromProto` refuse duplicates.                                                     |
| `LocalizedMessage` has both locale and message                                     | MUST   | `New`, also inside field violations.                                                                                             |
| `BadRequest` names the offending field                                             | SHOULD | FieldError → field violation with the request path as produced.                                                                  |
| `PERMISSION_DENIED` before `NOT_FOUND`                                             | MUST   | Service responsibility; `New(code.Code_PERMISSION_DENIED, ...)`.                                                                 |
| HTTP/JSON: `code` is the HTTP status, `status` the code name, details with `@type` | MUST   | `JSON`, `MarshalJSON`, `WriteHTTP`, `HTTPStatus`.                                                                                |
| Partial errors are avoided                                                         | SHOULD | Not provided; LRO metadata can carry `Proto()` statuses.                                                                         |

See [docs/apierror.md](../docs/apierror.md) for the full conformance notes and the `aipmap` table.

## API

| Symbol                                                                       | Purpose                                  |
|------------------------------------------------------------------------------|------------------------------------------|
| `Error`, `New`, `MustNew`, `Option`                                          | The error and its builder                |
| `Proto`, `Status`, `JSON`, `MarshalJSON`, `WriteHTTP`, `HTTPStatus`          | Rendering                                |
| `RenderOption`, `WithIncludeDebugInfo`                                       | Rendering options                        |
| `FromProto`                                                                  | Reading a status back                    |
| `Rule`, `DefaultRules`, `Mapper`, `NewMapper`, `MapperOption`, `FromError`   | Error mapping                            |
| `Classified`, `Classify`, `HiddenCause`                                      | Errors classified by a category alone    |
| `WithRules`, `WithDefaultDomain`, `WithExposeUnknown`                        | Mapper options                           |
| `Message*`, `Reason*`                                                        | Texts and reasons of the default rules   |
| `ValidReason`, `ValidMetadataKey`, `MaxReasonLength`, `MaxMetadataKeyLength` | AIP-193 format checks                    |
| `BoundMessage`, `MaxExposedMessageLength`, `TruncatedSuffix`                 | Bound on error text copied into a status |
| `ErrInvalidError`                                                            | Validation failure                       |

## Related packages

* [`aipmap`](aipmap/) — rules for the go-aip sentinels.
* [`fieldmask`](../fieldmask/), [`listing`](../listing/), [`pagination/pagetoken`](../pagination/pagetoken/) — produce `coreerrs.FieldError`
  values with request paths that map to field violations.
