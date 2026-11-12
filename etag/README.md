# etag

```go
import (
    "github.com/altessa-s/go-aip/etag"
    coreetag "github.com/altessa-s/go-atlas-core/encoding/etag"
)
```

Package `etag` implements [AIP-154](https://google.aip.dev/154) resource freshness validation
with [RFC 9110 §8.8.3](https://www.rfc-editor.org/rfc/rfc9110#section-8.8.3)
entity tags: it parses the etag a client sends, issues etags from revisions or resource content, checks the precondition of an Update, Delete
or custom method, and reads and stamps the `etag` field of proto messages. The [guide](../docs/etag.md) maps every AIP-154 rule to the function
that implements it. [fieldmask/update](../fieldmask/update) builds `Patch` preconditions on it.

## Tags

`coreetag.Tag` comes from `github.com/altessa-s/go-atlas-core/encoding/etag`: an opaque value plus a weak flag. The zero `coreetag.Tag` is the absent tag; it
renders as `""` and matches nothing. `coreetag.Tag.String` renders the wire form, `"value"` (strong) or `W/"value"` (weak), which is the value of an
AIP-154 `etag` field and of an HTTP `ETag` header alike.

| Function                           | Result                                                                                                                                                                |
|------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `coreetag.Strong(v)`, `coreetag.Weak(v)`             | `"v"`, `W/"v"` for a trusted value; an empty value is the zero `coreetag.Tag`.                                                                                                 |
| `FromRevision[int64 \| uint64](r)` | `W/"<r>"` in canonical decimal (the go-atlas-core version tag); weak because equal revisions mean semantically equal resources. A negative `int64` is the zero `coreetag.Tag`. |
| `Revision[int64 \| uint64](t)`     | The revision of a `FromRevision` tag; anything else (strong, non-canonical, out of range) is `ErrNotRevision`.                                                        |
| `FromMessage(m, opts...)`          | The content tag of a resource: SHA-256 of its deterministic binary encoding, unpadded base64url (43 bytes).                                                           |
| `Parse(s, opts...)`                | The tag a client sends (see [Parsing](#parsing)).                                                                                                                     |

`coreetag.Tag.StrongMatch` and `coreetag.Tag.WeakMatch` are the RFC 9110 §8.8.3.2 comparison functions:

| a       | b       | strong | weak |
|---------|---------|--------|------|
| `W/"1"` | `W/"1"` | no     | yes  |
| `W/"1"` | `W/"2"` | no     | no   |
| `W/"1"` | `"1"`   | no     | yes  |
| `"1"`   | `"1"`   | yes    | yes  |

### Content tags

`FromMessage` hashes a copy of the message, so `m` is never modified, with `proto.MarshalOptions{Deterministic: true}`:

* The top-level `etag` field is always left out, so stamping the tag into the resource does not change it.
* `WithHashFields(paths...)` limits the hash to a field subset; `WithExcludeFields(paths...)` leaves fields out. A path is a top-level field
  name or a dotted path through singular message fields (`shelf.theme`), by proto name; anything else is `ErrNoField`.
* `WithExcludeBehaviors(annotations.FieldBehavior_OUTPUT_ONLY)` leaves out the fields carrying the behaviors at any depth, such as an update
  time a store rewrites on every save.
* The tag is strong by default. Pass `WithWeak(true)` when exclusions make equal tags mean semantically, not byte-for-byte, equal resources.
* `WithHexEncoding(true)` renders the digest in lowercase hex (64 bytes).

Deterministic encoding is stable for one build of one message type, not across protobuf runtimes or languages. Compare tags one service
computes, or store them with the resource.

## Parsing

`Parse` accepts:

* `"value"` (strong) and `W/"value"` (weak, case-sensitive prefix), as RFC 9110 and AIP-154 recommend;
* leniently, a bare `value` as a strong tag, because AIP-154 examples and many clients send plain strings. It must consist of entity-tag
  characters only (`%x21 / %x23-7E / %x80-FF`: no space, quote or control character) and must not start with `W/`. `WithStrictQuotes(true)`
  refuses it.

Surrounding white space is trimmed. The empty string is `ErrMissing`; the empty quoted tag `""`, a blank value, an unclosed quote, trailing
content and invalid characters are `ErrInvalid`. A value longer than `WithMaxLength` bytes (default `DefaultMaxLength`, 256; 0 is
unlimited) is `ErrInvalid` before any parsing.

## Preconditions

`NewPrecondition(opts...)` returns the AIP-154 check of a mutation. The zero `Precondition` uses the defaults; it is immutable and safe for
concurrent use.

| Method                     | Behavior                                                                                                                                                                                                          |
|----------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Parse(sent)`              | The parsed tag; the zero `coreetag.Tag` for an empty `sent` (no precondition) unless `WithRequired(true)`.                                                                                                                 |
| `Match(expected, current)` | nil for a zero `expected`; `ErrMismatch` unless `expected` matches `current`. A zero `current` (a missing resource) matches nothing.                                                                              |
| `Check(sent, current)`     | `Parse` then `Match`.                                                                                                                                                                                             |
| `ParseRevision(sent)`      | The revision of a `FromRevision` etag, `ok == false` when absent; W/"0" only `WithSingleton(true)`, MaxInt64 never (the next revision would overflow). The caller compares it, typically atomically in its store. |

Matching follows `WithComparison`:

| Comparison               | Rule                                                                                                                                     |
|--------------------------|------------------------------------------------------------------------------------------------------------------------------------------|
| `CompareExact` (default) | The sent tag is the current tag as issued: same value, same weakness. A weak etag the service issues and the client echoes satisfies it. |
| `CompareStrong`          | RFC 9110 strong comparison, as HTTP `If-Match` uses: both strong and equal. A service issuing weak tags never satisfies it.              |
| `CompareWeak`            | RFC 9110 weak comparison: equal values, whatever the weakness.                                                                           |

RFC 9110 `If-Match` uses strong comparison, which would make weak etags useless as AIP-154 preconditions. AIP-154 lets a resource use weak
etags, so the default compares exactly instead: a client must send back the etag it read, and the weak etag of a revision is a valid
precondition, while a strong rewrite of a weak tag (or the reverse) is not.

```go
pre := etag.NewPrecondition(etag.WithField("book.etag"))

stored, err := repo.Get(ctx, req.GetBook().GetName())
if err != nil {
	return nil, err
}
if err := pre.Check(req.GetBook().GetEtag(), etag.FromRevision(stored.Revision)); err != nil {
	return nil, err // coreerrs.FieldError (INVALID_ARGUMENT) or etag.ErrMismatch (ABORTED)
}
saved, err := repo.Update(ctx, merge(stored, req.GetBook()))
if err != nil {
	return nil, err
}
resp := toProto(saved)
if err := etag.Stamp(resp, etag.FromRevision(saved.Revision)); err != nil {
	return nil, err
}
return resp, nil
```

## Fields

`Get(m)` reads the `etag` field of a request resource or of a Delete or custom method request; `Stamp(m, t)` writes `t.String()` into the
`etag` field of a response (the zero `coreetag.Tag` clears it). Both find the field by descriptor (`FieldName`, `"etag"`) and require a singular string
field: anything else is `ErrNoField`. A nil message is `ErrNilMessage` (`Get` reads a typed nil message as `""`).

## AIP-154 conformance

| Rule                                                              | Level      | How it is met                                                                                                                                                                                     |
|-------------------------------------------------------------------|------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| The etag field is a string named `etag`                           | MUST       | `Get` and `Stamp` accept only a singular string field named `etag` (`ErrNoField` otherwise).                                                                                                      |
| The server provides the etag on output                            | MUST       | `Stamp` writes the current tag into every response; `FromRevision` and `FromMessage` issue it.                                                                                                    |
| Etag values conform to RFC 7232 / RFC 9110 and include the quotes | SHOULD     | Issued tags always render quoted (`"v"`, `W/"v"`); `Parse` validates entity-tag characters.                                                                                                       |
| A matching etag permits the request                               | MUST       | `Precondition.Match` returns nil; other failures are the caller's.                                                                                                                                |
| A mismatching etag fails with ABORTED                             | MUST       | `ErrMismatch` wraps `coreerrs.ErrConflict`, which the go-atlas error mapping and the apierror package (AIP-193) report as ABORTED. Check permissions first so PERMISSION_DENIED takes precedence. |
| A request without an etag is permitted                            | SHOULD     | An empty sent etag is no precondition.                                                                                                                                                            |
| Services may require etags                                        | MAY        | `WithRequired(true)`: a missing etag is a `coreerrs.FieldError` (`ReasonRequired`).                                                                                                               |
| Delete and custom methods may carry an etag                       | MAY        | `Get` reads the request's own `etag` field; `Precondition` works for any method.                                                                                                                  |
| Strong or weak etags, documented                                  | MAY/SHOULD | `FromRevision` is weak, `FromMessage` strong unless `WithWeak(true)`; the comparison rule is documented above.                                                                                    |
| Declarative-friendly resources include an etag                    | MUST       | Schema rule, outside this package.                                                                                                                                                                |

Deliberate deviation: a bare unquoted etag is accepted as a strong tag unless `WithStrictQuotes(true)`; AIP-154 says values *should* include
quotes, and tolerating plain strings costs nothing.

## Errors

| Error                 | Returned by                                     | Meaning                                                                                         |
|-----------------------|-------------------------------------------------|-------------------------------------------------------------------------------------------------|
| `ErrMissing`          | `Parse`; in the chain of a `Precondition` error | An empty etag where one is required.                                                            |
| `ErrInvalid`          | `Parse`; in the chain of a `Precondition` error | Not an entity tag, or longer than `WithMaxLength`.                                              |
| `ErrNotRevision`      | `Revision`; in the chain of `ParseRevision`     | Not a tag `FromRevision` issues (or W/"0" without `WithSingleton`, MaxInt64).                   |
| `coreerrs.FieldError` | `Precondition.Parse`, `Check`, `ParseRevision`  | INVALID_ARGUMENT on `WithField` (default `"etag"`), reason `ReasonRequired` or `ReasonInvalid`. |
| `ErrMismatch`         | `Precondition.Match`, `Check`                   | The etag does not name the current state; wraps `coreerrs.ErrConflict` (ABORTED).               |
| `ErrNoField`          | `Get`, `Stamp`, `FromMessage`                   | No singular string `etag` field, or a hash path that does not name a field.                     |
| `ErrNilMessage`       | `Get`, `Stamp`, `FromMessage`                   | A nil message.                                                                                  |

`ErrMissing`, `ErrInvalid`, `ErrMismatch` and `ErrNotRevision` are the go-atlas-core `encoding/etag` sentinels, so `errors.Is` matches across both
packages.

## Options

| Option                 | Type         | Default                  | Behavior                                                                                        |
|------------------------|--------------|--------------------------|-------------------------------------------------------------------------------------------------|
| `WithField`            | `Option`     | `FieldName` (`"etag"`)   | The API path of the etag field errors name: `"book.etag"` for an Update, `"etag"` for a Delete. |
| `WithRequired`         | `Option`     | false                    | Refuse a request without an etag.                                                               |
| `WithComparison`       | `Option`     | `CompareExact`           | How a sent tag matches the current one.                                                         |
| `WithMaxLength`        | `Option`     | `DefaultMaxLength` (256) | Bound the bytes of a sent etag; 0 is unlimited, a negative value keeps the default.             |
| `WithStrictQuotes`     | `Option`     | false                    | Refuse bare (unquoted) etags.                                                                   |
| `WithSingleton`        | `Option`     | false                    | Let `ParseRevision` accept W/"0".                                                               |
| `WithHashFields`       | `HashOption` | all fields               | Hash only these field paths.                                                                    |
| `WithExcludeFields`    | `HashOption` | none                     | Leave these field paths out of the hash.                                                        |
| `WithExcludeBehaviors` | `HashOption` | none                     | Leave out the fields carrying these behaviors, at any depth.                                    |
| `WithWeak`             | `HashOption` | false                    | Issue a weak content tag.                                                                       |
| `WithHexEncoding`      | `HashOption` | false                    | Hex instead of base64url.                                                                       |

## Limits

A client controls only the etag it sends: `WithMaxLength` (default 256 bytes, 0 unlimited) bounds it before any parsing. Issued tags are at most
64 bytes (a hex content tag); a revision tag at most 24.
