# Change validation (validate_only)

[validateonly](../validateonly/) implements AIP-163: a mutation with `validate_only` set runs every check of the live request and returns the
live response without writing. [fieldmask](fieldmask.md) `Patch` supports it with `WithValidateOnly`.

## Conformance

| Rule                                                                                  | Implemented by                     | Behavior                                                                                                                                                                                                           |
|---------------------------------------------------------------------------------------|------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| AIP-163: validate without executing, same response (MAY)                              | `Run`, `update.WithValidateOnly`   | `Run` returns the validate step's response without calling commit; `Patch.Run` returns the merged resource without calling `Save` or `Create`.                                                                     |
| AIP-163: a `bool validate_only` request field (SHOULD)                                | `Requested`                        | Reads a singular bool `validate_only`; a field of another kind is `ErrInvalidField`, so it never silently runs the write.                                                                                          |
| AIP-163: permission checks and all live validation run (MUST)                         | `Run` (validate step), `Patch.Run` | The validate step is the same code for both requests; `Patch.Run` parses and checks the etag, the mask and the AIP-203 behaviors and merges.                                                                       |
| AIP-163: fail when the live request would fail (MUST)                                 | `Run`, `Patch.Run`                 | Errors of the checks are returned unchanged (`ErrConflict` for a stale etag, `FieldError` for a bad mask). Failures only the store can detect (unique indexes, compare-and-swap) are outside a validation request. |
| AIP-163: omit auto-generated fields (SHOULD)                                          | `OmitGenerated`; `Patch.Run`       | `OmitGenerated` clears OUTPUT_ONLY values; `Patch.Run` computes no store-assigned values (an update keeps the stored ones, an allow_missing create has none).                                                      |
| AIP-163: `validate_only` on mutating methods of declarative-friendly resources (MUST) | schema                             | Outside this package.                                                                                                                                                                                              |

## Response choices

| Method                                      | Validation response                                                                                                 |
|---------------------------------------------|---------------------------------------------------------------------------------------------------------------------|
| Create                                      | The would-be resource without server-generated values (`OmitGenerated`; a generated name is cleared by the caller). |
| Update                                      | The merged resource (`Patch.Run` with `WithValidateOnly(true)`), with the stored OUTPUT_ONLY values.                |
| Update with allow_missing, resource missing | The request resource without its OUTPUT_ONLY values, as `Create` would receive it.                                  |
| Delete                                      | What the live Delete returns: `google.protobuf.Empty`, or the resource for a soft delete.                           |

## See also

* [`validateonly`](../validateonly/README.md) — API and examples.
* [`etag.md`](etag.md) — the AIP-154 precondition a validation request checks.
* **AIP-163** — Change validation ([aip.dev/163](https://google.aip.dev/163)).
