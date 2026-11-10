# extract

```go
import "github.com/altessa-s/go-aip/fieldmask/extract"
```

Package `extract` reads field masks off request messages: the AIP-134 `update_mask` with its sibling resource, and the legacy AIP-157 `read_mask`. It
works on descriptors only and has no gRPC dependency; the helpers return `*fieldmaskpb.FieldMask`, which [fieldmask](../README.md)
`FromProtoFieldMask` turns into a `FieldMask`.

## Request messages

Default lookup selects the singular `google.protobuf.FieldMask` field declared as `update_mask` or `read_mask`. Only when that declared name is
absent does it try `updateMask` or `readMask`, respectively. If both names are declared, snake_case wins, even if that field is unset or has the
wrong type. Lookup uses declared proto names, not JSON names. `WithMaskField(name)` selects an exact declared name without fallback, including
when the override is `"update_mask"` or `"read_mask"`.

This rule is shared by `UpdateMask`, `SetUpdateMask`, `DefaultUpdate`, `IsCanonicalUpdateRequest`, `ReadMask`, and `DefaultRead`.
`IsCanonicalUpdateRequest` reports whether a request has the selected top-level mask field and exactly one other singular non-mask message field,
the AIP-134 shape the strict gRPC interceptor of go-atlas extracts automatically. Neither field needs to be populated for this shape check.
`UpdateMask` requires both the mask and resource to be populated; `ReadMask` requires a populated mask.

| AIP                          | Transport                                                       | Helper                         |
|------------------------------|-----------------------------------------------------------------|--------------------------------|
| AIP-134                      | `update_mask` or `updateMask` field on the request message      | `UpdateMask` / `DefaultUpdate` |
| AIP-157 via AIP-161 (legacy) | `read_mask` or `readMask` field on the request message, deprecated by AIP-161 | `ReadMask` / `DefaultRead` |

Reflection helpers stay gRPC-unaware (descriptors only) and work from non-gRPC callers too. Reading the modern AIP-157 `x-goog-fieldmask` metadata
header is transport-specific, so go-atlas provides that extractor with its gRPC interceptor and chains it before `DefaultRead`.

| Function                                 | Description                                                                                      |
|------------------------------------------|--------------------------------------------------------------------------------------------------|
| `UpdateMask(req, opts...)`               | Read the mask and unambiguous sibling resource.                                                  |
| `ReadMask(req, opts...)`                 | Read the mask on a Get/List/Search request using the selection rule above.                       |
| `IsCanonicalUpdateRequest(req, opts...)` | Report the AIP-134 shape: a top-level mask field and exactly one other singular non-mask message field. |
| `SetUpdateMask(req, mask, opts...)`      | Writeback helper used after `Apply` removes `OUTPUT_ONLY` entries. Pass `nil` to clear.          |

`SetUpdateMask` returns `ErrFieldNotSettable` when the selected field is absent or is not a singular `google.protobuf.FieldMask`.
It can populate an unset field or clear it with `nil`, and writes to the same field selected by `UpdateMask`.

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
| `WithMaskField`              | `Option`        | Select an exact declared mask field name, with no camelCase or JSON-name fallback.                  |
| `WithResourceField`          | `Option`        | Select the request resource field for descriptor-based extraction.                                  |
| `DefaultUpdateMaskFieldName` | `"update_mask"` | Preferred declared update-mask name; default lookup tries `updateMask` only if it is absent.      |
| `DefaultReadMaskFieldName`   | `"read_mask"`   | Preferred declared read-mask name; default lookup tries `readMask` only if it is absent.          |

## Errors

| Error                 | Returned by     | Meaning                                                                                |
|-----------------------|-----------------|----------------------------------------------------------------------------------------|
| `ErrFieldNotSettable` | `SetUpdateMask` | The selected mask field is absent or is not a singular `google.protobuf.FieldMask` |

## Related packages

- [fieldmask](../README.md)
- [fieldmask/update](../update/README.md)
- [projection](../../projection/README.md)
