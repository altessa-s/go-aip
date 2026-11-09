# pagetokentest

```go
import "github.com/altessa-s/go-aip/pagination/pagetoken/pagetokentest"
```

Provides page-token codecs with fixed signing keys for testing code built on github.com/altessa-s/go-aip/pagination/pagetoken.

## Contract

Fixed-key constructors are for tests only. They create reproducible signed or encrypted codecs and rotation fixtures; production services supply their
own keys to pagetoken.New. Clock-sensitive tests can configure the clock through ordinary codec options.

## Behavior

| Symbol                           | Description                                                                        |
|----------------------------------|------------------------------------------------------------------------------------|
| `Key`, `OtherKey`                | Distinct fixed keys of `pagetoken.MinKeyLength` bytes, a fresh copy on every call  |
| `Codec(tb, opts...)`             | Codec signing with `Key`; invalid options fail `tb`                                |
| `CodecWithKey(tb, key, opts...)` | Codec signing with `key`; a weak key or invalid options fail `tb`                  |
| `MustCodec(opts...)`             | Codec signing with `Key` for package-level test doubles; panics on invalid options |

A token signed with `OtherKey` is foreign (`pagetoken.FailureMalformed`) to a `Codec` unless it lists `OtherKey` in `pagetoken.WithPreviousKeys`.
Expiry tests drive the codec's clock with `pagetoken.WithClock`.

```go
codec := pagetokentest.Codec(t, pagetoken.WithClock(fake.Now))
foreign := pagetokentest.CodecWithKey(t, pagetokentest.OtherKey())
```

## Fixture ownership

| API          | Ownership                        |
|--------------|----------------------------------|
| `Key()`      | Fresh signing key bytes          |
| `OtherKey()` | Fresh distinct signing key bytes |

Mutating returned key bytes cannot affect another test or a future codec.

## Related packages

- [pagination/pagetoken](../README.md)
- [pagination guide](../../../docs/pagination.md)
