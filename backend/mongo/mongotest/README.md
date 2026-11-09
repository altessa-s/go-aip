# mongotest

```go
import "github.com/altessa-s/go-aip/backend/mongo/mongotest"
```

Test fixtures of the [MongoDB backend](../), shared by its unit tests and by the live MongoDB suite of the test-only module
[`integration/docker`](../../../integration/docker/), so the expectation tables exist once, and the explain-plan checks a store built on the
list engine runs against its own collection.

| Symbol                                      | Returns                                                                                                                                         |
|---------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------|
| `FilterConformanceBackend()`                | `filtertest.Backend` with `New` (`mongo.NewFilterTranslator`, `TranslateD` rendered as relaxed Extended JSON) and `Expect` (every case outcome) |
| `AssertIndexedSorts(tb, coll, list, scope)` | Fails unless every order the list accepts (each pattern and its reversal) runs, restricted to `scope`, as a scan of its index without a `SORT`  |
| `Explain(tb, coll, filter, sort)`           | The winning plan (`queryPlanner`) of one find limited to a default page                                                                         |
| `PlanOf(winning)`, `Plan`, `Plan.Indexed`   | The stage names and index names of a winning plan, and whether it scans an index without a blocking sort                                        |
| `Sorter`                                    | What `AssertIndexedSorts` reads from a list; a `*mongo.ListEngine` is one                                                                       |

The unit test `TestFilterConformance` runs `FilterConformanceBackend()` as is. The live run adds a `Match` executing every query on a MongoDB
server over `filtertest.ConformanceRecords` and a `Reference` (the evaluator with the MongoDB profile and value encoding), so every case also
checks that MongoDB selects the documents the profile selects.

```go
filtertest.RunConformance(t, mongotest.FilterConformanceBackend())

require.NoError(t, list.EnsureIndexes(ctx))
mongotest.AssertIndexedSorts(t, coll, list, bson.D{{Key: "owner", Value: "owner-1"}})
```

Call `AssertIndexedSorts` after `EnsureIndexes` with a few documents in the collection, so the planner's trial is representative; `scope` is
the store's base filter with its scope keys pinned by equality. It complements, and does not replace, `listingtest.Run`.
