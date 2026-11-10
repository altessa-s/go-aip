# fieldinfo

```go
import "github.com/altessa-s/go-aip/fieldinfo"
```

Package `fieldinfo` applies the `google.api.field_info` annotation of [AIP-202](https://google.aip.dev/202) to protobuf payloads. It reads the
format a string field declares (`UUID4`, `IPV4`, `IPV6`, `IPV4_OR_IPV6`), validates values against the RFC behind each format, normalizes them the
way AIP-202 allows, compares them by value instead of as text, and walks a whole message to report every invalid value with its field path. The
[guide](../docs/fieldinfo.md) maps every AIP-202 rule to its function. [`fieldbehavior`](../fieldbehavior/README.md) runs the same checks in its
AIP-203 walk, so one pass validates field behaviors and formats.

## Contract

`Lookup(fd)` returns the annotation of a field descriptor: its `Format` and its `ReferencedTypes` (the message types a `google.protobuf.Any` field
may hold, exposed only, never validated). The decoded annotation is cached per descriptor in a package-wide LRU bounded to 4096
descriptors, so dynamic descriptors are never retained without limit. `FormatOf(fd)` returns the format alone without allocating.

`ValidateValue`, `NormalizeValue` and `Equal` work on a single value:

| Format         | Accepted                                                                                                           | Normalized form                                                                            |
|----------------|--------------------------------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------|
| `UUID4`        | 8-4-4-4-12 hexadecimal digits of either case, version nibble `4`, RFC 9562 variant (`8`, `9`, `a`, `b`)            | lowercase                                                                                  |
| `IPV4`         | four dot-separated decimal octets of 1–3 digits, each ≤ 255; leading zeros accepted and read as decimal            | leading zeros stripped: `001.022.233.040` → `1.22.233.40`                                  |
| `IPV6`         | any RFC 4291 text form of either case, `::` compression, embedded dotted IPv4 tail; no zone (`%eth0`), no brackets | RFC 5952: `2001:0DB8:0::0` → `2001:db8::`; mapped IPv4 keeps its tail (`::ffff:192.0.2.1`) |
| `IPV4_OR_IPV6` | an `IPV6` value when the input holds a colon, an `IPV4` value otherwise                                            | per family                                                                                 |

`Equal(f, a, b)` compares the normalized forms (AIP-202 forbids comparing these values as plain text). An IPv4 address and its IPv4-mapped IPv6 form
are different values. `FORMAT_UNSPECIFIED` accepts every value; a format value this release does not know is `ErrUnknownFormat` from the value
functions and is skipped by the message walks.

`Validate(msg)` walks every populated message — singular sub-messages, repeated elements and map values, at any depth — and checks every populated
string field with a format: a singular value, each element of a repeated field, each value of a map field (keys are not checked). An unset field
is not checked; an explicitly set empty string is, and is invalid for every AIP-202 format. A format on a non-string field (AIP-202 allows it on
`string` only; api-linter rule `core::0202::string-only-format` reports it) is ignored. Message types whose descriptor tree has no formatted field
are skipped without walking their values (cached per descriptor, bounded to 1024 message types).

`Normalize(msg)` validates first and, only when every value is valid, rewrites each value in its normalized form: use it on input before
persisting it so stored values compare as text. On error the message is untouched.

`Checker` is the compiled configuration behind the package-level functions: build one with `NewChecker(opts...)` and reuse it; it is immutable and
safe for concurrent use. `Checker.CheckField`, `Checker.HasFieldFormat` and `Checker.HasFormats` let another walker (fieldbehavior's) check formats
in its own pass, building a field's path only when the field carries a format.

### Custom formats (email)

AIP-202 has no email format and `google.api.FieldInfo.Format` has no value for one; this package does not invent an annotation. A `Registry` holds
formats under names the caller chooses and assigns them to fields by full name:

```go
var reg fieldinfo.Registry
_ = reg.Register("email", fieldinfo.CustomFormat{
    Validate:  func(s string) error { _, err := mail.ParseAddress(s); return err },
    Normalize: strings.ToLower, // optional
    Reason:    "must be an email address",
})
_ = reg.Assign("library.v1.Author.email", "email")
checker := fieldinfo.NewChecker(fieldinfo.WithRegistry(&reg))
```

A field that has both an AIP-202 format and a custom one must satisfy both: the AIP-202 format first, then the custom one on the normalized value.
The validator's own error is not reported (a `Violation` carries the registered `Reason`), so a validator cannot leak the value into an API error.
`NewChecker` copies the registry; later changes do not reach a built checker.

## Paths

Violation paths are dot-separated, spelled with proto field names by default (`dns_servers.1`, `named_interfaces.lan.address`), matching
fieldbehavior and fieldmask paths; `WithJSONNames(true)` spells them with JSON names (`dnsServers.1`). Repeated elements are indexed, map values
keyed; a key that is empty or holds a dot or a backtick is backtick-quoted with every backtick doubled. Map entries are visited in key order, so
violations are deterministic.

## Errors

| Error                 | When                                                                                                                                                                                                  |
|-----------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `*ViolationError`     | `Validate`/`Normalize`: one or more invalid values; `Violations []Violation` carries `Path`, `Format`, `Reason`. Matches `ErrInvalidValue` and go-atlas-core `ErrInvalidArgument` (INVALID_ARGUMENT). |
| `*ValueError`         | `ValidateValue`/`NormalizeValue`/`Equal`: an invalid value; carries `Format` and `Reason`, never the value. Matches `ErrInvalidValue` and `ErrInvalidArgument`.                                       |
| `ErrInvalidValue`     | Every invalid value; matched by `*ViolationError` and `*ValueError`.                                                                                                                                  |
| `ErrUnknownFormat`    | A value function called with a format this release does not know.                                                                                                                                     |
| `ErrMaxDepthExceeded` | A walk descended more nested messages than `WithMaxDepth`.                                                                                                                                            |
| `ErrInvalidRegistry`  | `Registry.Register`/`Assign`: empty name, nil validator, duplicate name or field, unregistered name.                                                                                                  |

Values are never echoed in error text: IP addresses may be personal data.

## Options

| Option          | Default           | Effect                                                                                         |
|-----------------|-------------------|------------------------------------------------------------------------------------------------|
| `WithMaxDepth`  | `DefaultMaxDepth` | Bound the nested messages a walk descends; 0 is unlimited, a negative value keeps the default. |
| `WithJSONNames` | `false`           | Spell violation paths with JSON field names.                                                   |
| `WithRegistry`  | nil               | Custom formats and the fields they apply to.                                                   |

## Defaults and limits

| Constant          | Value | Bounds                                              |
|-------------------|-------|-----------------------------------------------------|
| `DefaultMaxDepth` | `32`  | Nested messages walked (client-controlled nesting). |

The caches are package-wide LRUs of a fixed size: 4096 field descriptors for decoded annotations and 1024 message descriptors for the "has formats"
answer.

Value parsing is bounded too: input longer than the longest valid value of its format (36 bytes for a UUID, 15 for IPv4, 45 for IPv6) is rejected
before it is parsed. Work is linear in the populated size of the message.

## API

| Symbol                                                         | Purpose                                                                                                                |
|----------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------|
| `Format`, `Unspecified`, `UUID4`, `IPv4`, `IPv6`, `IPv4OrIPv6` | The `google.api.FieldInfo.Format` enum and its values                                                                  |
| `Known(f)`                                                     | Whether this release validates format `f`                                                                              |
| `Info`, `Lookup(fd)`, `FormatOf(fd)`                           | The decoded annotation of a field (`Format`, `ReferencedTypes`); the format alone                                      |
| `ValidateValue`, `NormalizeValue`, `Equal`                     | One value: validate, normalize, compare by normalized form                                                             |
| `Validate`, `Normalize`                                        | A whole message, with the package-level checker built from the options                                                 |
| `Checker`, `NewChecker`                                        | The compiled configuration: `Validate`, `Normalize`, and `CheckField`/`HasFieldFormat`/`HasFormats` for another walker |
| `Registry`, `CustomFormat`                                     | Caller-named custom formats (`Register`) assigned to fields by full name (`Assign`)                                    |
| `Violation`, `ViolationError`, `ValueError`                    | A reported value (`Path`, `Format`, `Reason`); the errors of the message and value functions                           |
| `ReasonUUID4`, `ReasonIPv4`, `ReasonIPv6`, `ReasonIPv4OrIPv6`  | The reason of each AIP-202 format violation                                                                            |
| `ErrInvalidValue`                                              | Matched by every `*ViolationError` and `*ValueError`                                                                   |
| `Option`                                                       | `WithMaxDepth`, `WithJSONNames`, `WithRegistry`                                                                        |

## AIP-202 conformance

| Rule                                                                             | Level | How it is met                                                                                                     |
|----------------------------------------------------------------------------------|-------|-------------------------------------------------------------------------------------------------------------------|
| UUID4 per RFC 4122 (now RFC 9562); may be normalized to lowercase                | MAY   | `ValidateValue` checks form, version 4 and variant; `NormalizeValue`/`Normalize` lowercase.                       |
| IPV4 per RFC 791; may be condensed by stripping leading zeros                    | MAY   | Leading zeros accepted as decimal; `Normalize` strips them.                                                       |
| IPV6 per RFC 4291; may be normalized to lowercase, zeros compressed per RFC 5952 | MAY   | `netip` parsing without zones; `Normalize` emits the RFC 5952 form.                                               |
| IPV4_OR_IPV6: either family, normalized per family                               | MAY   | Family chosen by the presence of a colon.                                                                         |
| A format applies to `string` fields only                                         | MUST  | Schema rule for API authors: formats on other fields are ignored here (api-linter enforces the schema).           |
| Equivalence must not use primitive text comparison                               | MUST  | `Equal` compares normalized values; `Normalize` before persistence makes stored text comparable.                  |
| `referenced_types` on `google.protobuf.Any` fields                               | —     | Exposed by `Lookup`; not validated.                                                                               |
| Adding or changing a format on an existing field is not backwards compatible     | MUST  | API-evolution rule for authors; nothing to enforce at runtime.                                                    |
| New formats need an RFC or an AIP                                                | —     | Unknown enum values are skipped by walks, `ErrUnknownFormat` from value functions; custom formats use `Registry`. |

## See also

- [fieldinfo guide](../docs/fieldinfo.md)
- [fieldbehavior](../fieldbehavior/README.md) — AIP-203 rules and formats in one walk (`WithFieldFormats`, `WithFormatChecker`).
- [AIP-202](https://google.aip.dev/202), [RFC 9562](https://www.rfc-editor.org/rfc/rfc9562), [RFC 4291](https://www.rfc-editor.org/rfc/rfc4291),
  [RFC 5952](https://www.rfc-editor.org/rfc/rfc5952).
