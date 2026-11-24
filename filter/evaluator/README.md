# evaluator

```go
import "github.com/altessa-s/go-aip/filter/evaluator"
```

Evaluates a [filter AST](../) over Go values. Shared evaluator instances and prepared filters support concurrent calls without database drivers.

## Contract

`SimpleLower15_1(text)` applies Unicode 15.1 simple lowercase, including the one-code-point mapping of İ to i. Schema-aware profiles use it for
opt-in literal substring matching. The native evaluator uses case-sensitive contains.

`New(policy, opts...)` takes the shared `*filter.Policy` (built once per resource and shared with the translators) and evaluator `Option` values;
`WithFilterOptions` maps the policy's API names to data keys. `Evaluate` validates the expression and evaluates one value,
while `Prepare` snapshots and validates once for repeated execution. A nil filter matches every document.

`WithProfile(nil)` selects native evaluation. A custom `Profile` validates the policy and implements either `Compiler` or `Binder` (a compiler bound
to the evaluator's `ValueEncoding`); `FieldAccessor` optionally supplies lookup. The backends implement the profiles: `mongo.Schema`
([backend/mongo](../../backend/mongo/README.md#evaluator-profile)) is a `Binder` and `postgres.Schema`
([backend/postgres](../../backend/postgres/README.md)) a `Compiler`. `sqlbase.Decoder` supplies `ValueDecoder`; `mongo.ValueEncoding()` supplies
`ValueEncoding`. A `Compiler` profile rejects `ValueEncoding` and can accept its own decoder contract. Core names no backend: the package holds the
reference semantics, the profile contracts and the building blocks a profile's program reads data
with ([Composable execution](#composable-execution)).

`WithComparator` takes precedence when its handled result is true, including equality inside scalar and array membership. Errors propagate.
`ValueEncoding` provides stored conversion and ownership; `DocumentElements`, `StringExtractor`, `BinaryExtractor`, `ScalarEquality`, and `ScanSizer`
are optional capabilities.

`DefaultMaxDataBudget` is 2^22 work units per evaluation. AST limits and data limits are independent. Request-aware profiles can bind a shared
`Budget`; request-bound state must not be shared beyond that budget's concurrency guarantees. Regex cache statistics belong to the evaluator's
policy instance.

## Key types

| Type                      | Purpose                                                    |
|---------------------------|------------------------------------------------------------|
| `Evaluator`               | `Evaluate`, `Validate`, `ValidateAST`, `Prepare`, `Lookup` |
| `Prepared`                | Validated private AST snapshot for repeated evaluation     |
| `ValueEncoding`, `Budget` | Bounded storage-representation adapter contract            |
| `Option`                  | Evaluator-only configuration                               |
| `Profile`, `Compiler`     | Profile contract; a compiler returns a `Program`           |
| `Binder`                  | A profile whose compiler reads through a `ValueEncoding`   |
| `Reader`, `Guard`         | Building blocks of a `Program`: data reading, limits       |
| `ValueDecoder`            | Converts driver-owned values before a `Program` reads them |

## Usage

```go
policy, err := filter.NewPolicy(filter.WithAllowedFields("age"))
if err != nil {
    return err
}
eval, err := evaluator.New(policy) // evaluator.WithProfile(schema) selects a backend's semantics
if err != nil {
    return err
}
prepared, err := eval.Prepare(node)
if err != nil {
    return err
}
matched, err := prepared.Evaluate(map[string]any{"age": int64(21)})
```

`Prepare` copies the AST and byte literals before validating; later caller mutations cannot change its meaning.
`Validate`, `Prepare` and `Evaluate` check the shared filter shape, limits and every field reference, including short-circuited branches.
Every error the options make decidable without data is reported there, as a translator reports it, rather than per record (where an empty
data set would hide it): a literal of the wrong kind or outside a field's enum values (`ErrFieldTypeMismatch`, `ErrEnumValueNotAllowed`),
a decimal literal that is no number, a `time.Duration` literal on a timestamp field, two field operands of incompatible declared kinds
(`name == age`, `ErrFieldTypeMismatch`), `x in f` on a field declared with a scalar kind (`ErrInvalidExpression`), `substring()` literal
bounds out of range or reversed (`ErrInvalidExpression`), an ordered comparison with null (`age > null`, `ErrUnsupportedOperation`), and a
literal in the place of a predicate (a bare `true`, `ErrInvalidExpression`). Under `WithProfile` the profile's compiler validates instead (the
MongoDB profile with the MongoDB translator's rules).
Data-dependent errors can still occur during evaluation; a null or absent operand is not one of them ([Three-valued logic](#three-valued-logic)).

## Three-valued logic

Without a profile the evaluator gives a null or absent operand SQL's three-valued logic, so a filter selects the same records in memory as
the SQL translators select in the database. A predicate is true, false or **unknown**, and unknown is exactly what the SQL translators render as SQL
`UNKNOWN`:

| Unknown for a null or absent value                                | Not unknown (true or false)                                           |
|-------------------------------------------------------------------|-----------------------------------------------------------------------|
| an ordering comparison (`x > 1`, `name < other`, `tags > other`)  | `==` / `!=` (null equals only null), `in`, `has()`                    |
| a bare boolean (`active`, `!active`)                              | a string function on a non-string target (`x.contains("a")` is false) |
| `size()` of a value that is not an array field                    | `size()` of an array field (0)                                        |
| `substring()` of a null value, with a null bound, or out of range | a field compared with `size()` or `substring()` by `==` / `!=`        |
| a null string-function argument (`name.contains(note)`)           |                                                                       |

`&&` and `||` evaluate as SQL's `AND` and `OR`, whatever the order of their operands: `x > 1 || active` holds where `active` does, as does
`active || x > 1`; `x > 1 && false` is false; unknown otherwise propagates. `!` keeps unknown unknown, so `!(x > 1)` and `!(x > 1 || name == "z")`
do not select a record with a null `x` either. A predicate used as a value (`(x > 1) == false`) is unknown too. An unknown filter does not match:
`Evaluate` and `Prepared.Evaluate` return `false` and a nil error, as SQL's `WHERE` drops the row.

Every other error is returned as before and fails the evaluation whatever the other operand of `&&` or `||`: a value of an incomparable or
unsupported type (`name > 1` on a string, `size()` of a number, `ErrUnsupportedType` / `ErrUnsupportedOperation`), a non-bool predicate value
(`ErrInvalidExpression`), the data budget (`ErrDataLimitExceeded`), the operation and depth limits, a custom comparator's error. The right operand
is skipped only when the left one decides the result (`false &&`, `true ||`), so an error there may go unnoticed, as in a short-circuiting SQL
engine. Under the MongoDB profile (`WithProfile(mongo.Schema)`) the MongoDB rules apply instead: a negated predicate matches a missing field,
as `$not` and `$nor` do; the compiled PostgreSQL profile and the listing profile (`listing/evaluation`) apply the same three-valued logic as the
default evaluator. MongoDB missing-value rules are configured on `mongo.Schema` (backend/mongo), not on the shared policy.

## Exact decimals

`FieldKindDecimal` declares a field holding money or another value that must compare exactly. A numeric literal of the expression is read from its
source spelling — the parser keeps the text of a float literal (`LiteralNode.Text`) whenever its float64 would not reproduce it, so
`1.00000000000000001` stays 1.00000000000000001 and never matches 1. Decimal strings (`price >= "12345678901234567.89"`) are accepted too. A float64
that comes from elsewhere (a custom function, a float data value) is read as its shortest round-trip decimal and only with at most 15 significant
digits (`filter.MaxExactFloatDigits`). The default decimal parser bounds input to 8192 bytes (`filter.MaxDecimalStringLength`), coefficients to 4096
bits (`decimal128.MaxCoefficientBits`), and normalized nonzero exponents to -16383..16383. It normalizes zero and trailing zeros before comparison;
invalid inputs return `ErrFieldTypeMismatch`. These are work bounds, independent of Decimal128. `filter.WithDecimalParser` replaces conversion
consistently for comparisons and membership. MongoDB encoding separately requires Decimal128 representability (34 significant digits,
exponent -6176..6111).

| Consumer           | Behavior                                                                                                                                                                     |
|--------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `New`              | A `decimal.Decimal` data value compares exactly with the literal (`0.1+0.2 == 0.3` holds), declared or not; a declared field compares exactly whatever its data form         |
| `mongo` translator | Literals are emitted as BSON `Decimal128`, so MongoDB compares them with stored Decimal128 values exactly                                                                    |
| Other translators  | Scalar backends without exact encoding reject decimal declarations with `ErrUnsupportedType`; typed SQL schemas and custom literal encoders can supply exact representations |

Without `WithProfile`, a field declared `FieldKindDecimal` compares exactly whatever Go form its data takes, as the SQL and MongoDB
translators compare a decimal column: the data value is read as a decimal — a `decimal.Decimal` as it is, an integer by its value, a finite
`float64` as the shortest decimal that round-trips to it, a string through the policy's decimal parser — and the operand as the decimal the
expression spells. So `price == "1.5"`, `price == 1.5` and `price > 1` hold alike on `1.5`, `"1.50"` and `decimal.RequireFromString("1.5")`,
`price > "10"` is false on `"9"`, and `price in [1.5, "2"]` holds on `"2.00"`. A data value that is no decimal (a bool, a NaN, a string that is
no number) compares as the value it is. Field operands, array fields and `in` lists follow the same rule.

Under the MongoDB profile the evaluator follows MongoDB instead: the data keeps its stored form (a stored string `"1.5"` does not equal the
Decimal128 operand of `price == "1.5"`), a float literal compared with a field not declared `FieldKindDecimal` stays a double,
and a double compares with a decimal by its binary value, so a stored decimal `0.1` does not equal the literal `0.1` there.

```go
policy, err := filter.NewPolicy(filter.WithAllowedFields("price"), filter.WithFieldTypes(map[string]filter.FieldKind{"price": filter.FieldKindDecimal}))
eval, err := evaluator.New(policy)
ok, err := eval.Evaluate(node, map[string]any{"price": decimal.RequireFromString("12.30")}) // decimal.Decimal
```

## Array fields

`WithArrayFields("tags", "steps.providerId")` declares list-valued fields; a kind declared with `WithFieldTypes` is the element kind. A predicate on
an array field tests its elements, as MongoDB does: `tags == "red"`, `"red" in tags` and `tags in ["red", "blue"]` hold when any element equals an
operand, `tags != "red"` when none does, ordering comparisons and string functions when any element satisfies them (a null element satisfies none).
`"red" in tags` is the comparison `tags == "red"`: its literal is checked against the element kind (`1 in tags` fails with `ErrFieldTypeMismatch`
for a string array), and an absent or null array contains nothing. `tags.size()` measures the array, and an absent or null array has size 0 under
every operator.

The elements are the values a MongoDB query reaches on the path: the elements of a list at its end; through a list of documents (`steps.codes`) the
values found in each document — the elements of a list found there — and null for a document that lacks the rest of the path. An absent or null path
is null, an empty list has no elements, and `has(steps.codes)` holds when a document of the list holds the path. The MongoDB translator emits
`"red" in tags` as `{tags: "red"}` and measures `size()` with an aggregation expression, so a dotted path through an array of documents counts the
values it resolves to (`steps.codes.size()` counts the lists found, not their elements). The Mongo schema rejects a field declared both as an array
and as zero-when-absent (`ErrInvalidOptions`).

The evaluator applies these rules with and without the MongoDB profile. With it, it selects exactly the documents MongoDB selects; without it, it
agrees with MongoDB except that a nil Go value other than an untyped `nil` is no null — a nil slice, map or `[]byte` reads as its empty form, a nil
pointer or a `bson.Null` as a value no literal equals ([Data values](#data-values)). The integration suite runs the same matrix of shapes (absent,
null, empty, scalar, null elements, paths through documents ending in lists, nested lists) against MongoDB in both modes, on plain Go values.
Without the profile it also stays strict where MongoDB is not: an element of another type than the operand — a nested list included — fails with
`ErrUnsupportedType`, ordering bools fails with `ErrUnsupportedOperation`, and the first such element before a match is the error.

## Data values

The MongoDB profile — and any profile whose program reads through a `Reader`, and `StoredValue` — reads a data value in its stored form. Pointers,
named types, typed slices, arrays and maps with string keys it reads by their kind itself; the forms a storage encoding defines — a struct, a value
encoding itself, a driver's own types — it reads through the `ValueEncoding` of `WithValueEncoding`. `filter` imports no database driver: without an
encoding such a value is opaque (an `optional.Optional` is its value or null, a driver's type is read by its Go kind). With
`evaluator.WithValueEncoding(mongo.ValueEncoding())` (the MongoDB value encoding of [backend/mongo](../../backend/mongo/README.md#value-encoding))
it reads a value as the MongoDB driver stores it (with the `mongo.NewRegistry` codecs, so a `decimal.Decimal` is a Decimal128 and an `Optional` its
value or null), so Go values the driver stores alike evaluate alike:

| Go value                                                                        | Read as                                                        |
|---------------------------------------------------------------------------------|----------------------------------------------------------------|
| `[]any`, any other slice or array (`[]string`, `[]int`, `bson.A`, `[]bson.M`)   | a list of its elements                                         |
| a slice or array of `byte`, `bson.Binary` of the generic subtype                | bytes; a `bson.Binary` of another subtype equals no literal    |
| `map[string]any`, `bson.M`, `bson.D`, a map with string keys                    | a document                                                     |
| a struct, a `bson.Marshaler` / `bson.ValueMarshaler`, `json.Number`, `bson.Raw` | what the driver encodes it as                                  |
| sized or named numbers, strings and bools (`int8`, `float32`, `type S string`)  | the base value (`int64`, `float64`, `string`, `bool`)          |
| `bson.DateTime`, `bson.Decimal128`                                              | a timestamp, a decimal (NaN and the infinities as a `float64`) |
| a pointer                                                                       | the value it points to                                         |
| `nil`, a nil pointer, slice or map, `bson.Null`                                 | null (an empty slice or map is an empty list or document)      |

Values already in the base forms (`string`, `bool`, `int64`, `float64`, `time.Time`, `decimal.Decimal`, `[]byte`, `[]any`, `map[string]any`) are
read without conversion; the others are converted where the evaluator reads them. Other BSON types (`ObjectID`, `Timestamp`, `Regex`, ...) equal
no filter literal; under the MongoDB profile a `bson.Symbol` compares as a string, as MongoDB compares it. So does a value MongoDB cannot store
and the driver must not be asked to encode: a cyclic value, one nested deeper than MongoDB's 100 levels, one whose encoding exceeds 16 MiB, a
typed slice, array or map with more than 2^20 elements, a value the driver encodes with more than 2^20 parts, a document whose keys total more than
16 MiB, a struct whose type inlines itself (`bson:",inline"`, even through a nil pointer) or has, or inlines a type that has, more than 4096
fields, a value whose custom encoding returns more when the driver calls it than when it was measured, a map keyed by a type that encodes itself
as text (`bson.KeyMarshaler`, `encoding.TextMarshaler`: a `decimal.Decimal` key spells out its exponent), and, inside a value the driver
encodes, a `decimal.Decimal` whose coefficient exceeds 4096 bits (compared directly, such a decimal is no decimal). Such an opaque value, a
document and a list are members of no `in` list. A struct behind a pointer is encoded from its address, as the driver encodes it, so a field whose
type encodes itself through a pointer receiver reads as its encoding. The integration suite stores these values through the driver and checks that
the evaluator selects the documents MongoDB returns.

The default evaluator (without a profile) reads a data value as the Go value it is, with the outcome it has always had for it:

- a path steps only through a `map[string]any`: through a `bson.M`, a `bson.D`, a struct or another map it is absent;
- null is only `nil`: a nil `[]any`, `map[string]any` or `[]byte` is an empty list, document or byte string (`size(tags) == 0` holds, a nil
  `[]byte` equals `b""`), and a nil pointer, a nil slice or map of another type and a `bson.Null` are not null;
- `size()`, the string functions, `substring()` and a bare field take the value as it is: `size()` of a `[]string` fails with
  `ErrUnsupportedOperation`, `contains()` on a named string is false, a named bool is no bool (and fails as a predicate);
- `in` compares a value of a Go type other than the base forms as Go's `==` does — equal only to a value of the same type, so `int8(3) in [3]` is
  false. An interface inside a value is compared by the dynamic value it holds, and a value holding one `==` does not compare (a slice, a
  map) equals nothing. Every part `==` depends on — every interface and element walked, every string byte — is charged to the data budget
  before `==` runs; an array or a struct holding neither an interface nor a string is compared in time independent of its length.

Only where comparing such a value as it is fails with `ErrUnsupportedType` — `==`, `!=`, `<`, `<=`, `>`, `>=` — does the default evaluator compare
it in the stored form above (`int8(5) == 5`, `label("a") == "a"` and a `bson.Binary` against `b"ab"` hold), and on a field declared by
`WithArrayFields` it reads the values on the path in that form. Neither reads null for a value that is not `nil`: a nil slice or map of any type is
the empty list, byte string or document, and a nil pointer or a value stored as null (`bson.Null`, an empty `Optional`) stays the value it is,
which equals no literal. The opaque values above and the data budget below bound that reading as they bound the stored forms.

The AST limits bound the filter, not the data. `WithMaxDataBudget` (default 2^22 units) bounds the work of one `Evaluate` call on its data: a unit
per value visited, converted or compared (an element of a data list `in` reads included), one per 64 bytes of a string a string function or
`size()` scans (a regex more, by its length) or of a document key read, the parts and bytes of a value encoded through the driver — what a
custom encoding returns included, measured by calling it before the driver does — the fields and tag bytes of a struct type the driver
describes, and more for a decimal comparison or encoding. A large value, or one sharing its parts so that a path
through it multiplies (a map holding a list of itself: `a.a.a…` reaches 2^n values), then costs bounded time and memory: the evaluation fails
with `ErrDataLimitExceeded`, in both modes, whatever it would otherwise return.

What an evaluation leaves behind for the next is bounded too, however many types its data has (a program may construct them without end with
`reflect.StructOf`, `SliceOf`, `MapOf`): the descriptions of struct types kept hold at most 2^16 fields, and each registry pooled to encode values
through the driver caches at most 2^14 units of what the driver learns of the types it meets (an encoder and a decoder per type, a description per
struct type); a registry whose encoding would take it beyond is dropped after it. At most 16 idle registries are pooled, however many evaluations
overlap: one released to a full pool is dropped. The registry that decodes the encoding meets a fixed set of types.

## MongoDB profile

`WithProfile(schema)` with a `mongo.Schema` makes `New` match a document exactly as MongoDB matches the query the `mongo` translator builds from
the same AST, policy and storage schema. The profile — its validation, its query and expression forms and its comparison rules — lives with the
translator in [backend/mongo](../../backend/mongo/README.md#evaluator-profile), which runs it through this package's contracts (`Binder`, `Reader`,
`Guard`), beside the MongoDB value encoding it reads data through (`mongo.ValueEncoding`).

## Adapters and backend semantics

Adapters read storage-specific Go values; translators generate queries. Their package sets do not need to match.
`backend/mongo` supplies the MongoDB value adapter (`mongo.ValueEncoding`), which handles BSON types, struct tags and BSON marshalers.
The evaluator already reads ordinary Go scalar values, slices and string-keyed maps without an adapter.

| Backend                      | Query translator      | Storage-value adapter | Backend-specific in-memory behavior                           |
|------------------------------|-----------------------|-----------------------|---------------------------------------------------------------|
| MongoDB                      | `backend/mongo`       | `mongo.ValueEncoding` | `WithProfile(mongo.Schema)` implements MongoDB matching rules |
| PostgreSQL                   | `backend/postgres`    | `sqlbase.Decoder`     | `WithProfile(postgres.Schema)` evaluates the compiled program |
| MariaDB, ClickHouse          | SQL translators       | None supplied         | No SQL-specific evaluator mode                                |
| Meilisearch, RediSearch, Lua | Dedicated translators | None supplied         | No corresponding evaluator mode                               |

The default evaluator is not an emulator for every translator's backend. For example, SQL NULL comparison rules, database collations,
and search tokenization can change query results. A value adapter alone cannot reproduce those rules: support would require a defined
semantic contract and backend conformance tests as well as any needed driver-value conversion.

## PostgreSQL

Supply the same [typed schema](../../backend/postgres/README.md) used by `postgres.NewFilterTranslatorWithSchema`:

```go
eval, err := evaluator.New(policy, // the policy shared with postgres.NewFilterTranslatorWithSchema
    evaluator.WithProfile(schema),
    evaluator.WithValueDecoder(sqlbase.Decoder{}),
)
```

PostgreSQL mode evaluates the program whose SQL `postgres.NewFilterTranslatorWithSchema` renders, with this evaluator's NULL semantics (a NULL
column is a null value; a predicate this evaluator finds unknown is unknown there, with the same three-valued logic), uses PostgreSQL NUMERIC bounds
rather than Decimal128, and distinguishes SQL NULL, JSON null and missing paths. It supports arrays, including explicit dimensions/lower bounds
through `postgres.ArrayData`. `Prepare` owns the compiled expression; `Lookup` returns exact `decimal.Decimal` values, `JSONNull`, and `ArrayData`
where applicable. JSONB ordering requires an explicit comparator matching the database collation. See the schema README for the complete contract.

## Composable execution

`WithProfile` replaces the backend-specific options. A profile validates the policy (`Profile`); a `Compiler` supplies a compiled program, a
`Binder` binds to the evaluator's policy and `ValueEncoding` once, in `New`, and returns the `Compiler` it compiles every filter with, and
`FieldAccessor` (on the profile or on the bound compiler) optionally supplies `Lookup`. The contracts name no backend and carry no
database-driver dependency; a backend implements them in its own package, as `postgres.Schema` and `mongo.Schema` do:

| Contract          | Responsibility                                                                                                    |
|-------------------|-------------------------------------------------------------------------------------------------------------------|
| `Program`         | `Evaluate(data, maxWork, decoder)`: a compiled predicate within a work budget                                     |
| `Binder`          | `BindEvaluator(policy, enc)`: the `Compiler` of a profile whose programs read data through the `ValueEncoding`    |
| `ValueDecoder`    | Decodes driver-owned values (for example SQL nullable types) before evaluation                                    |
| `Reader`          | The data of one evaluation under its budget: stored forms, `WalkField`, `ResolvePath`, `QueryValues`, the charges |
| `Guard`           | The operation and depth limits of one evaluation, with the evaluator's errors                                     |
| `CompareResult`   | The outcome of a comparison operator over a three-way comparison                                                  |
| `CompareExact`    | Numbers by their exact values, as the evaluator compares them (decimals, integers against floats)                 |
| `LiteralPatterns` | The compiled literal patterns of a filter's `matches()` calls, for a program that skips the shared regex cache    |

A program built on `Reader` and `Guard` reads data exactly as the default evaluator does — the same stored forms, path traversals (an aggregation
path through lists of documents with `ResolvePath`, a query path with `QueryValues`), work units and limit errors — so a backend's semantics
differ from the reference only where its rules do.

`WithComparator` can override built-in scalar comparisons; returning handled=false delegates to built-in comparison.
`ValueEncoding` requires storage conversion and ownership only. DocumentElements, StringExtractor, BinaryExtractor, ScalarEquality
and ScanSizer are optional interfaces. Regex statistics are evaluator methods and refer to that evaluator's policy cache.
`Prepare` compiles the literal patterns of `matches()` once, so evaluating a `Prepared` reads them without the shared cache and
adds no hits; a pattern read from a field is still compiled through the cache on every record.
A nil filter matches every document in native and prepared evaluation.

Custom comparators also handle equality within scalar and array membership. Comparator errors propagate to the caller.

## Metrics

`WithCollector` takes a [`metrics.Meter`](../../metrics/README.md), as the filter translators' `WithFilterCollector` does, and records under the
subsystem `filter_evaluator` (the translators use `filter_<backend>`):

| Metric                      | Kind  | Recorded by                               |
|-----------------------------|-------|-------------------------------------------|
| `evaluate_duration_seconds` | Timer | `Evaluator.Evaluate`, `Prepared.Evaluate` |
| `prepare_duration_seconds`  | Timer | `Evaluator.Prepare`                       |

Failed calls are measured too. A nil meter (the default) disables collection at the cost of one nil check per call.

## Translator parity

The evaluator is the reference semantics of the filter language: it supports every operator and operand shape any translator supports
(comparisons, `in` including `"x" in arrayField`, `has()`, `contains`/`startsWith`/`endsWith`/`matches`, `size()` on strings and arrays,
field-to-field operands, timestamps, bytes, exact decimals, `substring()`). `filter/evaluator/conformance_test.go` runs
`filtertest.RunConformance` for the default evaluator; backend/mongo runs it for the MongoDB profile. `ValidateAST` makes an
`Evaluator` a `filter.Compatibility`, so it can be checked alongside the translators. Backend-only features have no evaluator
counterpart by design: inline rendering, predicate renderers and literal encoders (data is read through `WithValueEncoding` /
`WithValueDecoder` instead).

## Options

| Option              | Type     | Default              | Behavior                                                                                |
|---------------------|----------|----------------------|-----------------------------------------------------------------------------------------|
| `WithCollector`     | `Option` | nil / unset          | A `metrics.Meter` to record evaluation and preparation durations into; nil disables it. |
| `WithComparator`    | `Option` | nil / unset          | Override scalar and membership comparisons when handled is true; false delegates.       |
| `WithMaxDataBudget` | `Option` | DefaultMaxDataBudget | Bound work reading, converting, and comparing data in one evaluation.                   |
| `WithFilterOptions` | `Option` | nil / unset          | Binds the shared policy to the keys of the evaluated data (`filter.WithFieldMapping`).  |
| `WithProfile`       | `Option` | nil / unset          | WithProfile selects an explicit semantic contract. Nil selects native evaluation.       |
| `WithValueDecoder`  | `Option` | nil / unset          | Adapt SQL nullable and driver-owned values for a compiled profile.                      |
| `WithValueEncoding` | `Option` | nil / unset          | Adapt stored representations for the default evaluator and a `Binder` profile.          |

## Defaults and limits

| Constant               | Value     |
|------------------------|-----------|
| `DefaultMaxDataBudget` | `1 << 22` |

A `ValueEncoding` applies the evaluator's bounds and costs to the values it converts, so that a value reads the same with or without it and its
work is charged at the evaluator's rates (the MongoDB value encoding uses them):

| Constant              | Value      | Meaning                                                                                                       |
|-----------------------|------------|---------------------------------------------------------------------------------------------------------------|
| `MaxPointerHops`      | `32`       | Consecutive pointers and interfaces followed, so a self-referencing pointer cannot loop                       |
| `MaxStoredNesting`    | `100`      | MongoDB's limit on nested documents and arrays; a cyclic value is infinitely deep                             |
| `MaxStoredNodes`      | `1 << 20`  | Values inspected in one encoded value, elements of a typed slice, array or map copied                         |
| `MaxDocumentBytes`    | `16 << 20` | MongoDB's limit on a document: its estimated or found encoding, and the keys of one document (`DocumentKeys`) |
| `BytesPerUnit`        | `64`       | Bytes of a data string or binary one `Budget` unit scans                                                      |
| `EncodedBytesPerUnit` | `16`       | Bytes of a value one unit encodes or decodes through an encoding                                              |
| `DecimalCompareCost`  | `64`       | Units of a comparison involving a decimal, which normalizes it                                                |

## API

| Symbol             | Purpose                                                                                                                                                                                                     |
|--------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Comparator`       | Comparator overrides scalar comparison, including built-in strings and numbers. handled=false delegates to the built-in comparator.                                                                         |
| `RegexCacheStats`  | RegexCacheStats holds a point-in-time snapshot of regex pattern cache performance counters. All values are cumulative since the last `ResetRegexCacheStats` call (or since this evaluator was constructed). |
| `Profile`          | Profile validates a semantic contract against the policy; it supplies execution as a `Compiler` or a `Binder`.                                                                                              |
| `Compiler`         | Compiler compiles a filter into a concurrency-safe program.                                                                                                                                                 |
| `Binder`           | Binder binds a profile to the evaluator's policy and `ValueEncoding` and returns its `Compiler`.                                                                                                            |
| `Reader`           | Reader reads the data of one evaluation under the data budget, in stored forms, through the evaluator's path traversals.                                                                                    |
| `Guard`            | Guard counts the operations and depth of one evaluation against the policy's limits.                                                                                                                        |
| `CompareResult`    | CompareResult applies a comparison operator to a three-way comparison.                                                                                                                                      |
| `CompareExact`     | CompareExact orders two numbers by their exact values, as the evaluator does.                                                                                                                               |
| `LiteralPatterns`  | LiteralPatterns compiles the literal `matches()` patterns of a filter through the policy's regex rules and cache.                                                                                           |
| `FieldAccessor`    | FieldAccessor reads a field without compiling a predicate.                                                                                                                                                  |
| `DocumentElements` | DocumentElements recognizes document element slices in an encoding.                                                                                                                                         |
| `StringExtractor`  | StringExtractor recognizes driver string types.                                                                                                                                                             |
| `BinaryExtractor`  | BinaryExtractor recognizes binary values with backend subtypes.                                                                                                                                             |
| `ScalarEquality`   | ScalarEquality compares opaque stored values.                                                                                                                                                               |
| `ScanSizer`        | ScanSizer accounts for bytes scanned when comparing opaque values.                                                                                                                                          |
| `DocumentKeys`     | DocumentKeys accounts for the keys of a document a conversion to the stored form hashes, charging them to a `Budget` and bounding them by `MaxDocumentBytes`.                                               |
| `StoredValue`      | StoredValue returns a data value in the stored form a `Reader` reads, charging conversions to a `Budget`, for code that reads records beside the evaluator (the memory sort translator).                    |

## Related packages

- [filter](../README.md)
- [filter/filtertest](../filtertest/README.md)
- [filter/functions](../functions/README.md)
- [filter/parser](../parser/README.md)
- [filter backends](../README.md#backends)
- [filter guide](../../docs/filter.md)
