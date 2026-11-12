# Resource freshness (etags)

`coreetag` denotes `github.com/altessa-s/go-atlas-core/encoding/etag`.

[etag](../etag/) implements AIP-154 resource freshness validation with RFC 9110 entity tags. It works on strings, tags and `proto.Message`
values and has no gRPC dependency. [apierror](../apierror/) (or the go-atlas error mapping) turns its errors into status codes: `coreetag.ErrMismatch`
wraps `coreerrs.ErrConflict`, which maps to ABORTED.

## Conformance

| Rule                                                                         | Implemented by                                                               | Behavior                                                                                                                                                                  |
|------------------------------------------------------------------------------|------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| AIP-154: the field is a string named `etag` (MUST)                           | `Get`, `Stamp`                                                               | Found by descriptor; anything but a singular string `etag` field is `ErrNoField`.                                                                                         |
| AIP-154: the server provides the etag on output (MUST)                       | `Stamp`, `FromRevision`, `FromMessage`                                       | `Stamp` writes the current tag into a response; the zero tag clears the field.                                                                                            |
| AIP-154: values conform to RFC 7232 and include the quotes (SHOULD)          | `coreetag.Tag.String`, `Parse`                                                        | Issued tags render `"v"` or `W/"v"`; `Parse` validates entity-tag characters (`%x21 / %x23-7E / %x80-FF`).                                                                |
| AIP-154: a matching etag permits the request (MUST)                          | `Precondition.Match`, `Check`                                                | nil.                                                                                                                                                                      |
| AIP-154: a mismatch is ABORTED unless another error takes precedence (MUST)  | `Precondition.Match`, `Check`; `update.Int64Precondition`, `TagPrecondition` | `coreetag.ErrMismatch`, which wraps `coreerrs.ErrConflict` (ABORTED). A zero current tag (a missing resource) matches no sent etag. Permission checks run before the precondition. |
| AIP-154: no etag permits the request (SHOULD); services may require it (MAY) | `Precondition.Parse`                                                         | An empty etag is no precondition; `WithRequired(true)` makes it a `coreerrs.FieldError` (`ReasonRequired`).                                                               |
| AIP-154: Delete and custom methods may carry an etag (MAY)                   | `Get`, `Precondition`                                                        | `Get` reads the request's `etag` field; the check is method-agnostic.                                                                                                     |
| AIP-154: strong or weak etags, documented (MAY/SHOULD)                       | `FromRevision` (weak), `FromMessage` (strong, `WithWeak`)                    | See [Comparison](#comparison).                                                                                                                                            |
| AIP-193: field violations name the request field                             | `WithField`                                                                  | A malformed or missing required etag is a `coreerrs.FieldError` on the configured path (`book.etag`, `etag`).                                                             |
| Resource limits on client input                                              | `WithMaxLength`                                                              | A sent etag over 256 bytes (default; 0 unlimited) is refused before parsing.                                                                                              |

## Parsing

`Parse` accepts `"v"` and `W/"v"` and, leniently, a bare `v` as a strong tag (AIP-154 examples show plain strings): `WithStrictQuotes(true)`
refuses bare values. Surrounding white space is trimmed; `W/` is case-sensitive; the empty quoted tag `""` is invalid, the empty string is
`coreetag.ErrMissing`.

## Comparison

RFC 9110 defines strong comparison (both strong, equal values; used by `If-Match`) and weak comparison (equal values). `coreetag.Tag.StrongMatch` and
`coreetag.Tag.WeakMatch` implement both. A `Precondition` compares exactly by default (`CompareExact`): the client echoes the etag it read, so the sent tag
must equal the current tag in value and weakness. coreetag.Strong comparison would refuse every weak etag, making the weak revision etags of
`FromRevision` useless as preconditions; weak comparison would accept a strong rewrite of a weak tag. `WithComparison(CompareStrong)` and
`WithComparison(CompareWeak)` select the RFC functions.

## Issuing etags

* `FromRevision(rev)`: `W/"<rev>"` for an `int64` or `uint64` revision, the go-atlas-core version tag; `Revision` reads it back and
  `Precondition.ParseRevision` turns a sent etag into the revision a store compares atomically (W/"0" only with `WithSingleton`).
* `FromMessage(m)`: SHA-256 of the deterministic encoding, base64url (or hex), without the `etag` field. `WithHashFields`,
  `WithExcludeFields` and `WithExcludeBehaviors(OUTPUT_ONLY)` select the content; mark the tag weak with `WithWeak(true)` when exclusions make
  it semantic. Deterministic encoding is stable within one build only.

## fieldmask/update integration

`update.Int64Precondition` is built on `Precondition.ParseRevision`; `update.TagPrecondition` takes an `etag.Precondition` rule and the
current tag of an entity. Both refuse an etag sent for a missing resource (allow_missing) with `coreetag.ErrMismatch`. See
[fieldmask.md](fieldmask.md#update-plans-and-patches).

## See also

* [`etag`](../etag/README.md) — API, options and errors.
* [`validateonly.md`](validateonly.md) — AIP-163, which runs the precondition without the write.
* **AIP-154** — Resource freshness validation ([aip.dev/154](https://google.aip.dev/154)).
* **RFC 9110 §8.8.3** — Entity tags ([rfc-editor.org/rfc/rfc9110#section-8.8.3](https://www.rfc-editor.org/rfc/rfc9110#section-8.8.3)).
