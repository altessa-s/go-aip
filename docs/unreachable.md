# Unreachable resources

[unreachable](../unreachable/) implements [AIP-217](https://google.aip.dev/217) for reads that fan out across sources. It works on plain Go
values and protobuf descriptors only and has no gRPC dependency.

## Conformance

| Rule                                                                                                  | Level         | Implemented by                                                | Behavior                                                                                                                                                                                                                   |
|-------------------------------------------------------------------------------------------------------|---------------|---------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| A partially failing read has a response field for unreachable resources                               | MUST          | `LintMethod`                                                  | Checked in consumer tests; the API adds the field.                                                                                                                                                                         |
| The field is a repeated string, named `unreachable`                                                   | MUST / SHOULD | `LintMethod` (`unreachable-type`)                             | A singular or non-string `unreachable` is a violation. The package reads only fields named `unreachable`.                                                                                                                  |
| It holds the names of the unreachable resources or of those impeding access (the parent location)     | MUST          | `Source.Name`, `NewError`                                     | A source reports its own name; a fetch reports other names, such as a zone of its region, with `NewError`.                                                                                                                 |
| Service-relative names, not full names, URIs or IDs                                                   | MUST          | `ValidateName` (`Set.Add`, `Run`, `WithUpscope`)              | `//service/...`, `scheme://...`, a name without `/`, empty segments and whitespace are rejected (`ErrInvalidName`); an invalid name from a fetch falls back to the source name, an invalid up-scoped name to the original. |
| No other information about the issues                                                                 | MUST NOT      | `Result`, `Set.Names`                                         | Only names reach the response; the fetch error stays server side (in `Error.Err` when the request fails).                                                                                                                  |
| A more specific request returns the actual error                                                      | MUST          | the API                                                       | Out of scope: a Get or List on the unreachable parent, which `Run` with a `Required` source turns into an error.                                                                                                           |
| The names may be heterogeneous and the field documents which                                          | MAY / SHOULD  | the API proto comment                                         | Any valid service-relative names mix freely.                                                                                                                                                                               |
| No meaningful order; annotated `UNORDERED_LIST`                                                       | MUST          | `LintMethod` (`unreachable-unordered`), `Set.Names`           | The annotation is checked. Names are returned sorted and deduplicated for deterministic responses; the order carries no meaning.                                                                                           |
| A request that by definition returns no data when one resource is unreachable fails entirely          | MUST          | `Source.Required`                                             | An unreachable required source fails `Run` with an `*Error` (UNAVAILABLE) regardless of partial success.                                                                                                                   |
| Pagination: report unreachable names on the page that found them, regardless of `order_by`            | MUST          | `Run`, `Set` per page                                         | Names are collected while preparing the page and returned with it; nothing is deferred to the last page.                                                                                                                   |
| Pagination: the most appropriately scoped name                                                        | MUST          | `Source.Name`, `NewError`, `WithUpscope`                      | Sources are as narrow as the backend; a fetch may report narrower names; up-scoping only happens over the limit.                                                                                                           |
| Pagination: previously unreachable results appear on a later page when restored and allowed by paging | MUST          | the API's page token                                          | `Run` keeps no state between pages; each page refetches every source from the position in the page token, so a restored source contributes its items after that position (`Example_pagination`).                           |
| Pagination: a documented maximum of names per response, independent of `page_size`                    | MUST          | `WithMaxNames` (default 100), `WithUpscope`                   | Over the maximum the names are up-scoped, then truncated to the lexicographically first; `Truncated` reports it. The maximum is documented on the proto field by the API.                                                  |
| Adoption: the default behavior is retained                                                            | MUST          | `WithPartialSuccess`, `Set.Response`                          | `WithPartialSuccess(req.GetReturnPartialSuccess())`: false fails as soon as a source is unreachable (fail-fast, other fetches canceled). An API born with `unreachable` keeps the default `true`.                          |
| Adoption: a `bool return_partial_success` request field added together with `unreachable`             | MUST          | `LintMethod` (`partial-success-type`, `partial-success-pair`) | A non-bool or repeated flag, or a flag without `unreachable` in the response, is a violation.                                                                                                                              |
| Adoption: `return_partial_success = true` behaves as the guidance                                     | MUST          | `Run`, `Set.Response(true)`                                   | Reachable items plus unreachable names.                                                                                                                                                                                    |
| Granularity: `return_partial_success` beyond the supported scope is INVALID_ARGUMENT                  | SHOULD        | `CheckGranularity`                                            | `coreerrs.FieldError` on `returnPartialSuccess` wrapping `ErrPartialSuccessUnsupported`. The supported scope is the API's decision (typically a `-` parent) and is documented on the field.                                |

## Deliberate choices

* **Failure code.** AIP-217 does not name the status of a request that fails because resources are unreachable. `*Error` maps to UNAVAILABLE
  (`coreerrs.ErrUnavailable`), the AIP-193 code for a transient condition. The error text lists the names; it is a failure, not a response, so
  AIP-217's "no other information" rule on the response field does not apply to it.
* **What is unreachable.** Only errors the classifier accepts make a source unreachable: by default `ErrUnreachable`,
  `coreerrs.ErrUnavailable` and a per-source `context.DeadlineExceeded`. Permission, validation and internal errors fail the call, so they are
  never hidden as "unreachable". gRPC status errors from a backend client are mapped by the caller (`WithClassifier`), keeping the package free
  of a gRPC dependency.
* **Fail fast.** With partial success off, the first unreachable source cancels the remaining fetches; the `*Error` lists the names found up
  to that point.
* **Deterministic merge.** Items are merged in source order, not completion order, and names are sorted, so equal inputs give equal
  responses.

## Integration

* **listing / pagination.** `Run` is per page. When every source can seek in the list's order, a multi-source List keeps one global
  keyset position in its page token (`pagetoken.Typed`, sort values plus id): each `Source` reads `page_size + 1` items after it, the merged
  items are sorted and cut to `page_size`, and the next token carries the last item kept (`Example_pagination`). A restored source serves its
  items after the client's position; earlier ones are behind it, which AIP-217 permits ("if allowed by paging parameters"). Sources that
  cannot seek to a shared position need a per-source cursor map in the token instead (an unreachable source keeps its cursor and resumes
  there), at the price of items surfacing out of the merged order. The listing engines issue opaque per-engine tokens, so a merged order
  over them is not possible; page the stores directly. See the [README](../unreachable/README.md#pagination).
* **Transport.** A gRPC interceptor maps `*Error` (via `coreerrs.ErrUnavailable`) to UNAVAILABLE and the `CheckGranularity` field error to
  INVALID_ARGUMENT with a BadRequest field violation on `returnPartialSuccess`.
