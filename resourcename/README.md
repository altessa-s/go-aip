# resourcename

```go
import "github.com/altessa-s/go-aip/resourcename"
```

Package `resourcename` implements the resource names of AIP-122, the resource types and `google.api.resource` patterns of AIP-123 and the `-`
wildcard reads across collections of AIP-159. It compiles patterns such as `publishers/{publisher}/books/{book}`, matches names against them
without regular expressions or allocations, formats names from values, derives parents, reads the resource annotations of message descriptors and
checks the `name` and `parent` fields of a request against the referenced types, reporting every client error as a structured field violation. The
[guide](../docs/resourcename.md) maps every rule of the AIPs to the code.

## Contract

* A **`Pattern`** is compiled once by `ParsePattern` and is immutable. Matching is structural: literal segments must be equal, variable segments
  non-empty, and the segment count equal. It never validates the characters of an ID and treats `-` as an ordinary value; the ID policy, the
  wildcard and the limits belong to a `Validator`.
* A **`Resource`** is a resource type with one or more patterns. A name is matched against the patterns in declaration order and the first match
  wins (AIP-123 appends new patterns at the end, so the order is stable). Two patterns that match the same names are refused.
* A **`Validator`** holds the registered types and the policy for client names. Its checks return `nil` or a `*coreerrs.FieldError` on the request
  field, whose `Err` is a `*NameError` matching `ErrInvalidName` and a cause sentinel. Configuration problems (a bad pattern, a reference to an
  unregistered type) are plain errors, never field errors.
* Resource names are never URL-escaped (AIP-122): `Format` refuses a value holding `/` rather than escaping it.

## Patterns

```go
p, err := resourcename.ParsePattern("publishers/{publisher}/books/{book}")

p.Matches("publishers/acme/books/dune")               // true, no allocation
vals, err := p.Match("publishers/acme/books/dune")    // map[publisher:acme book:dune]
dst, err := p.AppendValues(buf[:0], name)             // [acme dune], no allocation with capacity
name, err := p.Format(map[string]string{"publisher": "acme", "book": "dune"})
name, err := p.FormatValues("acme", "dune")
parent, ok := p.Parent()                              // publishers/{publisher}; false for a top-level pattern
```

Grammar enforced by `ParsePattern` (errors match `ErrInvalidPattern`):

| Rule                                                                                   | Example refused                      |
|----------------------------------------------------------------------------------------|--------------------------------------|
| Segments separated by `/`, none empty, no leading or trailing `/`                      | `publishers//{publisher}`            |
| The first segment is a collection identifier                                           | `{publisher}/books`                  |
| A collection identifier is `[a-z][a-zA-Z0-9]*` (camelCase, ASCII)                      | `Publishers`, `pub-lishers`          |
| A collection identifier appears once per pattern                                       | `books/{book}/books/{other}`         |
| A variable is `{name}` and follows a collection identifier                             | `publishers/{publisher}/{book}`      |
| A variable name matches `[a-z][_a-z0-9]*[a-z0-9]` (snake_case, two characters or more) | `{p}`, `{pubLisher}`, `{publisher_}` |
| A variable has no `_id` suffix                                                         | `{publisher_id}`                     |
| A variable appears once per pattern                                                    | `a/{book}/b/{book}`                  |
| Path-template syntax is not a resource pattern                                         | `{name=**}`                          |

A collection identifier not followed by a variable is a singleton (AIP-156): `users/{user}/config` matches `users/vhugo1802/config` and its parent
pattern is `users/{user}`; singletons may be nested (`users/{user}/settings/rules/{rule}`).

Mismatches are a `*NameError` matching `ErrNoMatch` whose `Segment` names the first offending segment (`-1` for a wrong segment count) and whose
`Reason` says what was expected, for example `segment 2 must be "books" (pattern "publishers/{publisher}/books/{book}")`.

## Resources

```go
r, err := resourcename.NewResource("library.googleapis.com/Shelf",
	"projects/{project}/shelves/{shelf}",
	"organizations/{organization}/shelves/{shelf}")
r, err := resourcename.ResourceOf((&pb.Shelf{}).ProtoReflect().Descriptor())   // google.api.resource
r, err := resourcename.ResourceFromDescriptor(rd)                              // a *annotations.ResourceDescriptor

p, vals, err := r.Match("organizations/acme/shelves/s1")   // first matching pattern
parent, err := r.Parent("organizations/acme/shelves/s1")   // "organizations/acme"; "" (the root) for a top-level name
r.ParentPatterns()                                         // distinct parent patterns, in pattern order
r.HasRootParent()                                          // a pattern is top-level
```

