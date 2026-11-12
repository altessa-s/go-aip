# memory

```go
import "github.com/altessa-s/go-aip/backend/memory"
```

The in-memory backend: orders, projects and pages records held in memory with the semantics of every other backend, for small catalogs,
code-defined collections, caches and tests that must behave exactly like a database. In-memory **filtering** is the
[filter evaluator](../../filter/evaluator); this package builds on it.

| Kind       | Constructor                                                      | Output                                        | Options                                |
|------------|------------------------------------------------------------------|-----------------------------------------------|----------------------------------------|
| Order      | `NewOrderTranslator(policy, ...OrderOption)`                     | `Comparator` over `map[string]any` records    | `WithOrder…` (`OrderOption`)           |
| Projection | `NewProjectionTranslator(policy, ...ProjectionOption)`           | `*Projection` for `map[string]any` and protos | `WithProjection…` (`ProjectionOption`) |
| List       | `NewListEngine(signer, schema, id, fields, ...ListEngineOption)` | `*ListEngine[T]` paging a slice               | `WithList…` (`ListEngineOption`)       |

Every option of a kind carries the kind's prefix, so the three kinds share one package without ambiguity.

## Order

`NewOrderTranslator(policy, opts...)` binds the resource's shared `orderby.Policy` (allow-list, field access, denied API fields, default order)
to the storage options passed through `WithOrderOptions` (mapping, KeyResolver, denied storage fields, tie-breakers); a nil policy returns
`orderby.ErrPolicyRequired`. `OrderTranslator.Translate` validates and resolves the spec exactly like every other orderby translator and
returns a `Comparator` (`func(a, b map[string]any) int`) for `slices.SortFunc` / `slices.SortStableFunc`. An empty resolved order returns a
`nil` Comparator, matching the `nil` output of the MongoDB and Meilisearch translators. Translators and comparators are safe for concurrent
use; a comparator never modifies the records.

### Reading values

Each storage key is a dotted path through nested documents (`map[string]any`, any map keyed by strings, or a pointer to one). A missing
segment, a null, a nil pointer, a nil slice or map, or a path through a non-document (a scalar, an array) reads as null. Values are read in
their stored form with the filter evaluator's own reading, `evaluator.StoredValue`: pointers are followed (at most 32), named types read
as their base kind, typed maps keyed by strings as documents, an `optional.Optional` as its value or null, and with `WithOrderValueEncoding`
a driver's own types and structs read as the `evaluator.ValueEncoding` stores them, so the comparator and an evaluator configured with the
same encoding read a record identically. Arrays, documents and byte strings are unsortable.

### Value order

| Class          | Order                                                                                                            |
|----------------|------------------------------------------------------------------------------------------------------------------|
| null / missing | Before every value by default (first ascending, last descending — MongoDB order); `nulls first/last` override it |
| numbers        | Exact across integer, unsigned, float and `decimal.Decimal` values (`1<<53` < `float64(1<<53)+2`); NaN first     |
| strings        | Byte-wise (binary collation, case-sensitive like the evaluator)                                                  |
| bools          | `false` before `true`                                                                                            |
| timestamps     | By instant, at full nanosecond precision, whatever the location                                                  |
| unsortable     | Documents, arrays, bytes and structs no encoding reads: after every scalar, equal to each other                  |

The scalar classes and their order are those of the list engine (`pagetoken.SortValue`: number, string, bool, time). A descending key
reverses the value order; null placement is independent of direction.

| Attribute              | Support                                                                         |
|------------------------|---------------------------------------------------------------------------------|
| multiple keys          | Yes, compared in order                                                          |
| `nulls first` / `last` | Yes, both directions                                                            |
| collation              | `orderby.ErrUnsupportedOrdering` (no collation tables without a new dependency) |
| tie-breakers           | Yes (`orderby.WithTiebreaker` through `WithOrderOptions`)                       |

```go
tr, err := memory.NewOrderTranslator(policy, memory.WithOrderOptions(orderby.WithTiebreaker(orderby.Key{Name: "id"})))
if err != nil {
	return err
}

compare, err := tr.Translate(spec)
if err != nil {
	return err
}

if compare != nil {
	slices.SortStableFunc(records, compare)
}
```

| Option                   | Default                | Behavior                                                                              |
|--------------------------|------------------------|---------------------------------------------------------------------------------------|
| `WithOrderOptions`       | nil                    | Storage options: mapping, KeyResolver, denied storage fields, tie-breakers            |
| `WithOrderCollector`     | nil                    | Records the translation duration as `orderby_memory_translate_duration_seconds`       |
| `WithOrderValueEncoding` | nil                    | Read driver types and structs through an `evaluator.ValueEncoding`                    |
| `WithOrderMaxDataBudget` | `evaluator.DefaultMaxDataBudget` | Work the encoding may spend reading one sort value; beyond it the value is unsortable |

