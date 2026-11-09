# Resource names

[resourcename](../resourcename/) implements resource names (AIP-122), resource types and patterns (AIP-123) and reading across collections with
the `-` wildcard (AIP-159), plus the `name` and `uid` fields of AIP-148. It is meant for APIs that address resources by name; an API that addresses
them by bare ID (an accepted exception to AIP-122) does not need it.

## Transport wiring

A handler validates the request before any work and returns the error as is:

```go
var names = func() *resourcename.Validator {
    v := resourcename.NewValidator(resourcename.WithIDValidator(resourcename.StrictID))
    if err := v.RegisterFile(librarypb.File_library_v1_library_proto); err != nil {
        panic(err) // a malformed pattern is a programming error
    }
    return v
}()

func (s *server) ListBooks(ctx context.Context, req *librarypb.ListBooksRequest) (*librarypb.ListBooksResponse, error) {
    if err := names.ValidateRequest(req, resourcename.WithWildcard(resourcename.WildcardAny)); err != nil {
        return nil, err // *coreerrs.FieldError on "parent": INVALID_ARGUMENT with a field violation
    }
    // ...
}
```

The client errors are `coreerrs.FieldError` values, which the `apierror` package (and the go-atlas `errstatus` interceptor) map to
`INVALID_ARGUMENT` with a `BadRequest.FieldViolation` on the request's JSON field path (`name`, `parent`, `names[2]`, `book.name`).

## AIP-122 conformance

| AIP-122 rule                                                                                         | Level  | How it is met                                                                                                                                                                       |
|------------------------------------------------------------------------------------------------------|--------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Resource names use `/` to separate segments                                                          | MUST   | `Pattern` splits on `/` only; `Format` joins with `/`.                                                                                                                              |
| Non-terminal segments do not contain `/`                                                             | MUST   | Structural: a segment ends at the next `/`. `Format` refuses a value holding `/`.                                                                                                   |
| The terminal segment should not contain `/`                                                          | SHOULD | Enforced as a MUST: a name with extra segments does not match, and `Format` refuses `/`. Path-template patterns (`{path=**}`) are refused by `ParsePattern`.                        |
| Names should use only DNS (RFC 1123) characters, no URL-escaping, no non-ASCII                       | SHOULD | `StrictID` enforces the RFC-1034 subset. The default `PermissiveID` is deliberately wider (upper case, UUIDs, Unicode) and refuses only `/`, control characters, `.`, `..` and `-`. |
| Resource IDs should not use upper-case letters                                                       | SHOULD | `StrictID`.                                                                                                                                                                         |
| Unicode is stored in NFC (AIP-210)                                                                   | MUST   | Not checked (no normalization dependency): the API normalizes, or uses `StrictID`, which admits ASCII only.                                                                         |
| User-specified IDs should conform to RFC-1034 and use lower case                                     | SHOULD | `StrictID`: `[a-z]([a-z0-9-]{0,61}[a-z0-9])?`, at most 63 characters.                                                                                                               |
| Names are never URL-escaped                                                                          | MUST   | `Format` refuses rather than escapes; matching compares raw bytes.                                                                                                                  |
| Collection identifiers are camelCase, begin with a lower-case letter, ASCII letters and digits       | MUST   | `ParsePattern`: `[a-z][a-zA-Z0-9]*`.                                                                                                                                                |
| Collection identifiers are unique within a name                                                      | MUST   | `ParsePattern` refuses a repeated literal.                                                                                                                                          |
| Collection identifiers are the plural, concise American English                                      | MUST   | Not machine-checkable; `Singular`/`Plural` are reported, not cross-checked (AIP-122 allows shortened nested collections).                                                           |
| Resources expose a `name` field with the resource name                                               | MUST   | `ResourceOf` checks that the `name_field` (default `name`) is a singular string field; `ValidateRequest` checks it in a Create or Update resource.                                  |
| Full resource names: `//{service}/{relative name}`, no version                                       | SHOULD | `FullName`, `ParseFullName`; the service must be a DNS name. `WithFullNames(true)` accepts them in checks, with the service of the referenced type.                                 |
| Resource URIs (scheme, version) are not names                                                        | —      | `ParseFullName` refuses `https://...`.                                                                                                                                              |
| Resource ID aliases (`users/me`) are accepted on input; output uses canonical names                  | MUST   | An alias is an ordinary ID and matches; producing canonical output is the handler's job.                                                                                            |
| Fields referring to resources are strings with `google.api.resource_reference`                       | SHOULD | `ValidateRequest` checks every top-level string or repeated string field so annotated.                                                                                              |
| The `parent` field of a List/Create references the parent type, or `child_type` with several parents | SHOULD | `type` → `CheckName` against the parent's patterns; `child_type` → `CheckParent` against the derived parent patterns of the child.                                                  |

## AIP-123 conformance

