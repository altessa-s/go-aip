# mongo

```go
import "github.com/altessa-s/go-aip/backend/mongo"
```

This package is a separate Go module, released together with go-aip, so the MongoDB driver stays out of the root module:

```sh
go get github.com/altessa-s/go-aip/backend/mongo
```

The MongoDB backend of go-aip, in one package:

| Part                                            | Main API                                                                                                                   |
|-------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------|
| [Storage schema](#storage-schema)               | `Schema`, `NewSchema`, `DefaultSchema`, `SchemaOption`, `CheckSizeOperand`, `MaxSizeOperand`                               |
| [Filter translator](#filter-translator)         | `FilterTranslator`, `NewFilterTranslator`, `NewFilterTranslatorWithSchema`, `FilterOption`                                 |
| [Evaluator profile](#evaluator-profile)         | `Schema.BindEvaluator`: `evaluator.WithProfile(schema)`                                                                    |
| [Order translator](#order-translator)           | `OrderTranslator`, `NewOrderTranslator`, `OrderOption`                                                                     |
| [Projection translator](#projection-translator) | `ProjectionTranslator`, `NewProjectionTranslator`, `ProjectionOption`, `DefaultIDField`                                    |
| [List engine](#list-engine)                     | `ListEngine`, `NewListEngine`, `MustNewListEngine`, `ListEngineOption`; test helpers in [`mongotest`](mongotest/README.md) |
| [Value encoding](#value-encoding)               | `ValueEncoding`, `BSONEncoding`, `ValueEncodingOption`                                                                     |
| [Codecs](#codecs)                               | `NewRegistry`, `RegisterDateType`, `DateOption`                                                                            |

Every option is prefixed with its kind: `WithSchema…`, `WithFilter…`, `WithOrder…`, `WithProjection…`, `WithList…`, `WithValueEncoding…`,
`WithDate…`. `WithFilterOptions`, `WithOrderOptions` and `WithProjectionOptions` carry the storage options of the core packages
(`filter.TranslatorOption`, `orderby.TranslatorOption`, `projection.TranslatorOption`). All translators, schemas, list engines and encodings are
safe for concurrent use after construction. In-memory filtering with MongoDB semantics is the [evaluator profile](#evaluator-profile) of this
package: pass a `Schema` to `evaluator.WithProfile`. The MongoDB matching rules live here, beside the translator they mirror, and run through the
backend-neutral contracts of [filter/evaluator](../../filter/evaluator/README.md#composable-execution) (`Binder`, `Reader`, `Guard`); every
evaluator case of the profile, the evaluator conformance suite and the translator agreement tests run in this module.

## Storage schema

`Schema` holds driver-independent MongoDB storage semantics shared by the filter translator and the evaluator's MongoDB profile. The immutable schema
keeps MongoDB's missing-value rules out of the shared filter policy. BSON value conversion belongs to the [value encoding](#value-encoding) of the
evaluator.

`NewSchema(opts...)` captures omission (`WithSchemaOmitEmpty`), zero-when-absent (`WithSchemaZeroWhenAbsent`), explicit presence
(`WithSchemaPresenceFields`) and resolved-size (`WithSchemaSizeZeroWhenAbsent`) rules; `DefaultSchema()` has none. A field cannot be both a
presence field and zero-when-absent or omitted, and an empty field name is rejected (`filter.ErrInvalidOptions`). Pass the same schema to
`NewFilterTranslatorWithSchema` and `evaluator.WithProfile` so validation and execution agree; both bind it to the policy and reject a field
declared both an array (`filter.WithArrayFields`) and zero-when-absent or presence, or with conflicting kinds (`filter.ErrInvalidOptions`).

`BindEvaluator` binds the schema to an evaluator's policy and value encoding ([Evaluator profile](#evaluator-profile)). Presence comparisons use
nullable logic; implicit zero fields and arrays retain their configured behavior. `WithSchemaSizeZeroWhenAbsent` makes a missing or null `size()`
resolve to zero. `CheckSizeOperand` validates a `size()` operand: a number in 0..`MaxSizeOperand` (`math.MaxInt32`, the range MongoDB accepts for
`$size`); anything else is a `*coreerrs.FieldError` naming the field that matches `filter.ErrInvalidExpression`. Fractional operands are accepted:
integer sizes never equal them. `Schema.ValidateDecimal` checks that a decimal fits BSON Decimal128 (`filter.ErrFieldTypeMismatch` otherwise).

```go
schema, err := mongo.NewSchema(
    mongo.WithSchemaZeroWhenAbsent(map[string]filter.FieldKind{"retries": filter.FieldKindInt}),
    mongo.WithSchemaOmitEmpty("description"),
)
if err != nil { return err }

policy, err := filter.NewPolicy(filter.WithAllowedFields("retries", "description")) // one policy for both
if err != nil { return err }

translator, err := mongo.NewFilterTranslatorWithSchema(policy, schema)
if err != nil { return err }
eval, err := evaluator.New(policy, evaluator.WithProfile(schema))
if err != nil { return err }

err = mongo.CheckSizeOperand("tags", int64(3))
```

## Filter translator

`FilterTranslator` translates filter AST nodes into MongoDB `bson.M` query documents. It supports all comparison, logical, membership and string
operations, field-to-field operands and `substring()`.

### Contract

`NewFilterTranslator` takes the shared `*filter.Policy` (field authorization, types, limits, decimal conversion, value codecs and regex rules, in API
names, built once per resource) and `FilterOption` values; `WithFilterOptions` binds the policy to storage names (`filter.WithFieldMapping`).
A nil policy fails with `filter.ErrPolicyRequired`; `filter.NewPolicy` requires an allowlist or an explicit trusted `WithAnyField` opt-out.

`ValidateAST` uses the configured renderer to check operand shapes and values without contacting a server. Unsupported operations remain explicit
errors. Translators support concurrent calls with per-call traversal state; injected callbacks must also support concurrent use.

`WithFilterLiteralEncoder` converts values before backend encoding; it does not return executable query fragments. Decimal operands are normalized
through the shared policy, including membership. Backend-specific storage semantics can require schema configuration instead of a scalar encoder.

`WithFilterPredicateRenderer` supplies an application renderer after policy validation; unhandled nodes use built-in rendering. The callback remains
responsible for safe backend encoding.

`NewFilterTranslatorWithSchema` accepts the shared policy, a `Schema` and `FilterOption` values. Supply the same storage schema to evaluation for
omission, zero, array, and nullable-presence parity. Output is a BSON filter; projections and sorting are separate packages. It returns the same
`*FilterTranslator` as `NewFilterTranslator`, which is `NewFilterTranslatorWithSchema` with `DefaultSchema()`: unlike the SQL backends, whose
`SchemaFilterTranslator` is a second renderer typed by its column schema, MongoDB has one renderer, and a `Schema` only adds storage rules to it.

### Behavior

`TranslateD` returns the same filter as an ordered `bson.D` with sorted keys, recursively, so translating one AST twice yields identical BSON bytes —
what a fingerprint needs, such as the page-token binding of a keyset pager. Sorting never changes what the filter matches: the translator
emits no embedded-document literal, only query and operator documents, whose key order MongoDB ignores.

```go
query, err := trans.TranslateD(ast)
cur, err := coll.Find(ctx, query)
```

A bare identifier used as a condition becomes a boolean field test — `active` translates to `{active: true}`, `!active` to `{active: {$ne: true}}` —
at the root of an expression and on either side of `$and` / `$or` alike.

Negation keeps MongoDB's own semantics, which differ from SQL's three-valued logic that the default evaluator and the SQL translators apply: a
`$nor` (or `$ne`) matches a document whose field is missing or null, so `!(age > 1)`, `!active`, `!(size(note) == 0)` and `!name.contains(note)`
select a document without `age`, `active` or `note`, where the default evaluator and SQL find the predicate unknown and select nothing. `&&` and
`||` agree with SQL, since a MongoDB predicate on a missing field is simply false. The MongoDB profile of the evaluator
(`evaluator.WithProfile(schema)`) follows the translator; this is a documented backend limit, not a gap.

### size()

`size()` measures an array by its element count and a string by its code points (`$strLenCP`, as CEL defines it):

| Expression       | Array (or absent/null)                                    | String                     |
|------------------|-----------------------------------------------------------|----------------------------|
| `f.size() == n`  | `{f: {$size: n}}` — an absent or null field never matches | code-point length `== n`   |
| `f.size() != n`  | `{f: {$not: {$size: n}}}` — absent and null match         | code-point length `!= n`   |
| `f.size() > n` … | `$size` of `$ifNull(f, [])` — absent and null count as 0  | code-point length compared |

The string test is guarded by `$type`, so `$strLenCP` never sees another type; a value that is neither an array nor a string has no size and matches
no ordering comparison. The operand must be a number in 0..`MaxSizeOperand` (2147483647), the range MongoDB accepts for `$size`; anything else is a
`*coreerrs.FieldError` naming the field that matches `filter.ErrInvalidExpression` (`CheckSizeOperand`). A fractional operand never reaches `$size`,
which MongoDB rejects: `==` becomes `{$expr: false}` and `!=` `{$expr: true}`, as no size equals it. `size()` names a field only on the left of a
comparison with a literal, or on either side of a comparison with another field (see [Field operands](#field-operands-and-substring)); as the target
of `in`, a string function or another `size()` it fails with `filter.ErrUnsupportedOperation`.

### Array fields

A bare identifier is a boolean field test: `active` renders `{active: true}`, and `!active` renders `{active: {$ne: true}}`.

Fields declared with `filter.WithArrayFields` hold lists. MongoDB already matches a list element-wise — `{tags: "red"}` holds when any element equals
`"red"`, `{tags: {$ne: "red"}}` when none does — so comparisons, `in` lists and the string predicates translate as for a scalar. Two forms change:

| Expression         | Translation                                                                                          |
|--------------------|------------------------------------------------------------------------------------------------------|
| `"red" in tags`    | `{tags: "red"}`; the literal is checked against the element kind declared by `WithFieldTypes`        |
| `tags.size() op n` | `{$expr: {op: [<size of $ifNull("$tags", [])>, n]}}` for every operator — absent and null count as 0 |

The size expression measures the value the aggregation path resolves to, so a dotted path through an array of documents (`steps.providerId`) counts
the values found in its documents, the same list the in-memory evaluator measures. A field cannot also be declared with both array semantics and
`WithSchemaZeroWhenAbsent` (`filter.ErrInvalidOptions`).

### Fields omitted when zero

A document encoded with `omitempty` lacks a field whose value is zero, and a plain MongoDB query tells the two apart: `{description: ""}` misses a
document without `description`, while `{description: {$ne: ""}}` selects it. Declare such fields with `WithSchemaZeroWhenAbsent` — a kind per
CEL field name — and every predicate over them matches a document lacking the field exactly when it matches one storing the zero value:

| Expression          | Translation                                                      |
|---------------------|------------------------------------------------------------------|
| `description == ""` | `{$or: [{description: ""}, {description: null}]}`                |
| `description != ""` | `{$and: [{description: {$ne: ""}}, {description: {$ne: null}}]}` |
| `retries < 3`       | `{$or: [{retries: {$lt: 3}}, {retries: null}]}`                  |

For these declared fields, both missing and BSON null read as the kind's zero value. Thus `description == null` is equivalent to `description == ""`,
and `has(description)` means the value is non-zero. Comparisons match only within a type class; timestamps compare at the millisecond precision of a
BSON date. The rule covers comparisons, `in`, `size()`, string predicates and `matches()`. Each predicate is exact on its own, so negation and `&&` /
`||` nesting compose. Every `size()` comparison must compare with a numeric literal — anything else is rejected with `filter.ErrInvalidExpression`.
String comparisons are judged byte-wise, as under MongoDB's default simple collation; a query or collection with a locale collation (which may, for
example, ignore whitespace or case) can match a stored `""` where the translator's decision for an absent field differs, so use the option with the
simple collation. Paths traversing arrays of documents are outside the listing storage contract; their matching behavior is not guaranteed by this
presence translation.

### Decimal fields

A field declared `filter.FieldKindDecimal` (money stored as `Decimal128`) receives its literals as BSON `Decimal128` values read by
`filter.DecimalOperand` from their source spelling, so the server compares them exactly: `price >= 0.1` becomes
`{price: {$gte: NumberDecimal("0.1")}}`, not a comparison with the binary double nearest 0.1, and `1.00000000000000001` stays exact. Values are
normalized (`"12.30"` is sent as 12.3, equal for MongoDB). Lists of `in` are converted element by element, `null` is left alone, and a literal that
is not a Decimal128-sized decimal (more than 34 significant digits, an exponent out of range, a non-numeric string) is `filter.ErrFieldTypeMismatch`.
Declared with `WithSchemaZeroWhenAbsent`, an absent decimal stands for 0.

### Ordered comparisons with null and bytes

An ordered comparison with `null` (`age > null`, either side) is `filter.ErrUnsupportedOperation`, as on every translator: CEL orders
nothing against null, while MongoDB's `{age: {$gt: null}}` matches no document and `{$gte: null}` matches the absent and null ones.
Ordered comparisons of bytes — a `b"…"` literal, a field declared `filter.FieldKindBytes`, or two such fields under `$expr` — are
`filter.ErrUnsupportedOperation` too: MongoDB orders BinData by length before its bytes, so `data < b"b"` would miss `"ab"`, which CEL,
the in-memory evaluator and the SQL translators order lexicographically. Equality and `in` on bytes are exact and stay translated.

### Anchors

MongoDB evaluates `$regex` with PCRE, where `$` also matches just before a final newline. `endsWith(s)` therefore anchors with `\z`, the absolute end,
so `"abc\n"` does not end with `"abc"`; `startsWith(s)` keeps `^`, which without the `m` option matches only at the start. A `matches()` pattern is RE2,
as CEL defines it: every `$` RE2 reads as end of text is rewritten to `\z` before the pattern is sent (`^A.*e$` becomes `^A.*e\z`), while a `$` under
`(?m)`, an escaped `\$` and a `$` in a character class are left alone. The rewrite verifies itself by parsing both patterns, so its result is memoized
per pattern in a bounded, process-wide cache (1024 patterns, emptied when full); only successful rewrites are cached.

A BSON regex is a C string, so a raw NUL byte in a `contains()` / `startsWith()` / `endsWith()` argument or a `matches()` pattern is sent as `\x00`,
which matches the same byte; inside `\Q…\E` the quote is closed around it (`\E\x00\Q`), so the pattern keeps its meaning.

### Security

`matches()` passes the user-supplied pattern through to MongoDB's `$regex`. Host-side validation compiles it with Go's RE2 engine (which cannot
backtrack) and enforces a length cap, but MongoDB executes the pattern with its own PCRE-family engine, where a crafted pattern can backtrack
catastrophically — a DB-side ReDoS the length cap bounds but does not eliminate. Expose `matches()` only to trusted callers, or tighten the cap via
`filter.WithMaxRegexLength`. `contains()`, `startsWith()`, and `endsWith()` escape their argument with `regexp.QuoteMeta` and are not affected.

### Evaluator parity

The [value encoding](#value-encoding) reads data values the way the driver stores them; with the profile it gives an in-memory evaluator the
results of the translator:

```go
schema, _ := mongo.NewSchema()
policy, _ := filter.NewPolicy(filter.WithAllowedFields("name")) // one policy for both
eval, _ := evaluator.New(policy,
	evaluator.WithProfile(schema),
	evaluator.WithValueEncoding(mongo.ValueEncoding()),
)
trans, _ := mongo.NewFilterTranslatorWithSchema(policy, schema)
```

Build one `Schema` and pass it to both constructors when rules such as `WithSchemaZeroWhenAbsent` or `WithSchemaOmitEmpty` affect the query.

### Concurrency

A `FilterTranslator` isolates per-call state and is safe for concurrent translation.

### Field operands and substring()

A query document compares a field only with a constant, so the forms below translate to an aggregation expression under `$expr`. Field mapping and
the allowlist apply to every field reference, and every literal inside the expression is wrapped in `$literal`, so a string such as `"$secret"` is
never read as a field path. `$expr` predicates cannot use an index the way a query predicate does; prefer literal comparisons on hot paths.

| Expression                  | Translation                                                                                                  |
|-----------------------------|--------------------------------------------------------------------------------------------------------------|
| `a == b`, `a != b`          | `{$expr: {$eq: [A, B]}}`, `!=` as `$not` of it                                                               |
| `a < b`, `<=`, `>`, `>=`    | `$lt` … guarded: both non-null and of one type class (`$isNumber` on both, or equal `$type`)                 |
| `a in [b, "x", 1]`          | `{$or: [{a: {$in: ["x", 1]}}, <a == b>]}` — literal members keep the query form                              |
| `a in tags` (array field)   | `a == tags`, see below                                                                                       |
| `tags.size() == n`          | the `size()` expression of the Array fields section compared with `n`                                        |
| `a.contains(b)`             | `$indexOfCP(A, B) != -1`, under a `$cond` requiring both values to be strings                                |
| `a.startsWith(b)`           | `$indexOfCP(A, B) == 0`, same guard                                                                          |
| `a.endsWith(b)`             | the last `$strLenCP(B)` code points of A equal B, same guard                                                 |
| `a.substring(s, e) == "ab"` | `$substrCP(A, s, e-s)` compared with `{$literal: "ab"}`; also with a field, in `in` lists and on either side |

Semantics:

- A field operand is the value its aggregation path resolves to, as a whole: missing and BSON null are the same null (`$ifNull`), and a field
  declared with `WithSchemaZeroWhenAbsent` reads its zero value. Values do not match element-wise, except a field declared with
  `filter.WithArrayFields` compared with a non-array operand by `==` / `!=`, which also matches when the list holds the operand (as `tags == "x"`).
  Ordering comparisons, string functions and `substring()` of an array field are `filter.ErrUnsupportedOperation`.
- `==` holds for equal values of one type class (numbers by value across int, long, double and decimal); two nulls are equal, as `a == null`
  matches a null field. `!=` is the negation of `==`.
- A null `WithSchemaPresenceFields` field makes every comparison false, `!=` included; a negated string function over one requires both
  values to be strings.
- `substring(start[, end])` takes integer literals with `0 <= start <= end <= 2147483647` (otherwise `filter.ErrInvalidExpression`; a non-literal
  bound is `filter.ErrUnsupportedOperation`) and counts code points, as CEL does. It has a value only when the field holds a string at least `end`
  (or `start`) code points long; otherwise every comparison with it is false, `!=` included, where the in-memory evaluator reports an error.
  `substring()` of a field declared with a non-string kind, or compared with a literal of another kind, is `filter.ErrFieldTypeMismatch`;
  comparing it with `null` is `filter.ErrUnsupportedOperation`. It is a comparison operand only: as a predicate, a string-function target or a
  string-function argument it is `filter.ErrUnsupportedOperation`.
- Two fields whose declared kinds (`filter.WithFieldTypes`) can never compare equal, such as an int and a string, are `filter.ErrFieldTypeMismatch`.
- `matches()` requires a literal pattern (`filter.ErrUnsupportedOperation` otherwise), so regex validation and length limits apply before execution.
- A literal on the field side of a comparison (`5 == tags.size()`, `"x" == name`) remains `filter.ErrInvalidExpression`.

The [evaluator profile](#evaluator-profile) evaluates these forms with the same semantics.

### Explicit presence

Configure `WithSchemaPresenceFields` with nullable fields. The translator applies negation normal form for that profile and uses strict
non-null membership guards for `!=` predicates. Positive MongoDB comparisons already reject missing and null values.

The [list engine](#list-engine) does not use `WithSchemaPresenceFields`: under it a null value fails every comparison, `!=`, a negated `in` and
a negated string function included, while the listing contract makes null differ from every value, so those hold for it. The engine instead
puts the filter in negation normal form over the fields of `Schema.PresenceFields()` (complementing a negated comparison, so an ordering
comparison stays unknown on null) and renders the rest with MongoDB's own null rules, turning `has()` of a presence field into `field != null`
([Storage semantics](#storage-semantics)). The limits and the allow-list apply to the filter as the client wrote it, before the rewrite, so the
nodes it adds do not count against `listing.WithMaxFilterOperations`.

### Configuration and compatibility

| API           | Purpose                                                       |
|---------------|---------------------------------------------------------------|
| `ValidateAST` | Check the configured renderer without contacting the database |

Constructors accept `FilterOption` values. Schema constructors take the schema followed by the same options. Shared policies and
callbacks must be safe for concurrent use. The visit methods are unexported: `Translate` walks the filter on its own per-call state. `ValidateAST`
checks actual expression shapes and values, including field-to-field operands, rather than promising support for an operator regardless of its
operands. Unsupported expressions return filter sentinel errors. Backend-native differences remain explicit.

Literal encoders return values, never executable query text. They can choose timestamp units or stored enum representations. Schema-aware SQL
renderers use their declared storage semantics instead; configure those through the schema and rendering options.

Field authorization runs before any shape check: a denied field anywhere in the expression, a function argument included
(`name.contains(secret)`), fails with `filter.ErrFieldNotAllowed`.

`WithFilterPredicateRenderer` installs a trusted, concurrent-safe callback for application predicates. Field authorization and limits run first.
Return `handled=false` to delegate to built-ins; encode every client value safely before returning query text.

Backend regex rewrites apply only to the default RE2 compiler (`Policy.UsesRE2Regex`). A custom compiler supplies backend-compatible
syntax and matching semantics; its patterns pass through without RE2 anchor or mode rewrites.

### Configuration example

```go
policy, err := filter.NewPolicy(
    filter.WithAllowedFields("name", "age", "email", "userName"),
    filter.WithMaxDepth(10),
) // build once per resource
if err != nil { return err }

trans, err := mongo.NewFilterTranslator(policy, mongo.WithFilterOptions(filter.WithFieldMapping(map[string]string{
    "userName": "user_name",
})))
```

## Evaluator profile

`evaluator.New(policy, evaluator.WithProfile(schema))` matches a `map[string]any` document exactly as MongoDB matches the query the filter
translator builds from the same AST, policy and storage schema — so an in-memory backend and a MongoDB backend return the same rows. Build the
schema with `NewSchema` and pass it to both constructors.

A `Schema` is an [`evaluator.Binder`](../../filter/evaluator/README.md#composable-execution): `evaluator.New` calls `BindEvaluator(policy, enc)` once,
which binds the schema to the evaluator's policy and to the value encoding of `evaluator.WithValueEncoding`, and compiles every filter with the
compiler it returns. The compiler validates a filter with the rules of the translator and compiles it to a program that reads each record through
an `evaluator.Reader` and counts its operations with an `evaluator.Guard`, so the data budget, the stored forms, the paths through lists of
documents and the limit errors are those of the default evaluator; only the matching rules below are MongoDB's. The compiler also reads fields for
`Evaluator.Lookup`. A `ValueDecoder` (`evaluator.WithValueDecoder`) adapts SQL values and is not used.

| Case                              | Default evaluator                               | MongoDB profile                                                                                                                                                             |
|-----------------------------------|-------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `name > 1` (another type)         | fails (`ErrUnsupportedType`)                    | false; `!=` holds                                                                                                                                                           |
| `active > false`                  | fails (`ErrUnsupportedOperation`)               | false orders before true                                                                                                                                                    |
| a list value                      | compared as a whole, unless an array field      | any element matches; `!=` holds when no element is equal                                                                                                                    |
| `steps.providerId` through a list | absent, unless an array field (then as MongoDB) | the values found in its documents; a document lacking it is null, an empty list has none                                                                                    |
| `info.name` through a `bson.M`    | absent, unless an array field (then as stored)  | the value in the document the driver stores (also a `bson.D`, a struct, a typed map)                                                                                        |
| a nil slice, map or pointer       | not null; a nil `[]any`, map, `[]byte` is empty | null, as the driver stores it; an empty slice or map is an empty list or document                                                                                           |
| timestamps                        | nanosecond precision                            | millisecond precision, as a BSON date stores them                                                                                                                           |
| Mongo schema zero-value fields    | not configured                                  | a field `$exists` misses (none in a list) reads as the zero value; `has()` holds                                                                                            |
| `size()` of an absent field       | unknown, unless an array field (0)              | the translator's rules: no size under `==`/`!=`, size 0 when ordered; 0 for an array field                                                                                  |
| Mongo schema omitted fields       | not configured                                  | an empty value is absent, by the driver's omitempty rules: `IsZero()` when defined (zero decimal, pointer to zero time), else length 0, never a struct, else the zero value |
| a bare field `active`             | its value must be a bool                        | `active == true`, as `{active: true}`                                                                                                                                       |
| an integer against a float        | exact: `2^53+1` is above `2^53.0`               | exact, as MongoDB: an integer never rounds to a double; NaN equals only NaN                                                                                                 |
| a float against a decimal         | the float's shortest decimal: `0.1` == `0.1`    | the double's binary value, as MongoDB: the double `0.1` is above the decimal `0.1`                                                                                          |

`Evaluate` first validates the whole expression without the data (`Evaluator.Validate`), with the translator's rules, so an expression either fails
whatever the document — also in a branch a short circuit would skip — or yields a bool for every document: a field outside the allow-list, a literal of
the wrong kind, an invalid regex, a `size()` operand outside 0..`MaxSizeOperand` (a `*coreerrs.FieldError` naming the field,
[`CheckSizeOperand`](#storage-schema)), exceeded limits, and the shapes the translator refuses — a literal compared with a field must be on the right
(`2 == tags.size()`), `x in f` needs an array field `f`, `size()` names a field only as a comparison operand, an ordered comparison with null or of
bytes (MongoDB orders BinData by length first) fails with `ErrUnsupportedOperation`, and the expression forms below accept only what the translator
renders. `Evaluator.Prepare` copies the AST, validates the copy once and returns a `Prepared` filter whose `Evaluate(data)` skips the validation; later
changes to the caller's nodes do not reach it. `size()` of an undeclared path through a list measures what the translator's query does: `==` and `!=`
test each array at the end of the path (`$size`), ordering comparisons the list the aggregation path resolves to. `WithSchemaSizeZeroWhenAbsent(true)`
makes `size()` measure every field (but a `WithSchemaZeroWhenAbsent` one) as it measures an array field: the resolved value under every operator, an
absent or null value counting as 0, in the translator and the evaluator. `Evaluator.Lookup(data, field)` returns a field's value as a predicate reads it
(under the profile the stored form, typed maps and pointers followed, Mongo schema omission and zero rules applied; in the default mode an array field
through lists of documents, any other through maps; within the data budget), for an in-memory backend that sorts by the same value it filters on.

### Expression forms

The predicates the filter translator renders as aggregation expressions under `$expr` evaluate with the same semantics (verified against
MongoDB 8): a field compared with a field or a `size()` (`a == b`, `a.size() < b`), an `in` list holding a field (`a in [b, "x"]`: a query
`$in` of the literals or one expression equality per other element), `x in tags` with a scalar field or a `substring()` `x`, a string
function with a field argument (`a.contains(b)`) and `substring()` as a comparison operand.

| Rule                      | Semantics                                                                                                                                                                      |
|---------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| field operand             | the value the aggregation path resolves to (through a list of documents, the list of the values found); missing and null are one null; a zero-when-absent field reads its zero |
| `==`, `!=`                | whole values (an array equals only an equal array; numbers across types; documents by their fields); two nulls are equal; `!=` is the negation of `==`                         |
| array field               | `tags == a` (and `a in tags`) also holds when the array holds `a`                                                                                                              |
| `<`, `<=`, `>`, `>=`      | only between non-null operands of one type class (numbers, otherwise one BSON type); arrays element by element, documents by field                                             |
| `size()` operand          | a missing or null value is an empty array; a value without a size has none                                                                                                     |
| `substring(start[, end])` | code points of a string field; none when the field is no string or the range passes its end                                                                                    |
| no value                  | a `size()` or `substring()` without a value, or a null presence field, makes every comparison false, `!=` included                                                             |
| string function           | holds only when the target and the argument are both strings; negated over a presence field, both must be strings                                                              |

A Go map keeps no field order, so two documents compare by their fields in key order and documents with the same fields and values are equal
(MongoDB compares stored field order). Validation mirrors the translator: an operand is a literal, a field, a `size()` or a `substring()` of a
field with integer literal bounds (`0 <= start <= end`); no null operand, no array field in an ordering, no array field or computed argument
in a string function, compatible declared kinds (`ErrFieldTypeMismatch`), and a negated `in` of a presence field over an array field is
rejected (`ErrInvalidExpression`). Fields are authorized before the shape of an operand is judged, so `name.contains(secret)` fails with
`ErrFieldNotAllowed`.

```go
schema, _ := mongo.NewSchema(mongo.WithSchemaOmitEmpty("name"))
policy, _ := filter.NewPolicy(filter.WithAllowedFields("name", "tags"), filter.WithArrayFields("tags")) // one policy for both
trans, _ := mongo.NewFilterTranslatorWithSchema(policy, schema) // MongoDB backend
eval, _ := evaluator.New(policy, evaluator.WithProfile(schema),
    evaluator.WithValueEncoding(mongo.ValueEncoding())) // in-memory, same results
```

The data values are read through the [value encoding](#value-encoding); see the [filter guide](../../docs/filter.md).

## Order translator

`OrderTranslator` translates an `orderby.Spec` value into a MongoDB `bson.D` sort document. Sort key precedence is preserved because `bson.D` is
an ordered document — a `bson.M` has no key order, and the Go driver rejects a multi-key map as a sort argument (`ErrMapForOrderedArgument`).

### Contract

`NewOrderTranslator(policy, opts...)` binds the resource's shared `orderby.Policy` (allow-list, field access, denied API fields, default order) to
the storage options passed through `WithOrderOptions` (mapping, KeyResolver, denied storage fields, tie-breakers); a nil policy returns
`orderby.ErrPolicyRequired`. `WithOrderCollector` records the translation duration as the histogram `orderby_mongo_translate_duration_seconds`.
Translation validates every order with the policy before mapping it, so it never renders a key the policy refuses. The three option layers are
described in the [root README](../../README.md#parser--policy--translator). SQL translators emit an ORDER BY body, MongoDB emits bson.D, Meilisearch
emits sort arguments, and RediSearch supports a single sort key.

MongoDB orders null and missing values before every other value (first ascending, last descending) and has no per-key placement: an explicit
`nulls first` on an ascending key or `nulls last` on a descending key is accepted (it is the native order and renders as the plain key), the
opposite placement and every collation return `orderby.ErrUnsupportedOrdering`. MongoDB collation is a query-level option, not a sort-key
attribute, so it is not expressed here. Translator instances support concurrent use. Configure parser behavior separately with
orderby.ParserOption; use the public backend/sqlbase order translator when an application needs custom trusted SQL column expressions.

### Usage

```go
parser, _ := orderby.NewParser()
ob, _ := parser.Parse(ctx, "create_time desc, slug")

policy, err := orderby.NewPolicy(orderby.WithAllowedFields("create_time", "slug"))
if err != nil {
    return err
}

trans, err := mongo.NewOrderTranslator(policy)
if err != nil {
    return err
}
sort, _ := trans.Translate(ob)
// sort == bson.D{{"create_time", -1}, {"slug", 1}}

col.Find(ctx, filter, options.Find().SetSort(sort))
```

Ascending keys emit `int32(1)`, descending keys emit `int32(-1)`. An empty `Spec` translates to a `nil` `bson.D` when the policy has no default
order and the translator no tie-breakers — matching the Meilisearch translator's "nil for empty" shape so call sites use the same `if sort != nil`
check across backends. Passing `nil` to `SetSort` is valid and equivalent to omitting the sort entirely.

### Configuration example

```go
policy, err := orderby.NewPolicy(
    orderby.WithAllowedFields("createdAt", "slug"),
    orderby.WithUntrustedInput(true),
)
if err != nil {
    return err
}

trans, err := mongo.NewOrderTranslator(policy,
    mongo.WithOrderOptions(
        orderby.WithFieldMapping(map[string]string{
            "createdAt": "created_at",
        }),
        orderby.WithTiebreaker(orderby.Key{Name: "_id", Direction: orderby.DirectionAscending}),
    ),
    mongo.WithOrderCollector(meter),
)
```

### Errors

`Translate` returns `orderby.ErrFieldNotAllowed` when an entry references a field the policy refuses (allow-list, field access, denied fields) or that
resolves to a denied storage path, `orderby.ErrDuplicateKey` when two keys resolve to the same storage name, and `orderby.ErrUnsupportedOrdering` for a
null placement opposite to the native one or a collation. `NewOrderTranslator` returns `orderby.ErrPolicyRequired` for a nil policy and
`orderby.ErrFieldNotAllowed` for a tie-breaker on a denied storage path. `orderby.ErrAllowlistRequired` (`orderby.WithUntrustedInput` without
`orderby.WithAllowedFields` or `orderby.WithFieldAccess`) is raised by `orderby.NewPolicy`.

## Projection translator

`ProjectionTranslator` translates a [`projection.Spec`](../../projection/README.md) into a MongoDB projection document (`bson.M`) for an
aggregation `$project` stage or a find projection. The document is a pure field selection: no `$`, `$slice`, `$elemMatch` or expression is
ever emitted.

### Contract

`NewProjectionTranslator(policy, opts...)` binds the resource's shared `projection.Policy` (allow-list, field access, denied API fields, views) to
storage options; a nil policy returns `projection.ErrPolicyRequired`. Translation validates every request with the policy before encoding storage
fields. Authorization covers mapped aliases, ancestor/descendant denies, required storage fields, and view selections. Configure parser grammar
separately from the policy; translator instances support concurrent use. The three option layers are described in the
[root README](../../README.md#parser--policy--translator).

`NewProjectionTranslator` accepts `ProjectionOption` values after the policy; wrap `projection.TranslatorOption` storage options (mapping, denied
storage fields, required storage fields) with `WithProjectionOptions`. `WithProjectionIDField` chooses the identity field (default `_id`), and
`WithProjectionIncludeID` controls its inclusion. Required fields and deny checks still apply. Output is an inclusion or exclusion BSON projection.
`Translate(spec)` takes a read mask alone; `TranslateView(spec, view)` applies the AIP-157 rule of `projection.Policy.Select`: a present mask
decides, an omitted one defers to the view.

### Output

| Request / policy                                       | Output                                         |
|--------------------------------------------------------|------------------------------------------------|
| Mask with paths, or a BASIC/custom view without a mask | `{path: 1, …}` plus required fields            |
| Omitted mask, `*` or FULL, allow-list                  | `{root: 1, …}` of the allow-list plus required |
| Omitted mask, `*` or FULL, no allow-list, denied       | `{denied: 0, …}`                               |
| Omitted mask, `*` or FULL, no allow-list, no denies    | `nil` — no `$project` stage                    |

In inclusion mode `_id` is suppressed (`{_id: 0}`) unless a selected path covers it — keep it with `projection.WithRequiredFields("_id")`. Paths
covered by a selected ancestor are dropped after field mapping, so the document never triggers MongoDB's "Path collision" error.

`ModeNone` without required fields fails with `projection.ErrUnsupportedSelection`: a projection document cannot select no field.

### Usage

```go
policy, err := projection.NewPolicy(
    projection.WithUntrustedInput(true),
    projection.WithAllowedFields("name", "createTime", "address.*"),
)
if err != nil {
    return err
}

trans, err := mongo.NewProjectionTranslator(policy,
    mongo.WithProjectionOptions(
        projection.WithFieldMapping(map[string]string{"createTime": "created_at"}),
        projection.WithRequiredFields("_id", "created_at"), // cursor ID and sort key
    ),
)
proj, err := trans.Translate(spec)

cur, err := col.Find(ctx, filter, options.Find().
    SetSort(bson.D{{Key: "created_at", Value: -1}}).
    SetProjection(proj), // nil selects every field
)
```

A keyset pager reads the next-page cursor from the last returned row, so keep the cursor ID and primary sort fields with
`projection.WithRequiredFields`; the [list engine](#list-engine) refuses a projection that computes a sort field.

`WithProjectionIDField` chooses the application identity included by `WithProjectionIncludeID`. Inclusion projections suppress only the special
MongoDB `_id` field; they never add an exclusion for an unselected application identity.

### Projection semantics

The document is a pure field selection, valid both for an aggregation `$project` stage and for a find projection. No projection operator (`$`,
`$slice`, `$elemMatch`) or expression is ever emitted.

- Inclusion `{path: 1}`: a requested path list, a view or the allow-list roots plus the required fields. MongoDB returns `_id` unless told
  otherwise, so the translator adds `{_id: 0}` when no selected path covers `_id`.
- Exclusion `{path: 0}`: every authorized field under a policy without an allow-list, listing the denied fields. It is never mixed with inclusion.
- `nil`: every field; add no `$project` stage (or find projection).

A path whose ancestor is selected is dropped after the field mapping is applied, because MongoDB rejects overlapping paths in one projection ("Path
collision"). A dotted path into an array of documents (`items.name`) selects that field in every element; it does not filter the elements.

## List engine

The engine refuses `listing.Request.Cutoff` with `listing.ErrInvalidRestriction` because keyset tokens bind the complete BSON filter.


A schema selecting case-insensitive contains fields is rejected with `listing.ErrInvalidSchema`. MongoDB's contains predicate uses case-sensitive
literal substring matching.

`NewListEngine[D](coll, signer, schema, opts...)` is the MongoDB engine of the [`listing`](../../listing/README.md) contract (`listing.Engine[D,
bson.D]`): `List(ctx, subject, req, scope)` compiles the request with the schema, translates its filter with the filter translator of this package, ANDs
it with the caller's `scope` and reads one keyset page with a plain `find` (the sort, `limit` page size + 1, one batch), decoding the documents into
`D`. The page token carries the sort values of the last document as BSON, so native identity types (ObjectID, integers, strings) keep their BSON order,
and is bound to subject, the parent, the schema's contract ID, the marshaled filter (scope included) and the sort. A `bson.M` or `map[string]any` of
several entries in the scope or the filter, at any depth, is encoded as its entries in key order, since the driver encodes a map in an order that varies
between runs: the same scope binds the same token on every page; a `bson.D` keeps its order, and every value keeps its BSON type (a `[]bson.E` stays a
document). A nil signer serves a single page and refuses a page token. The engine is safe for concurrent use; build one per collection. It passes
`listingtest.Run` against a live server ([`integration/docker/mongo`](../../integration/docker/README.md)).

```go
list, err := mongo.NewListEngine[widgetDoc](coll, signer, widgetSchema,
	mongo.WithListFieldPaths(map[string]string{"id": "_id", "createTime": "created_at"}),
	mongo.WithListScopeKeys("owner"), mongo.WithListParentKey("owner"),
	mongo.WithListIndexNames(map[string]string{"createTime desc": "idx_create_time"}), // keep an existing index name
)
err = list.EnsureIndexes(ctx, storeIndexes...)
page, err := list.List(ctx, method, req, bson.D{{Key: "owner", Value: req.Parent}})
```

### Sorts and indexes

| Rule            | Behavior                                                                                                                                           |
|-----------------|----------------------------------------------------------------------------------------------------------------------------------------------------|
| Sort            | The keys of the order at their sort paths, then the tie-breaker (`_id`, `WithListTiebreakPath`) in the direction of the last key (`Sort`)          |
| Index           | One compound index per declared pattern, `{scope keys…: 1, k1: d1, …, kn: dn, _id: dn}`, serving the pattern and its reversal (`Index`, `Indexes`) |
| Built-in index  | An unscoped id-only pattern uses `_id_` (`IDIndex`), which `Indexes` does not return                                                               |
| Names           | The name MongoDB would generate from the keys (`owner_1_created_at_-1__id_-1`), or the one of `WithListIndexNames`                                 |
| `EnsureIndexes` | One idempotent `createIndexes` of the store's own models and the pattern indexes; a conflicting index fails with the server error, never dropped   |
| Explain check   | `mongotest.AssertIndexedSorts` proves every order is an index scan without a blocking `SORT`                                                       |

`NewListEngine` refuses (`listing.ErrInvalidSchema`) a schema without declared sort patterns or with an unrestricted order, a maximum page size
above `MaxListPageSize`, a field or sort path that is not a BSON path or names an undeclared field, a sort path or scope key on the tie-breaker, an
order that sorts on the tie-breaker anywhere but last, twice on one path or on a scope key, null placement or collation in a pattern, a pattern
over a Presence field (which `listing.WithSortableFields` can allow: a page token on a null sort value has no MongoDB range to continue from), a cursor
precision that is not a positive multiple of a millisecond, a negative time cap, a parent key that is not a scope key, and an index name keyed by
anything but a declared pattern, given twice, reused, or naming (or renaming) the built-in index. A nil collection plans only: `Sort`, `Index`
and `Indexes` work without a connection.

`MustNewListEngine` takes the same arguments for a configuration fixed in code (a package-level `var`, like `listing.MustSchema`; a planning
engine with a nil collection, say): it returns the engine `NewListEngine` builds and panics on a refused one with an error that starts
`mongo.MustNewListEngine: ` and wraps the `NewListEngine` error.

### Scope

`WithListScopeKeys` declares the BSON paths every query pins by equality (the parent of a child list); they prefix every pattern index. `List` refuses
with `ErrScope`, before querying, a scope that does not pin each key by a top-level equality on a scalar — judged by its BSON encoding with the engine's
registry, so an operator document, an array or null is never a pin — and, for the key of `WithListParentKey`, a pin that is not `req.Parent`. A single
scope key does not implicitly become the parent.

A scope, like the whole filter sent to the server (the scope ANDed with the request filter), holds only these types; any other value fails with
`ErrScope` before it is encoded:

| Kind       | Admitted types                                                                                                                                                     |
|------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Containers | `bson.D`, `bson.E`, `bson.M`, `map[string]any`, `bson.A`, and unnamed slices of admitted values (`[]any`, `[]bson.D`, `[]bson.M`, `[]string`, …)                   |
| Raw BSON   | `bson.Raw`, `bson.RawArray`, `bson.RawValue`, at most 16 MiB, without JavaScript, code with scope, symbol or DBPointer values                                      |
| Leaves     | `nil`; booleans, integers, floats and strings, named or not, without their own BSON marshaling; `[]byte`, `time.Time`                                              |
| BSON       | `bson.DateTime`, `Timestamp`, `ObjectID`, `Decimal128`, `Regex`, `Binary`, `Null`, `Undefined`, `MinKey`, `MaxKey`                                                 |
| Registry   | `decimal.Decimal`, `decimal.NullDecimal`, the date types registered on the engine's registry (`RegisterDateType`, the go-atlas-core `civil.Date` of `NewRegistry`) |
| Optional   | `optional.Optional` of a leaf                                                                                                                                      |

Structs, pointers (typed nil included), other maps, named slices, arrays, values with their own BSON marshaling (`bson.Marshaler`,
`bson.ValueMarshaler`), `bson.JavaScript`, `bson.Symbol`, channels and functions are refused. The reason is that a scope is application code
handed to the driver: the filter translator emits only the containers and leaves above, so a whitelist checks every scope exactly as the
driver will encode it, where a walk mirroring the driver's reflective encoding of arbitrary Go values (cycles through pointers, self-inlining
structs, custom marshalers) cannot be kept exact. Build a scope from `bson.D` and scalars; convert a struct to a `bson.D` first.

Every key at every depth, raw BSON included, is checked against the server-side JavaScript operators (`$where`, `$function`, `$accumulator`). The check
fails past 1000 nesting levels — a value that holds itself fails there — and past 262144 values, every container element counted, and counted again each
time the encoding repeats it — a small value sharing its parts (a list holding another twice, nested) counts as its expansion — and past 16 MiB of keys,
strings, binaries, regular expressions and raw BSON, counted the same way, so the canonical key ordering of the filter's maps is bounded as well (a map
reached through several references is ordered once); it refuses raw BSON beyond 16 MiB before reading it, refuses a decimal (`decimal.Decimal`,
`decimal.NullDecimal`, an `optional.Optional` of one, alone or in a list) whose coefficient has more than 4096 bits without copying it, and stops when
the request's context ends. The filter and every scope pin encode through a writer bounded to MongoDB's 16 MiB document limit, and the encoded filter is
checked against the operators again, so a codec of the registry that expands or writes an operator fails too.

### Storage semantics

A field must hold the BSON type its `listing.Field.Kind` declares. An Optional scalar reads as its kind's zero value when missing or null;
an array treats missing, null and `[]` as the empty array. A Presence field follows the listing contract: null (or missing) equals null and
differs from every value, is in no list and holds no string, so `!=`, a negated `in` and a negated string function hold for it, while an
ordering comparison is unknown on null, also under negation: the engine puts the filter in negation normal form over the presence fields
(complementing a negated comparison) and renders the rest with MongoDB's own null rules, and `has()` of a presence field holds for a non-null
value only. `size()` of a missing or null value is 0. Stored dates must align to the cursor precision (`WithListTimestampPrecision`, a BSON
millisecond by default); an unaligned date fails the page instead of issuing a token that could repeat rows.

### Failures

| Failure                                       | Error                                                                                                    |
|-----------------------------------------------|----------------------------------------------------------------------------------------------------------|
| Invalid request field                         | InvalidArgument `coreerrs.FieldError` on pageSize, parent, orderBy, filter or pageToken                  |
| Query beyond the time cap (`WithListMaxTime`) | `ErrTimeout`: a field error on filter when one was sent, else wrapped with `coreerrs.ErrUnavailable` (2) |
| Caller's context ended during the query       | The context's error (2)                                                                                  |
| Document that does not decode into `D`        | The `WithListCorruptRecord` category (default `ErrCorruptRecord`) with the decode error (1)              |
| Scope not pinning its keys, or refused        | `ErrScope`                                                                                               |
| Anything else                                 | Wrapped with the collection name; `WithListErrorMapper` replaces the classification                      |

(1) Classified through `apierror.Classify`: the failure reads as and matches the category alone, so an `apierror.Mapper` classifies it by the
category and a decoder that returns a page-token, deadline or field error does not turn a stored record into a client error; errors.Is and
errors.As no longer reach the decode error (a decoder's `context.DeadlineExceeded` does not match). `apierror.HiddenCause` retains the typed
error for logs. A go-mongox decode error exposes the decoder error through `Unwrap`. Its message omits the stored value.
The whole decoding failure is classified, even when its chain already holds a classified error, so no cause joined with that one reaches the
mapper.

(2) Once the query context has ended (the time cap or the caller's context), that end is reported alone, whatever `FindPage` returned — a cursor,
decoder or page-token error included — and a driver timeout is `ErrTimeout` as well. The failure is hidden behind `apierror.Classify`: a
FieldError, `apierror.Error` or sentinel it holds never selects the status or reaches the client, and `apierror.HiddenCause` returns it for logs.

`WithListMaxTime` (default `DefaultMaxTime`, ten seconds) is the only deadline the engine adds; zero relies on the caller's context, and a
shorter caller deadline wins. `WithListProjection` limits the returned fields and must keep every sort field and the tie-breaker as plain
inclusions. `WithListRegistry` is the registry the filter and the scope pins are encoded with (default `NewRegistry()`): pass the registry of
the collection's client when it is another.

## Value encoding

`ValueEncoding(opts...)` returns the `evaluator.ValueEncoding` of the MongoDB driver (`*BSONEncoding`): an evaluator configured with
`evaluator.WithValueEncoding(mongo.ValueEncoding())`, or a memory list with `memory.WithListValueEncoding`, reads a data value exactly as the
driver stores it with the [codecs](#codecs) of this package — `bson.M`, `bson.D` and a struct as the document the driver encodes, `bson.A` as a
list, `bson.DateTime` as a timestamp, `bson.Decimal128` as a decimal (its NaN and infinities as the float64 ones), a generic `bson.Binary` as
bytes, a value with a custom BSON encoding (`bson.Marshaler`, `bson.ValueMarshaler`, an `optional.Optional`) as what it encodes to, a
`json.Number` as the number the driver writes. A `bson.Symbol` compares as a string; the other BSON types (ObjectID, Timestamp, Regex, …) are
kept and equal only themselves. `BSONEncoding` also implements the evaluator's optional extensions (document elements, string and binary
extraction, scan sizes, equality).

Every conversion is bounded and charged to the evaluation's data budget: a value nested beyond MongoDB's 100 levels, larger than its 16 MiB
document limit, cyclic, with more than 4096 struct fields or a decimal coefficient beyond 4096 bits stays opaque (it equals no literal). The
bounds and costs are the evaluator's own (`evaluator.MaxStoredNesting`, `evaluator.MaxDocumentBytes`, `evaluator.BytesPerUnit`, … and
`evaluator.DocumentKeys`), and the coefficient bound is `decimal128.MaxCoefficientBits`, so a value reads the same with or without the encoding. Values
that encode themselves are measured before the driver encodes them, and a hook that returns more the second time fails the encoding. Raw BSON,
which the driver copies trusting the lengths inside (`bson.Raw`, `bson.RawValue`, `bsoncore.Document` and `Array`, what a hook returns, at any
depth), is checked for well-formed lengths, terminators and nesting before the driver reads it, and a value holding a malformed one is opaque;
a panic of the driver while it encodes or decodes a value makes the value opaque too. The
registries that encode are pooled with bounded caches. `WithValueEncodingRegistry` encodes through the application's registry instead,
bounded by `WithValueEncodingMaxDocumentBytes` (`DefaultMaxDocumentBytes`): its encoders write through a writer that measures every value and
charges it to the data budget before the driver buffers it, so an encoder expanding a small value stops as soon as the bound is passed. The
value it encodes is converted to its stored form at every depth at once, so the values inside are read as the registry stored them and never
pass through it again: an encoder that is not idempotent is applied once, as when the driver writes the value. A value in a stored form is
never encoded again, on any path (a path through a list of documents, an array comparison, `in`, ordering, `size()`, a list engine's reader):
the base forms (strings, numbers, `time.Time`, `[]any`, `map[string]any`, …) and the values of the BSON types the driver keeps as they are
(ObjectID, Symbol, Timestamp, Regex, a binary of another subtype, …). So a value of such a type placed directly in the data map, a `[]any`
or a `map[string]any` is read as it is, not through the registry's encoder for the driver's type; inside a struct, a `bson.D` or any other
value the registry encodes whole it is encoded as the driver writes it.

## Codecs

The backend uses `github.com/altessa-s/go-mongox` for BSON codecs, bounded writes, raw BSON validation, filter preparation, keyset
pagination and create-only indexes. Applications can configure the registry through the functions below.

`NewRegistry()` returns a BSON registry with the driver's defaults and the codecs of the values go-aip and go-atlas-core define; install it on
the client (`options.Client().SetRegistry(mongo.NewRegistry())`) so documents are stored the way the value encoding reads them.

| Value                                                 | Stored as                                                                                                                                                                                         |
|-------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `optional.Optional[T]`, `*optional.Optional[T]`       | The value's encoding, or null; null decodes as None; `omitempty` omits None                                                                                                                       |
| `decimal.Decimal`, `decimal.NullDecimal` and pointers | An exact Decimal128; more than 34 digits or an exponent outside [-6176, 6111] fails with `ErrDecimalOutOfRange`, and so does a coefficient of more than 4096 bits, before its trailing zeros fold |
| Decimal128, Int32, Int64 read into a decimal          | The exact value; NaN or an infinity fails with `ErrDecimalNotFinite`; a double is refused                                                                                                         |
| go-atlas-core `civil.Date`                            | `"YYYY-MM-DD"` (sorts in date order); the zero date as `""`                                                                                                                                       |
| A date type registered with `RegisterDateType[T]`     | `"YYYY-MM-DD"`; the zero date as null, or `""` with `WithDateZeroAsEmptyString()`; a BSON date is refused                                                                                         |

`RegisterDateType[T](reg, opts...)` registers the calendar-date codec for any struct with the fields `Year int`, `Month time.Month` and
`Day int` (such as `cloud.google.com/go/civil.Date`, which this module does not depend on) and fails with `ErrNotADateType` for another type; a
date outside years 0–9999, or one its `Valid() bool` method refuses, fails with `ErrInvalidDate`. The decimal codec stores exactly the decimals
[`decimal128.Fits`](../../decimal128/README.md) accepts (validate input with it), in the form `decimal128.Canonical` returns and reads back.

## Errors

| Sentinel               | Returned by                                                           | Code (aipmap)                              |
|------------------------|-----------------------------------------------------------------------|--------------------------------------------|
| `ErrScope`             | `ListEngine.List`: a scope not pinning its keys, or a refused value   | INTERNAL                                   |
| `ErrTimeout`           | `ListEngine.List`: the time cap expired                               | UNAVAILABLE, or INVALID_ARGUMENT on filter |
| `ErrCorruptRecord`     | `ListEngine.List`: a document that does not decode (default category) | INTERNAL                                   |
| `ErrDecimalOutOfRange` | The decimal codec: a decimal a Decimal128 cannot hold exactly         | INTERNAL                                   |
| `ErrDecimalNotFinite`  | The decimal codec: a stored NaN or infinity                           | INTERNAL                                   |
| `ErrInvalidDate`       | The date codecs: an invalid date or stored string                     | INTERNAL                                   |
| `ErrNotADateType`      | `RegisterDateType`: a type without the date fields                    | INTERNAL                                   |

The table is checked against the package by `errors_test.go`.

## Options

| Option                              | Kind                  | Default                      | Behavior                                                                                                 |
|-------------------------------------|-----------------------|------------------------------|----------------------------------------------------------------------------------------------------------|
| `WithSchemaOmitEmpty`               | `SchemaOption`        | none                         | CEL fields encoded with BSON `omitempty`: an absent field reads as omitted.                              |
| `WithSchemaZeroWhenAbsent`          | `SchemaOption`        | none                         | CEL fields whose missing or null value reads as the zero value of the declared kind.                     |
| `WithSchemaPresenceFields`          | `SchemaOption`        | none                         | Nullable fields with strict presence semantics (negation normal form, non-null guards).                  |
| `WithSchemaSizeZeroWhenAbsent`      | `SchemaOption`        | false                        | `size()` of a missing or null field without a declared kind resolves to 0.                               |
| `WithFilterOptions`                 | `FilterOption`        | none                         | Binds the shared policy to the storage names of this backend (`filter.WithFieldMapping`).                |
| `WithFilterCollector`               | `FilterOption`        | nil                          | A `metrics.Meter` recording the translation duration; nil disables it.                                   |
| `WithFilterLiteralEncoder`          | `FilterOption`        | nil                          | Converts values before backend encoding; it must be safe for concurrent use.                             |
| `WithFilterPredicateRenderer`       | `FilterOption`        | nil                          | A trusted predicate override; return handled=false to use built-ins.                                     |
| `WithOrderOptions`                  | `OrderOption`         | none                         | Storage options: mapping, KeyResolver, denied storage fields, tie-breakers.                              |
| `WithOrderCollector`                | `OrderOption`         | nil                          | Records `orderby_mongo_translate_duration_seconds`; nil disables collection.                             |
| `WithProjectionOptions`             | `ProjectionOption`    | none                         | Storage options: `projection.TranslatorOption` values (mapping, denied and required storage fields).     |
| `WithProjectionIDField`             | `ProjectionOption`    | `DefaultIDField`             | The storage identity included by `WithProjectionIncludeID`. Only MongoDB `_id` is implicitly suppressed. |
| `WithProjectionIncludeID`           | `ProjectionOption`    | false                        | Includes the identity field through the required-field policy.                                           |
| `WithProjectionCollector`           | `ProjectionOption`    | nil                          | Records `projection_mongo_translate_duration_seconds`; nil disables collection.                          |
| `WithListFieldPaths`                | `ListEngineOption`    | none                         | Public field name to the BSON path it is filtered and sorted on; others are stored under their name.     |
| `WithListSortPaths`                 | `ListEngineOption`    | none                         | Public field name to a separate BSON sort path (a normalized sort field).                                |
| `WithListScopeKeys`                 | `ListEngineOption`    | none                         | BSON paths every scope pins by equality; they prefix every pattern index.                                |
| `WithListParentKey`                 | `ListEngineOption`    | ""                           | The scope key bound to `Request.Parent`; empty binds none.                                               |
| `WithListIndexNames`                | `ListEngineOption`    | none                         | The index name of a declared pattern (or its reversal), any spelling.                                    |
| `WithListTiebreakPath`              | `ListEngineOption`    | `DefaultTiebreakPath`        | The unique BSON path every sort ends on.                                                                 |
| `WithListTimestampPrecision`        | `ListEngineOption`    | `listing.TimestampPrecision` | The cursor date resolution, a positive multiple of a millisecond.                                        |
| `WithListMaxTime`                   | `ListEngineOption`    | `DefaultMaxTime`             | Caps one query; zero relies on the caller's deadline.                                                    |
| `WithListProjection`                | `ListEngineOption`    | nil                          | Returned fields; must keep the sort fields and the tie-breaker.                                          |
| `WithListRegistry`                  | `ListEngineOption`    | `NewRegistry()`              | Encodes the bound filter and the scope pins.                                                             |
| `WithListCorruptRecord`             | `ListEngineOption`    | `ErrCorruptRecord`           | The category of a document that does not decode; nil keeps the default.                                  |
| `WithListErrorMapper`               | `ListEngineOption`    | nil                          | Replaces the classification of query failures; receives the original error.                              |
| `WithValueEncodingRegistry`         | `ValueEncodingOption` | nil                          | Encodes values with the application's registry instead of `NewRegistry()`.                               |
| `WithValueEncodingMaxDocumentBytes` | `ValueEncodingOption` | `DefaultMaxDocumentBytes`    | Bounds the BSON a custom registry may encode one value to.                                               |
| `WithDateZeroAsEmptyString`         | `DateOption`          | false                        | Stores the zero date as `""` instead of null.                                                            |

The filter translator records `filter_mongo_translate_duration_seconds`.

## Defaults and limits

| Constant                  | Value              |
|---------------------------|--------------------|
| `MaxSizeOperand`          | `math.MaxInt32`    |
| `DefaultIDField`          | `"_id"`            |
| `DefaultTiebreakPath`     | `"_id"`            |
| `IDIndex`                 | `"_id_"`           |
| `MaxListPageSize`         | `10000`            |
| `DefaultMaxTime`          | `10 * time.Second` |
| `DefaultMaxDocumentBytes` | `16 << 20`         |

## API

| Symbol                                                                                                                     | Purpose                                                                                                                       |
|----------------------------------------------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------|
| `Schema`                                                                                                                   | Immutable MongoDB storage rules, independent of the shared filter policy; the evaluator's MongoDB profile.                    |
| `Schema.BindEvaluator`                                                                                                     | Binds the schema to an evaluator's policy and value encoding (`evaluator.Binder`); the compiler of the profile.               |
| `Schema.BindPolicy`, `Schema.ValidatePolicy`                                                                               | Checks a filter policy against the schema (`filter.ErrInvalidOptions`); `BindPolicy` binds a copy to its field mapping.       |
| `Schema.ValidateDecimal`                                                                                                   | Checks that a decimal fits BSON Decimal128 (`filter.ErrFieldTypeMismatch` otherwise).                                         |
| `Schema.IsOmitEmpty`, `ZeroWhenAbsent`, `PresenceFields`, …                                                                | The declared storage rules, read by the translator, the evaluator profile and the list engine.                                |
| `NewSchema`                                                                                                                | Validates and freezes the storage rules.                                                                                      |
| `DefaultSchema`                                                                                                            | A schema with no MongoDB storage rules.                                                                                       |
| `CheckSizeOperand`                                                                                                         | Rejects a non-numeric, negative, NaN or oversized `size()` operand.                                                           |
| `FilterTranslator`                                                                                                         | Converts filter AST nodes to `bson.M` (`Translate`) or sorted `bson.D` (`TranslateD`); `ValidateAST` checks support.          |
| `NewFilterTranslator`                                                                                                      | A filter translator over the shared policy with the default schema; `filter.ErrPolicyRequired` for a nil policy.              |
| `NewFilterTranslatorWithSchema`                                                                                            | A filter translator with explicit storage semantics over the shared policy.                                                   |
| `OrderTranslator`                                                                                                          | Converts an `orderby.Spec` into a `bson.D` sort document.                                                                     |
| `NewOrderTranslator`                                                                                                       | An order translator bound to the shared `orderby.Policy`; `orderby.ErrPolicyRequired` for a nil policy.                       |
| `ProjectionTranslator`                                                                                                     | Converts a `projection.Spec` into a `bson.M` projection (`Translate`, `TranslateView`).                                       |
| `NewProjectionTranslator`                                                                                                  | A projection translator bound to the shared `projection.Policy`; `projection.ErrPolicyRequired` for a nil policy.             |
| `ListEngine`                                                                                                               | A keyset list over a collection (`listing.Engine[D, bson.D]`): `List`, `Sort`, `Index`, `Indexes`, `EnsureIndexes`, `Schema`. |
| `NewListEngine`                                                                                                            | A list engine of a collection (nil plans only) under a listing schema.                                                        |
| `MustNewListEngine`                                                                                                        | Panicking `NewListEngine` for package-level declarations fixed in code (as `listing.MustSchema`).                             |
| `ValueEncoding`, `BSONEncoding`                                                                                            | The MongoDB `evaluator.ValueEncoding`.                                                                                        |
| `NewRegistry`, `RegisterDateType`                                                                                          | The codec registry and the calendar-date codec of a date type.                                                                |
| `SchemaOption`, `FilterOption`, `OrderOption`, `ProjectionOption`, `ListEngineOption`, `ValueEncodingOption`, `DateOption` | The functional options of each kind.                                                                                          |

## Related packages

- [filter](../../filter/README.md) and [filter/evaluator](../../filter/evaluator/README.md): the value encoding contract, its bounds and costs
- [orderby](../../orderby/README.md)
- [projection](../../projection/README.md) and [projection/projectiontest](../../projection/projectiontest/README.md)
- [listing](../../listing/README.md) and [listing/listingtest](../../listing/listingtest/README.md)
- [decimal128](../../decimal128/README.md): which decimals the decimal codec stores, in what form, and the coefficient bound
- [mongotest](mongotest/README.md): the shared filter expectations and the explain-plan index checks
- [backend/sqlbase](../sqlbase/README.md)
- [filter guide](../../docs/filter.md), [orderby guide](../../docs/orderby.md), [projection guide](../../docs/projection.md),
  [listing guide](../../docs/listing.md)