The type must be `{service}/{Kind}`: a DNS service name and an UpperCamelCase kind. `ResourceOf` also checks that the `name_field` (default `name`)
is a singular string field of the message. `Singular` and `Plural` are reported as declared and are not cross-checked against the patterns, because
AIP-122 allows a nested collection to be shortened (`users/{user}/events/{event}` for a `UserEvent`).

## Validator

```go
v := resourcename.NewValidator(resourcename.WithIDValidator(resourcename.StrictID))
err := v.RegisterFile(librarypb.File_google_example_library_v1_library_proto)   // or v.RegisterFiles(nil), v.Register(r)

err = v.ValidateRequest(getReq)                                                 // name must be a Book
err = v.ValidateRequest(listReq, resourcename.WithWildcard(resourcename.WildcardAny)) // parent may be "publishers/-"
err = v.CheckName("name", name, bookType)
err = v.CheckParent("parent", parent, bookType, resourcename.WithWildcard(resourcename.WildcardAny))
```

`RegisterFile` reads the file's `google.api.resource_definition` options and the `google.api.resource` annotations of its messages (nested ones
included) and registers all of them or none. `RegisterFiles` does it for every file of a registry (`protoregistry.GlobalFiles` when nil), keeps the
files that succeed and returns the failures joined. Registering the same definition twice is a no-op; a different definition of a known type is
`ErrDuplicateType`. Register at startup, then check from any goroutine.

`ValidateRequest` checks, in field order, and reports the first invalid name:

| Field of the request                                                     | Check                                                                       |
|--------------------------------------------------------------------------|-----------------------------------------------------------------------------|
| top-level `string` with `resource_reference.type = T`                    | `CheckName`: a name of `T`                                                  |
| top-level `string` with `resource_reference.child_type = C`              | `CheckParent`: the name of a parent of `C` (the root when `C` is top-level) |
| top-level `repeated string` with either annotation                       | each element, the field error names the index (`names[2]`)                  |
| `resource_reference.type = "*"`                                          | not checked (any type)                                                      |
| top-level singular message field whose message has `google.api.resource` | its name field (`book.name`), never with a wildcard                         |

Empty values are skipped: requiring a value is the job of `REQUIRED` (package `fieldbehavior`). A reference to a type that is not registered is
`ErrUnknownType`. The plan of each request type is computed once and cached; registering types drops the cache.

### Checks

`CheckName` and `CheckParent` run, cheapest first:

1. the length limit on the raw value;
2. for a full name (`//service/...`): refused unless `WithFullNames(true)`; then the service must be the type's and the relative part is checked;
3. the empty value: the root for `CheckParent` of a type with a top-level pattern, otherwise refused;
4. the segment limit;
5. the first matching pattern (`ErrNoMatch` when none does);
6. every ID: `-` under the wildcard policy, everything else through the `IDValidator`.

### Wildcards (AIP-159)

| Policy (`WithWildcard`)  | Where `-` is accepted                        | Use                                                                      |
|--------------------------|----------------------------------------------|--------------------------------------------------------------------------|
| `WildcardNone` (default) | nowhere                                      | Get, Create, Update, Delete, and every resource's own name               |
| `WildcardAny`            | any ID of the value                          | the `parent` of a List reading across collections: `publishers/-`        |
| `WildcardAncestors`      | any ID except the last variable of the value | the unique resource lookup of a Get: `publishers/-/books/les-miserables` |

The library cannot know whether IDs are unique across parents, so `WildcardAncestors` is opt-in: offer it only on a Get whose child IDs cannot
collide. The response must carry canonical names with the actual parents, never `-`.

### ID validators

| Validator                | Accepts                                                                                                            |
|--------------------------|--------------------------------------------------------------------------------------------------------------------|
| `PermissiveID` (default) | any non-empty valid UTF-8 without `/` or control characters, except `.`, `..` and `-` (UUIDs, upper case, Unicode) |
| `StrictID`               | `[a-z]([a-z0-9-]{0,61}[a-z0-9])?`: the RFC-1034 recommendation of AIP-122 for user-specified IDs                   |
| custom `IDValidator`     | `func(id string) error`; its error text becomes the field violation reason                                         |

`ValidateUID` checks the AIP-148 `uid` field: a UUID4 in canonical text form.

## Full resource names

```go
full, err := resourcename.FullName("library.googleapis.com", "publishers/123/books/les-miserables")
// "//library.googleapis.com/publishers/123/books/les-miserables"
service, name, err := resourcename.ParseFullName(full)
resourcename.IsFullName(full) // true
```

A full name has no version and no scheme; `https://library.googleapis.com/v1/...` is a resource URI, not a full name, and is refused.

## Options

