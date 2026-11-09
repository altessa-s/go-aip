# Field formats

[fieldinfo](../fieldinfo/) applies the `google.api.field_info` annotation of AIP-202: the standard format of a string field (`UUID4`, `IPV4`,
`IPV6`, `IPV4_OR_IPV6`) and the referenced types of a generic `google.protobuf.Any` field. [fieldbehavior](fieldbehavior.md) checks the formats in
the same walk as its AIP-203 rules.

## AIP-202 conformance

| AIP-202 rule                                                                      | Implemented by                                             | Behavior                                                                                                                                                 |
|-----------------------------------------------------------------------------------|------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------|
| UUID4: a version 4 UUID (RFC 4122, now RFC 9562); MAY be normalized to lowercase  | `ValidateValue`, `NormalizeValue`, `Validate`, `Normalize` | 8-4-4-4-12 hex of either case, version `4`, variant `10xx`; braces, `urn:uuid:` and the unhyphenated form rejected. Normalized to lowercase.             |
| IPV4: RFC 791; MAY be condensed by stripping leading zeros                        | same                                                       | Four decimal octets of 1–3 digits, ≤ 255. `001.022.233.040` is accepted (decimal, never octal) and normalized to `1.22.233.40`.                          |
| IPV6: RFC 4291; MAY be normalized to lowercase with zeros compressed (RFC 5952)   | same                                                       | Any RFC 4291 text form incl. an embedded IPv4 tail; zones and brackets rejected. Normalized to the RFC 5952 canonical form.                              |
| IPV4_OR_IPV6: either family, normalized per family                                | same                                                       | IPv6 rules when the value holds a colon, IPv4 rules otherwise.                                                                                           |
| A format MUST be applied to `string` fields only                                  | schema (api-linter `core::0202::string-only-format`)       | A format on another field kind is ignored by the walks.                                                                                                  |
| Equivalence MUST NOT use primitive text comparison                                | `Equal`, `Normalize`                                       | `Equal` compares normalized values; `Normalize` on input before persistence makes stored values comparable as text.                                      |
| `referenced_types` (Any fields only)                                              | `Lookup`                                                   | Exposed as `Info.ReferencedTypes`; not validated.                                                                                                        |
| Adding a format to an existing field, or changing it, is not backwards compatible | —                                                          | API-evolution rule for authors.                                                                                                                          |
| Formats are backed by an IETF RFC or a Google AIP                                 | `Known`, `Registry`                                        | Unknown enum values are skipped by walks (`ErrUnknownFormat` from value functions). Other formats — email has none — are caller-defined in a `Registry`. |

AIP-202 does not itself oblige a service to reject a malformed value (the annotation documents the format). This library treats a declared format
as an input contract: `fieldbehavior` input walks reject malformed values by default (`DefaultFieldFormats = true`), and `WithFieldFormats(false)`
opts out.

## Deliberate decisions

* **The AIP-202 UUID example is not a version 4 UUID.** `F47AC10B-58CC-0372-8567-0E02B2C3D479` has version nibble `0`; the format is named
  UUID4, so the version is enforced and that value is rejected.
* **Leading zeros in IPv4 are decimal.** AIP-202 allows condensing them, so they are accepted and stripped, never read as octal. An embedded IPv4
  tail inside an IPv6 value follows the stricter RFC 4291 grammar (no leading zeros).
* **No zones.** `fe80::1%eth0` names an interface-scoped address (RFC 4007), not an RFC 4291 address.
* **No email format.** The enum has none; a `Registry` maps a caller-chosen name to a validator and fields to names.
* **Paths use proto field names** by default to match fieldbehavior and fieldmask violation paths; `WithJSONNames(true)` switches to JSON names.
* **Bounded caches.** The annotation of a field (4096 field descriptors) and the "subtree has formats" answer (1024 message descriptors) are
  cached per descriptor in bounded, lock-free-read caches (CLOCK eviction).

## Quick start

```go
// Reject malformed values (INVALID_ARGUMENT with one violation per value).
if err := fieldinfo.Validate(req.GetHost()); err != nil {
    return nil, err
}

// Or validate and normalize before persisting.
if err := fieldinfo.Normalize(req.GetHost()); err != nil {
    return nil, err
}

// Compare by value, never as text.
same, err := fieldinfo.Equal(fieldinfo.IPv6, stored.GetAddress(), req.GetHost().GetAddress())
```

In one pass with the AIP-203 rules:

```go
if err := fieldbehavior.StripCreate(res); err != nil { return nil, err }      // OUTPUT_ONLY/IDENTIFIER + formats
if err := fieldbehavior.ValidateRequired(res); err != nil { return nil, err } // REQUIRED + formats
if err := fieldinfo.Normalize(res); err != nil { return nil, err }            // optional, before persistence
```

## See also

* [fieldinfo README](../fieldinfo/README.md) — API, errors, options, limits.
* [fieldbehavior guide](fieldbehavior.md).
* **AIP-202** — [aip.dev/202](https://google.aip.dev/202).
