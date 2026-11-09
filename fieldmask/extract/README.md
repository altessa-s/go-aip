# extract

```go
import "github.com/altessa-s/go-aip/fieldmask/extract"
```

Package `extract` reads field masks off request messages: the AIP-134 `update_mask` with its sibling resource, and the legacy AIP-157 `read_mask`. It
works on descriptors only and has no gRPC dependency; the helpers return `*fieldmaskpb.FieldMask`, which [fieldmask](../README.md)
`FromProtoFieldMask` turns into a `FieldMask`.

## Request messages

`IsCanonicalUpdateRequest` reports whether a request has one top-level `update_mask` and exactly one other singular message field, the AIP-134 shape
the strict gRPC interceptor of go-atlas extracts automatically. Read the `update_mask` and the read mask off a request message; the helpers return
`*fieldmaskpb.FieldMask`.

| AIP                          | Transport                                                       | Helper                         |
|------------------------------|-----------------------------------------------------------------|--------------------------------|
| AIP-134                      | `update_mask` field on the request message                      | `UpdateMask` / `DefaultUpdate` |
| AIP-157 via AIP-161 (legacy) | `read_mask` field on the request message, deprecated by AIP-161 | `ReadMask` / `DefaultRead`     |

Reflection helpers stay gRPC-unaware (descriptors only) and work from non-gRPC callers too. Reading the modern AIP-157 `x-goog-fieldmask` metadata
header is transport-specific, so go-atlas provides that extractor with its gRPC interceptor and chains it before `DefaultRead`.

| Function                                 | Description                                                                                      |
|------------------------------------------|--------------------------------------------------------------------------------------------------|
| `UpdateMask(req, opts...)`               | Read the mask and unambiguous sibling resource.                                                  |
| `ReadMask(req, opts...)`                 | Return the `read_mask` on a Get/List/Search request. Defaults to `"read_mask"`.                  |
| `IsCanonicalUpdateRequest(req, opts...)` | Report the AIP-134 shape: one top-level mask field and exactly one other singular message field. |
| `SetUpdateMask(req, mask, opts...)`      | Writeback helper used after `Apply` removes `OUTPUT_ONLY` entries. Pass `nil` to clear.          |

`SetUpdateMask` returns `ErrFieldNotSettable` when the request does not expose an `update_mask` field of type `google.protobuf.FieldMask`.

## Custom extractors

For requests that do not follow the AIP-134 flat shape, most commonly when the mask lives inside an `Options` sub-message (`req.Options.UpdateMask`),
use the function-type API: call the extractor directly, or register it on the go-atlas gRPC interceptor.

| Symbol                                                                   | Purpose                                                                                        |
|--------------------------------------------------------------------------|------------------------------------------------------------------------------------------------|
| `UpdateFunc`                                                             | `func(ctx, req) (mask, resource, writeback, ok)`: transport-agnostic shape, no gRPC dependency |
| `ReadFunc`                                                               | `func(ctx, req) (mask, ok)`                                                                    |
| `NewUpdateFunc[ReqT, ResT proto.Message](getMask, getResource, setMask)` | Typed getter adapter; rejects mismatched or nil values                                         |
| `NewReadFunc[ReqT proto.Message](getMask)`                               | Generic builder for read masks                                                                 |
| `DefaultUpdate(opts ...Option)` / `DefaultRead(opts ...Option)`          | Adapt reflection helpers to the function types                                                 |
| `ChainRead(fns ...)` / `ChainUpdate(fns ...)`                            | First extractor that signals `ok=true` wins; nil entries are skipped                           |

From `ExampleChainRead` ([extractor_example_test.go](extractor_example_test.go)), where `pb` holds the request messages:

```go
read := extract.ChainRead(
    extract.NewReadFunc(func(r *pb.NestedGetResourceRequest) *fieldmaskpb.FieldMask {
        return r.GetOptions().GetReadMask()
    }),
    extract.DefaultRead(), // top-level read_mask fallback
)

mask, ok := read(ctx, req) // the nested read_mask, or the top-level one
```

`ExampleChainUpdate` in the same file shows the update flow: extract, `Apply`, `DropIdentifiers`, writeback.

## Options and defaults

| Option / constant            | Type / value    | Behavior                                                                                            |
|------------------------------|-----------------|-----------------------------------------------------------------------------------------------------|
| `WithMaskField`              | `Option`        | Select the request mask field for descriptor-based extraction.                                      |
| `WithResourceField`          | `Option`        | Select the request resource field for descriptor-based extraction.                                  |
| `DefaultUpdateMaskFieldName` | `"update_mask"` | The mask field of `UpdateMask`, `SetUpdateMask`, `IsCanonicalUpdateRequest` without `WithMaskField` |
| `DefaultReadMaskFieldName`   | `"read_mask"`   | The mask field of `ReadMask` without `WithMaskField`                                                |

## Errors

| Error                 | Returned by     | Meaning                                                                                |
|-----------------------|-----------------|----------------------------------------------------------------------------------------|
| `ErrFieldNotSettable` | `SetUpdateMask` | The request does not expose an `update_mask` field of type `google.protobuf.FieldMask` |

## Related packages

- [fieldmask](../README.md)
- [fieldmask/update](../update/README.md)
- [projection](../../projection/README.md)
