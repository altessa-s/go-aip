# States

[state](../state/) implements [AIP-216](https://google.aip.dev/216): transition tables for state enums, state-changing custom methods and
descriptor checks. It works on generated protobuf enums and descriptors only and has no gRPC dependency.

## Conformance

| Rule                                                                                           | Level         | Implemented by                                               | Behavior                                                                                                                                                  |
|------------------------------------------------------------------------------------------------|---------------|--------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------|
| A resource's state is an enum called `State` or ending in `State`                              | SHOULD        | `Lint` (`state-enum-name`)                                   | A state field whose enum name does not end in `State` is reported.                                                                                        |
| The enum is nested in the message it describes                                                 | SHOULD        | `Lint` (`state-enum-nested`)                                 | A top-level state enum is reported; an enum shared by several resources may skip the rule.                                                                |
| `State`, not `Status`                                                                          | —             | `Lint` (`state-not-status`)                                  | An enum field named `status` or whose enum ends in `Status` is reported.                                                                                  |
| Values: `ACTIVE`, past participles for completed actions, present participles for ongoing ones | guidance      | `Lint` (`state-value-term`)                                  | The known replaced terms (`READY`, `AVAILABLE`, `SUCCESS`, `SUCCESSFUL`, `FAILURE`) are reported; participle forms are a naming judgment and not checked. |
| The state field is OUTPUT_ONLY                                                                 | SHOULD        | `CheckOutputOnly`, `Lint` (`state-output-only`)              | A state field without the annotation is reported.                                                                                                         |
| Create and Update do not set the state                                                         | SHOULD NOT    | `fieldbehavior`, `fieldmask` with the OUTPUT_ONLY annotation | A client value is cleared on Create and ignored in an update mask; only `Machine.Transition` changes it.                                                  |
| State transition methods: verb + singular resource name                                        | SHOULD        | `LintTransitionMethod` (`transition-method-name`)            | `PublishBook` for `Book`; `Unpublish` is reported.                                                                                                        |
| Request named `<Rpc>Request`                                                                   | MUST          | `LintTransitionMethod` (`transition-request-name`)           |                                                                                                                                                           |
| Response is the resource, or an LRO resolving to it                                            | SHOULD        | `LintTransitionMethod` (`transition-response`)               | The resource or `google.longrunning.Operation`; the LRO's response type annotation is not inspected.                                                      |
| HTTP verb POST                                                                                 | MUST          | `LintTransitionMethod` (`transition-http-method`)            | Checked when the method has `google.api.http`.                                                                                                            |
| URI ends with `:` + the RPC's verb, camelCase                                                  | MUST          | `LintTransitionMethod` (`transition-http-verb`)              | `:publish`, `:softDelete`; a missing, mismatched or snake_case verb is reported.                                                                          |
| `body: "*"`                                                                                    | MUST          | `LintTransitionMethod` (`transition-http-body`)              |                                                                                                                                                           |
| Resource name field, called `name`, mapped to the URI path                                     | MUST / SHOULD | `LintTransitionMethod` (`transition-name-field`)             | The request must have a singular string `name`; the URI variable mapping is not parsed.                                                                   |
| A disallowed transition fails with FAILED_PRECONDITION                                         | MUST          | `Machine.Check`, `Machine.Transition`                        | `*TransitionError` matching `coreerrs.ErrFailedPrecondition`; the resource is left untouched.                                                             |
| Zero value `STATE_UNSPECIFIED`                                                                 | SHOULD        | `Lint` (`state-zero-value`)                                  | The enum name in UPPER_SNAKE_CASE plus `_UNSPECIFIED`; a closed enum without a zero value is reported too.                                                |
| The unspecified state is not provided to users or used                                         | SHOULD NOT    | `New`                                                        | The zero value cannot appear in a table, so no method moves a resource into or out of it.                                                                 |
| Top-level enum values unique within a package                                                  | MUST          | protoc                                                       | Enforced by the compiler (C++ scoping); not rechecked.                                                                                                    |
| Values not prefixed with the enum name, except the zero value                                  | SHOULD        | `Lint` (`state-value-prefix`)                                | `STATE_ACTIVE` in `State` is reported.                                                                                                                    |
| New states may be added; clients code for unknown values                                       | MAY / SHOULD  | `Machine`                                                    | An undeclared number is never in a table: it moves nowhere and its name is reported as a number.                                                          |

## Deliberate choices

* **No implicit self-transition.** `Check(PUBLISHED, PUBLISHED)` fails, matching AIP-216's `PublishBook` example. Idempotent methods declare
  the self-edge.
* **Terminal by construction.** Terminal states are the states with no targets; there is no separate list that could disagree with the table.
* **Lint is advisory.** Most AIP-216 rules are SHOULDs. Every finding carries a `Rule`, so an API that shares a top-level state enum, for
  example, filters `state-enum-nested` in its test.
* **Transition does not persist.** Concurrency control (etag, AIP-154) and persistence belong to the handler and storage; `Transition` only
  checks and mutates the in-memory resource, so the check and the save must run against the same stored version.
* **Status is reported, not reclassified.** A `status` field is not treated as a state field: it gets one `state-not-status` finding and no
  further checks.

## Integration

* **fieldbehavior / fieldmask.** With `state` annotated `OUTPUT_ONLY`, `fieldbehavior.StripCreate` clears it on Create and
  `update.Apply` drops it from an update mask, so the transition methods are the only writers.
* **Transport.** A gRPC interceptor maps `*TransitionError` (via `coreerrs.ErrFailedPrecondition`) to FAILED_PRECONDITION.
