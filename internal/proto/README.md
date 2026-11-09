# proto

```go
import (
    batchtestv1 "github.com/altessa-s/go-aip/internal/proto/gen/batchtest/v1"
    etagtestv1 "github.com/altessa-s/go-aip/internal/proto/gen/etagtest/v1"
    fieldbehaviortestv1 "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
    fieldinfotestv1 "github.com/altessa-s/go-aip/internal/proto/gen/fieldinfotest/v1"
    fieldmasktestv1 "github.com/altessa-s/go-aip/internal/proto/gen/fieldmasktest/v1"
    requestidtestv1 "github.com/altessa-s/go-aip/internal/proto/gen/requestidtest/v1"
    resourcenametestv1 "github.com/altessa-s/go-aip/internal/proto/gen/resourcenametest/v1"
    softdeletetestv1 "github.com/altessa-s/go-aip/internal/proto/gen/softdeletetest/v1"
    statetestv1 "github.com/altessa-s/go-aip/internal/proto/gen/statetest/v1"
    unreachabletestv1 "github.com/altessa-s/go-aip/internal/proto/gen/unreachabletest/v1"
)
```

Protobuf test fixtures: the `.proto` sources and the Go code generated from them. Internal to the module and used only by tests of
[apierror/aipmap](../../apierror/aipmap), [batch](../../batch), [etag](../../etag), [fieldbehavior](../../fieldbehavior),
[fieldinfo](../../fieldinfo), [fieldmask](../../fieldmask), [projection/protopath](../../projection/protopath), [requestid](../../requestid),
[resourcename](../../resourcename), [softdelete](../../softdelete), [state](../../state), [unreachable](../../unreachable),
[validateonly](../../validateonly) and [internal/behavior](../behavior).

## Layout

| Path                   | Contents                                                                                                                                                                                                                                                                                                                                                                                                                                             |
|------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `batchtest/v1`         | `Book` and the standard Get/Create/Update/Delete requests with the AIP-231 to AIP-235 batch requests (names and nested forms, hoisted `update_mask` and `validate_only`, a same-named field of another kind, a nested resource reached after a message without `name`, a non-batch message) for the batch tests (`batch.proto`)                                                                                                                      |
| `etagtest/v1`          | `Book` (`etag`, OUTPUT_ONLY fields, sub-message, repeated and map fields) and `Shelf` for AIP-154 content hashes; `CreateBookRequest` and `DeleteBookRequest` with `etag`/`validate_only`; `Malformed` and `Lists` with `etag`/`validate_only` fields of the wrong kind; used by the etag and validateonly tests                                                                                                                                     |
| `fieldbehaviortest/v1` | `Resource` with every `google.api.field_behavior` value, plus AIP-134/AIP-157 request shapes (canonical and nested `Options`); `Account`, `Owner` and `Settings` for REQUIRED shapes, stored-value comparison and recursive expansion                                                                                                                                                                                                                |
| `fieldinfotest/v1`     | `Host` with every `google.api.field_info` format on singular, optional, repeated, map and oneof string fields, nested/repeated/map `Interface` messages, a recursive parent, an `Any` with `referenced_types`, a format on bytes (ignored), OUTPUT_ONLY/IMMUTABLE fields with formats; `Plain` without formats                                                                                                                                       |
| `fieldmasktest/v1`     | `UpdateRequest`, `Address` and `Options` for update-mask behavior validation (`test.proto`); `Catalog` and `Shelf` for the AIP-161 path grammar (`grammar.proto`): repeated and map fields of every map key kind, nested messages with multi-word names and a dynamic `google.protobuf.Struct`                                                                                                                                                       |
| `requestidtest/v1`     | `CreateBookRequest` (AIP-155 shape: `request_id` annotated `(google.api.field_info).format = UUID4`, plus `validate_only`, a numeric and a repeated field for option checks), `Book` (with a bytes `cover` for response-size limits) and `GetBookRequest` without a `request_id` (`requestid.proto`)                                                                                                                                                 |
| `resourcenametest/v1`  | Resource annotations: `Publisher` (top-level), `Book` (nested), `Shelf` (multi-pattern over file-level `Project`/`Organization` `resource_definition`s), `PublisherConfig` (singleton), `Edition` (custom `name_field`); request shapes with `resource_reference` `type`/`child_type` (Get, List, Create, Update, BatchGet with repeated names, `"*"`, an unknown type) (`test.proto`); `InvalidResource` with a malformed pattern (`invalid.proto`) |
| `softdeletetest/v1`    | `Book` (OUTPUT_ONLY `delete_time` and `purge_time`), `Shelf` (`delete_time` and `expire_time`), `Note` (no `delete_time`), `Draft` (`delete_time` not OUTPUT_ONLY), `Scroll` (wrong-typed and repeated time fields) for AIP-164 descriptor checks; `PurgeBooksRequest` and `PurgeTopicsRequest` for the AIP-165 request shapes                                                                                                                       |
| `statetest/v1`         | `Book` (conforming nested `State`, OUTPUT_ONLY), `Job` with top-level `JobState`, `Shelf` and `Order` breaking each AIP-216 enum rule; `StateTestService` with conforming (`PublishBook`, `SoftDeleteBook`) and non-conforming state transition methods with `google.api.http` bindings (`state.proto`)                                                                                                                                              |
| `unreachabletest/v1`   | `UnreachableTestService`: `ListBooks` with conforming `return_partial_success`/`unreachable`, and one method per violated AIP-217 field rule (`unreachable.proto`)                                                                                                                                                                                                                                                                                   |
| `gen/`                 | Generated Go packages `batchtestv1`, `etagtestv1`, `fieldbehaviortestv1`, `fieldinfotestv1`, `fieldmasktestv1`, `requestidtestv1`, `resourcenametestv1`, `softdeletetestv1`, `statetestv1` and `unreachabletestv1`; do not edit                                                                                                                                                                                                                      |
| `buf.yaml`, `buf.lock` | buf module with its `buf.build/googleapis/googleapis` dependency (for `google/api/field_behavior.proto`, `field_info.proto`, `resource.proto` and `annotations.proto`)                                                                                                                                                                                                                                                                               |
| `buf.gen.yaml`         | Code generation: `protoc-gen-go` into `gen/` with `paths=source_relative`                                                                                                                                                                                                                                                                                                                                                                            |

## Regenerating

The generated code is checked in and is not part of `make generate`. After changing a `.proto` file, run in this directory:

```sh
buf generate
```

and format the output from the repository root with `bin/golangci-lint fmt` (the binary is built by `make generate`). `buf.gen.yaml` runs
`protoc-gen-go` as `go run google.golang.org/protobuf/cmd/protoc-gen-go`, so the generated code matches the protobuf runtime version pinned in
the root `go.mod`. buf itself must be installed, and resolving the googleapis dependency needs network access to buf.build.
