# pagetoken

```go
import "github.com/altessa-s/go-aip/pagination/pagetoken"
```

Package `pagetoken` creates and verifies protected, stateless continuation (page) tokens. A token carries an opaque position payload, its
issue time and digests of its bindings — sort, filter and subject fingerprints — signed with HMAC-SHA256 or, opt-in, encrypted with AES-256-GCM.

## Contract

`New(key, opts...)` constructs a `Codec` implementing `Signer`. Tokens are stateless, authenticated with HMAC-SHA256, expire after 24 hours by
default, and tolerate one minute of future clock skew. Keys are at least 32 bytes; payloads default to at most 1,024 bytes. `WithClock` supplies an
instance clock for deterministic tests. `WithExpiryDisabled(true)` explicitly removes expiry.

`WithPreviousKeys` accepts retired verification keys while issuing only with the current key. Keep retired keys until their tokens expire.
`WithEncryption(true)` issues AES-256-GCM encrypted tokens and still accepts signed tokens; every codec can resolve encrypted tokens. After signed
tokens have expired, `WithStrictEncryption(true)` rejects them. Signed tokens reveal payloads; encryption protects cursor confidentiality.

`NewBindings(subject, sort, filter...)` separates components with length prefixes. Bind the list, parent/scope, canonical filter and order, and
application semantic version; do not bind page size. A token is not a database snapshot. `Classify` distinguishes malformed and expired tokens while
preserving sentinel causes; `TokenFieldError` maps failures to pageToken.

`NewTyped[C](signer, opts...)` provides typed cursors and uses JSONPayloadCodec unless `WithPayloadCodec` replaces it. A nil signer issues no token;
resolving an empty token returns the first-page flag, while a supplied token without a signer fails. `NewOffset` wraps nonnegative integer offsets for
small collections.

`Position` holds ordered `SortValue` keys and a typed `SortValue` in `ID`; an absent or untyped identity is rejected. `PositionCodec` combines
`PayloadCodec`, `ValueComparator`, normalization, and a semantic Name. ScalarPositionCodec is scalar-v2; it orders numbers, strings, booleans, and
times, normalizing timestamps at positive precision. OpaqueSortValue requires a matching custom codec. Change codec names and bindings when encoding
or ordering changes. A `SortValue` is an inline tagged value: building, copying and comparing a string, integer, float, bool or timestamp allocates
nothing; compare values with `Compare`, never with `==`. The zero `SortValue` holds no value, such as an SQL NULL key: it orders first and encodes
as the empty object `{}`.

`SlicePager` pages sorted sources with configurable comparison. `ResolvePageSize` applies defaults and clamping and rejects negative requests. Page
result types live in the sibling page package. The import paths are `github.com/altessa-s/go-aip/pagination/pagetoken` and
`github.com/altessa-s/go-aip/pagination/page`.

## Usage

```go
codec, err := pagetoken.New(signingKey) // ≥ 32 bytes, identical on every replica
token, err := codec.Issue([]byte(position), pagetoken.Bindings{Sort: sortFP, Filter: filterFP, Subject: user})
position, err := codec.Resolve(token, pagetoken.Bindings{Sort: sortFP, Filter: filterFP, Subject: user})
```

`Resolve` verifies the signature first, then expiry, then each binding, so a forged token never reveals which binding it would have failed. Tokens use
canonical URL-safe base64 only; any other spelling of the same bytes is rejected. Payloads default to `MaxPayloadLength` bytes (configurable with
`WithMaxPayloadLength`) and tokens to the matching encoded length. Tokens are stateless: they cannot be revoked individually, only by rotating the key.

By default tokens are signed, not encrypted: base64 decoding a token reveals its payload. Put in it only what the client may read. The sort values of
the last row of a page are values the client was just sent, unless a read mask or a projection hid them; sort on fields the client can see, keep the
position server-side, or encrypt the tokens when they are hidden ([`listing`](../../listing) refuses such orders by default).

## Encrypted tokens

```go
codec, err := pagetoken.New(signingKey, pagetoken.WithEncryption(true))       // issues encrypted tokens, still resolves signed ones
codec, err = pagetoken.New(signingKey, pagetoken.WithStrictEncryption(true)) // issues encrypted tokens, rejects signed ones
```

