# MongoDB migration fixtures

The four fixtures are copied byte-for-byte from `testdata/` in
`github.com/altessa-s/go-mongox v0.1.0`. Upstream generated them using the Atlas codecs and
keyset at commit `d59f630a`, MongoDB driver v2.10.1 and go-aip pagetoken v0.1.8.

`codecs.bson` pins a complete ordered document. `raw-filters.json` records valid filter
bytes and rejected inputs. The two signed tokens have the same position and differ in the
order of keys within an embedded document. Map encoding resumes the sorted token. Ordered
documents can resume either token when their BSON matches.

`backend-list-token.json` was issued separately by `backend/mongo.ListEngine.List` at
go-aip commit `f5f570c`, using the cached driver v2.10.1 and the same fixed clock and key
as the upstream fixtures. It binds the engine's own schema contract, subject and scope.
The test reissues the first page and checks the BSON seek predicate sent for the next page.
