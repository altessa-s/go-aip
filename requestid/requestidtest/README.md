# requestidtest

```go
import "github.com/altessa-s/go-aip/requestid/requestidtest"
```

The conformance suite of the [`requestid.Store`](../README.md#store-contract) contract: every store adapter runs `Run` to prove it reserves,
completes, releases and expires deduplication entries exactly as the in-memory reference store does. The `MemoryStore` runs it in
`requestid`; the MongoDB, Redis and SQL adapters run it in go-atlas against a reachable server.

## Usage

```go
func TestStoreConformance(t *testing.T) {
	t.Parallel()
	requestidtest.Run(t, func(t *testing.T) requestid.Store {
		return newStore(t) // a fresh collection, table or key prefix per scenario
	})
}
```

The factory is called once per scenario and the scenarios run in parallel, so a shared backend must isolate them. Every time the suite
passes lies on a millisecond (`Base` and offsets of whole milliseconds), so a store may keep `ExpiresAt` at millisecond precision. `Key`
builds keys shaped as the guard's: 64 hexadecimal characters.

## Scenarios

| Scenario            | Checks                                                                                                 |
|---------------------|--------------------------------------------------------------------------------------------------------|
| `ReserveAbsent`     | reserving a missing key stores and returns the pending entry                                           |
| `ReserveLive`       | a live entry is returned, not replaced, up to one millisecond before `ExpiresAt`                       |
| `ReserveExpired`    | an entry is absent from `ExpiresAt` on: a new reservation takes the key                                |
| `CompleteOwned`     | the owner's `Complete` replaces the reservation; the retention then bounds the entry                   |
| `CompleteFailure`   | a completed entry keeps `Failure`                                                                      |
| `CompleteOmitted`   | a completed entry keeps `ResponseOmitted`                                                              |
| `CompleteNotOwned`  | `ErrReservationLost` for a missing key, another token, an expired lease, a takeover, a completed entry |
| `CompletedExpires`  | a completed entry frees its key at the end of its retention                                            |
| `ReleaseOwned`      | the owner's `Release` frees the key                                                                    |
| `ReleaseNotOwned`   | `ErrReservationLost` for a missing key, another token, an expired lease, a completed entry             |
| `KeyIsolation`      | operations on one key leave another untouched                                                          |
| `BinaryValues`      | arbitrary bytes (zero bytes, 64 KiB) round-trip                                                        |
| `NoAliasing`        | the store neither keeps the caller's slices nor returns its own                                        |
| `ConcurrentReserve` | of 32 concurrent reservations of one key exactly one wins and all see the winner                       |
| `CanceledContext`   | every method fails with a canceled context (`errors.Is(err, context.Canceled)`) and changes nothing    |