`WithOrderMaxDataBudget` is charged for every conversion `evaluator.StoredValue` makes (a typed slice or map copied, a document's keys);
non-positive values are ignored. Pointer chains are followed at most 32 hops; a deeper or cyclic value, or one the budget cannot read, is
unsortable. Reading a path through plain documents costs no allocation.

## Projection

`NewProjectionTranslator(policy, opts...)` binds the resource's shared `projection.Policy` to storage options passed with
`WithProjectionOptions` (mapping, denied and required storage fields); a nil policy returns `projection.ErrPolicyRequired`.
`ProjectionTranslator.Translate(spec)` and `ProjectionTranslator.TranslateView(spec, view)` resolve the request with the policy and return an
immutable `*Projection`, applied with the rules of the [MongoDB backend](../mongo), so an in-memory store (for example the list engine
serving `Request.ReadMask`) returns what a MongoDB collection would. Translators and projections support concurrent use. Every selection can
be applied: inclusion, exclusion (every field but the denied ones, for a policy without an allow-list) and `ModeNone`.

### Applying to maps

`Projection.Apply(doc)` returns a new map and never modifies `doc`; unselected values are not copied, so the result shares the selected
subtrees.

| Selection   | Result                                                                                        |
|-------------|-----------------------------------------------------------------------------------------------|
| Inclusion   | Only the selected paths, required fields included                                             |
| Exclusion   | Everything but the denied paths; the maps and lists on the way are copied, the rest is shared |
| `ModeNone`  | An empty map (required fields, when configured, make it an inclusion)                         |
| Every field | A shallow copy of `doc`                                                                       |

Dotted paths follow MongoDB:

* into a nested map they select that key; an inclusion keeps the map even when the key is missing (`{}`);
* into a list (`[]any` or `[]map[string]any`) they select per element: an inclusion keeps the map elements, each with only the selected keys,
  walks nested lists and drops the other elements; an exclusion removes the key from every map element and keeps the others;
* into any other value an inclusion drops the value and an exclusion leaves it as it is;
* a path an ancestor covers adds nothing (`address`, `address.city` → `address`).

### Applying to proto messages

`Projection.ApplyProto(msg)` changes `msg` in place through [`fieldmask`](../../fieldmask): `FieldMask.Filter` for an inclusion,
`FieldMask.Prune` for an exclusion, `FieldMask.Select` with an empty mask for `ModeNone` (clears the message); every field leaves it as it
is. Storage names must be proto field paths of the message (map them with `projection.WithFieldMapping` when the API names differ); a path
the message does not declare fails with an error matching `projection.ErrInvalidFieldPath` (and the `*fieldmask.ValidationError`) and leaves
`msg` untouched. Repeated message fields select per element, as for maps.

```go
policy, err := projection.NewPolicy(
	projection.WithUntrustedInput(true),
	projection.WithAllowedFields("name", "items.*"),
)
trans, err := memory.NewProjectionTranslator(policy,
	memory.WithProjectionOptions(projection.WithRequiredFields("id")),
)
proj, err := trans.Translate(spec) // "items.sku"
out := proj.Apply(doc)             // {id: 1, items: [{sku: "a"}, {sku: "b"}]}
err = proj.ApplyProto(msg)         // msg keeps id and items[].sku
```

| Option                    | Default | Behavior                                                                                            |
|---------------------------|---------|-----------------------------------------------------------------------------------------------------|
| `WithProjectionOptions`   | nil     | Storage options: `projection.TranslatorOption` values (mapping, denied and required storage fields) |
| `WithProjectionCollector` | nil     | Records `projection_memory_translate_duration_seconds`; nil disables collection                     |

## List engine

The in-memory engine of [`listing`](../../listing/README.md): pages a slice held in memory with the semantics of every listing engine (the
same request validation, the same orders with the id tie-breaker, keyset page tokens). It is run against the shared engine contract suite
[`listing/listingtest`](../../listing/listingtest/README.md). For small collections only — every page scans and filters the
whole slice and keeps the page's rows (plus one) in a bounded heap: O(n log k) comparisons for page size k.

`NewListEngine(signer, schema, id, fields, opts...)` accepts a typed scalar identity callback and public stored-field values; a nil schema,
id or fields fails with `listing.ErrInvalidSchema`. `ListEngine.List(ctx, subject, request, items)` filters the supplied slice on every page
and orders the matches after the page token's position in a bounded heap; `ListEngine.Validate(ctx, request)` returns the request errors of
`List` without the items, so a caller can reject a bad request before loading them (the page token is checked by `List`). A nil signer
serves one page and rejects a page token.

`MustNewListEngine` takes the same arguments for a configuration fixed in code (a package-level `var`, like `listing.MustSchema`): it returns
the engine `NewListEngine` builds and panics on a refused one with an error that starts `memory.MustNewListEngine: ` and wraps the
`NewListEngine` error.

```go
list, err := memory.NewListEngine(codec, widgetSchema, func(w Widget) string { return w.ID }, widgetValues)
if err := list.Validate(ctx, req); err != nil { return err } // optional: reject a bad request before loading the items
page, err := list.List(ctx, method, req, items)              // items already restricted to the parent
```

### Semantics

| Concern        | Behavior                                                                                                                        |
|----------------|---------------------------------------------------------------------------------------------------------------------------------|
| Filter         | Proto3 contract: optional scalar absence/null is zero; array absence/null is empty; array leaves are existential and two-valued |
| Paging         | `pagetoken.SlicePager` over the sorted matches, cursor = `pagetoken.Position` (typed sort values in MongoDB's order + id)       |
| Token bindings | subject, canonical orderBy, canonical filter (`filter.Canonical`, so spelling does not matter), parent, schema contract ID      |

The default evaluation profile comes from `listing/evaluation` and implements required, Optional, Array and Presence semantics.
`WithListProfile` replaces it; custom profiles must agree with the schema's contract ID. Values are read through the memory evaluator and
`pagetoken.NewSortValue`: pointers are dereferenced, named types use their underlying string/int/float/bool, slices are lists, and times
have the cursor precision. Optional scalar fields use their kind's zero value when absent or null; an explicit zero remains zero. Pass
`memory.WithListValueEncoding(mongo.ValueEncoding())` (the MongoDB value encoding of [`backend/mongo`](../mongo/README.md#value-encoding)) to read
a struct, a `bson.D`, or a value with a custom BSON encoding the way the MongoDB driver stores it. Numbers of one sort field order by value
across int, float and decimal; a sort value of another class, or a missing one, is an internal error.

Fields declared with `Presence` preserve null and follow the default evaluator's null semantics, as the SQL engines do: null differs from
every value, is in no list and contains no string, so `rating != 1` and `!(rating in [1, 2])` select a null rating; an ordered comparison
with null is unknown, also under negation, and a filter selects an item only when the full expression is true. `has()` and `size()` are
two-valued.

### Cursor configuration

`NewListEngine[T, ID]` accepts an identity callback returning any supported scalar. The position codec (`WithListPositionCodec`) drives
sorting, SlicePager seeks and token serialization together; the default scalar codec and `listing.TimestampPrecision` preserve typed
identities, and timestamp sort values are truncated to the configured resolution (`WithListTimestampPrecision`, positive). Changing the
precision, codec semantics or the tie-breaker invalidates existing tokens.

`WithListTiebreakField` identifies a declared required sortable scalar schema field as the identity instead of the callback's value. It may
appear only as the last sort key and is stored only in `Position.ID`. Without it, every schema field (including `id`) is an ordinary sort
key and the callback supplies the final identity. Empty orders sort by ascending identity. Null placement and collation in a schema order
fail with `listing.ErrInvalidSchema`.

### Request accounting

`WithListMaxWork` shares one request budget across scanned records, predicate visits, field traversal, array comparisons and sorting. It is
on by default (`DefaultMaxWork`) and requires a profile implementing `RequestProfile` (the default one does); a custom profile that does not
implement it needs an explicit `WithListMaxWork(0)`, which lifts the limit. `WithListMaxScanned` and `WithListMaxMaterialized` independently
bound input items and retained rows (a page retains at most its size plus one); zero leaves them off. Cancellation is checked during
scanning and sorting either way.

The page token is verified against all of its bindings (subject, order, filter, parent, contract, position codec, timestamp precision,
tie-breaker) before any item is read, so a forged, tampered or foreign token costs no scan or sort. Errors about a stored item (a filter
it cannot be evaluated against, a sort value it lacks) name the field, never the item's id or a stored value. `WithListMaxDataBudget`
independently limits each evaluation and lookup. A new request gets a fresh budget, including when it reuses the same `ListEngine`
concurrently. Custom profiles must charge their nested work and stop when `Charge` returns false; a bound profile belongs
to one request. The caller's slice is never modified.

| Option                       | Default                           | Behavior                                                                        |
|------------------------------|-----------------------------------|---------------------------------------------------------------------------------|
| `WithListMaxDataBudget`      | `evaluator.DefaultMaxDataBudget`            | Bound work reading, converting and comparing data in one evaluation             |
| `WithListMaxMaterialized`    | 0                                 | Rows held in the page heap (at most page size + 1); zero is unlimited           |
| `WithListMaxScanned`         | 0                                 | Input items per request; zero is unlimited                                      |
| `WithListMaxWork`            | `DefaultMaxWork`                  | Scans, field reads, predicate visits and sorting per request; zero is unlimited |
| `WithListPositionCodec`      | `pagetoken.ScalarPositionCodec{}` | Replace position normalization, comparison and wire encoding together           |
| `WithListProfile`            | the `listing/evaluation` profile  | Replace portable evaluation; it must agree with the schema contract             |
| `WithListTiebreakField`      | `""`                              | Use a public field path as the identity instead of the callback                 |
| `WithListTimestampPrecision` | `listing.TimestampPrecision`       | Cursor timestamp resolution; storage must use this resolution                   |
| `WithListValueEncoding`      | nil                               | Adapt stored representations for field traversal and native/MongoDB evaluation  |

## API

| Symbol                                            | Purpose                                                                                        |
|---------------------------------------------------|------------------------------------------------------------------------------------------------|
| `Comparator`                                      | `func(a, b map[string]any) int`, a strict weak order over records                              |
| `OrderTranslator`, `NewOrderTranslator`           | Binds the shared `orderby.Policy`; `Translate` builds the comparator (`nil` for no order)      |
| `OrderOption`                                     | Option of `NewOrderTranslator`                                                                 |
| `ProjectionTranslator`, `NewProjectionTranslator` | Binds the shared `projection.Policy`; `Translate` / `TranslateView` return a `*Projection`     |
| `ProjectionOption`                                | Option of `NewProjectionTranslator`                                                            |
| `Projection`                                      | `Apply(doc)`, `ApplyProto(msg)`, `Selection()`                                                 |
| `ListEngine[T]`, `NewListEngine`                  | `Validate(ctx, req)`, `List(ctx, subject, req, items)`                                         |
| `MustNewListEngine`                               | Panicking `NewListEngine` for package-level declarations fixed in code                         |
| `ListEngineOption`                                | Option of `NewListEngine`                                                                      |
| `RequestProfile`                                  | `BindBudget` binds a custom profile to request accounting without mutating it                  |
| `ErrWorkBudget`                                   | A list request exceeded its scan, materialization or work limit; wraps `listing.ErrWorkBudget` |

## Defaults and limits

| Constant                    | Value                                                                                        |
|-----------------------------|----------------------------------------------------------------------------------------------|
| `DefaultMaxWork`            | `1 << 24` work units per list request (`WithListMaxWork(0)` lifts it)                        |

The request work limit is on by default; the scan and materialization limits are off (0); the data budgets are always on.

## Errors

| Error                            | Kind       | When                                                                                        |
|----------------------------------|------------|---------------------------------------------------------------------------------------------|
| `orderby.ErrPolicyRequired`      | Order      | Nil policy                                                                                  |
| `orderby.ErrFieldNotAllowed`     | Order      | A key the policy refuses, or one resolving to a denied storage path                         |
| `orderby.ErrDuplicateKey`        | Order      | Two keys resolve to the same storage name                                                   |
| `orderby.ErrUnsupportedOrdering` | Order      | A collation or an unknown null placement                                                    |
| `projection.ErrPolicyRequired`   | Projection | Nil policy                                                                                  |
| `projection.ErrFieldNotAllowed`  | Projection | A path outside the policy, or reaching a denied storage path                                |
| `projection.ErrInvalidFieldPath` | Projection | Grammar error; from `ApplyProto`, a storage path the message lacks                          |
| `projection.ErrUnsupportedPath`  | Projection | Wildcards, map keys or indexes (AIP-161)                                                    |
| `projection.ErrInvalidView`      | Projection | A view the policy does not serve                                                            |
| `listing.ErrInvalidSchema`       | List       | Construction: nil parts, a bad tie-breaker, unsupported schema orders, bad limits           |
| `ErrWorkBudget`                  | List       | A request limit (`WithListMaxWork`, `WithListMaxScanned`, `WithListMaxMaterialized`) is hit |
| `filter.ErrDataLimitExceeded`    | List       | An evaluator data limit is hit                                                              |

A comparator never fails: values it cannot read are unsortable rather than an error. Every invalid list request field is an InvalidArgument
`core/errors.FieldError` (`listing.Schema.Compile`, `pageToken`).

## Related packages

- [filter/evaluator](../../filter/evaluator/README.md) — in-memory filtering
- [backend/mongo](../mongo/README.md) — the MongoDB rules this backend mirrors
- [backend/sqlbase](../sqlbase/README.md) — the SQL listing engine
- [listing](../../listing/README.md) and [listing/listingtest](../../listing/listingtest/README.md)
- [orderby](../../orderby/README.md), [projection](../../projection/README.md), [fieldmask](../../fieldmask/README.md)
- [listing guide](../../docs/listing.md), [orderby guide](../../docs/orderby.md), [projection guide](../../docs/projection.md)
