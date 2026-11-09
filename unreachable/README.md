# unreachable

```go
import "github.com/altessa-s/go-aip/unreachable"
```

Package `unreachable` implements [AIP-217](https://google.aip.dev/217) for reads that fan out across sources: Lists across collections
([AIP-159](https://google.aip.dev/159)), locations, shards or cells. It collects the service-relative names of the unreachable resources of a
response, applies `return_partial_success`, bounds the reported names and runs the fan-out itself with bounded concurrency and per-source
timeouts. It works on plain Go values and protobuf descriptors and has no gRPC dependency. The [guide](../docs/unreachable.md) maps every
AIP-217 rule to the code that implements it.

## Quick start

```go
func (s *Server) ListBooks(ctx context.Context, req *pb.ListBooksRequest) (*pb.ListBooksResponse, error) {
	// return_partial_success is only supported across collections ("-" parent).
	if err := unreachable.CheckGranularity(req.GetReturnPartialSuccess(), isWildcard(req.GetParent())); err != nil {
		return nil, err // INVALID_ARGUMENT on returnPartialSuccess
	}

	sources := make([]unreachable.Source[*pb.Book], 0, len(s.locations))
	for _, loc := range s.locations {
		sources = append(sources, unreachable.Source[*pb.Book]{
			Name:  loc.Name, // "projects/p/locations/us-east1"
			Fetch: func(ctx context.Context) ([]*pb.Book, error) { return loc.ListBooks(ctx, req) },
		})
	}

	res, err := unreachable.Run(ctx, sources,
		unreachable.WithPartialSuccess(req.GetReturnPartialSuccess()), // omit for an API without the field
		unreachable.WithSourceTimeout(2*time.Second))
	if err != nil {
		return nil, err // UNAVAILABLE (*unreachable.Error) or the failing source's error
	}

	return &pb.ListBooksResponse{Books: res.Items, Unreachable: res.Unreachable}, nil
}
```

## API

| Symbol                             | Purpose                                                                                                                                                                      |
|------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Set`, `NewSet`                    | Collects names for one response: `Add` validates and deduplicates, `Names` sorts and bounds, `Response(partialSuccess)` returns the names or fails. Safe for concurrent use. |
| `Run[T]`, `Source[T]`, `Result[T]` | Fan-out of one page: fetches every source concurrently, merges the items in source order, reports unreachable sources.                                                       |
| `NewError`, `Error`                | An *Error from a fetch names finer-scoped unreachable resources; from `Run` or `Set.Response` it fails the request as UNAVAILABLE.                                           |
| `SourceError`                      | A fetch error the classifier does not accept; it fails `Run`.                                                                                                                |
| `IsUnreachable`                    | Default classifier: `ErrUnreachable`, `coreerrs.ErrUnavailable` or `context.DeadlineExceeded`.                                                                               |
| `CheckGranularity`                 | `return_partial_success` on an unsupported request scope → `coreerrs.FieldError` on `returnPartialSuccess` (INVALID_ARGUMENT).                                               |
| `ValidateName`                     | Service-relative resource name check used by `Add`, `Run` and `WithUpscope`.                                                                                                 |
| `LintMethod`, `Violation`, `Rule`  | Descriptor check of a method's `unreachable` and `return_partial_success` fields, for consumer tests.                                                                        |

## Semantics

### Names

`unreachable` holds service-relative resource names only (`projects/p/locations/us-east1`, `shelves/s1/books/b1`). `ValidateName` rejects an
empty name, a full resource name (`//library.googleapis.com/...`), a URI (`https://...`), a bare ID (no `/`), an empty segment and whitespace or
control characters. Names are deduplicated, and returned sorted: AIP-217 gives the list no order, sorting only makes responses deterministic
and testable. Clients must not rely on the order.

### Partial success

| Request                                                                               | Outcome                                                                                   |
|---------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------|
| API without `return_partial_success` (`WithPartialSuccess` omitted, `Response(true)`) | Items of the reachable sources, unreachable names in the response.                        |
| `return_partial_success = true`                                                       | Same as above.                                                                            |
| `return_partial_success = false`, nothing unreachable                                 | Items, empty `unreachable`.                                                               |
| `return_partial_success = false`, any source unreachable                              | `*Error` (UNAVAILABLE) listing the names found; `Run` stops the other fetches at once.    |
| a `Required` source unreachable (the single parent the request is scoped to)          | `*Error` (UNAVAILABLE) whatever the flag says: the request returns no data by definition. |
| `return_partial_success = true` on an unsupported scope                               | `CheckGranularity` → `coreerrs.FieldError` on `returnPartialSuccess` (INVALID_ARGUMENT).  |

AIP-217 names no status code for the failing case; the package uses UNAVAILABLE (AIP-193: a transient condition the client may retry, for
example with a narrower request). The `*Error` lists the names; per AIP-217 the response itself carries no other detail, and a more specific
Get or List on the unreachable parent gives the actual error.

### Run

* Sources are validated first: a nil `Fetch`, an invalid or repeated `Name` is an error matching `ErrInvalidSource`.
* At most `WithConcurrency` fetches run at once (16 by default, 0 unlimited); each gets a context bounded by `WithSourceTimeout`.
* A fetch error the classifier accepts makes the source unreachable: its `Name` is reported, or the names of an `*Error` it returned
  (`NewError("projects/p/locations/us-west1-a")` to report a zone of the region a source covers, the "most appropriately scoped" name). Items
  returned along with such an error are kept.
* Any other fetch error fails `Run` with a `*SourceError`, the lowest-indexed one when several fail, and cancels the other fetches.
* The caller's context ending fails `Run` with the context's cause; its deadline is never reported as unreachable sources.
* `Result.Items` are in source order, each source's items as returned; reorder or page them afterwards if the page has its own order.
* `Run` waits for every started fetch before returning, so a fetch must honor its context.

### Pagination

`Run` and `Set` serve one page: the names reported are those found while preparing that page, as AIP-217 requires ("immediately upon
discovery", regardless of `order_by`). Nothing is retained between pages; the read's page token carries the position.

When every source can read in the list's order from a position (a keyset `WHERE (key, id) > (?, ?) ORDER BY key, id LIMIT ?`), one global
position serves all of them, and the merged list keeps its order across pages:

1. Resolve the token (`pagetoken.Typed[C]`, bound to the RPC, parent, canonical filter and order) into the position of the last item served.
2. Build one `Source` per location whose `Fetch` reads `page_size + 1` items after that position.
3. `Run`, then sort `Result.Items` by the list order (`Run` merges in source order) and cut them to `page_size`.
4. If more than `page_size` items were merged, issue the next token with the position of the last item kept; otherwise the walk is done.
5. Return the page, the token and `Result.Unreachable`.

Every source then contributes exactly the items that follow the client's position. A source that comes back after an outage serves its items
after that position on the next page; those before it are behind the client and are skipped — AIP-217 asks for restored results only "if
allowed by paging parameters". `Example_pagination` runs this with three regions and a signed token. Key the position by the sort values
and a unique id (`pagetoken.Position` and `pagetoken.ComparePositions` do this for multi-key orders), so ties never skip or repeat an item.

A source that cannot seek to a shared position (an offset-only or cursor-only backend) needs its own cursor in the token instead: a
`map[source]cursor` payload where a reachable source advances to the last item it contributed and an unreachable one keeps its old cursor.
The restored source then resumes where it stopped, but its earlier items surface on a later page out of the merged order; prefer the global
position whenever the sources can seek.

The listing engines (`NewListEngine` of the backends) issue their own opaque tokens and cannot resume from an arbitrary merged position, so a
multi-source List over listing engines pages each source independently; a merged order needs the global position over the stores
themselves.

## Errors

| Error                          | Match with                                                                            | Status                        |
|--------------------------------|---------------------------------------------------------------------------------------|-------------------------------|
| `*Error`                       | `ErrUnreachable`, `coreerrs.ErrUnavailable`, its `Err`                                | UNAVAILABLE                   |
| `*SourceError`                 | the fetch error                                                                       | that error's status           |
| `CheckGranularity` field error | `ErrPartialSuccessUnsupported`, `coreerrs.ErrInvalidArgument`, `*coreerrs.FieldError` | INVALID_ARGUMENT              |
| invalid name                   | `ErrInvalidName`                                                                      | programmer error              |
| invalid source                 | `ErrInvalidSource` (and `ErrInvalidName` for a bad name)                              | programmer error              |
| canceled request               | the context's cause                                                                   | CANCELLED / DEADLINE_EXCEEDED |

## Options

| Option               | Default                        | Applies to   | Behavior                                                                                         |
|----------------------|--------------------------------|--------------|--------------------------------------------------------------------------------------------------|
| `WithMaxNames`       | `DefaultMaxNames` (100)        | `Set`, `Run` | Maximum names reported; 0 unlimited, negative keeps the default.                                 |
| `WithUpscope`        | nil                            | `Set`, `Run` | Maps a name to its broader scope, applied only over the limit; an invalid result keeps the name. |
| `WithConcurrency`    | `DefaultConcurrency` (16)      | `Run`        | Concurrent fetches; 0 unlimited, negative keeps the default.                                     |
| `WithSourceTimeout`  | none                           | `Run`        | Per-fetch timeout; exceeding it makes the source unreachable.                                    |
| `WithPartialSuccess` | `DefaultPartialSuccess` (true) | `Run`        | The request's `return_partial_success`.                                                          |
| `WithClassifier`     | `IsUnreachable`                | `Run`        | Decides which fetch errors make a source unreachable; nil restores the default.                  |

## Limits

AIP-217 requires a documented maximum of names per response, independent of `page_size`. `WithMaxNames` is that maximum (100 by default):
over it the names are up-scoped with `WithUpscope` (zones to their region, say), then truncated to the lexicographically first ones, and
`Truncated` reports the truncation. Document the maximum on the `unreachable` field of the proto:

```proto
// Unreachable locations, e.g. "projects/p/locations/us-east1". At most 100
// names are returned; more unreachable resources are reported by their
// region. The list may later hold other resource types.
repeated string unreachable = 3 [(google.api.field_behavior) = UNORDERED_LIST];
```

The number of sources is server-controlled and not limited; `WithConcurrency` bounds the parallel work.

## Descriptor check

```go
func TestAIP217(t *testing.T) {
	m := pb.File_library_v1_library_proto.Services().ByName("Library").Methods().ByName("ListBooks")
	for _, v := range unreachable.LintMethod(m) {
		t.Errorf("%s: %s (%s)", v.Element, v.Message, v.Rule)
	}
}
```

| Rule                    | Check                                                                     |
|-------------------------|---------------------------------------------------------------------------|
| `unreachable-type`      | `unreachable` is a repeated string                                        |
| `unreachable-unordered` | `unreachable` is annotated `UNORDERED_LIST`                               |
| `partial-success-type`  | `return_partial_success` is a singular bool                               |
| `partial-success-pair`  | a request with `return_partial_success` has a response with `unreachable` |

## Related packages

- [go-aip](../README.md)
- [listing](../listing/README.md)
- [pagination/pagetoken](../pagination/pagetoken/README.md)
- [fieldbehavior](../fieldbehavior/README.md)
- [unreachable guide](../docs/unreachable.md)
