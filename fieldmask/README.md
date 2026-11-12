# fieldmask

```go
import "github.com/altessa-s/go-aip/fieldmask"
```

Package `fieldmask` provides hierarchical field mask utilities for Protocol Buffers. A `FieldMask` is a nested map representing dot-separated field
paths with support for filtering, pruning, selection, set operations and resolution by one AIP-161 path grammar. A `structpb.Struct` field is
addressed as a `map<string, Value>` by every mask operation, and by default a path goes on into the dynamic JSON below (`attrs.work.from`,
`attrs.items.*.name`), an extension beyond AIP-161 that `WithStrictDynamicPaths(true)` turns off. The [guide](../docs/fieldmask.md) maps every
AIP-134, AIP-154, AIP-161, AIP-163 and AIP-203 rule to the function that implements it.

| Package               | Job                                                                                                                       |
|-----------------------|---------------------------------------------------------------------------------------------------------------------------|
| `fieldmask`           | The mask type, the path grammar, set algebra, `Filter`, `Select`, `Prune`, `Resolve`, `Validate`, masks from messages     |
| [`update`](update/)   | Update masks: AIP-203 field behaviors, `Spec.Plan`, `Merge`, `MergePaths`, `Patch` with etag preconditions, validate_only |
| [`extract`](extract/) | Reading `update_mask` and `read_mask` off request messages, transport-agnostic extractor chains                           |

## Path grammar (AIP-161)

Every entry point that reads a path, read masks and update masks alike, uses one grammar:

```text
path    = segment { "." segment }
segment = name | "`" { char | "``" } "`" | "*"
```

