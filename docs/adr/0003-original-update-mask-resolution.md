# ADR 0003 — Resolve the original update mask before checking field values

## Context

Update interceptors need the same selection to check field behaviors and scope downstream validators.
Write planning can return a behavior violation before publishing a mask, or collapse a whole message
to a write path. Validators need the declared leaves, including a selected empty field below an
absent message, and concrete list positions for diagnostics.

## Decision

`update.Spec.ResolveMask` resolves the sent mask and records each canonical validation path's
original index or synthesized origin. An omitted mask selects populated mutable leaves,
an explicit empty mask selects nothing and `*` expands by descriptor. A selected singular
message or a message-valued map key or wildcard expands through the value descriptor, including
when the request omits a named message or key. A list stays whole in the public scope. Explicit OUTPUT_ONLY paths are ignored in unrestricted resolution.

The optional allow-list is compiled once in `NewSpec`. Malformed, unknown and OUTPUT_ONLY
entries and paths into list elements fail construction. Each explicitly requested spelling must resolve to a configured
path, and a rejection identifies its original index. A listed message grants expansion of
its eligible subtree unless listed descendants narrow the selection. For a partial map scope,
`*` expands the configured keys and descendants and an omitted mask intersects that scope with
the populated request values. A dynamic root with an allow-list uses the configured paths for `*`.
An empty allow-list denies all paths. Passing this setting to
`ResolveMask` or `ValidateResolved` returns `ErrAllowedPathsAtCall`.

`update.Spec.ValidateResolved` checks the original resource against the selection in descriptor
field order. Expansion follows active oneof message branches. Explicit leaves retain their
own checks and original indexes. A selected REQUIRED message gets its own check even when
there are no leaves. A missing REQUIRED message suppresses its expanded descendant checks for
that instance. An ordinary absent parent still has its selected REQUIRED leaves checked. An absent
map key can stay in scope and has no request value to check. Covered collection paths retain their
element-count checks with original indexes.

Without stored state, an omitted mask retains the planner's conservative oneof switch check.
With stored state, it compares supplied protected input and the protected effects of populated
collections and atomic values through a private scope that precedes allow-list filtering. An explicit
empty selection returns after checking the resolution, resource type and call options. Protected
comparisons preserve whole-message selections and their unknown fields. A protected scalar under
ordinary singular messages can be compared directly when the selection has no other protected
merge effects. Validation checks the schema of foreign dynamic children reached by those checks,
including reachable enum definitions, and normalizes compatible children for reflection.

Violations carry the original mask index, when available, and structured list indexes and map keys
alongside the resource-relative path.
`Path` contains declared field names and dot-separated map keys, with field-mask quoting for keys
such as `a.b`. Both structured positions identify the collection field's segment.
`IndexedPath()` renders brackets, as in `members[1].email` and
`delegates["a.b"].email`. The caller supplies the request's resource prefix.

## Consequences

Interceptors can share the original resource and validation scope in request context. A rejected
value leaves that scope available for downstream diagnostics. Pass the original mask to the
existing write flow. A whole-message write still clears an absent section or replaces it,
dropping the prior message's unknown fields.

## Addendum — Inspect policy independently of the mask

`Spec.FieldPolicy` exposes the effective identity, output-only, write-only,
immutable and required flags for a descriptor route. Mask resolution,
validation, planning and policy inspection share the declared root field
snapshot taken by `NewSpec`. Resolver results for those descriptors also
apply when they recur at depth. Other nested fields and extensions are
resolved on demand. Routes use the spec's exact schema descriptors, including
the extendee of an extension. Independently built equivalent dynamic
descriptors return `ErrMessageMismatch`. Interceptors can inspect fields
outside the allow-list and inside atomic messages.
