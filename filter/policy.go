// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

import (
	"strings"

	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/fieldpolicy"
	"github.com/altessa-s/go-aip/filter/internal/regexcache"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Policy is an immutable, validated set of field and expression rules in CEL-side (API) names: what a client may
// filter on and how. It is backend-independent: build it once per resource with NewPolicy and pass the same policy to
// every translator and evaluator, which bind it to their storage names with [NewTranslatorContext]. Its zero value is
// not a configured policy.
type Policy struct {
	options
	regexCache *regexcache.Cache
}

// NewPolicy builds a [Policy] with the given
// options applied and validates the result. It fails with
// [ErrAllowlistRequired] when no non-empty [WithAllowedFields]
// allow-list was provided, unless [WithAnyField] opts out of it: a
// filter usually comes from a client, and without an allow-list any
// field the storage holds (a password hash, an internal role) could be
// probed through it. [WithUntrustedInput] always requires the
// allow-list, whatever WithAnyField says. A [WithFieldAccess]
// [fieldpolicy.AllowList] stands in for WithAllowedFields only when it
// restricts the fields (not the zero AllowList, not "*"); an opaque
// [fieldpolicy.Access] does so only on trusted input. A malformed
// AllowList entry fails with [ErrInvalidOptions].
//
// Build the policy at process start and hand it to the translator and
// evaluator constructors, so misconfiguration surfaces at boot rather
// than on the first request.
func NewPolicy(opts ...PolicyOption) (*Policy, error) {
	ctx := &Policy{options: *newOptions(opts...)}
	if ctx.decimalParser == nil {
		return nil, ErrInvalidOptions
	}

	if ctx.valueCodecs != nil {
		for _, codec := range ctx.valueCodecs.All() {
			if nilcheck.IsNil(codec) {
				return nil, ErrInvalidOptions
			}
		}
	}

	if err := ctx.checkFieldAccess(); err != nil {
		return nil, err
	}

	if err := ctx.requireAllowlist(); err != nil {
		return nil, err
	}

	if err := ctx.checkRegexOptions(); err != nil {
		return nil, err
	}

	var err error

	ctx.regexCache, err = regexcache.New(ctx.regexCacheSize)
	if err != nil {
		return nil, err
	}

	ctx.resolveFields()

	return ctx, nil
}

// IsArrayField reports whether a CEL-side field name is declared by
// [WithArrayFields].
func (o *Policy) IsArrayField(field string) bool {
	return o.arrayFields != nil && o.arrayFields.Contains(field)
}

// PortableContract reports whether shared listing rules are enabled.
func (o *Policy) PortableContract() bool { return o.portableContract }

// ArrayFields returns the declared array fields as a detached map.
func (o *Policy) ArrayFields() map[string]bool {
	fields := make(map[string]bool)

	if o.arrayFields != nil {
		for name := range o.arrayFields.Keys() {
			fields[name] = true
		}
	}

	return fields
}

// ApplyFieldMapping returns the storage name of a CEL-side field: its [WithFieldMapping] entry on a policy bound by
// [NewTranslatorContext], otherwise field unchanged (always unchanged on a policy straight from NewPolicy).
func (o *Policy) ApplyFieldMapping(field string) string {
	if o.fieldMapping == nil {
		return field
	}

	if mapped, ok := o.fieldMapping.Get(field); ok {
		return mapped
	}

	return field
}

// IsFieldAllowed reports whether a field is allowed. Returns true when
// no allow-list is configured at all, which [NewPolicy]
// admits only with [WithAnyField].
func (o *Policy) IsFieldAllowed(field string) bool {
	if o.fieldAccess != nil && !o.fieldAccess.Allows(field) {
		return false
	}

	if o.allowedFields == nil {
		return true
	}

	return o.allowedFields.Contains(field)
}

// DeclaresField reports whether the service named a CEL-side field explicitly: a [WithAllowedFields] entry, a
// [WithFieldTypes] declaration, or a [WithFieldMapping] entry on a policy bound by [NewTranslatorContext]. A field that
// [Policy.IsFieldAllowed] admits only through a [WithFieldAccess] wildcard ("labels.*") or [WithAnyField] is not
// declared. A translator whose storage reads a dot as structure rather than nesting — an SQL dialect, where "a.b.c" is
// schema.table.column — uses it to refuse a dotted name the client composed itself.
func (o *Policy) DeclaresField(field string) bool {
	switch {
	case o.allowedFields != nil && o.allowedFields.Contains(field),
		o.fieldTypes != nil && o.fieldTypes.Contains(field),
		o.fieldMapping != nil && o.fieldMapping.Contains(field):
		return true
	default:
		return false
	}
}

// MaxDepth returns the configured maximum depth.
func (o *Policy) MaxDepth() int { return o.maxDepth }

// MaxRegexLength returns the configured maximum regex pattern length.
func (o *Policy) MaxRegexLength() int { return o.maxRegexLength }

// MaxOperations returns the configured maximum number of AST node visits.
func (o *Policy) MaxOperations() int { return o.maxOperations }

// requireAllowlist returns [ErrAllowlistRequired] when no usable
// allow-list was configured and either the context was marked as receiving
// untrusted input ([WithUntrustedInput]) or the caller did not opt out with
// [WithAnyField]. A usable allow-list is a non-empty [WithAllowedFields]
// list, or a [WithFieldAccess] policy that restricts the fields:
//
//   - a [fieldpolicy.AllowList] counts only when it restricts
//     ([fieldpolicy.AllowList.Restricts]) — the zero AllowList, one with a
//     lone "*" and one with an entry of stars only ("*.*") admit every field
//     or every field of a depth, so they are no allow-list at all;
//   - an opaque [fieldpolicy.Access], whose reach cannot be inspected,
//     counts on trusted input; under [WithUntrustedInput] it must be paired
//     with a non-empty WithAllowedFields list, as projection requires.
//
// [NewPolicy] runs this once at construction so the check disappears from
// the translation hot path — callers never see a misconfigured *Policy.
//
// An empty allow-list (e.g. `WithAllowedFields()` or
// `WithAllowedFields(slice...)` where the slice happened to be empty)
// is treated the same as no allow-list at all: the deny-all behavior
// it would otherwise produce is almost always a configuration bug — a
// forgotten append, an empty slice from a config loader, a misspelled
// struct field — and it is more useful to fail loudly than to silently
// reject every query.
func (o *Policy) requireAllowlist() error {
	if o.allowedFields != nil && o.allowedFields.Len() > 0 {
		return nil
	}

	if o.fieldAccess != nil {
		if list, ok := fieldpolicy.AsAllowList(o.fieldAccess); ok {
			if list.Restricts() {
				return nil
			}
		} else if !o.untrustedInput {
			return nil
		}
	}

	if o.anyField && !o.untrustedInput {
		return nil
	}

	return ErrAllowlistRequired
}

// checkFieldAccess rejects a [fieldpolicy.AllowList] field access policy built from a malformed entry with
// [ErrInvalidOptions]: the entry matches nothing, which is almost always a typo the caller wants to hear about.
func (o *Policy) checkFieldAccess() error {
	if o.allowedFields != nil {
		for name := range o.allowedFields.Keys() {
			if strings.Contains(name, "*") {
				return coreerrs.Wrapf(ErrInvalidOptions,
					"WithAllowedFields takes exact field names, not %q; pass a pattern through WithFieldAccess(fieldpolicy.NewAllowList(...))", name)
			}
		}
	}

	list, ok := fieldpolicy.AsAllowList(o.fieldAccess)
	if !ok {
		return nil
	}

	if err := list.Validate(); err != nil {
		return coreerrs.Wrapf(ErrInvalidOptions, "field access: %w", err)
	}

	return nil
}

// RegexCacheStats returns counters for this policy's default compiler.
func (o *Policy) RegexCacheStats() (hits, misses uint64, size int) { return o.regexCache.Stats() }

// ResetRegexCacheStats clears this policy's counters, retaining compiled patterns.
func (o *Policy) ResetRegexCacheStats() { o.regexCache.ResetStats() }
