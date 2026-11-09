# Pagination

[pagination/pagetoken](../pagination/pagetoken/) provides authenticated continuation tokens, typed cursor helpers, and slice pagers.
[pagination/page](../pagination/page/) defines the driver-free Page[T] result used by listing engines.

## Signing and request bindings

`New(key, opts...)` returns a Codec implementing Signer. The default wire format uses HMAC-SHA256, a 24-hour TTL, one minute of allowed future clock
skew, and a 1,024-byte payload limit. Signing keys must contain at least 32 bytes. WithTTL, WithMaxClockSkew, WithMaxPayloadLength, WithClock, and
WithExpiryDisabled configure these policies.

Bindings identify the list subject, canonical sort, filter, and scope. NewBindings length-prefixes filter components, preventing concatenation
ambiguity. Include an application contract version when semantics can change. Page size is deliberately not a binding, so a client may request a
different size for the next page. A token is not a snapshot or a substitute for current authorization.

```go
package example

import "github.com/altessa-s/go-aip/pagination/pagetoken"

type Cursor struct { ID int64 }

func NextToken(key []byte, id int64) (string, error) {
    signer, err := pagetoken.New(key)
    if err != nil {
        return "", err
    }
    tokens := pagetoken.NewTyped[Cursor](signer)
    bindings := pagetoken.NewBindings("widgets.List", "id asc", "tenant:42", "contract:v1")
    return tokens.Issue(Cursor{ID: id}, bindings)
}
```

Load the key from application secret configuration and reuse the codec. Resolve with the same bindings. Typed.Resolve returns cursor, first-page flag,
and error; an empty token is the first page. A nil signer issues no token and rejects a nonempty token. TokenFieldError maps failures to pageToken while
retaining causes; Classify distinguishes genuine expiry from malformed, tampered, or foreign tokens.

## Rotation and encryption

Construct the new codec with the current key and WithPreviousKeys(retiredKeys...). It verifies old tokens but signs new ones only with the current key.
Retain old verification keys for the entire acceptance window of outstanding tokens; replicas must share the intended verification set.

Signed payloads are readable. WithEncryption(true) issues AES-256-GCM tokens using a key derived from the signing key, while still accepting signed
tokens; every codec can resolve encrypted tokens. After all signed tokens have expired, WithStrictEncryption(true) requires encryption and rejects
signed input with ErrUnencryptedToken. Previous keys support rotation of both formats.

## Codecs and positions

| Port or type          | Contract                                                                      |
|-----------------------|-------------------------------------------------------------------------------|
| `Signer`              | Issue and resolve authenticated payload bytes with Bindings                   |
| `PayloadCodec`        | Marshal and unmarshal typed payloads; JSONPayloadCodec is the default         |
| `Typed[C]`            | Cursor-specific Issue/Resolve; WithPayloadCodec changes the representation    |
| `Offset`              | Nonnegative integer position for small catalogs, constructed with NewOffset   |
| `ValueComparator`     | Compare normalized SortValue scalars                                          |
| `PositionCodec`       | PayloadCodec plus comparison, Normalize(value, precision), and semantic Name  |
| `ScalarPositionCodec` | scalar-v2 typed representation for numbers, strings, booleans, and timestamps |
| `Position`            | Ordered SortValue keys plus a required typed SortValue ID                     |

Codecs and comparators must be concurrency-safe and preserve values exactly. Change PositionCodec.Name and request bindings when ordering or encoding
changes. OpaqueSortValue carries a custom immutable scalar that needs custom encoding and comparison. SQL custom positions must also provide values
compatible with their declared storage kinds.

The scalar position codec requires positive timestamp precision and normalizes to UTC. Listing defaults to milliseconds; stored values must align so
cursor rounding cannot duplicate or skip rows. SQL and memory support typed scalar identities; the MongoDB list engine preserves BSON scalar
identity types, including ObjectID.

## Pages and storage adapters

`Page[T]` contains Items and NextPageToken; an empty token marks the final page. `SlicePager` works with a sorted source, cursor extractor, and
comparator. `NewSliceOffsetPager` is available when offset positions are appropriate. `ResolvePageSize` rejects negative input and applies defaults and
a maximum; PageSize is the lower-level clamping helper.

The MongoDB list engine ([backend/mongo](../backend/mongo/README.md#list-engine)) reads keyset pages through the driver and signs its tokens
with a codec from this package, like the other [listing engines](listing.md).

[pagetokentest](../pagination/pagetoken/pagetokentest/) supplies fixed-key codecs for tests. Production code must supply its own signing
material.