| AIP-123 rule                                                                | Level  | How it is met                                                                                                           |
|-----------------------------------------------------------------------------|--------|-------------------------------------------------------------------------------------------------------------------------|
| Type is `{Service Name}/{Type}`, Type UpperCamelCase alphanumeric, singular | MUST   | `NewResource`: DNS service name, `[A-Z][A-Za-z0-9]*` kind (`ErrInvalidResource`). Singularity is not machine-checkable. |
| Types are annotated with `google.api.resource`                              | SHOULD | `ResourceOf`, `RegisterFile`; file-level `google.api.resource_definition` too.                                          |
| Patterns correspond to the resource names                                   | MUST   | `ParsePattern` grammar (see the package README).                                                                        |
| Pattern variables are snake_case `[a-z][_a-z0-9]*[a-z0-9]`, no `_id` suffix | MUST   | `ParsePattern`.                                                                                                         |
| Pattern variables are unique within a pattern                               | MUST   | `ParsePattern`.                                                                                                         |
| Several patterns are mutually unique                                        | MUST   | `NewResource` refuses two patterns of the same shape (same literals, variables at the same positions).                  |
| New patterns are appended; existing ones are not reordered or removed       | MUST   | Matching is in declaration order, first match wins, so appending never changes how an existing name resolves.           |
| Singular is the lowerCamel type, plural its plural                          | MUST   | Reported by `Singular`/`Plural`; not enforced (declarative metadata).                                                   |
| Singleton patterns (AIP-156) end in a collection identifier                 | —      | Supported: `users/{user}/config`, parent `users/{user}`.                                                                |

## AIP-159 conformance

| AIP-159 rule                                                                   | Level    | How it is met                                                                                                                               |
|--------------------------------------------------------------------------------|----------|---------------------------------------------------------------------------------------------------------------------------------------------|
| A List may accept `-` as the parent's collection ID to read across collections | MAY      | `WithWildcard(WildcardAny)` on the List check; any ID of the parent may be `-` (`publishers/-`, `projects/-/locations/-`).                  |
| The URI pattern does not hard-code `-`                                         | MUST     | `-` is never a pattern literal (`ParsePattern` refuses it); it is a value accepted per call.                                                |
| The behavior is documented                                                     | MUST     | The handler's API documentation; the policy is explicit at the call site.                                                                   |
| Responses use canonical names with actual parents, never `-`                   | MUST     | Handler's job. A resource's own name is always checked with `WildcardNone`, so an Update cannot carry `-`.                                  |
| Get across collections is not supported when child IDs may collide             | MUST NOT | `WildcardNone` is the default for every check. `WildcardAncestors` (unique resource lookup) is opt-in and never accepts `-` as the last ID. |
| Unique resource lookup: the resource ID is unique within parent collections    | MUST     | The service decides; `WildcardAncestors` accepts `-` for ancestors only.                                                                    |
| Cross-parent `order_by` is best effort; partial failures follow AIP-217        | SHOULD   | Out of scope (see `orderby`, `unreachable`).                                                                                                |

## AIP-148 conformance

| AIP-148 rule                                  | Level  | How it is met                                                                             |
|-----------------------------------------------|--------|-------------------------------------------------------------------------------------------|
| Every resource has a `string name` field      | MUST   | `ResourceOf` (honoring `name_field`).                                                     |
| `parent` refers to the parent of a collection | SHOULD | `CheckParent`, `ValidateRequest` on `child_type` references.                              |
| `uid` is output only, a UUID4                 | MUST   | `ValidateUID` checks the text form and version/variant; OUTPUT_ONLY is `fieldbehavior`'s. |

## Deliberate choices

* **Permissive by default.** Server-generated IDs (UUIDs, ULIDs) start with digits and fail RFC-1034, and AIP-122's character rules are SHOULD.
  `PermissiveID` refuses only what breaks names or routing; `StrictID` is one option away.
* **Structural patterns, policy in the validator.** A `Pattern` answers "does this name have this shape"; the `Validator` answers "is this a valid
  client name for this method". One pattern serves both the server's own formatting and client validation.
* **Empty values are skipped by `ValidateRequest`.** Presence is `REQUIRED`'s job (`fieldbehavior.ValidateRequired`); `CheckName` called directly
  refuses an empty name.
* **Top-level fields only.** `ValidateRequest` checks the request's own reference fields and the name of a resource it carries, not references deep
  inside resources, which are data rather than addressing.
* **First invalid name wins**, like the field-mask checks; a request rarely carries more than one name.

## Limits

| Limit                  | Default | Option              |
|------------------------|---------|---------------------|
| Name length (bytes)    | 4096    | `WithMaxNameLength` |
| Relative name segments | 64      | `WithMaxSegments`   |
| Strict ID length       | 63      | fixed (RFC 1034)    |

Both configurable limits are checked before any matching; 0 disables a limit.

## Errors

| Error                                                                                                                               | API mapping                              |
|-------------------------------------------------------------------------------------------------------------------------------------|------------------------------------------|
| `coreerrs.FieldError` wrapping `*NameError` (`ErrNoMatch`, `ErrInvalidID`, `ErrWildcard`, `ErrInvalidFullName`, `ErrLimitExceeded`) | INVALID_ARGUMENT, field violation        |
| `ErrInvalidUID` (`ValidateUID`)                                                                                                     | INVALID_ARGUMENT (`INVALID_UID`)         |
| `ErrUnknownType`, `ErrInvalidPattern`, `ErrInvalidResource`, `ErrDuplicateType`, `ErrNoResource`                                    | configuration: INTERNAL (fix the server) |
| `ErrInvalidValue` (`Pattern.Format` with a bad value)                                                                               | caller fault: INTERNAL                   |

## See also

* [resourcename README](../resourcename/README.md): the complete API.
* [fieldbehavior](fieldbehavior.md): `REQUIRED` and `IDENTIFIER` on name fields.
* [fieldmask](fieldmask.md): the Update whose resource name is checked here.
