# meili

```go
import "github.com/altessa-s/go-aip/backend/meili"
```

Everything go-aip renders for a Meilisearch search, in one package:

| Part                                            | Search parameter       | Main API                                                              |
|-------------------------------------------------|------------------------|-----------------------------------------------------------------------|
| [Filter translator](#filter-translator)         | `filter`               | `FilterTranslator`, `NewFilterTranslator`, `FilterOption`, `Version`  |
| [Order translator](#order-translator)           | `sort`                 | `OrderTranslator`, `NewOrderTranslator`, `OrderOption`                |
| [Projection translator](#projection-translator) | `attributesToRetrieve` | `ProjectionTranslator`, `NewProjectionTranslator`, `ProjectionOption` |

Each constructor takes the resource's shared, client-facing policy (`*filter.Policy`, `*orderby.Policy`, `*projection.Policy`, built once per
resource in API names) and the options of its kind. Every option is prefixed with its kind: `WithFilter…`, `WithOrder…`, `WithProjection…`;
`WithFilterOptions`, `WithOrderOptions` and `WithProjectionOptions` carry the core storage options (field mapping and friends) of the DSL. The
three option layers are described in the [root README](../../README.md#parser--policy--translator). Translators are safe for concurrent use after
construction; injected callbacks must be too.

```go
fp, _ := filter.NewPolicy(filter.WithUntrustedInput(true), filter.WithAllowedFields("status", "title", "createdAt"))
op, _ := orderby.NewPolicy(orderby.WithAllowedFields("createdAt", "title"))
pp, _ := projection.NewPolicy(projection.WithAllowedFields("title", "status"))

ft, err := meili.NewFilterTranslator(fp, meili.WithFilterServerVersion(meili.Version{Major: 1, Minor: 12}))
ot, err := meili.NewOrderTranslator(op)
pt, err := meili.NewProjectionTranslator(pp, meili.WithProjectionOptions(projection.WithRequiredFields("id")))

expr, _ := ft.Translate(ast)    // "(status = 2) AND (title IS EMPTY)"
sort, _ := ot.Translate(ob)     // []string{"createdAt:desc"}
attrs, _ := pt.Translate(spec)  // []string{"id", "title"}
req := &meilisearch.SearchRequest{Filter: expr, Sort: sort, AttributesToRetrieve: attrs}
```

## Filter translator

`FilterTranslator` converts filter AST nodes into Meilisearch filter expressions (strings such as `(status = 2) AND (type IN [1, 2])`). It
covers comparisons, logical operators, list membership, element membership in array fields, null and empty-value tests, `size()` emptiness
tests, `has()`, and — where the server supports them — `contains()` / `startsWith()`. Operations Meilisearch cannot express are rejected with
`filter.ErrUnsupportedOperation`, the same sentinel the other translators use.

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

### Operation mapping

| CEL                                  | Meilisearch                                                            | Needs                                      |
|--------------------------------------|------------------------------------------------------------------------|--------------------------------------------|
| `a == v`, `!=`, `<`, `<=`, `>`, `>=` | `a = v`, `a != v`, `a < v`, …                                          |                                            |
| `a && b`, `a \|\| b`                 | `(a) AND (b)`, `(a) OR (b)`                                            |                                            |
| `!p`                                 | pushed down to the predicates (see [Negation](#negation))              |                                            |
| `a in [v1, v2]`                      | `a IN [v1, v2]`                                                        |                                            |
| `v in tags` (array field)            | `tags = v`                                                             | `filter.WithArrayFields("tags")`           |
| `tags == v` (array field)            | `tags = v` (any element equals)                                        |                                            |
| `a == null` / `a != null`            | `(a IS NULL OR a NOT EXISTS)` / `(a EXISTS AND a IS NOT NULL)`         | 1.2                                        |
| `a == ""` / `a != ""`                | `a IS EMPTY` / `NOT (a IS EMPTY)`                                      | 1.2                                        |
| `a.size() == 0` (also `<= 0`, `< 1`) | `a IS EMPTY`; array field: `(a NOT EXISTS OR a IS NULL OR a IS EMPTY)` | 1.2                                        |
| `a.size() > 0` (also `!= 0`, `>= 1`) | `(a EXISTS AND a IS NOT NULL AND a IS NOT EMPTY)`                      | 1.2                                        |
| `has(a.b)`                           | `a.b EXISTS`                                                           |                                            |
| `a.startsWith("p")`                  | `a STARTS WITH "p"`                                                    | 1.17, or 1.11 + `WithFilterContainsFilter` |
| `a.contains("s")`                    | `a CONTAINS "s"`                                                       | 1.10 + `WithFilterContainsFilter`          |
| bare `active` / `!active`            | `active = true` / `active = false`                                     |                                            |

The "Needs" column names the minimum server version ([`WithFilterServerVersion`](#server-version)); with the default (latest) version only
`contains()` needs an option.

### Server version

Meilisearch added filter operators over several releases. `WithFilterServerVersion(meili.Version{Major: 1, Minor: 12})` declares the release the
filters are sent to, so an operator that release lacks fails translation with `filter.ErrUnsupportedOperation` instead of failing on the
server. The zero `Version` (the default) means the latest release. `WithFilterContainsFilter(true)` declares that the experimental
`containsFilter` feature is enabled on the server (`PATCH /experimental-features {"containsFilter": true}`).

| Operator              | Release                                                                     |
|-----------------------|-----------------------------------------------------------------------------|
| `EXISTS`, `IN`, `NOT` | 1.0 (the minimum; an earlier version fails with `filter.ErrInvalidOptions`) |
| `IS NULL`, `IS EMPTY` | 1.2                                                                         |
| `\\` in a string      | 1.4 (see [String literals](#string-literals))                               |
| `CONTAINS`            | 1.10, experimental `containsFilter` only                                    |
| `STARTS WITH`         | 1.11 with `containsFilter`; 1.17 and later without it                       |

`WithFilterContainsFilter(true)` with a version before 1.10 fails with `filter.ErrInvalidOptions`.

### Null

Meilisearch splits a question CEL's `null` treats as one. `EXISTS` asks whether the attribute is present in the document; `IS NULL` asks whether it is
present **and** null. A filter built from `IS NULL` alone would miss every document that simply omits the attribute — and its negation would match all
of them, which is how a soft-delete filter comes to return the deleted documents.

Both halves are therefore covered explicitly:

| CEL                    | Meilisearch                                                |
|------------------------|------------------------------------------------------------|
| `field == null`        | `(field IS NULL OR field NOT EXISTS)`                      |
| `field != null`        | `(field EXISTS AND field IS NOT NULL)`                     |
| `field in ["a", null]` | `(field IN ["a"] OR (field IS NULL OR field NOT EXISTS))`  |
| `has(field)`           | `field EXISTS` (true for a present null, as the evaluator) |

### Negation

Meilisearch filters are two-valued: a predicate on a null or missing attribute is false, and `NOT (…)` selects every document its operand does
not — null and missing attributes included. The default evaluator and the SQL translators apply SQL's three-valued logic instead: an ordered
comparison, a bare boolean or `size()` of a null or absent value is unknown, `!` keeps it unknown, and an unknown filter selects nothing
([evaluator](../../filter/evaluator/README.md#three-valued-logic)). Wrapping such a predicate in `NOT` would select the documents the evaluator
leaves out, so `!` is never rendered around a clause: it is pushed down through `&&` and `||` (De Morgan) to each predicate, which renders its own
negation.

| CEL                                                                          | Meilisearch                                         | Null or missing `a`                                       |
|------------------------------------------------------------------------------|-----------------------------------------------------|-----------------------------------------------------------|
| `!(a && b)`, `!(a \|\| b)`                                                   | `(!a) OR (!b)`, `(!a) AND (!b)`                     | each side as below                                        |
| `!(a < v)` (`<=`, `>`, `>=`)                                                 | `a >= v` (the opposite ordering)                    | unmatched: the evaluator's unknown                        |
| `!active` (bare boolean)                                                     | `active = false`                                    | unmatched: the evaluator's unknown                        |
| `!(a.size() == 0)` (any emptiness test)                                      | the opposite emptiness test                         | unmatched for a non-array field; an absent array is empty |
| `!(a == v)`, `!(a != v)`                                                     | `a != v`, `a = v` (null and empty-string forms too) | `!(a == v)` matches, as in the evaluator                  |
| `!(a in [...])`, `!has(a)`, `!a.startsWith(s)`, `!p` (application predicate) | `NOT (…)`                                           | matched: the evaluator finds the predicate false          |
| `!(tags < v)` (array field)                                                  | `NOT (tags < v)`                                    | matched: no element of an absent array satisfies it       |

A negated ordering needs no `IS NULL`, so it translates for any server version. The rendering keeps each predicate's evaluator outcome for a null
or absent value, so the whole filter selects the documents the evaluator does; the conformance suite (`filter/filtertest`) pins every case.

### Empty values

Meilisearch does not index the empty string as a filter value: `field = ""` would match nothing and `field != ""` everything. The translator
renders the empty string with `IS EMPTY` instead — `field == ""` → `field IS EMPTY`, and an empty string inside an `in` list becomes an
`IS EMPTY` alternative. `IS EMPTY` also matches `[]` and `{}`; a field declared string never holds those. The check applies to the value after
`WithFilterLiteralEncoder`, so an encoder that stores the empty string as a sentinel keeps the plain equality.

`size()` has no Meilisearch counterpart beyond emptiness: the comparisons that only separate size 0 from the rest render with `IS EMPTY` (see
the mapping table); counting comparisons such as `size() == 3` or `size() > 1` fail with `filter.ErrUnsupportedOperation`. On an array field
an absent or null array is empty, as the evaluator and the MongoDB translator read it; on any other field `size() == 0` holds for an existing
empty value only. A non-number operand fails with `filter.ErrInvalidExpression`.

### Array fields

Equality on an array attribute matches a document when any element equals the value, so `tags == "red"` and `"red" in tags` both render as
`tags = "red"`. The membership form requires the field to be declared with `filter.WithArrayFields`; the literal is checked against the field's
declared element kind. `value in field` on an undeclared field fails with `filter.ErrInvalidExpression` (CEL `in` needs a list), as on the
MongoDB translator. Meilisearch cannot test for a null or empty-string element, so `null in tags`, `"" in tags`, `tags == ""` and those
elements inside an `in` list fail with `filter.ErrUnsupportedOperation`.

### Timestamps

`timestamp(...)` literals are emitted as Unix seconds (Meilisearch filters numeric attributes only); store the corresponding fields as numeric epoch
seconds and add them to the index's `filterableAttributes`. The sub-second part is kept as an exact decimal fraction
(`created < 1672531199.5`): Meilisearch parses a filter number into the same f64 it stores a numeric attribute as, so the boundary stays
where the literal puts it and a record stored at `1672531199` matches `< 23:59:59.5`. Beyond about microsecond precision the f64 of an
epoch value rounds, identically for the literal and a stored value. `bytes` literals are emitted as strings.

### Case sensitivity

Meilisearch normalizes filter strings: `=`, `IN`, `CONTAINS` and `STARTS WITH` compare case-insensitively, unlike CEL. Only fields listed in the
index's `filterableAttributes` can be filtered.

### Attribute names

Meilisearch takes a filter as a string, and the translator writes each attribute unquoted. The filter grammar reads an unquoted attribute as
one word of letters, digits, `_`, `-` and `.`, so a name with any other character — white space, a quote, a backtick, an operator, a parenthesis,
a control character — would end the word early and turn the rest of the name into filter syntax. Every storage name (after the field mapping)
is therefore checked before it is written, independently of the policy, which may let a client-chosen suffix through (a prefix allow-list such
as `labels.*`, or a prefix mapping):

- the allowed charset is one or more dot-separated, non-empty segments of ASCII letters, digits, `_` and `-` (`a`, `labels.team-a`, `_meta.v2`);
- the words the grammar reserves are refused: `AND`, `OR`, `IN`, `NOT`, `TO`, `EXISTS`, `IS`, `NULL`, `EMPTY`, `CONTAINS`, `STARTS`, `WITH`
  (case-sensitive, as in Meilisearch), `_geo`, `_geoRadius`, `_geoBoundingBox`, `_geoPolygon`, `_foreign`, and any name starting with
  `_geoPoint` or `_geoDistance`.

Any other name fails with `filter.ErrInvalidExpression`, whatever the policy allows. The order and projection translators apply the same charset
(without the reserved words, which only the filter grammar has) and fail with `orderby.ErrInvalidFieldPath` / `projection.ErrInvalidFieldPath`.

### String literals

A string value is written between double quotes. From Meilisearch 1.4 the filter parser unescapes `\"` and `\\`, so the translator escapes
`\` as `\\` and `"` as `\"` and the server reads back the exact value. Meilisearch 1.0 to 1.3 unescape only `\"`: with
`WithFilterServerVersion` set to one of them a backslash is written as it is, and a value with an odd run of backslashes before a quote or at
its end — which those releases cannot express — fails with `filter.ErrUnsupportedOperation`.

### Bare identifiers

A bare identifier used as a condition becomes a boolean test — `active` translates to `active = true` — at the root of an expression and on either
side of `AND` / `OR` alike.

### Literal operands

Comparison values, `in` list elements, `size()` operands and string-function arguments must be literals. Dynamic operands such as `a == b`,
`a in [b]`, `a in tags`, or `a.contains(b)` return `filter.ErrUnsupportedOperation`; a field reference is never converted into a string value.

### Not supported

| Expression                                      | Error                                                                                                   | Reason                                      |
|-------------------------------------------------|---------------------------------------------------------------------------------------------------------|---------------------------------------------|
| `endsWith()`                                    | `filter.ErrUnsupportedOperation`                                                                        | no suffix operator                          |
| `matches()`                                     | `filter.ErrUnsupportedOperation`                                                                        | no regular expressions                      |
| `substring()`                                   | `filter.ErrUnsupportedOperation`                                                                        | no string functions in operand position     |
| counting `size()` comparisons                   | `filter.ErrUnsupportedOperation`                                                                        | filters cannot count elements or characters |
| field-to-field operands                         | `filter.ErrUnsupportedOperation`                                                                        | filters compare attributes with constants   |
| `contains()` without `WithFilterContainsFilter` | `filter.ErrUnsupportedOperation`                                                                        | experimental server feature                 |
| operators newer than `WithFilterServerVersion`  | `filter.ErrUnsupportedOperation`                                                                        | see [Server version](#server-version)       |
| `filter.FieldKindDecimal` fields                | `filter.ErrUnsupportedType` (construction), unless `WithFilterLiteralEncoder` returns a printable value | numbers are stored as doubles               |

### Errors

| Error                                                                                                                                                        | When                                                                                                                                                                                                                                   |
|--------------------------------------------------------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `filter.ErrPolicyRequired`                                                                                                                                   | nil policy                                                                                                                                                                                                                             |
| `filter.ErrInvalidOptions`                                                                                                                                   | `WithFilterServerVersion` before 1.0, or `WithFilterContainsFilter` with a version before 1.10                                                                                                                                         |
| `filter.ErrUnsupportedOperation`                                                                                                                             | the operation or operand shape has no counterpart on the configured server (table above), or a string before 1.4 cannot express                                                                                                        |
| `filter.ErrUnsupportedType`                                                                                                                                  | a literal (after encoding) Meilisearch cannot print; decimal fields without an encoder                                                                                                                                                 |
| `filter.ErrInvalidExpression`                                                                                                                                | malformed expression: missing field, non-string string-function argument, non-number `size()` operand, `value in field` on a field not declared an array; a storage name outside the [attribute charset](#attribute-names) or reserved |
| `filter.ErrFieldNotAllowed`, `filter.ErrFieldTypeMismatch`, `filter.ErrEnumValueNotAllowed`, `filter.ErrMaxDepthExceeded`, `filter.ErrMaxOperationsExceeded` | shared policy checks                                                                                                                                                                                                                   |

### Configuration and compatibility

| API           | Purpose                                                       |
|---------------|---------------------------------------------------------------|
| `ValidateAST` | Check the configured renderer without contacting the database |

`NewFilterTranslator` accepts the generated `FilterOption` values. Shared policies and callbacks must be safe for concurrent use. The translator
exposes no `filter.Visitor` methods; call `FilterTranslator.Translate`. `ValidateAST` checks actual expression shapes and values, including
field-to-field operands, rather than promising support for an operator regardless of its operands. Unsupported expressions return filter sentinel
errors. Backend-native differences remain explicit.

Literal encoders return values, never executable query text. They can choose timestamp units or stored enum representations.

Field authorization runs before any shape check: a denied field anywhere in the expression, a function argument included
(`name.contains(secret)`), fails with `filter.ErrFieldNotAllowed`, never with `filter.ErrUnsupportedOperation`.

`WithFilterPredicateRenderer` installs a trusted, concurrent-safe callback for application predicates. Field authorization and limits run first.
Return `handled=false` to delegate to built-ins; encode every client value safely before returning query text.

### Filter options

| Option                        | Default | Behavior                                                                                                          |
|-------------------------------|---------|-------------------------------------------------------------------------------------------------------------------|
| `WithFilterOptions`           | nil     | Binds the shared policy to the storage names of this backend (`filter.WithFieldMapping`).                         |
| `WithFilterServerVersion`     | latest  | The Meilisearch release the filters target; operators it lacks fail translation.                                  |
| `WithFilterContainsFilter`    | false   | The server's experimental `containsFilter` feature is on: enables `contains()` (and `startsWith()` on 1.11–1.16). |
| `WithFilterLiteralEncoder`    | nil     | Converts values before backend quoting. It must be concurrency safe.                                              |
| `WithFilterPredicateRenderer` | nil     | Supplies a trusted predicate override. Return handled=false to use built-ins.                                     |
| `WithFilterCollector`         | nil     | Records `filter_meili_translate_duration_seconds`; nil disables collection.                                       |

### Filter configuration example

```go
policy, err := filter.NewPolicy(
    filter.WithUntrustedInput(true),
    filter.WithAllowedFields("status", "type", "organizationIds"),
) // build once per resource
if err != nil { return err }

trans, err := meili.NewFilterTranslator(policy,
    meili.WithFilterServerVersion(meili.Version{Major: 1, Minor: 12}),
    meili.WithFilterContainsFilter(true), // containsFilter enabled on the server
    meili.WithFilterOptions(filter.WithFieldMapping(map[string]string{
        "organizationIds": "organization_id",
    })))
```

## Order translator

`OrderTranslator` translates an `orderby.Spec` into the Meilisearch `sort` argument: a `[]string` of `"field:asc"` / `"field:desc"` entries
in the order they appear in the source.

`NewOrderTranslator(policy, opts...)` binds the resource's shared `orderby.Policy` (allow-list, field access, denied API fields, default
order) to the storage options passed through `WithOrderOptions` (mapping, KeyResolver, denied storage fields, tie-breakers); a nil policy
returns `orderby.ErrPolicyRequired`. Translation validates every order with the policy before mapping it, so it never renders a key the
policy refuses.

Meilisearch places documents whose sort attribute is null or missing after every other document in both directions: an explicit
`nulls last` is accepted (it renders as the plain key), `nulls first` and every collation return `orderby.ErrUnsupportedOrdering`.

```go
parser, _ := orderby.NewParser()
ob, _ := parser.Parse(ctx, "createdAt desc, name")

policy, err := orderby.NewPolicy(orderby.WithAllowedFields("createdAt", "name"))
if err != nil {
    return err
}

trans, err := meili.NewOrderTranslator(policy)
if err != nil {
    return err
}
sort, _ := trans.Translate(ob)
// sort == []string{"createdAt:desc", "name:asc"}
```

An empty `Spec` translates to a `nil` slice when the policy has no default order and the translator no tie-breakers — callers can pass it
straight through.

### Order options

| Option               | Default | Behavior                                                                                                                            |
|----------------------|---------|-------------------------------------------------------------------------------------------------------------------------------------|
| `WithOrderOptions`   | nil     | Storage options: `orderby.WithFieldMapping`, `orderby.WithKeyResolver`, `orderby.WithDeniedStorageFields`, `orderby.WithTiebreaker` |
| `WithOrderCollector` | nil     | Records `orderby_meili_translate_duration_seconds`; nil disables collection                                                         |

### Order errors

| Error                            | When                                                                                                       |
|----------------------------------|------------------------------------------------------------------------------------------------------------|
| `orderby.ErrPolicyRequired`      | nil policy (`NewOrderTranslator`)                                                                          |
| `orderby.ErrFieldNotAllowed`     | a key the policy refuses or that resolves to a denied storage path; a tie-breaker on a denied storage path |
| `orderby.ErrDuplicateKey`        | two keys resolve to the same storage name                                                                  |
| `orderby.ErrUnsupportedOrdering` | `nulls first` or a collation                                                                               |
| `orderby.ErrInvalidFieldPath`    | a storage name outside the [attribute charset](#attribute-names)                                           |
| `orderby.ErrAllowlistRequired`   | raised earlier, by `orderby.NewPolicy`                                                                     |

## Projection translator

`ProjectionTranslator` translates a [`projection.Spec`](../../projection/README.md) into the `attributesToRetrieve` parameter of a
Meilisearch search.

`NewProjectionTranslator(policy, opts...)` binds the resource's shared `projection.Policy` to storage options passed with
`WithProjectionOptions` (mapping, denied and required storage fields) and renders the selection of every view it serves once, so a
misconfigured view fails at construction. A nil policy returns `projection.ErrPolicyRequired`. `Translate(spec)` takes a read mask alone;
`TranslateView(spec, view)` applies the AIP-157 rule of `projection.Policy.Select`.

| Selection                                               | `attributesToRetrieve`                                |
|---------------------------------------------------------|-------------------------------------------------------|
| Mask with paths, a view, or FULL under an allow-list    | `["attr", …]` plus required fields, lexical order     |
| `ModeNone` with required fields                         | the required fields                                   |
| `ModeNone`, no required fields                          | `projection.ErrUnsupportedSelection`                  |
| Omitted mask, `*` or FULL, no allow-list, no denies     | `nil` — omit the parameter (Meilisearch uses `["*"]`) |
| Omitted mask, `*` or FULL, no allow-list, denied fields | construction error (no exclusion form)                |

Dotted names address nested attributes (`author.name`); a path an ancestor covers is dropped (`author`, `author.name` → `author`). The
primary key is not added implicitly: keep it with `projection.WithRequiredFields`.

`ModeNone` without required fields is rejected rather than rendered as `[]`: Meilisearch always returns one hit object per match, and the
common client libraries serialize `attributesToRetrieve` with `omitempty`, so an empty list would silently return every attribute.

```go
policy, err := projection.NewPolicy(
	projection.WithUntrustedInput(true),
	projection.WithAllowedFields("title", "author.*"),
)
trans, err := meili.NewProjectionTranslator(policy,
	meili.WithProjectionOptions(projection.WithRequiredFields("id")),
)
attrs, err := trans.Translate(spec) // "title" → ["id", "title"]
res, err := index.Search(query, &meilisearch.SearchRequest{AttributesToRetrieve: attrs})
```

### Projection options

| Option                    | Default | Behavior                                                                                            |
|---------------------------|---------|-----------------------------------------------------------------------------------------------------|
| `WithProjectionOptions`   | nil     | Storage options: `projection.TranslatorOption` values (mapping, denied and required storage fields) |
| `WithProjectionCollector` | nil     | Records `projection_meili_translate_duration_seconds`; nil disables collection                      |

### Projection errors

| Error                                 | When                                                                                              |
|---------------------------------------|---------------------------------------------------------------------------------------------------|
| `projection.ErrPolicyRequired`        | Nil policy                                                                                        |
| `projection.ErrAllFieldsUnresolvable` | Every authorized field would exclude denied fields; list the allowed fields                       |
| `projection.ErrUnsupportedSelection`  | `ModeNone` without required fields                                                                |
| `projection.ErrInvalidFieldPath`      | Grammar error, or a storage name outside the [attribute charset](#attribute-names) (`*` included) |
| `projection.ErrFieldNotAllowed`       | A path outside the policy, or reaching a denied storage path                                      |
| `projection.ErrUnsupportedPath`       | Wildcards, map keys or indexes (AIP-161)                                                          |
| `projection.ErrInvalidView`           | A view the policy does not serve                                                                  |

## API

| Symbol                                                 | Purpose                                                                                                 |
|--------------------------------------------------------|---------------------------------------------------------------------------------------------------------|
| `NewFilterTranslator(policy, ...FilterOption)`         | Filter translator over the shared policy; nil policy fails with `filter.ErrPolicyRequired`              |
| `FilterTranslator.Translate(node)`                     | Renders the Meilisearch filter expression                                                               |
| `FilterTranslator.ValidateAST(node)`                   | Renders and discards, to check compatibility without contacting the server                              |
| `FilterOption`                                         | Functional option of `NewFilterTranslator`                                                              |
| `Version`                                              | A Meilisearch release (major.minor) for `WithFilterServerVersion`; the zero value is the latest release |
| `NewOrderTranslator(policy, ...OrderOption)`           | Sort translator over the shared policy; nil policy fails with `orderby.ErrPolicyRequired`               |
| `OrderTranslator.Translate(spec)`                      | Returns the `sort` argument; `nil` for an empty order                                                   |
| `OrderOption`                                          | Functional option of `NewOrderTranslator`                                                               |
| `NewProjectionTranslator(policy, ...ProjectionOption)` | Projection translator; rejects policies that need an exclusion                                          |
| `ProjectionTranslator.Translate(spec)`                 | Returns the attribute list; `nil` for every attribute                                                   |
| `ProjectionTranslator.TranslateView(spec, view)`       | Like `Translate`, with the AIP-157 view deciding when the read mask is omitted                          |
| `ProjectionOption`                                     | Functional option of `NewProjectionTranslator`                                                          |

## Related packages

- [filter](../../filter/README.md), [filter/filtertest](../../filter/filtertest/README.md)
- [orderby](../../orderby/README.md), [orderby/orderbytest](../../orderby/orderbytest/README.md)
- [projection](../../projection/README.md), [projection/projectiontest](../../projection/projectiontest/README.md)
- [backend/sqlbase](../sqlbase/README.md)
- [filter guide](../../docs/filter.md), [orderby guide](../../docs/orderby.md), [projection guide](../../docs/projection.md)
