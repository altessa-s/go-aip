# ADR 0002 — Per-request visibility cutoff for List

## Context

A tombstone with an expired restore window can remain in storage until physical deletion. Filtering after a page
has been read can yield short or empty pages and inflate the exact total. Putting the current time in the token-bound
restriction invalidates a continuation whenever the clock advances.

## Decision

The handler supplies a trusted AST in `listing.Request.Cutoff`. Compilation normalizes and validates a private copy
under the restriction policy, including the combined complexity limits. Engines receive it through `Query.Cutoff()`.
List and Count intersect it with the token-bound filter before reading rows. Token bindings and their encoding stay stable.
A backend that cannot apply a cutoff independently of its token bindings refuses the request as a server fault.

## Consequences

The caller can refresh the instant on every RPC. With a tightening cutoff and stable order keys, rows that stay
eligible retain their relative positions and every continuation still starts strictly after the last key and identity.
A walk is not a snapshot. Loosening the predicate cannot recover newly eligible rows behind the cursor.
Changes to authorization, tenant or request scope belong in `Restriction` so they invalidate the walk.

SQL and memory support cutoffs. MongoDB binds the entire BSON filter in its keyset reader and refuses a nonempty cutoff.
Use a cutoff-aware engine release before enabling the request field. A service requesting a page and a total should reuse
the same cutoff instant for both calls and use a common snapshot when concurrent writes matter.
