# protopath

```go
import "github.com/altessa-s/go-aip/projection/protopath"
```

Package `protopath` validates read mask paths against the descriptor of a resource message, so the paths a [projection](../README.md) policy
accepts satisfy AIP-161 for that resource. The default projection validator checks only the grammar; `protopath` also checks that every segment
names a declared field and that the path does not reach into a collection or a well-known type.

## Contract

`New(md, opts...)` binds a `Validator` to the descriptor of the resource and fails with `ErrDescriptorRequired` for a nil descriptor.
`Validator.Validate(path)` has the signature of `projection.PathValidator`, so the method value plugs into the policy and the parser directly:

```go
v, err := protopath.New((&pb.Book{}).ProtoReflect().Descriptor(), protopath.WithJSONNames(true))
if err != nil {
	return err
}

policy, err := projection.NewPolicy(
	projection.WithPathValidator(v.Validate),
	projection.WithAllowedFields("title", "author.*"),
)
parser, err := projection.NewParser(projection.WithPathGrammar(v.Validate))
```

A `Validator` is immutable and safe for concurrent use. Every error names the offending path and wraps a projection sentinel, so a transport maps
it to `INVALID_ARGUMENT` like any other malformed mask.

## AIP-161 rules

| Path                                       | Result                                                                  |
|--------------------------------------------|-------------------------------------------------------------------------|
| `title`, `author.name`                     | valid: proto field names relative to the resource                       |
| `authors` (repeated)                       | valid: a repeated field is a leaf                                       |
| `authors.0`, `authors.name`, `authors.*.x` | `ErrInvalidFieldPath`: a repeated field must be the last segment        |
| `labels` (map)                             | valid: a map field is a leaf                                            |
| `labels.key`, ``labels.`k` ``, `labels.*`  | `ErrUnsupportedPath`: map key paths are valid AIP-161 but not served    |
| `create_time.seconds`                      | `ErrInvalidFieldPath`: `google.protobuf` well-known types are leaves    |
| `title.length`                             | `ErrInvalidFieldPath`: scalars and enums have no subfields              |
| `nickname` (undeclared)                    | `ErrInvalidFieldPath` and `ErrUnknownField`, unless `WithIgnoreUnknown` |
| `displayName`                              | valid only with `WithJSONNames`; proto names are always accepted        |
| `parent.parent.title` (recursive)          | valid: the walk follows the path, so recursion never loops              |
| `*`, `owner.0`, ``owner.`k` ``             | `ErrUnsupportedPath`, as in the default grammar                         |
| empty path or segment, `owner-email`       | `ErrInvalidFieldPath`, as in the default grammar                        |
| more than `WithMaxDepth` segments          | `ErrMaxFieldPathDepthExceeded`                                          |

The well-known types are every message of the `google.protobuf` package: `Timestamp`, `Duration`, `Struct`, `Value`, `ListValue`, `FieldMask`,
`Any`, `Empty` and the wrappers.

## Options

| Option              | Default | Effect                                                                                                      |
|---------------------|---------|-------------------------------------------------------------------------------------------------------------|
| `WithJSONNames`     | `false` | Also accept the `json_name` of each field                                                                   |
| `WithIgnoreUnknown` | `false` | Accept an undeclared field (AIP-161 lets read masks ignore it); grammar and the declared prefix still apply |
| `WithMaxDepth`      | `8`     | Maximum number of dot-separated segments (`DefaultMaxDepth`, the parser default)                            |

## Errors

| Error                                     | When                                                                      |
|-------------------------------------------|---------------------------------------------------------------------------|
| `ErrDescriptorRequired`                   | `New` received a nil descriptor                                           |
| `ErrUnknownField`                         | a segment names no field of its message; also wraps `ErrInvalidFieldPath` |
| `projection.ErrInvalidFieldPath`          | malformed path, traversal past a repeated field, scalar, enum or WKT      |
| `projection.ErrUnsupportedPath`           | map key, wildcard, quoted key or numeric segment                          |
| `projection.ErrMaxFieldPathDepthExceeded` | the path is deeper than `WithMaxDepth`                                    |

## Storage paths

The policy runs its validator over API paths only: requested, denied and view paths. The denied and required storage paths of a translator follow
`projection.WithStoragePathValidator` (default: the dotted grammar), so a `protopath` validator never constrains storage names, which need not be
proto field names.

## Related packages

- [projection](../README.md)
- [fieldmask](../../fieldmask/README.md)
- [go-aip](../../README.md)
