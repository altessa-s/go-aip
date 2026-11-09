# fieldpolicy

```go
import "github.com/altessa-s/go-aip/fieldpolicy"
```

Shared immutable field policies for [filter](../filter/README.md), [orderby](../orderby/README.md) and [projection](../projection/README.md). The
package handles allow-lists and mappings from API paths to storage paths.

## Contract

AllowList supports exact names and wildcard prefixes. Mapping applies exact mappings first, then the longest matching prefix; Reach and Subtree expose
mapped descendants for authorization checks. Constructors capture owned configuration, and accessors return detached values where mutation would
affect policy.

Access is the shared field authorization port for filter, orderby, and projection. Existing allowlists and deny rules
remain restrictive when custom access is supplied. Storage renderers still validate and quote mapped names.

## Key types

| Type        | Purpose                                                                 |
|-------------|-------------------------------------------------------------------------|
| `AllowList` | Exact paths and `.*` wildcard prefixes; an unset list allows everything |
| `Mapping`   | Exact rewrites followed by the longest matching prefix rewrite          |

## Subtree mapping

`Subtree` and `AppendSubtree` include descendants moved outside their parent's storage path. `Reach` also includes targets implied by shadowed
ancestor rules, allowing projection deny-lists to reject aliases to protected data. Exact keys ending in `.` name a subtree; their targets are
normalized by removing a final `.` when used as storage roots.

```go
mapping := fieldpolicy.Mapping{}.WithExact(map[string]string{"credentials.": "private."})
mapping.Subtree("credentials")        // ["private"]
mapping.Reach("credentials.password") // ["credentials.password", "private.password"]
```

## Shared contracts

| API                  | Purpose                                                                                  |
|----------------------|------------------------------------------------------------------------------------------|
| `AsAllowList`        | Inspect a value or pointer allow-list, preserving the zero-list meaning of a nil pointer |
| `Access.Allows`      | Explicit authorization shared by filter, orderby, and projection                         |
| `NormalizeEnumValue` | Normalize built-in integer representations while retaining comparable custom values      |
| `IntegerValue`       | Signed-int64 subset required by the listing integer enum contract                        |

A segment `*` matches one nonempty segment (`items.*.id`); trailing `.*` retains subtree matching. A configured AllowList never allows a field
with an empty segment, so `a.*` admits neither `a.` nor `a..b`. A malformed entry — empty, with an empty segment (`a..b`, `.a`, `a.`) or with a
star inside a segment (`a*`, `**`) — matches nothing, and `AllowList.Validate` reports it as `ErrMalformedPattern`; the filter policy refuses
such a list at construction. `AllowList.Restricts` reports whether a list can stand as the allow-list untrusted input requires: it is configured,
holds an entry and has neither a lone `*` nor an entry of stars only (`*.*`, `*.*.*`), which admits every path of its depth; filter, orderby and
projection refuse any other list under `WithUntrustedInput`. A trailing wildcard does not authorize the bare
parent: list the parent explicitly when needed. Embedded wildcard policies require explicit projection defaults, since selecting their parent could
expose sibling fields. Prefix mapping normalizes a missing trailing dot and always matches segment boundaries. Backend storage metadata remains
backend-specific; the shared contracts do not force SQL column details or search index types into neutral schemas.

## API

| Symbol                | Purpose                                                                                                                                                                                |
|-----------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Access`              | Access is a shared, immutable field authorization policy. Implementations must be safe for concurrent use.                                                                             |
| `NewAllowList`        | NewAllowList freezes fields into an `AllowList`. The result is always configured, even for an empty fields list — an empty configured list allows nothing.                             |
| `Overlaps`            | Overlaps reports whether the dotted paths a and b are equal or one is an ancestor of the other ("a" overlaps "a.b", "a.b" does not overlap "a.bc").                                    |
| `ErrMalformedPattern` | ErrMalformedPattern indicates an `AllowList` entry that is empty, has an empty dotted segment or puts a star inside a segment; `AllowList.Validate` returns it wrapped with the entry. |

## Field mapping example

```go
m := fieldpolicy.Mapping{}.
    WithExact(map[string]string{"createdAt": "created_at"}).
    WithPrefixes(map[string]string{"address.": "addr."})
m.Apply("address.city") // "addr.city"
```

## Related packages

- [go-aip](../README.md)
- [fieldbehavior](../fieldbehavior/README.md)
- [fieldmask](../fieldmask/README.md)
- [filter](../filter/README.md)
- [listing](../listing/README.md)
- [orderby](../orderby/README.md)
- [projection](../projection/README.md)
