# validateonly

```go
import "github.com/altessa-s/go-aip/validateonly"
```

Package `validateonly` implements [AIP-163](https://google.aip.dev/163) change validation: a mutation whose request sets `validate_only` runs
every check the live request runs, permission checks, field validation and [AIP-154](../etag) preconditions included, and returns the response
the live request would return, without the write or its side effects. The [guide](../docs/validateonly.md) maps every AIP-163 rule to the code
that implements it. [fieldmask/update](../fieldmask/update) `Patch` takes the flag as `WithValidateOnly`.

## API

| Symbol          | Purpose                                                                                                                                      |
|-----------------|----------------------------------------------------------------------------------------------------------------------------------------------|
| `Run`           | Run a mutation as a validate step and a commit step; with `validate_only` set, skip the commit step and return the validate step's response. |
| `Requested`     | Read the `validate_only` flag of a request by descriptor; a field that is not a singular bool is `ErrInvalidField`, never `false`.           |
| `OmitGenerated` | Clear the OUTPUT_ONLY values of a would-be resource at any depth.                                                                            |
| `FieldName`     | `"validate_only"`.                                                                                                                           |

## Run

```go
func Run[T any](ctx context.Context, validateOnly bool,
	validate func(context.Context) (T, error),
	commit func(context.Context, T) (T, error)) (T, error)
```

`validate` performs every check of the live request and builds the response the request would produce; `commit` writes, triggers the side
effects (events, notifications, long-running operations) and returns the actual response. `Run` calls `validate`; on an error it returns that
error and nothing else runs. With `validateOnly` it then returns `validate`'s response; otherwise it passes the response to `commit` and returns
the result. Both steps are required whatever `validateOnly` says (`ErrMissingStep`), so a misconfigured method fails its validation requests
too.

Keep every check that can fail in `validate`: whatever runs only in `commit` (a unique-index violation, a store-side compare-and-swap) cannot
be reported by a validation request. Check permissions first, so PERMISSION_DENIED takes precedence as for the live request.

### What validate returns

AIP-163 asks for the response the request would return, and allows omitting values that cannot be produced without the write. This package
chooses:

| Method | Response of a validation request                                                                                                                                                                                                                     |
|--------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Create | The resource as it would be created, with `OmitGenerated` clearing the server-generated OUTPUT_ONLY values (create time, uid, etag). A resource name the service generates is cleared by the caller; a name derived from a client-chosen id is kept. |
| Update | The merged resource the update would store (`update.Patch` with `WithValidateOnly(true)`), keeping the stored OUTPUT_ONLY values.                                                                                                                    |
| Delete | The response the live Delete returns: `google.protobuf.Empty`, or the resource for a soft delete (AIP-164).                                                                                                                                          |
| Custom | The response the live method returns, minus values only the write produces.                                                                                                                                                                          |

```go
func (s *Server) CreateBook(ctx context.Context, req *pb.CreateBookRequest) (*pb.Book, error) {
	return validateonly.Run(ctx, req.GetValidateOnly(),
		func(ctx context.Context) (*pb.Book, error) {
			if err := s.authorize(ctx, "books.create", req.GetParent()); err != nil {
				return nil, err
			}
			book, err := s.prepare(req) // validate, apply defaults, build the name
			if err != nil {
				return nil, err
			}
			if req.GetValidateOnly() {
				if err := validateonly.OmitGenerated(book); err != nil {
					return nil, err
				}
			}
			return book, nil
		},
		func(ctx context.Context, book *pb.Book) (*pb.Book, error) {
			return s.store.Create(ctx, book)
		})
}
```

A Delete with an etag:

```go
return validateonly.Run(ctx, req.GetValidateOnly(),
	func(ctx context.Context) (*emptypb.Empty, error) {
		stored, err := s.store.Get(ctx, req.GetName())
		if err != nil {
			return nil, err
		}
		if err := etag.NewPrecondition().Check(req.GetEtag(), etag.FromRevision(stored.Revision)); err != nil {
			return nil, err
		}
		return &emptypb.Empty{}, nil
	},
	func(ctx context.Context, out *emptypb.Empty) (*emptypb.Empty, error) {
		return out, s.store.Delete(ctx, req.GetName())
	})
```

## Update with fieldmask/update

`update.Patch.Run` takes `update.WithValidateOnly(req.GetValidateOnly())`: it parses and checks the etag, checks the mask paths, reads
the stored resource, plans against it (AIP-203 behaviors) and merges, then returns the merged resource without calling `Save`. With
`update.WithAllowMissing(true)` and a missing resource it checks the precondition and returns the request resource without its
OUTPUT_ONLY values, without calling `Create`.

## AIP-163 conformance

| Rule                                                                    | Level  | How it is met                                                                                         |
|-------------------------------------------------------------------------|--------|-------------------------------------------------------------------------------------------------------|
| A method may validate without executing, with the same response         | MAY    | `Run` returns the validate step's response; `update.WithValidateOnly` returns the merged resource.    |
| The option is a `bool validate_only` request field                      | SHOULD | `Requested` reads exactly a singular bool `validate_only`; other kinds are `ErrInvalidField`.         |
| Permission checks and all live validation are performed                 | MUST   | Everything in the validate step runs for both kinds of request; the commit step holds only the write. |
| A validation request fails if the live request would fail               | MUST   | Errors of the validate step are returned unchanged; keep every failing check there.                   |
| Auto-generated fields that cannot be produced are omitted               | SHOULD | `OmitGenerated` clears OUTPUT_ONLY values; generated names are the caller's.                          |
| Declarative-friendly resources have `validate_only` on mutating methods | MUST   | Schema rule, outside this package.                                                                    |

## Errors

| Error             | Returned by     | Meaning                                                         |
|-------------------|-----------------|-----------------------------------------------------------------|
| `ErrInvalidField` | `Requested`     | The `validate_only` field is not a singular bool.               |
| `ErrMissingStep`  | `Run`           | A nil validate or commit step.                                  |
| any               | `Run`           | The error of the validate or commit step, unchanged.            |
| wrapped           | `OmitGenerated` | A field behavior resolution failure from `fieldbehavior.Strip`. |

## Limits

The package adds no client-controlled work: `Requested` reads one field, and `OmitGenerated` walks the would-be resource the server built.
