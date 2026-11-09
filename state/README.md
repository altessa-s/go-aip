# state

```go
import "github.com/altessa-s/go-aip/state"
```

Package `state` implements [AIP-216](https://google.aip.dev/216): a transition table for a generated state enum, the core of state-changing
custom methods (`PublishBook` moves a book from `DRAFT` to `PUBLISHED`), and descriptor checks of state fields, state enums and transition
methods for consumer tests. It works on generated protobuf enums and descriptors and has no gRPC dependency. The
[guide](../docs/state.md) maps every AIP-216 rule to the code that implements it.

## Quick start

```go
var bookStates = state.MustNew(map[pb.Book_State][]pb.Book_State{
	pb.Book_DRAFT:     {pb.Book_PUBLISHED, pb.Book_DELETED},
	pb.Book_PUBLISHED: {pb.Book_ARCHIVED},
	pb.Book_ARCHIVED:  {pb.Book_PUBLISHED, pb.Book_DELETED},
	// DELETED appears only as a target: it is terminal.
})

func (s *Server) PublishBook(ctx context.Context, req *pb.PublishBookRequest) (*pb.Book, error) {
	book, err := s.store.Get(ctx, req.GetName())
	if err != nil {
		return nil, err
	}

	if _, err := bookStates.Transition(book, "PublishBook", pb.Book_PUBLISHED); err != nil {
		return nil, err // FAILED_PRECONDITION: the book is not a DRAFT or ARCHIVED
	}

	book.UpdateTime = timestamppb.Now()

	return s.store.Save(ctx, book) // with the etag precondition of the stored version
}
```

And in a test of the API:

```go
func TestAIP216(t *testing.T) {
	for _, v := range state.Lint((&pb.Book{}).ProtoReflect().Descriptor()) {
		t.Errorf("%s: %s (%s)", v.Element, v.Message, v.Rule)
	}

	m := pb.File_library_v1_library_proto.Services().ByName("Library").Methods().ByName("PublishBook")
	for _, v := range state.LintTransitionMethod(m, (&pb.Book{}).ProtoReflect().Descriptor()) {
		t.Errorf("%s: %s (%s)", v.Element, v.Message, v.Rule)
	}
}
```

## API

| Symbol                                    | Purpose                                                                                                            |
|-------------------------------------------|--------------------------------------------------------------------------------------------------------------------|
| `Enum`                                    | Constraint: a generated protobuf enum (`~int32` implementing `protoreflect.Enum`), such as `Book_State`.           |
| `Machine[S]`, `New`, `MustNew`            | Immutable transition table declared as `map[from][]to`; safe for concurrent use.                                   |
| `Machine.Check`, `Allowed`                | Whether a transition is declared; `Check` returns a `*TransitionError` (FAILED_PRECONDITION) when it is not.       |
| `Machine.Transition`                      | State-changing method core: reads the resource's state field, checks, sets the target, returns the previous state. |
| `Machine.Targets`, `IsTerminal`, `States` | Introspection, in enum number order.                                                                               |
| `TransitionError`                         | Method, current and requested state names, and whether the current state is terminal.                              |
| `CheckOutputOnly`                         | One field annotated `OUTPUT_ONLY`, or `ErrNotOutputOnly`.                                                          |
| `Lint`                                    | AIP-216 rules of a resource's state fields and enums.                                                              |
| `LintTransitionMethod`                    | AIP-216 rules of a state transition method.                                                                        |
| `Violation`, `Rule`                       | One finding: rule, element full name, message.                                                                     |

## Semantics

### Transition table

* Only declared transitions are allowed. A state into itself is not implied: AIP-216's example fails `PublishBook` on a `PUBLISHED` book. Declare
  `DRAFT: {DRAFT}` to make a method idempotent.
* A state with no targets (`DELETED: nil`) or one that appears only as a target is terminal; `TransitionError.Terminal` says so.
* The zero value (`STATE_UNSPECIFIED`) and numbers the enum does not declare are rejected by `New` (`ErrInvalidTable`): AIP-216 says the zero
  value is never used. A stored resource still holding it, or an unknown number from a newer schema, can therefore move nowhere.
* `New` copies the table; the `Machine` is immutable.

### State-changing methods

`Transition(resource, method, to)` reads the field `WithField` names (`state` by default), which must be a singular field of the
`Machine`'s enum (`ErrInvalidField` otherwise, a programmer error). A refused move returns a `*TransitionError` naming the method and leaves the
resource untouched; an allowed one sets the field and returns the previous state, for events or audit records. Loading, the etag
precondition (AIP-154), other field changes such as `update_time`, and saving stay with the handler, which keeps the read and the write in one
transaction or precondition so two concurrent transitions cannot both succeed.

The state field itself must be `OUTPUT_ONLY` so that Create and Update cannot set it: the [`fieldbehavior`](../fieldbehavior/README.md) and
[`fieldmask`](../fieldmask/README.md) packages then clear a client value. `CheckOutputOnly` and `Lint` verify the annotation.

### Descriptor checks

`Lint(md)` inspects the enum fields of the resource `md` (not nested messages). A state field is an enum field named `state` or ending in
`_state`, or whose enum is called `State` or ends in `State`; each enum is checked once.

| Rule                 | Level  | Check                                                                                                                  |
|----------------------|--------|------------------------------------------------------------------------------------------------------------------------|
| `state-output-only`  | SHOULD | the state field is `OUTPUT_ONLY`                                                                                       |
| `state-enum-name`    | SHOULD | the enum is called `State` or ends in `State`                                                                          |
| `state-enum-nested`  | SHOULD | the enum is nested in a message, not top-level                                                                         |
| `state-not-status`   | —      | an enum field named `status` or whose enum ends in `Status` (Status is reserved for HTTP/gRPC)                         |
| `state-zero-value`   | SHOULD | the zero value is the enum name in UPPER_SNAKE_CASE plus `_UNSPECIFIED` (`STATE_UNSPECIFIED`, `JOB_STATE_UNSPECIFIED`) |
| `state-value-prefix` | SHOULD | other values do not carry that prefix (`ACTIVE`, not `STATE_ACTIVE`)                                                   |
| `state-value-term`   | SHOULD | `READY`/`AVAILABLE` → `ACTIVE`, `SUCCESS`/`SUCCESSFUL` → `SUCCEEDED`, `FAILURE` → `FAILED`                             |

`LintTransitionMethod(md, resource)`:

| Rule                      | Level         | Check                                                                                |
|---------------------------|---------------|--------------------------------------------------------------------------------------|
| `transition-method-name`  | SHOULD        | the method is a verb followed by the resource name (`PublishBook`)                   |
| `transition-request-name` | MUST          | the request is `<Method>Request`                                                     |
| `transition-response`     | SHOULD        | the response is the resource or `google.longrunning.Operation`                       |
| `transition-name-field`   | MUST / SHOULD | the request has a singular string field `name`                                       |
| `transition-http-method`  | MUST          | `google.api.http` uses `post`                                                        |
| `transition-http-body`    | MUST          | `body: "*"`                                                                          |
| `transition-http-verb`    | MUST          | the URI ends with `:` and the method's verb in camelCase (`:publish`, `:softDelete`) |

The HTTP rules apply only to a method with a `google.api.http` annotation. The rules are mostly SHOULDs: a consumer test may skip some by
`Rule`.

## Errors

| Error                | Match with                                                  | Status                              |
|----------------------|-------------------------------------------------------------|-------------------------------------|
| `*TransitionError`   | `ErrTransitionNotAllowed`, `coreerrs.ErrFailedPrecondition` | FAILED_PRECONDITION                 |
| invalid table        | `ErrInvalidTable`                                           | programmer error (`MustNew` panics) |
| invalid state field  | `ErrInvalidField`                                           | programmer error                    |
| writable state field | `ErrNotOutputOnly`                                          | descriptor check                    |

## Options

| Option      | Default                | Behavior                                                                      |
|-------------|------------------------|-------------------------------------------------------------------------------|
| `WithField` | `DefaultField` (state) | Proto name of the field `Transition` reads and sets; empty keeps the default. |

## Limits

The package handles no client-controlled sizes: the table is declared by the server and `Transition` touches one field. `Lint` walks the
fields of one message and its state enums only.

## Related packages

- [go-aip](../README.md)
- [fieldbehavior](../fieldbehavior/README.md)
- [fieldmask](../fieldmask/README.md)
- [state guide](../docs/state.md)