| Option                    | Default                       | Effect                                                                        |
|---------------------------|-------------------------------|-------------------------------------------------------------------------------|
| `WithIDValidator(f)`      | `PermissiveID`                | checks every resource ID segment; nil keeps the default                       |
| `WithMaxNameLength(n)`    | `DefaultMaxNameLength` (4096) | bytes of a name, full names included; 0 unlimited, negative keeps the default |
| `WithMaxSegments(n)`      | `DefaultMaxSegments` (64)     | segments of a relative name; 0 unlimited, negative keeps the default          |
| `WithFullNames(b)`        | false                         | accept `//service/relative` names of the type's service                       |
| `WithWildcard(p)` (check) | `WildcardNone`                | where `-` is accepted, per call                                               |

## Errors

| Sentinel             | Kind          | Meaning                                                                                     |
|----------------------|---------------|---------------------------------------------------------------------------------------------|
| `ErrInvalidName`     | client        | matches every invalid name below                                                            |
| `ErrNoMatch`         | client        | the name matches none of the expected patterns, or is empty                                 |
| `ErrInvalidID`       | client        | an ID refused by the `IDValidator`                                                          |
| `ErrWildcard`        | client        | `-` where the policy does not allow it                                                      |
| `ErrInvalidFullName` | client        | malformed full name, another service, or a full name where only relative names are accepted |
| `ErrLimitExceeded`   | client        | name length or segment count over the limit                                                 |
| `ErrInvalidUID`      | client        | `ValidateUID` failed                                                                        |
| `ErrInvalidPattern`  | configuration | malformed pattern                                                                           |
| `ErrInvalidResource` | configuration | malformed type, no pattern, duplicate patterns, bad name field, nil resource                |
| `ErrNoResource`      | configuration | `ResourceOf` on a message without `google.api.resource`                                     |
| `ErrUnknownType`     | configuration | a `resource_reference` to an unregistered type                                              |
| `ErrDuplicateType`   | configuration | a type registered with two definitions                                                      |
| `ErrInvalidValue`    | caller        | `Format` with a missing, empty, unknown or `/`-holding value                                |

A client error from a `Validator` check is `&coreerrs.FieldError{Field: "<json field>", Reason: ne.Reason, Err: ne}`: it matches
`coreerrs.ErrInvalidArgument` (INVALID_ARGUMENT with a field violation for the API), `ErrInvalidName` and its cause. `NameError.Reason` never repeats
the name.

## Defaults and limits

| Limit             | Default | Checked                             |
|-------------------|---------|-------------------------------------|
| name length       | 4096 B  | first, before parsing a full name   |
| relative segments | 64      | before matching                     |
| strict ID length  | 63      | by `StrictID` (`MaxStrictIDLength`) |

Patterns are server-defined and not limited. The plan cache holds one entry per request message type checked.

## Performance

Matching walks the name once with `strings.IndexByte`, skips patterns whose segment count differs and returns values as substrings of the name.
On an Apple M-series machine (`go test -bench . ./resourcename`):

| Benchmark                               | ns/op | allocs/op |
|-----------------------------------------|-------|-----------|
| `Pattern.Matches` (8 segments)          | ~50   | 0         |
| `Pattern.AppendValues`                  | ~45   | 0         |
| `Pattern.Match` (map)                   | ~180  | 2         |
| `Resource.Matches` (4 patterns)         | ~50   | 0         |
| `Validator.CheckName` with `StrictID`   | ~110  | 0         |
| `Validator.ValidateRequest` (batch get) | ~290  | 3         |

## API

| Symbol                                                                                    | Purpose                                  |
|-------------------------------------------------------------------------------------------|------------------------------------------|
| `ParsePattern`, `MustParsePattern`, `Pattern`                                             | compiled patterns: match, format, parent |
| `NewResource`, `ResourceOf`, `ResourceFromDescriptor`, `Resource`                         | resource types with one or more patterns |
| `NewValidator`, `Validator`, `Option`, `CheckOption`, `WildcardPolicy`                    | registry, request and field checks       |
| `IDValidator`, `PermissiveID`, `StrictID`, `ValidateUID`, `Wildcard`, `MaxStrictIDLength` | ID policies                              |
| `FullName`, `ParseFullName`, `IsFullName`                                                 | full resource names                      |
| `DefaultMaxNameLength`, `DefaultMaxSegments`, `DefaultNameField`                          | defaults                                 |
| `NameError` and the sentinels above                                                       | errors                                   |

## Related packages

* [`fieldbehavior`](../fieldbehavior/README.md): `REQUIRED` for `name` and `parent`, `IDENTIFIER` for the resource's own name.
* [`fieldmask`](../fieldmask/README.md): the update mask of the Update whose `book.name` this package checks.