| Version | Layout (before base64)                                                            | Client sees                  |
|---------|-----------------------------------------------------------------------------------|------------------------------|
| 1       | `1 · issue time · digests · length · payload · HMAC-SHA256`                       | payload, issue time, digests |
| 2       | `2 · key id · nonce · AES-256-GCM(issue time · digests · length · payload) · tag` | key id only                  |

Version 2 tokens are sealed by [go-kryptos `sealbox`](https://github.com/altessa-s/go-kryptos/tree/develop/sealbox) under a 32-byte key derived with
HKDF-SHA256 from the signing key, with the version and key id as associated data. No extra key material is configured: `WithPreviousKeys` rotates both
formats, and every codec resolves encrypted tokens whether or not it issues them. Roll out in two steps: enable `WithEncryption` on every replica,
then, once the signed tokens in circulation have expired (the TTL), switch to `WithStrictEncryption`, under which a signed token is
`ErrUnencryptedToken` (an `ErrInvalidToken`, classified `malformed`). In the go-atlas YAML configuration these are `encryption.enabled` and
`encryption.strict` of the paging section. Tampering, truncation, an unknown key, expiry and changed bindings fail as for signed tokens. Each token
draws a random 96-bit nonce, so rotate the signing key well before 2^32 tokens are issued under it.

Cost on an Apple M4 Pro: issue ~370 ns signed vs ~600 ns encrypted (1 vs 3 allocations), resolve ~515 ns for both (1 vs 2 allocations).

## Typed cursors and offsets

```go
tokens := pagetoken.NewTyped[Position](codec) // JSON-encoded cursor; nil codec = single page
b := pagetoken.NewBindings("ListItems", "name:1", filter, kind) // filter parts are length-prefixed
token, err := tokens.Issue(Position{Name: last.Name, ID: last.ID}, b)
after, first, err := tokens.Resolve(req.PageToken, b) // "" → first page
offset, err := pagetoken.NewOffset(codec).Resolve(req.PageToken, b) // small in-memory catalogs
```

`Typed[C]` and `Offset` sign through a `Signer` (`Issue`/`Resolve` over raw payloads), which `*Codec` implements. Over a nil signer they issue no
tokens (a single page) and fail a presented token with `ErrCodecRequired`. A verified payload that does not decode, or a negative offset, is
`ErrInvalidToken`.

## Slice pagers and page sizes

```go
size := pagetoken.PageSize(req.GetPageSize(), 50, 200) // ≤ 0 → 50, clamped to [1, 200]
size, err := pagetoken.ResolvePageSize(req.GetPageSize(), 50, 200) // the same, a negative size is a field error on pageSize
pager := pagetoken.NewSlicePager(pagetoken.NewTyped[string](codec), func(it Item) string { return it.Name }, strings.Compare)
page, err := pager.Page(items, size, req.GetPageToken(), pagetoken.NewBindings("ListItems", "name:1"))
// page.Items, page.Next
```

| Symbol                           | Description                                                                                               |
|----------------------------------|-----------------------------------------------------------------------------------------------------------|
| `PageSize(req, def, max)`        | Zero or negative is `def`, the result is clamped to `[1, max]` (`max < 1` is 1)                           |
| `ResolvePageSize(req, def, max)` | `PageSize` for a list request: negative sizes return `*coreerrs.FieldError` on `SizeField`                |
| `NewSlicePager(t, key, cmp)`     | Keyset pager over a slice ordered by `cmp` on unique `key`s, cursor found by binary search                |
| `NewSliceOffsetPager(o)`         | Offset pager: the token holds the index of the next page; an offset beyond the slice is `ErrInvalidToken` |
| `SlicePager.Page`                | Returns `SlicePage{Items, Next}`; `Next` is empty on the last page and without a codec                    |

### Typed positions

`Position` holds typed sort keys and a typed unique identity. String identities remain the listing defaults, but integer and other scalar
identities use the same representation as sort keys. Changing position codecs invalidates old tokens; there is no legacy cursor decoder.

```go
profile := pagetoken.ScalarPositionCodec{}
key := func(it Item) pagetoken.Position {
    name, _ := profile.Normalize(it.Name, time.Millisecond)
    id, _ := profile.Normalize(it.ID, time.Millisecond)
    return pagetoken.Position{Keys: []pagetoken.SortValue{name}, ID: id}
}
compare := pagetoken.ComparePositions([]bool{false}, false, profile)
tokens := pagetoken.NewTyped[pagetoken.Position](codec, pagetoken.WithPayloadCodec(profile))
pager := pagetoken.NewSlicePager(tokens, key, compare)
```

| Symbol                | Purpose                                                                                  |
|-----------------------|------------------------------------------------------------------------------------------|
| `PositionCodec`       | Concurrent normalization, scalar comparison, payload encoding and a versioned name       |
| `ValueComparator`     | Scalar comparison passed explicitly to `ComparePositions`                                |
| `ScalarPositionCodec` | Standard scalar JSON representation and number/string/bool/time order                    |
| `OpaqueSortValue`     | Immutable custom scalar owned, compared and encoded by a custom codec                    |
| `NewSortValue`        | Standard scalar with UTC millisecond timestamps; decimals have no BSON range restriction |
| `Position`            | Keys and ID are `SortValue`; JSON identity example: `"i":{"n":42}`                       |
| `SortValue.Valid`     | Whether a value is held (false only for the zero value) without boxing it like `Value`   |

Custom codecs must preserve every value exactly, define a total order and change `Name()` when their semantics change. Listing engines bind
that name and timestamp precision into tokens. Standalone callers must include equivalent versioning in their own bindings.
The MongoDB engine (`backend/mongo`) uses native BSON cursor values and BSON ordering; other engines can select their own position codec.

A slice pager over a nil codec serves a single page and fails a presented token with `ErrCodecRequired`. `PageSize` is the lower-level clamp
for callers that apply their own defaults and maximums.

## Error classification

`Classify(err)` reports a page-token failure independently of the transport: `FailureMalformed` (invalid, tampered, foreign, decoded badly, bound to
another sort, filter or subject, or presented without a codec) or `FailureExpired`; any other error, including `ErrPayloadTooLarge` and
`ErrWeakKey`, is `FailureNone`. `Failure.String()` is the reason a transport reports (`malformed`, `expired`) in a field violation on the
page-token field.

`FieldError(err, field)` does that mapping: a token failure becomes `*coreerrs.FieldError{Field: field, Reason: Classify(err).String(), Err: err}`
(so `errors.Is` matches both `coreerrs.ErrInvalidArgument` and the token error), any other error is returned unchanged, and nil stays nil.

`TokenFieldError(err)` is `FieldError(err, TokenField)`. `TokenField` (`pageToken`) and `SizeField` (`pageSize`) are the AIP-158 JSON names of
the request fields.

```go
cursor, first, err := typed.Resolve(req.PageToken, bindings)
if err != nil {
    return pagetoken.TokenFieldError(err)
}
```

A storage pager that resolves tokens itself (such as the keyset reader of the MongoDB list engine) is mapped the same way; the caller wraps any other
error itself:

```go
page, err := store.FindPage(ctx, codec, query)
if pagetoken.Classify(err) != pagetoken.FailureNone {
    return pagetoken.TokenFieldError(err)
}
if err != nil {
    return coreerrs.WrapOperation(err, "list items")
}
```

## Testing

[`pagetokentest`](pagetokentest/README.md) builds codecs with fixed signing keys (`Key`, `OtherKey`) for tests.

## Errors

| Error                 | Meaning                                                                                                            |
|-----------------------|--------------------------------------------------------------------------------------------------------------------|
| `ErrInvalidToken`     | Malformed token, bad signature or issue time in the future                                                         |
| `ErrExpiredToken`     | Token older than the TTL                                                                                           |
| `ErrSortChanged`      | Query sort differs from the token's                                                                                |
| `ErrFilterChanged`    | Query filter differs from the token's                                                                              |
| `ErrSubjectMismatch`  | Token issued to another principal                                                                                  |
| `ErrPayloadTooLarge`  | `Issue` payload over `MaxPayloadLength`                                                                            |
| `ErrWeakKey`          | Signing key shorter than `MinKeyLength`                                                                            |
| `ErrCodecRequired`    | Token presented to a `Typed`/`Offset`, a slice pager or a list engine without a codec                              |
| `ErrNegativeOffset`   | `Offset.Issue` called with a negative offset (caller bug)                                                          |
| `ErrUnencryptedToken` | Signed token resolved under `WithStrictEncryption`; wraps `ErrInvalidToken` (classified `malformed`)               |
| `ErrUnsortableValue`  | `NewSortValue` given nil or a non-scalar; a decoded sort value with several members or a null member               |

## Payload configuration

| API            | Purpose                               |
|----------------|---------------------------------------|
| `PayloadCodec` | Concurrency-safe cursor serialization |

Payload bounds apply to signed and encrypted tokens. Scratch buffers belong to each codec instance. Applications changing payload representation
must version their bindings; all replicas resolving a token must use compatible limits and codecs. Boolean options take an explicit `bool`.

## Options

| Option                 | Type          | Default             | Behavior                                                                                                                                                                                                                                                       |
|------------------------|---------------|---------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `WithClock`            | `Option`      | time.Now            | Replace the instance clock, typically for deterministic expiry tests.                                                                                                                                                                                          |
| `WithEncryption`       | `Option`      | false               | Encryption issues encrypted tokens (AES-256-GCM under a key derived from the signing key) whose payload and bindings the client cannot read. Signed tokens are still accepted, so it can be enabled replica by replica: every codec resolves encrypted tokens. |
| `WithExpiryDisabled`   | `Option`      | false               | ExpiryDisabled makes tokens valid indefinitely, ignoring ttl.                                                                                                                                                                                                  |
| `WithMaxClockSkew`     | `Option`      | DefaultMaxClockSkew | MaxClockSkew bounds how far in the future an issue time may lie.                                                                                                                                                                                               |
| `WithMaxPayloadLength` | `Option`      | MaxPayloadLength    | MaxPayloadLength bounds both issued and accepted payloads.                                                                                                                                                                                                     |
| `WithPreviousKeys`     | `Option`      | nil / unset         | PreviousKeys verify tokens signed before a key rotation; tokens are always issued with the current key.                                                                                                                                                        |
| `WithStrictEncryption` | `Option`      | false               | StrictEncryption issues encrypted tokens and rejects signed ones (ErrUnencryptedToken): the final step of a rollout, once every signed token in circulation has expired.                                                                                       |
| `WithTTL`              | `Option`      | DefaultTTL          | Set token lifetime; expiry is disabled only by WithExpiryDisabled.                                                                                                                                                                                             |
| `WithPayloadCodec`     | `TypedOption` | JSONPayloadCodec{}  | Replace JSON encoding of typed cursor payloads.                                                                                                                                                                                                                |

## Defaults and limits

| Constant              | Value            |
|-----------------------|------------------|
| `DefaultTTL`          | `24 * time.Hour` |
| `DefaultMaxClockSkew` | `time.Minute`    |
| `MinKeyLength`        | `32`             |
| `MaxPayloadLength`    | `1024`           |

## Named values

| Constant           | Meaning                                                                                                                                                                                 |
|--------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `TokenField`       | "pageToken"                                                                                                                                                                             |
| `SizeField`        | "pageSize"                                                                                                                                                                              |
| `SortClassNone`    | SortClassNone is the class of the zero SortValue, which orders before every other value.                                                                                                |
| `SortClassNumber`  | SortClassNumber holds integers, floats and decimals, which compare by their exact values.                                                                                               |
| `SortClassString`  | SortClassString holds strings, which compare byte-wise.                                                                                                                                 |
| `SortClassBool`    | SortClassBool holds bools: false orders before true.                                                                                                                                    |
| `SortClassTime`    | SortClassTime holds normalized timestamps.                                                                                                                                              |
| `FailureNone`      | FailureNone means the error is not a page-token failure.                                                                                                                                |
| `FailureMalformed` | FailureMalformed covers a token that is not ours, was tampered with, does not decode, was issued for another list, filter, sort or subject, or was presented to a list without a codec. |
| `FailureExpired`   | FailureExpired is a genuine token older than the codec TTL.                                                                                                                             |

## API

| Symbol                                  | Purpose                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
|-----------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `FieldError`                            | FieldError maps a page-token failure to a field violation on field (for example `TokenField`): for an error `Classify` recognizes it returns a [*coreerrs.FieldError] whose Reason is the failure class ("malformed" or "expired") and whose Err is err, so errors.Is still matches both the token error and `coreerrs.ErrInvalidArgument`. Any other error, including a server-side failure such as `ErrWeakKey`, is returned unchanged, and nil yields nil. |
| `TokenFieldError`                       | TokenFieldError is `FieldError` on `TokenField`, the page-token field of an AIP-158 list request.                                                                                                                                                                                                                                                                                                                                                             |
| `Bindings`                              | Bindings tie a token to the query it was issued for. Each value is an opaque fingerprint chosen by the caller; `Codec.Resolve` rejects a token whose bindings differ.                                                                                                                                                                                                                                                                                         |
| `Codec`, `Codec.Issue`, `Codec.Resolve` | The token codec: issue a payload under bindings; verify a token (signature, expiry, bindings) and return its payload.                                                                                                                                                                                                                                                                                                                                         |
| `New`                                   | New returns a Codec signing with key, which must be at least `MinKeyLength` bytes and the same on every replica that resolves the tokens.                                                                                                                                                                                                                                                                                                                     |
| `Clock`                                 | Clock returns the current time. Tests replace it; production uses `time.Now`.                                                                                                                                                                                                                                                                                                                                                                                 |
| `Option`                                | Option is a functional option for configuring options.                                                                                                                                                                                                                                                                                                                                                                                                        |
| `SortClass`                             | SortClass is the comparison class used by the built-in scalar preset.                                                                                                                                                                                                                                                                                                                                                                                         |
| `SortValue`                             | SortValue holds an immutable typed scalar. NewSortValue builds the standard representation; PositionCodec can normalize, order and encode other scalars using OpaqueSortValue.                                                                                                                                                                                                                                                                                |
| `ComparePositions`                      | ComparePositions returns the comparison of positions under a sort whose keys are descending where desc says so, followed by the id tie-breaker, descending when idDesc is set. comparator defines scalar order.                                                                                                                                                                                                                                               |
| `SlicePage`                             | SlicePage is one page of a `SlicePager` read.                                                                                                                                                                                                                                                                                                                                                                                                                 |
| `SlicePager`                            | SlicePager pages a slice held in memory — a small catalog or configuration collection — with signed page tokens. Build it with `NewSlicePager` (keyset position: an item added or removed between pages neither repeats nor skips the others) or `NewSliceOffsetPager` (position by index).                                                                                                                                                                   |
| `NewBindings`                           | NewBindings returns the bindings of a list request. subject names the list (for example its RPC) so tokens of different lists never cross; sort fingerprints the order; filter holds the request fields the token is bound to — never the page size, which may change between pages.                                                                                                                                                                          |
| `Signer`                                | Signer signs and verifies token payloads; `Typed` and `Offset` build on it and [*Codec] implements it.                                                                                                                                                                                                                                                                                                                                                        |
| `Typed`                                 | Typed issues and resolves tokens carrying a cursor of type C, encoded as JSON by default, or a configured PayloadCodec. The zero value and a Typed over a nil `Signer` issue no tokens, so a list without page tokens serves a single page.                                                                                                                                                                                                                   |
| `NewTyped`                              | NewTyped returns a Typed signing with codec, which may be nil (including a typed nil such as a nil *Codec).                                                                                                                                                                                                                                                                                                                                                   |
| `Offset`                                | Offset issues and resolves tokens carrying a non-negative offset, for small in-memory catalogs that are paged by position. Like `Typed`, the zero value and an Offset over a nil codec issue no tokens.                                                                                                                                                                                                                                                       |
| `NewOffset`                             | NewOffset returns an Offset signing with codec, which may be nil.                                                                                                                                                                                                                                                                                                                                                                                             |
| `Failure`                               | Failure classifies a page-token error for a transport, independently of how the transport reports it.                                                                                                                                                                                                                                                                                                                                                         |
| `Classify`                              | Classify reports which page-token failure err is, matching the package errors with errors.Is anywhere in its chain. `ErrPayloadTooLarge` and `ErrWeakKey` are server-side failures, not token failures, and classify as `FailureNone` like nil and any other error.                                                                                                                                                                                           |
| `PayloadCodec`                          | Concurrency-safe cursor serialization of `Typed` (`Marshal`, `Unmarshal`).                                                                                                                                                                                                                                                                                                                                                                                    |
| `JSONPayloadCodec`                      | JSONPayloadCodec is the default cursor representation.                                                                                                                                                                                                                                                                                                                                                                                                        |
| `TypedOption`                           | TypedOption is a functional option for configuring typedOptions.                                                                                                                                                                                                                                                                                                                                                                                              |

## Related packages

- [go-aip](../../README.md)
- [pagination/page](../page/README.md)
- [pagination guide](../../docs/pagination.md)
