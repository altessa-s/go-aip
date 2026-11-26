# listing

```go
import "github.com/altessa-s/go-aip/listing"
```

The List contract of a service's List RPCs, implemented once and backend-neutral: page size, signed page token, CEL filter (AIP-160), `orderBy`
restricted to declared sort patterns (AIP-132), and the parent of a child list. The engines of the backend packages serve it with identical proto3
semantics: [`backend/memory`](../backend/memory/#list-engine) (`pagetoken.SlicePager`), [`backend/sqlbase`](../backend/sqlbase/#listing-engine)
(PostgreSQL, MariaDB and ClickHouse, with the dialect fixed by `postgres.NewListEngine`, `mariadb.NewListEngine` and `clickhouse.NewListEngine`) and
[`backend/mongo`](../backend/mongo/#list-engine) (MongoDB). [`listingtest`](listingtest) is the conformance suite every engine runs.

## Contract

`NewSchema(fields, patterns, profile)` separates immutable field and order metadata from execution policy. Pass nil for the portable proto3 preset or
reuse `NewProfile(opts...)`; `MustSchema` has the same arguments and panics on invalid metadata. Options are `ProfileOption` values. Field lookups
return detached enum metadata.

`Schema.Compile(ctx, Request)` resolves page size, parent requirements, order, read-mask visibility, and filter validation before execution. Defaults
are 50 items, a 500-item maximum, filter depth 12, and 128 operations. `RequestFromProto` accepts `ProtoRequest`; parent is read when the message also
supplies `GetParent`.

Required scalars must be stored. `Optional` means implicit proto3 presence: absent or null reads as the kind's zero value. `Array` means repeated
values: absent, null, and empty all read as an empty list. `Presence` means explicit nullable presence with the default evaluator's null semantics:
null differs from every value, so `!=` and a negated equality, membership or string predicate select it, while an ordered comparison with null is
unknown. `Presence` cannot be combined with `Optional` or `Array`. Null tests, `has`, and `size` are two-valued; `size(null)` is zero.

The portable preset requires literal operands, finite floats, nonempty membership lists without null, and the common regex subset. Arrays match any
element for equality and ordered predicates; inequality is the complement of equality. Optional enums declare `EnumZero`. Timestamps normalize to UTC
milliseconds by default. Profile precision zero preserves nanoseconds; engine cursor precision must be positive and storage-aligned.

A declared sort pattern or its full reversal is accepted. Sort fields are required comparable scalars by default. Hidden fields and fields excluded by
`Request.ReadMask` cannot sort unless `WithUnreadableSortAllowed(true)` permits them: signed tokens disclose sort values. Engines append a unique
identity in the last key's direction. Unrestricted and ID-only ordering are explicit profile policies.

`Engine[T, S]` serves an explicit source or scope; `Bind` produces `Lister[T]` with source and subject captured. The caller must not mutate a bound
scope during requests. Tokens bind the subject, parent, canonical filter and order, engine storage contract, and `ContractID`; page size may change
between pages. A nil signer serves one page and rejects supplied tokens. Pagination is not a snapshot.

Parsers, validators, literal conversion, field-name validation, page-size resolution, and error formatting are replaceable. Injected dependencies must
support concurrent use and parsers return caller-owned ASTs. Later filter options override metadata-derived defaults in both compilation and engines.
Change `WithContractID` whenever custom parsing, evaluation, or ordering semantics change; its default is `portable-v1`.

Default request failures are field errors on `pageSize`, `parent`, `orderBy`, `filter`, or `pageToken`, preserving the cause. `WithErrorFormatter`
customizes schema failures and translation errors; engines separately expose query error mapping. Every engine should run the shared listingtest
conformance suite.

## Request contract

```proto
int32  pageSize  = 1;  // 0 → 50 (WithDefaultPageSize); > 500 is clamped (WithMaxPageSize); negative is invalid
string pageToken = 2;  // opaque, signed, bound to RPC + parent + filter (with the server restriction) + order
string filter    = 3;  // CEL over the schema's public field names
string orderBy   = 4;  // a declared pattern or its full reversal; empty → the default order
// child lists: string parent (required, listing.WithParentRequired)
// partial responses: the resolved read mask (AIP-157) goes to Request.ReadMask
// response: repeated items; string nextPageToken; no totalSize
```

`listing.Request{PageSize, PageToken, Filter, OrderBy, Parent, ReadMask, Restriction, Cutoff}` carries it (both predicates are set by the handler, never read
from the message; see [Server restriction](#server-restriction)); a gRPC handler builds it from the request message with
`listing.RequestFromProto(req)` (any message with the `ProtoRequest` getters; `parent` when it also has `GetParent`) and passes the gRPC full method
(`xxxv1.XxxService_ListYyy_FullMethodName`) as the list subject down to the store, so tokens of different RPCs never cross. Every invalid request is
an InvalidArgument [`go-atlas-core/errors.FieldError`](https://github.com/altessa-s/go-atlas-core/tree/develop/errors) on exactly one field —
`pageSize`, `parent`, `orderBy`, `filter` (checked in that order before the query) or `pageToken` (when the engine resolves the position) — with the
cause kept in the chain (`errors.Is(err, filter.ErrFieldNotAllowed)`, `pagetoken.ErrExpiredToken`, …). A gRPC error mapper that honors canonical field
errors (such as the go-atlas errstatus interceptor) reports it as a BadRequest field violation.

## Server restriction

`Request.Restriction` is a `filter.Node` the server ANDs onto every request: the AIP-164 `show_deleted` rule
([`softdelete.Policy.Restriction`](../softdelete/README.md#show_deleted-in-list)), a tenant or caller scope. `Compile` builds
`(client filter && default equalities) && restriction`, so a blank filter still gets it and a client filter can only narrow it, never widen it
(`archived == true || archived == false` under `archived == false` still returns only unarchived items). Every engine applies it, because it
is part of `Query.Filter`, and binds it into the page token through the canonical filter: a token issued under one restriction is `malformed`
under another or none.

```go
req := listing.RequestFromProto(msg)
req.Restriction = softdelete.New().Restriction(msg.GetShowDeleted()) // deleteTime == null unless show_deleted
page, err := books.List(ctx, req)
```

| Aspect             | Behavior                                                                                                                                                                                                                                         |
|--------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Trust              | Server input. It never comes from the request message (`RequestFromProto` leaves it nil); nil or a typed nil restricts nothing.                                                                                                                  |
| Validation         | The schema filter policy and the profile's call, `size()` and presence checks apply. Server predicates can use fields named by `WithServerOnlyFields` and use stored enum values. |
| Refusal            | `ErrInvalidRestriction` (wraps `ErrInvalidSchema`, INTERNAL in `apierror/aipmap`), never a field error: a server bug is not blamed on the client. Checked at the filter step, before the client filter is parsed.                                |
| Default equalities | Decided by the client filter alone; the restriction is joined after them and never suppresses or triggers one.                                                                                                                                   |
| Literals           | The AST skips the parser: an enum field takes its stored value (`"a"`), not the proto name (`"KIND_A"`, refused). Timestamps are cut to the profile precision on a copy.                                                                         |
| Immutability       | The node is cloned per request; the caller's node is never modified and may be shared by concurrent requests.                                                                                                                                    |
| Limits             | The combined filter must fit `WithMaxFilterDepth` / `WithMaxFilterOperations`; a client filter that only exceeds them with the restriction is a field error on `filter` (`filter.ErrMaxOperationsExceeded`). Leave headroom for the restriction. |
| Paging             | Must be the same on every page of a walk: derive it from the request and the caller, never from the clock.                                                                                                                                       |
| Engines            | Use operations every engine of the list translates (equalities, null tests, ranges); an engine translator's refusal is reported like a client filter's.                                                                                          |

A handler may also set `Request.Cutoff` to a server predicate whose value advances between pages, such as a restore deadline.
`Schema.Compile` clones and validates it under the restriction rules and returns it through `Query.Cutoff()`. A refusal,
including a cutoff that pushes the whole expression over its limits, is `ErrInvalidRestriction` (INTERNAL).
SQL List and Count and the memory engine apply the cutoff before pagination. MongoDB refuses it because its keyset
reader binds the entire BSON predicate. Use matching root and backend releases when enabling cutoffs.

The cutoff only removes rows. Keyset pagination resumes strictly after the same sort values and identity, even if
the last returned row has since expired. Rows that remain eligible keep their positions. Use `Restriction` for
tenant, caller, and request predicates whose change must invalidate the walk.

Declare server-only predicate fields in the schema as usual, then pass
`listing.NewProfile(listing.WithServerOnlyFields("deleteTime", "purgeTime"))` to the schema constructor.
`Restriction` and `Cutoff` can use these fields subject to the shared filter policy and engine rules. A client filter
that references one, including through a custom function expansion, gets the same field error as a disallowed field
(`filter.ErrFieldNotAllowed`). `WithFilterOptions` still applies to both client and server predicates. Sort declarations
and projection rules are configured independently.

## Page tokens and page sizes

Page tokens come from the [`pagination/pagetoken`](../pagination/pagetoken) codec (HMAC-SHA256 signed, or AES-256-GCM encrypted with
`pagetoken.WithEncryption`, with retired keys and a TTL), bound to the list (subject), its parent, its order and its filter (the client's
filter with the default equalities and the server restriction). A service builds the
codec once and passes it to its engines. A nil codec issues no token: the list serves one page (a CLI) and a presented token is `malformed`. An empty
token is the first page. Tokens of one engine are not valid on another; a store uses one engine.

- `pagetoken.ResolvePageSize(requested, default, max)` resolves the page size with the schema's `WithDefaultPageSize` / `WithMaxPageSize`
  (defaults 50 / 500; 0 is the default, larger is clamped) and rejects a negative one with a field error on `pageSize`.
- `pagetoken.TokenFieldError(err)` maps every token failure (`pagetoken.Classify`) to a field error on `pageToken`: `expired` for a genuine token
  past its TTL, `malformed` for a bad, tampered or foreign token (other list, parent, filter, order or key) and for a token sent to a list
  without a codec. The page-token error stays in the chain (`errors.Is(err, pagetoken.ErrExpiredToken)`).
- `pagetoken.SizeField` and `pagetoken.TokenField` are `pagetoken.SizeField` and `pagetoken.TokenField`.

Tests build codecs with fixed keys through [`pagination/pagetoken/pagetokentest`](../pagination/pagetoken/pagetokentest); expiry tests drive
`pagetoken.WithClock`.

## Schema and execution profile

`NewSchema(fields, patterns, profile)` binds metadata to a reusable `Profile`. Pass `nil` for the portable proto3 defaults or construct
one with `NewProfile(opts...)`. All policy options are generated `ProfileOption` values.
`MustSchema` takes the same three arguments. Field, enum, and order lookup maps are immutable; `Field` returns detached enum metadata.
Injected parsers, validators, converters, and formatters must be concurrency safe. Mutable values captured by hooks remain application-owned.

```go
var widgetSchema = listing.MustSchema(
	[]listing.Field{
		{Name: "id", Kind: filter.FieldKindString},
		{Name: "name", Kind: filter.FieldKindString},
		{Name: "state", Kind: filter.FieldKindString, Enum: map[string]any{"STATE_ACTIVE": "active"}},
		{Name: "createTime", Kind: filter.FieldKindTimestamp},
		{Name: "note", Kind: filter.FieldKindString, Optional: true},
		{Name: "tags", Kind: filter.FieldKindString, Array: true},
	},
	[]string{"createTime desc", "name", "state, name"}, // the first pattern is the default order
	listing.NewProfile(
		listing.WithFunctions(map[string]filter.CustomFunction{"search": functions.TextSearch([]string{"name", "note"})}),
	),
)
```

Fields and patterns are public names only. Where a field is stored (a BSON path, a column) and which index backs a pattern is configured on the engine
(for example `WithListFieldPaths` and `WithListIndexNames` on the MongoDB engine).

With the default profile, `NewSchema` refuses (`ErrInvalidSchema`, at start-up) a default page size above the maximum, a field without name or kind,
duplicate fields, an enum on a kind other than string/int, and any pattern that does not parse, names an undeclared, `Array`, `Optional` or non-scalar
field (only string, int, float, decimal, bool, timestamp sort) or repeats another pattern or its reversal. A pattern over a `Hidden` field is also
`ErrUnreadableSortField` unless `WithUnreadableSortAllowed` is set. It needs no context (patterns are parsed by the orderBy parser, which also parses
request orders; a blank pattern is refused).

`MustSchema` takes the same arguments for a declaration fixed in code (a package-level `var`, as above): it returns the schema `NewSchema`
builds and panics on a refused one with an error that starts `listing.MustSchema: ` and wraps the `NewSchema` error
(`errors.Is(recovered, ErrInvalidSchema)`). Use `NewSchema` where the declaration comes from configuration.

## Filter semantics

Listing uses proto3 implicit presence. An absent or BSON null optional scalar is read as its kind's zero value (`""`, `0`, `false`,
empty bytes, decimal zero, or the zero timestamp). An optional enum must declare `EnumZero`, whose stored value must be that zero.
Absent, null, and empty repeated fields are all empty arrays. For fields without explicit `Presence`, every filter leaf is two-valued: scalar
comparisons use the resolved value;
array comparisons match when any element satisfies the predicate; `!=` is the complement of `==`; `has(x)` means non-zero (non-empty for
arrays); null comparisons are aliases for equality/inequality with zero (or empty for arrays); and `size()` returns the resolved length.

| Field category                         | Schema declaration | Absent storage value                              |
|----------------------------------------|--------------------|---------------------------------------------------|
| Required scalar                        | `Field{}`          | No absence; the stored value is used              |
| Optional scalar with implicit presence | `Optional: true`   | Kind's zero value; enum uses `EnumZero`           |
| Repeated field                         | `Array: true`      | Empty array (`[]`), including BSON null           |
| Optional scalar with explicit presence | `Presence: true`   | Null, with the default evaluator's null semantics |

| Leaf                                        | Scalar fields, including Optional           | Repeated fields                      |
|---------------------------------------------|---------------------------------------------|--------------------------------------|
| `x == v`, ordering, `in`, string predicates | Compare the resolved scalar                 | Any element matches; empty is false  |
| `x != v`                                    | Complement of equality                      | No element equals `v`; empty is true |
| `has(x)`                                    | `x != zero`                                 | Non-empty                            |
| `x == null` / `x != null`                   | `x == zero` / `x != zero`                   | Empty / non-empty                    |
| `size(x)`                                   | String length (zero for an absent optional) | Element count                        |

Portable validation rejects empty `in` lists, null in `in`, ordered comparisons with null, non-finite float literals, and regex patterns
outside the shared literal, anchor, and `.*` subset. Stored float fields must contain finite values only; NaN and infinities sort
differently across supported databases and cannot have portable filter semantics.

### Case-insensitive contains

`WithCaseInsensitiveContainsFields("name", "tags")` selects declared plain string fields and string arrays by exact public name. On these fields
`contains` lowercases both operands with the Unicode 15.1 simple mapping. It searches each array element independently. Filter access, equality and
ordering still follow their usual policies. `Schema.CaseInsensitiveContainsFields()` returns a detached sorted slice.

A nonempty selection binds the mapping revision, sorted field set and application contract ID into the page token. A token issued under another
selection requires restarting at page one. Schema construction now rejects any caller-supplied `ContractID` beginning with `go-aip:contains:`, even with no fields
selected. Callers using that prefix must choose a new ID and restart pagination.

## Sort patterns

- A pattern is a complete key list with directions, in public names: `createTime desc`, `kind, name`. Free field combinations are not
  accepted.
- A request may use a declared pattern or its **full** reversal (`kind desc, name desc` for `kind, name`); an index serves both directions. A
  prefix (`kind`), a partial reversal (`kind desc, name`) or any other combination is a field error on `orderBy` that lists the accepted
  orders. Whitespace and the case of `asc`/`desc` are free; `asc` equals no direction.
- The id is the **tie-breaker**: every engine orders equal keys by the item id in the direction of the pattern's last key. With time-ordered
  ids (UUIDv7) equal keys keep creation order.
- Sort fields must be scalar and present on every item (keyset paging needs a comparable value in every sort field).

## Unreadable sort fields

A page token carries the sort values of the last item of a page. Signed `pagination/pagetoken` tokens are not encrypted, so whoever holds one
can read those values. That is harmless while the client also reads the sort fields, and a leak when it does not. Two checks keep it so by default:

- `Field.Hidden` declares a field the client never reads: it is filterable, but `NewSchema` refuses a pattern over it with
  `ErrUnreadableSortField` (wrapped with `ErrInvalidSchema`).
- `Request.ReadMask` carries the public paths the response returns (an AIP-157 read mask, e.g. `projection` `Spec.Paths`); a path covers
  itself and the fields below it (`meta` covers `meta.createTime`), and empty or `*` returns everything. `Compile` refuses an order, requested
  or default, with a key the mask does not cover: an InvalidArgument field error on `orderBy` with `ErrUnreadableSortField` in the chain.

`WithUnreadableSortAllowed(true)` lifts both checks for a schema that sorts on such fields deliberately, typically with encrypted page tokens
(`pagetoken.WithEncryption`).

## Filters (CEL)

- Only declared fields are allowed (untrusted input, by default). Built-in functions exposed: `contains()`, `startsWith()`, `endsWith()`,
  `size()`, `timestamp()`, plus the schema's custom functions. String functions take both forms: `name.startsWith("x")` and
  `startsWith(name, "x")` (the filter parser turns the global form into the method form; a string function on anything but a field is a
  filter error naming both forms); **`matches()` and `substring()` are not exposed** (a custom function may expand into `matches()` with a
  quoted pattern, like `functions.TextSearch`). `substring()` stays out of the listing contract even when `WithBuiltinFunctions` allows it:
  the request validation (`filter.Validate`) takes a field or a literal as a comparison operand, so the filter is refused before an engine,
  the [`listing/evaluation`](evaluation/README.md) profile or a contract translator sees it.
- Literal kinds are checked against the declared kind: timestamps through `timestamp("2026-03-01T00:00:00Z")`, never strings.
- Timestamps compare at the profile precision (`TimestampPrecision` by default: the millisecond, as BSON stores a date): a literal and a default
  equality are cut to it in UTC, so `createTime == timestamp("…:00.123456Z")` matches a stored `.123` on every engine, `in` and range boundaries
  agree, and in-memory page positions use the same values as MongoDB.
- Enums use the proto enum names (`state == "STATE_ACTIVE"`, `state in [...]`), which the filter parser (`filterparser.WithEnumNames`) rewrites
  to the stored values; only `==`, `!=`, `in` apply; a stored value, a number or an unknown name is a field error naming the field, never the
  literal.
- Bools order `false` before `true`, as MongoDB orders them: `enabled > false` matches the enabled items on every engine.
- Translator limits: the field on the left and a literal on the right (`field op literal`); no field-to-field comparison; `size()` only
  inside a comparison (`size(note) > 3`); `has()` needs a selector (`has(a.b)`), use `field != null` for top-level presence.
- Absent and null are the same: `note == null` matches an item without `note`, `note != "x"` matches it too, an ordering comparison
  (`note > "a"`) never does and its negation (`!(note > "a")`) does. An `Optional` field is stored with `omitempty`, so its empty value is
  absent.
- `size()` of an absent or null value is 0 (an empty string or list is stored absent) under every operator and negation. The schema's size check
  (`WithSizeValidator`) applies MongoDB matching validation on every engine: operands must be integers in `0..2147483647`, and
  `size(f) in [...]` is a filter error.
- `Array` fields match per element: `tags == "x"` (or `"x" in tags`) when any element equals `"x"`, `tags != "x"` when none does,
  `tags in [...]`, `tags.startsWith("x")` and ordering comparisons when any element matches; `size(tags)` measures the list; an empty or
  absent list is null. A dotted public name (`steps.providerId`) reaches into a list of sub-documents.

A refused filter never echoes its literals (a filter may carry personal data, an INN): the reason is generic, except for an enum violation
and a `size()` operand out of range, which name only the field, the operator or the bounds.

## Writing an engine

| Symbol                                                                       | Purpose                                                                                                                |
|------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------|
| `Schema.Compile(ctx, req) (Query, error)`                                    | Validates a request; `Query{Size, Order, Filter}` — the validated AST (defaults and restriction joined)                |
| `Schema.CompileWithOrderValidator(ctx, req, validate)`                       | `Compile` with the engine's physical order check, run after the schema's order check and before the filter is parsed   |
| `Schema.FormatError(field, reason, cause)`                                   | The profile's request-error formatter (`WithErrorFormatter`), for the engine's own request errors                      |
| `Schema.FilterPolicy()`                                                      | The schema's filter policy in public names; an engine passes it to its translator or evaluator                         |
| `Schema.FilterOptions()`                                                     | The options of that policy; storage names go in the engine's translator (`filter.WithFieldMapping`)                    |
| `Schema.OrderOptions()`                                                      | The `orderby.PolicyOption`s of the schema: untrusted input, sortable fields, default order (public names)              |
| `Schema.OrderPolicy()`                                                       | The `*orderby.Policy` built from `OrderOptions` in `NewSchema`; bind it with an orderby translator                     |
| `Schema.ProjectionOptions()`                                                 | The `projection.PolicyOption`s the schema implies (Hidden fields denied); append the allow-list and views              |
| `Schema.TranslationError(err)`                                               | A translator's refusal as a field error on `filter` that does not echo literals                                        |
| `Schema.FieldInfo(name)`                                                     | A declared field as `FieldInfo` (enum-free, stored enum zero precomputed); zero-copy, for per-item reads               |
| `Schema.Orders()`, `Order.Keys()`                                            | Every accepted order (pattern, then reversal) and its keys by public name                                              |
| `Schema.Default()`, `Schema.UnrestrictedOrder()`                             | The order of a request without `orderBy`; whether request orders may be absent from `Orders` (`WithUnrestrictedOrder`) |
| `Schema.ZeroFields()`, `Schema.OptionalFields()`, `Schema.PresenceFields()`  | The `Optional` fields with their kinds, their names, and the `Presence` fields: the engine's storage configuration     |
| `Schema.MaxPageSize()`, `Schema.TimestampPrecision()`, `Schema.ContractID()` | The page ceiling, the filter timestamp precision and the contract ID an engine checks or binds into its tokens         |
| `filter.Canonical(q.Filter)`                                                 | The spelling-independent filter a page token binds to (restriction included)                                           |

The schema builds its policies once, in `NewSchema`, following the parser / policy / translator model of the
[root README](../README.md#parser--policy--translator): the filter policy from `FilterOptions` and the order policy from `OrderOptions`. An
engine binds them to its storage mapping (an orderby translator or `orderby.NewTranslatorContext(schema.OrderPolicy(), ...)` with its field
mapping and tie-breaker), so one policy serves every engine of the list. The sortable fields are the keys of the accepted orders or, under
`WithUnrestrictedOrder`, every field that passes the pattern key checks; a schema with no sortable field refuses every key. The default order
is stated in public names.

`ProjectionOptions` only denies the `Hidden` fields: a list's fields are its filter fields, not every field of the resource, so the schema
cannot state the projection allow-list. Append `projection.WithAllowedFields` (a field access policy needs one too) and any resource views
(`projection.WithView`, `projection.WithDefaultView`) before calling `projection.NewPolicy`. An omitted read mask returns every allowed field
except the Hidden ones (AIP-157); when a view selects the fields instead, pass the selected paths as `Request.ReadMask`.

An engine translates `Query.Filter` as a whole and binds its canonical form; it needs nothing else for `Request.Restriction`. An engine
must also apply `Query.Cutoff()` to the rows it reads or refuse it as a server fault. The `listingtest.Run` suite checks both predicates.

## Tests

`go test ./listing/... ./backend/memory/...` runs the schema, compile and in-memory tests and the in-memory conformance suite.
`go -C integration/docker test -race -tags integration_docker ./...` runs the conformance suite against live PostgreSQL, MariaDB, ClickHouse and
MongoDB servers started in Docker, in the test-only module [`integration/docker`](../integration/docker/README.md), with the MongoDB engine's
scope, time cap, corrupt-record and `explain` tests.

## Explicit presence

| Declaration      | Storage and filter behavior                        | Sortable |
|------------------|----------------------------------------------------|----------|
| `Field{}`        | Required scalar; always present                    | Yes      |
| `Optional: true` | Implicit proto3 scalar; missing reads as kind zero | No       |
| `Array: true`    | Repeated field; missing reads as empty list        | No       |
| `Presence: true` | Explicit optional or wrapper; nullable             | No       |

`Field.Presence` declares a nullable proto3 `optional` or wrapper field. It is incompatible with `Optional` and `Array`, and cannot be
sorted. `Schema.PresenceFields()` returns these names for engine configuration. A null value follows the default evaluator: it equals null and
differs from every value (`rating != 1` and `!(rating == 1)` select it), is in no list (`!(rating in [1, 2])` selects it) and contains no string
(`!score.startsWith("a")` selects it). An ordered comparison with null and a bare boolean test of a null `active` are unknown, also under negation,
and never select a row. `== null`, `!= null`, `has()`, and `size()` are two-valued, with `size(null) == 0`. The memory, SQL and MongoDB
engines agree on every expression.

## Extension interfaces

| API                           | Contract                                             |
|-------------------------------|------------------------------------------------------|
| `Engine[T, S]`                | Common engine port with an explicit source or scope  |
| `Lister[T]`, `Bind`           | Bind a source and subject to a transport-facing port |
| `FilterParser`, `OrderParser` | Concurrency-safe parser dependencies                 |

The default contract ID is `portable-v1`. Change it when changing parsing or evaluation semantics. Parser injection retains schema validation,
portable filtering rules, and declared sort patterns. Parser instances must return caller-owned ASTs and preserve stored enum values.
`Schema.Field` returns an independent enum snapshot with integer values normalized to `int64`; modifying input or returned maps cannot change a
schema.
`Schema.FieldInfo` returns the same declaration without the enum map and copies nothing; engines use it on hot paths.
Boolean options take an explicit `bool`, so later options can disable earlier settings.

## Replaceable policies

For example, a profile can disable the portable preset with `WithFilterOptions(filter.WithPortableContract(false))`, or change just one
rule with `filter.WithContractRules(map[filter.ContractRule]bool{filter.LiteralComparisons: false})`. The same option list supports
`WithUntrustedInput`, `WithSafeRegex`, `WithUnsafeRegex`, regex validation/compilation, custom value codecs, and AST limits. Filter policy
validation still rejects contradictory settings. Disabling a Schema AST check does not disable the filter package's structural validation.

The formatter receives `(field, defaultReason, originalCause)` and must return a non-nil error. It can expose parser diagnostics, rename
`orderBy` to `order_by`, or use an application error type. Preserve the cause when callers need `errors.Is`/`errors.AsType`. Engine token
and database-execution errors retain their engine-specific hooks. `Schema.TranslationError` applies the profile formatter to translation
failures.

Profiles describe accepted requests, not backend capabilities. Match timestamp precision and custom semantics to the engine/storage. Memory supports
unrestricted and empty (ascending ID-only) orders. SQL constructs request orders dynamically. The MongoDB engine precompiles index plans and rejects
unrestricted orders at construction (an allowed empty order sorts on its tie-breaker). `Schema.Orders` enumerates declarations and reversals, including
the two identical empty orders; it does not enumerate unrestricted requests. Unknown sort fields remain invalid metadata references.

For an empty field declaration, explicitly choose a filter policy, for example `filter.WithUntrustedInput(false)` with
`filter.WithAnyField(true)` for trusted service filters; a parser can reject client filters entirely. The default untrusted allowlist
requirement remains in force. Application-supplied converters and formatters cannot be nil; optional AST and sort validators can.

Schema-owned enum vocabulary and function settings take precedence over WithFilterParserOptions. Use profile options to configure them.
An allowed empty order with no patterns creates an identity-only order, including its scoped indexes.

`Schema.FormatError` applies the configured request-error formatter in both schema compilation and engines. Engines validate order before page tokens.
A custom page-size resolver must return a positive size with room for one lookahead row. Default equalities support decimal and byte values.

## Policy propagation

Field enum vocabularies accept immutable stored values checked by the configured filter kind and value codecs. Optional enums retain the portable
zero-storage contract: EnumZero must map to an empty string or integer zero, not an arbitrary sentinel such as UNSPECIFIED. WithFieldNameValidator
replaces public-name syntax validation; custom syntax also requires matching parsers. Order keys retain null placement and collation in canonical
bindings. Built-in engines reject these attributes until their cursor comparisons support them.

Default equalities are validated against the configured filter policy during schema construction, including custom decimal parsers and value
codecs. Engines use `CompileWithOrderValidator` to check physical sort restrictions before parsing a filter, preserving request field precedence.

Engine identity options use storage terminology: the MongoDB engine uses `WithListTiebreakPath`, memory uses `WithListTiebreakField`, and SQL uses
`WithListIDColumn` with `WithListIDKind`. Each selects the unique final pagination key; SQL public aliases are configured through `WithListColumns`.

## Options

| Option                      | Type            | Default                                         | Behavior                                                                                                                                                                                                                                                                                                |
|-----------------------------|-----------------|-------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `WithBuiltinFunctions`      | `ProfileOption` | contains, startsWith, endsWith, size, timestamp | BuiltinFunctions replaces the default CEL builtin allowlist; an empty slice disables builtins.                                                                                                                                                                                                          |
| `WithCallValidator`         | `ProfileOption` | field-target call validator                     | CallValidator replaces the string-function target check; nil disables it.                                                                                                                                                                                                                               |
| `WithContractID`            | `ProfileOption` | DefaultContractID                               | ContractID versions parser and semantic behavior in page-token bindings.                                                                                                                                                                                                                                |
| `WithDefaultEqualities`     | `ProfileOption` | equalityMap(nil)                                | DefaultEqualities restrict every request whose filter does not refer to a field to field == value (public name → literal; an enum field takes an enum name). It hides what a client must ask for explicitly, e.g. `{"deleted": false}`: soft-deleted items appear only when the filter tests `deleted`. |
| `WithDefaultLiteral`        | `ProfileOption` | portable literal conversion                     | DefaultLiteral replaces default equality conversion, including array and enum rules. Timestamp results are normalized using timestampPrecision after conversion.                                                                                                                                        |
| `WithDefaultOrderBy`        | `ProfileOption` | ""                                              | DefaultOrderBy is the order of a request without orderBy, in public names; it must be a declared pattern or its reversal. Empty uses the first declared pattern.                                                                                                                                        |
| `WithDefaultPageSize`       | `ProfileOption` | DefaultPageSize                                 | DefaultPageSize is the page size of a request with pageSize 0; it must not exceed maxPageSize.                                                                                                                                                                                                          |
| `WithEmptyFieldsAllowed`    | `ProfileOption` | false                                           | EmptyFieldsAllowed permits a schema without filter fields. Configure filterOptions for its policy.                                                                                                                                                                                                      |
| `WithEmptyOrderAllowed`     | `ProfileOption` | false                                           | EmptyOrderAllowed permits empty declarations and an ID-only default order.                                                                                                                                                                                                                              |
| `WithErrorFormatter`        | `ProfileOption` | field error formatter                           | ErrorFormatter formats and classifies request errors. It must return a non-nil error and be concurrency safe.                                                                                                                                                                                           |
| `WithFieldNameValidator`    | `ProfileOption` | public dotted-name validator                    | Checks public field names; nil disables syntax checks. Custom syntax also requires matching filter and order parsers.                                                                                                                                                                                   |
| `WithFilterOptions`         | `ProfileOption` | nil / unset                                     | FilterOptions override the metadata-derived filter policy, in order, for Schema and engines.                                                                                                                                                                                                            |
| `WithServerOnlyFields`      | `ProfileOption` | nil / unset                                     | Rejects the named declared fields in the client filter while allowing them in server predicates. |
| `WithFilterParser`          | `ProfileOption` | nil / unset                                     | FilterParser replaces parsing. It must preserve the declared enum representation.                                                                                                                                                                                                                       |
| `WithFilterParserOptions`   | `ProfileOption` | nil / unset                                     | FilterParserOptions configure the default parser before schema-owned vocabulary and function settings.                                                                                                                                                                                                  |
| `WithFunctions`             | `ProfileOption` | functionMap(nil)                                | Functions are custom CEL functions of the filter (e.g. a free-text search()), expanded by the parser into ordinary predicates over the declared fields; the allowlist applies to the expansion.                                                                                                         |
| `WithMaxFilterDepth`        | `ProfileOption` | DefaultMaxFilterDepth                           | MaxFilterDepth bounds the nesting of a filter expression.                                                                                                                                                                                                                                               |
| `WithMaxFilterOperations`   | `ProfileOption` | DefaultMaxFilterOperations                      | MaxFilterOperations bounds the AST nodes a filter may visit.                                                                                                                                                                                                                                            |
| `WithMaxPageSize`           | `ProfileOption` | DefaultMaxPageSize                              | MaxPageSize clamps a larger requested page size. An engine may serve a lower ceiling and then refuses the schema (the MongoDB engine has its own maximum, `mongo.MaxListPageSize`).                                                                                                                     |
| `WithOrderParser`           | `ProfileOption` | nil / unset                                     | OrderParser replaces order syntax parsing while retaining declared pattern validation.                                                                                                                                                                                                                  |
| `WithOrderParserOptions`    | `ProfileOption` | nil / unset                                     | OrderParserOptions configure the default order parser.                                                                                                                                                                                                                                                  |
| `WithPageSizeResolver`      | `ProfileOption` | nil / unset                                     | PageSizeResolver replaces page-size defaults, limits and validation when non-nil.                                                                                                                                                                                                                       |
| `WithParentRequired`        | `ProfileOption` | false                                           | ParentRequired rejects a `Request` without a parent: the list is a child list addressed by its parent id.                                                                                                                                                                                               |
| `WithPresenceValidator`     | `ProfileOption` | portable presence validator                     | PresenceValidator replaces required-field null/has checks; nil disables them.                                                                                                                                                                                                                           |
| `WithSizeValidator`         | `ProfileOption` | portable size validator                         | SizeValidator replaces the size comparison contract; nil disables it.                                                                                                                                                                                                                                   |
| `WithSortFieldValidator`    | `ProfileOption` | required sortable scalar validator              | SortFieldValidator replaces scalar/presence restrictions; nil disables them. Hidden-field checks remain separate.                                                                                                                                                                                       |
| `WithTimestampPrecision`    | `ProfileOption` | TimestampPrecision                              | Truncates filter literals and defaults; zero preserves nanoseconds.                                                                                                                                                                                                                                     |
| `WithUnreadableSortAllowed` | `ProfileOption` | false                                           | UnreadableSortAllowed accepts a sort pattern over a Hidden field and an order a request's ReadMask does not cover. A page token then discloses the sort values of the last item of a page to the client; set it only when that is intended or the tokens are encrypted (pagetoken.WithEncryption).      |
| `WithUnrestrictedOrder`     | `ProfileOption` | false                                           | UnrestrictedOrder accepts any parsed order whose fields pass sortFieldValidator.                                                                                                                                                                                                                        |

## Defaults and limits

| Constant                     | Value              |
|------------------------------|--------------------|
| `DefaultPageSize`            | `50`               |
| `DefaultMaxPageSize`         | `500`              |
| `DefaultMaxFilterDepth`      | `12`               |
| `DefaultMaxFilterOperations` | `128`              |
| `DefaultContractID`          | `"portable-v1"`    |
| `TimestampPrecision`         | `time.Millisecond` |

## Named values

| Constant         | Meaning              |
|------------------|----------------------|
| `pagetoken.SizeField`  | pagetoken.SizeField  |
| `pagetoken.TokenField` | pagetoken.TokenField |
| `FieldFilter`    | "filter"             |
| `FieldOrderBy`   | "orderBy"            |
| `FieldParent`    | "parent"             |

## API

| Symbol                                                                | Purpose                                                                                                                                                                                                                                                                                                                                                     |
|-----------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Engine`                                                              | Engine serves pages with an explicit engine-specific source or scope S. Memory uses []T; database engines use their typed equality scopes.                                                                                                                                                                                                                  |
| `Lister`                                                              | Lister serves pages after the source and subject have been bound.                                                                                                                                                                                                                                                                                           |
| `ErrorMapper`                                                         | ErrorMapper maps a query failure at the engine boundary. filtered reports whether a predicate was sent.                                                                                                                                                                                                                                                     |
| `Query`                                                               | Query is a validated request, ready for an engine.                                                                                                                                                                                                                                                                                                          |
| `Request`                                                             | Request is the common shape of a List request (AIP-132, AIP-158, AIP-160); `Restriction` carries the server predicate ANDed onto every filter.                                                                                                                                                                                                              |
| `Profile`                                                             | Profile is the parsing, filtering, sorting and request policy bound to Schema metadata. Construct it with NewProfile and reuse it across schemas.                                                                                                                                                                                                           |
| `NewProfile`                                                          | NewProfile captures policy options. NewSchema validates the profile against its metadata.                                                                                                                                                                                                                                                                   |
| `ProfileOption`                                                       | ProfileOption is a functional option for configuring profileOptions.                                                                                                                                                                                                                                                                                        |
| `ProtoRequest`                                                        | ProtoRequest is the getter set every generated List request message shares (AIP-132, AIP-158, AIP-160).                                                                                                                                                                                                                                                     |
| `RequestFromProto`                                                    | RequestFromProto converts a generated List request message to a `Request`: pageSize, pageToken, filter, orderBy and, when the message has a GetParent method (a child list), parent. The handler passes the gRPC full method next to it as the list subject.                                                                                                |
| `Field`                                                               | Field declares one filterable field of a list by its public name. Where an engine stores it (a BSON path, a column) is the engine's mapping.                                                                                                                                                                                                                |
| `FieldInfo`                                                           | FieldInfo is the declaration of a listing field without its enum map, returned by `Schema.FieldInfo`.                                                                                                                                                                                                                                                       |
| `OrderKey`                                                            | OrderKey is one key of a resolved `Order`.                                                                                                                                                                                                                                                                                                                  |
| `Order`                                                               | Order is an accepted order of a list: a declared pattern or its full reversal. Engines append their id tie-breaker in the direction of the last key, so every order is total.                                                                                                                                                                               |
| `Schema`                                                              | Schema is the validated query surface of one list: its filter fields, custom filter functions and accepted orders. It is immutable and safe for concurrent use; build it once (package level or in a constructor) and share it between the engines that serve the list.                                                                                     |
| `NewSchema`                                                           | NewSchema binds fields and patterns to a Profile; nil selects the portable default. Profile options may replace the validation rules described below.                                                                                                                                                                                                       |
| `MustSchema`                                                          | MustSchema is `NewSchema` for package-level schema declarations (var widgets = listing.MustSchema(fields, patterns, nil)): it returns the schema NewSchema builds and panics on an invalid declaration. The panic value is an error prefixed with "listing.MustSchema: " that wraps the NewSchema error, so errors.Is(recovered, `ErrInvalidSchema`) holds. |
| `ErrInvalidSchema`, `ErrUnreadableSortField`, `ErrInvalidRestriction` | Sentinels: a refused schema (start-up), an order over an unreadable field, a server restriction the schema refuses (wraps `ErrInvalidSchema`).                                                                                                                                                                                                              |
| `ErrWorkBudget`                                                       | A request that exhausted an engine's scan, materialization or work limit; engines wrap it in their own sentinels (RESOURCE_EXHAUSTED).                                                                                                                                                                                                                      |
| `Bind`                                                                | Binds an `Engine` to a subject and a source or scope, producing a `Lister`.                                                                                                                                                                                                                                                                                 |
| `FilterParser`, `OrderParser`                                         | Concurrency-safe parsers a profile may inject (`WithFilterParser`, `WithOrderParser`).                                                                                                                                                                                                                                                                      |

## Related packages

- [go-aip](../README.md)
- [fieldbehavior](../fieldbehavior/README.md)
- [fieldmask](../fieldmask/README.md)
- [fieldpolicy](../fieldpolicy/README.md)
- [filter](../filter/README.md)
- [orderby](../orderby/README.md)
- [projection](../projection/README.md)
- [listing guide](../docs/listing.md)