| Rule                                                            | Accepted                                                                                                     | Rejected                                                           |
|-----------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------|--------------------------------------------------------------------|
| Field spelling                                                  | `display_name`, `displayName`, `noise_percent7d` (protojson)                                                 | an unknown field                                                   |
| Repeated field                                                  | `aliases`, `aliases.*`, `aliases.*.display_name`                                                             | `aliases.0`, `aliases.0.id` (index), `aliases.display_name`        |
| Map field                                                       | `labels`, `labels.*`, `labels.k`, `labels.k.display_name`                                                    | ``labels.`*` `` (the literal key `*`)                              |
| Typed map key                                                   | `ports.443` (int32), `flags.true` (bool)                                                                     | `ports.0443`, `ports.x`, `flags.yes`: a key must format back as is |
| Quoted segment                                                  | ``labels.`a.b` ``, ``labels.`x``y.z` `` (the key ``x`y.z``)                                                  | `` a`b ``, `` `a`b ``, an unclosed backtick                        |
| Scalar                                                          | `display_name`                                                                                               | `display_name.x`, `tags.*.x` (a repeated scalar)                   |
| `Struct` field                                                  | `attrs`, `attrs.*` (both whole), `attrs.color`, ``attrs.`a.b` ``, ``attrs.`*` `` (literal key)                       | malformed quoting                               |
| Dynamic JSON (default, beyond AIP-161)                          | `attrs.work.from`, `attrs.*.from`, `attrs.items.*.name`, `value.key`, `items.*.name` (`items` a `ListValue`) | `items.0` (index), `items.name` (a `ListValue` without `*`)        |
| Dynamic JSON with `WithStrictDynamicPaths(true)` (AIP-161 only) | `attrs`, `attrs.*`, `attrs.color`, `value`, `items`                                                          | `attrs.color.hue`, `attrs.*.hue`, `value.key`, `items.*`           |
| Full replacement                                                | `*` alone                                                                                                    | `*` with any other path (`ErrWildcardNotAlone`), `*.x`             |
| Shape                                                           | non-empty segments                                                                                           | `""`, `a.`, `.a`, `a..b`                                           |

In dynamic JSON a key addresses an entry of a `Struct` value and `*` every entry of a `Struct` value or every element of a list value. A path through
a value of another shape (a scalar or null, a list without `*`) addresses nothing. `Filter` and `Select` return only what the mask addresses, so they
drop such a value (its key, or the field holding it) unless another path keeps it; under `*` an element of another shape is dropped and the other
elements are kept in order, each with only its addressed keys. `Prune` leaves such a value as it is. With [`update`](update/) a nested key is replaced
from the request or deleted when the request lacks it, an intermediate `Struct` is created (or replaces a value of another shape) only when the
request holds a value to write there, and `*` on a list updates its elements in place by index (`ErrElementCountMismatch`). The read entry points
(`FromProtoFieldMask`, `Resolve`, `Validate`, `Filter`, `Prune`, `Select`, `ResolvePath`) take `WithStrictDynamicPaths` as an `Option`; the update
entry points take the `update.WithStrictDynamicPaths` option of their package. `FromMessage` and `FromSetFields` emit the top-level keys of a `Struct`
only, valid in both modes.

A segment that is empty or holds a dot or a backtick is written between backticks, with every literal backtick doubled (an empty map key is two
backticks). `ToPaths`, `FromMessage`, `FromSetFields` and every violation path quote such a segment the same way, so a path round-trips through
`ParsePaths`.

## Constructors

| Function             | Description                                                                                                           |
|----------------------|-----------------------------------------------------------------------------------------------------------------------|
| `ParsePaths`         | Create a mask from client paths, checking each by the grammar first (`*ValidationError` for a malformed path)         |
| `FromProtoFieldMask` | Create a mask from a `fieldmaskpb.FieldMask`, checking its paths as `ParsePaths` does; nil stays nil (omitted)        |
| `FromPaths`          | Create a mask from trusted paths written in code; a malformed path is kept verbatim and every schema check rejects it |
| `FromMessage`        | Create a mask covering every field in the message schema (no list indexes)                                            |
| `FromSetFields`      | Create a mask covering only the populated fields of a message; options shape the result                               |

A `FieldMask` is a set of paths none of which covers another: `FromPaths`, `ParsePaths` and `FromProtoFieldMask` collapse a path another path of the
same mask covers (`profile.id` with `profile`, `aliases.*` with `aliases`) into the covering path, after checking its grammar. A redundant read mask
selects the same fields either way, but the collapsed path reaches no schema or field behavior check. To check every raw path of an update mask on its
own, apply the raw mask with `update.ApplyMask` or plan it with `update.Spec.Plan` (or `update.Patch`).

## Operations

| Method             | Description                                                                                        |
|--------------------|----------------------------------------------------------------------------------------------------|
| `Filter`           | Keep only the fields present in the mask, clearing everything else; an empty mask keeps everything |
| `Select`           | Like `Filter`, but an empty mask clears everything: use it for masks computed with exclusions      |
| `Prune`            | Remove the fields present in the mask, keeping everything else                                     |
| `Union`            | Combine two masks into one that covers both sets of paths                                          |
| `Intersection`     | Return a mask containing only the paths present in both masks                                      |
| `Difference`       | Return a mask containing paths in the receiver but not in the argument                             |
| `ToPaths`          | Return a sorted slice of dot-separated path strings, quoted by the grammar                         |
| `Resolve`          | Validate the mask against a descriptor and spell every field segment by its declared name          |
| `Validate`         | Check every path against the message schema by the grammar (`*ValidationError` otherwise)          |
| `SelectsAll`       | Report the full-replacement mask `*`; `*` with other paths is `ErrWildcardNotAlone`                |
| `Contains`         | Report whether a dot-separated path is present in the mask                                         |
| `IsEmpty`          | Report a mask without paths                                                                        |
| `Clone`            | Deep copy of the mask                                                                              |
| `ToProtoFieldMask` | Convert the mask to a `fieldmaskpb.FieldMask`                                                      |

`Filter`, `Select` and `Prune` validate and resolve the mask first and return the `*ValidationError` of an invalid path, leaving the message
untouched: an index into a repeated field is rejected on read as on update.

### Quoted segments and wildcards

```go
mask, err := fieldmask.ParsePaths(
    "reviews.`John Smith`.score",    // key has a space, no dot: quoting is optional
    "metadata.`google.com/project`", // key has a dot
    "metadata.`x``y.z`",             // key x`y.z: the inner backtick is doubled
)
mask.ToPaths() // ["metadata.`google.com/project`", "metadata.`x``y.z`", "reviews.John Smith.score"]
```

The `*` segment matches every element of a repeated field or every value of a map field. A leaf wildcard keeps (or, for `Prune`, clears) the whole
collection; a branch wildcard applies the nested sub-mask to every element or value. For maps, a specific-key entry wins over the wildcard for matched
keys:

```go
mask, err := fieldmask.ParsePaths(
    "labels.admin.id",       // specific key wins for "admin"
    "labels.*.display_name", // wildcard governs the rest
)
```

## Errors

| Error                                                                        | Returned by                                                                            | Meaning                                                                                                           |
|------------------------------------------------------------------------------|----------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------|
| `*ValidationError`                                                           | `ParsePaths`, `FromProtoFieldMask`, `Resolve`, `Validate`, `Filter`, `Select`, `Prune` | A path the grammar or the schema rejects; matches `ErrInvalidPath`, `Cause` may refine it (`ErrWildcardNotAlone`) |
| `ErrInvalidPath`                                                             | every entry point that reads a path                                                    | Every path error, `*ValidationError` and the update path errors included                                          |
| `ErrLimitExceeded`                                                           | every entry point a mask enters                                                        | Every limit error below                                                                                           |
| `ErrWildcardNotAlone`                                                        | `SelectsAll` and every entry point that reads raw paths                                | `*` combined with other paths                                                                                     |
| `ErrMaxPathsExceeded`, `ErrMaxPathDepthExceeded`, `ErrMaxPathLengthExceeded` | every entry point a mask enters                                                        | A mask over a limit (see [Limits](#limits)); each matches `ErrLimitExceeded` and `ErrInvalidPath`                 |

The update errors are in [update](update/README.md#errors); every update path error matches `ErrInvalidPath` as well.

## Options

| Option                   | Type              | Default                     | Behavior                                                                                             |
|--------------------------|-------------------|-----------------------------|------------------------------------------------------------------------------------------------------|
| `WithStrictDynamicPaths` | `Option`          | false                       | Keep paths into `Struct`, `Value` and `ListValue` to AIP-161 (no dynamic JSON below a `Struct` key). |
| `WithMaxPathDepth`       | `Option`          | DefaultMaxPathDepth (32)    | Bound the segments of a path, `*` and quoted keys included; 0 is unlimited.                          |
| `WithMaxPaths`           | `Option`          | DefaultMaxPaths (1000)      | Bound the paths of a mask; 0 is unlimited.                                                           |
| `WithMaxPathLength`      | `Option`          | DefaultMaxPathLength (1024) | Bound the bytes of a raw path; 0 is unlimited.                                                       |
| `WithDeclarationOrder`   | `SetFieldsOption` | false                       | Return synthesized paths in protobuf declaration order.                                              |
| `WithSkipFields`         | `SetFieldsOption` | nil / unset                 | Exclude configured fields from set-field synthesis.                                                  |
| `WithTopLevelOnly`       | `SetFieldsOption` | false                       | Limit synthesized set-field paths to top-level fields.                                               |

**Apply a mask computed with `WithSkipFields` with `Select` or `update.Merge`, never with `Filter`.** When every populated field is skipped the
computed mask is empty; `Filter` reads an empty mask as "keep everything" (and would keep the skipped fields), `Select` clears everything and
`update.Merge` changes nothing.

## Defaults

| Constant               | Value  |
|------------------------|--------|
| `DefaultMaxPathDepth`  | `32`   |
| `DefaultMaxPaths`      | `1000` |
| `DefaultMaxPathLength` | `1024` |

## Limits

A client controls the size of its mask, so every entry point a mask enters bounds it before any parsing or descriptor work, cheapest check first: the
number of paths (`WithMaxPaths`, default 1000), then the bytes of each raw path (`WithMaxPathLength`, default 1024) and its segments, `*` and quoted
keys included (`WithMaxPathDepth`, default 32). Zero lifts a limit; a negative value keeps the default. The limits are `Option` values of
`FromProtoFieldMask`, `Resolve`, `Validate`, `Filter`, `Prune`, `Select` and `ResolvePath`; `ParsePaths` applies the defaults. The
[update](update/README.md#limits) entry points take the same limits as options of their package.

A mask over a limit is `ErrMaxPathsExceeded`, `ErrMaxPathLengthExceeded` or `ErrMaxPathDepthExceeded`, each matching `ErrLimitExceeded` and
`ErrInvalidPath`: a `*ValidationError` naming the path (none for the path count). `FromMessage` and `FromSetFields` stop a recursive message at
`DefaultMaxPathDepth` segments; the deepest path then stands for the subtree below it.

## API

| Symbol            | Purpose                                                                                                                       |
|-------------------|-------------------------------------------------------------------------------------------------------------------------------|
| `FieldMask`       | A hierarchical field mask as a nested map; nil values are leaves (keep or prune the entire subtree).                          |
| `ValidationError` | A path the grammar or the schema rejects: `Path` as written, `Reason`, and an optional `Cause` such as `ErrWildcardNotAlone`. |
| `ResolvePath`     | A path of a descriptor spelled by declared names, or `""` when the grammar or the schema rejects it or it is `*`.             |
| `Option`          | The path checks of the read entry points: strict dynamic paths and the limits.                                                |
| `SetFieldsOption` | The shape of the paths `FromSetFields` returns.                                                                               |

## Related packages

- [go-aip](../README.md)
- [fieldmask/update](update/README.md)
- [fieldmask/extract](extract/README.md)
- [fieldbehavior](../fieldbehavior/README.md)
- [fieldpolicy](../fieldpolicy/README.md)
- [projection](../projection/README.md)
- [fieldmask guide](../docs/fieldmask.md)

## Protected update input

Use `update.ApplyMask` for request masks. Without current state it retains IMMUTABLE and IDENTIFIER input for caller comparison and excludes
protected paths from the effective mask; with `WithCurrent`, equal values are ignored and changes rejected. For two-stage updates, `update.Prepare`
retains explicit clears and deferred replacements in a plan carried by `update.ContextWithPlan`; `Spec.Plan` / `Patch.Run` complete it with stored
state. `Apply` writes an effective mask directly, without adding routing identifiers. See the [update contract](update/README.md).
