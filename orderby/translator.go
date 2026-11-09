// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package orderby

import (
	"slices"

	"github.com/altessa-s/go-aip/fieldpolicy"
	"github.com/altessa-s/go-aip/metrics"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// TranslatorContext is the public read-side handle that database translator subpackages thread through their Translate calls:
// a [Policy] bound to the storage options of one backend. It is a type alias for the package-private translatorOptions struct,
// so its read methods are accessible cross-package while the raw fields stay encapsulated. A *TranslatorContext is always the
// product of a successful [NewTranslatorContext] call.
type TranslatorContext = translatorOptions

// NewTranslatorContext binds policy to the storage options of one backend and validates the result. It fails with
// [ErrPolicyRequired] for a nil policy, with [ErrInvalidFieldPath], [ErrInvalidDirection] or [ErrUnsupportedOrdering] for a
// malformed tie-breaker, and with [ErrFieldNotAllowed] for a tie-breaker that overlaps a denied storage path or a storage path a
// denied API field reaches through the mapping. The policy is shared, never modified, and may be bound to any number of
// translators.
//
// Translator constructors call this from their NewTranslator implementations and propagate the error. Misconfiguration therefore
// surfaces at process start rather than on the first request.
func NewTranslatorContext(policy *Policy, opts ...TranslatorOption) (*TranslatorContext, error) {
	if policy == nil {
		return nil, ErrPolicyRequired
	}

	ctx := newTranslatorOptions(opts...)
	ctx.policy = policy

	ctx.deniedStorage = slices.Clone(ctx.deniedStorageFields)
	for _, d := range policy.deniedFields {
		ctx.deniedStorage = append(ctx.deniedStorage, ctx.fieldMapping.Reach(d)...)
	}

	for _, k := range ctx.tiebreaker {
		if k.Name == "" {
			return nil, ErrInvalidFieldPath
		}

		if err := checkAttributes(k); err != nil {
			return nil, err
		}

		if ctx.storageDenied(k.Name) {
			return nil, ErrFieldNotAllowed
		}
	}

	return ctx, nil
}

// Policy returns the client-facing policy the context is bound to.
func (o *translatorOptions) Policy() *Policy { return o.policy }

// ApplyFieldMapping returns the storage name for an API field. The lookup order is: exact mapping (constant-time), then prefix
// mappings (longest first), then the input field unchanged when nothing matches.
func (o *translatorOptions) ApplyFieldMapping(field string) string {
	return o.fieldMapping.Apply(field)
}

// DeclaresField reports whether the service named the API field explicitly: an exact [WithAllowedFields] entry of the policy
// ([Policy.DeclaresField]) or an exact [WithFieldMapping] entry. A field reached only through a wildcard allow-list entry or a
// [WithFieldPrefixMapping] rule is not declared; an SQL translator refuses such a dotted field, whose dot would name a table.
func (o *translatorOptions) DeclaresField(field string) bool {
	return o.policy.DeclaresField(field) || o.fieldMapping.HasExact(field)
}

// KeyResolver maps an authorized API key to a storage key. It must be concurrency safe.
// SQL expressions belong in sqlbase.ColumnResolver, not request keys.
type KeyResolver interface{ ResolveKey(Key) (Key, error) }

// Resolve validates spec with the bound [Policy] (default order, authorization) and expresses the result in storage names:
// mapping or [KeyResolver], storage deny-lists, duplicate storage keys and tie-breakers. A translator therefore never renders a
// key its policy refuses. It never changes the caller's specification.
func (o *translatorOptions) Resolve(spec Spec) (Spec, error) {
	validated, err := o.policy.Validate(spec)
	if err != nil {
		return Spec{}, err
	}

	keys := validated.Keys

	seen := make(map[string]struct{}, len(keys)+len(o.tiebreaker))
	for i, k := range keys {
		k, err = o.resolveKey(k)
		if err != nil {
			return Spec{}, err
		}

		if _, ok := seen[k.Name]; ok {
			return Spec{}, coreerrs.Wrapf(ErrDuplicateKey, "%s", k.Name)
		}

		seen[k.Name] = struct{}{}
		keys[i] = k
	}

	for _, k := range o.tiebreaker {
		if _, ok := seen[k.Name]; !ok {
			keys = append(keys, k)
			seen[k.Name] = struct{}{}
		}
	}

	return Spec{Keys: keys}, nil
}

// CheckBasicOrdering rejects attributes that a backend cannot represent: any explicit null placement and any collation fail
// with [ErrUnsupportedOrdering].
func CheckBasicOrdering(spec Spec) error {
	return CheckNativeOrdering(spec, nil)
}

// CheckNativeOrdering rejects attributes that a backend with a fixed null placement cannot represent. native reports where the
// backend places nulls for a key (by its direction): an explicit [NullsFirst] or [NullsLast] equal to it is accepted, because the
// backend sorts that way anyway, and the opposite placement fails with [ErrUnsupportedOrdering]. A nil native accepts no explicit
// placement. Every collation fails with [ErrUnsupportedOrdering].
func CheckNativeOrdering(spec Spec, native func(Key) NullOrder) error {
	for _, k := range spec.Keys {
		if k.Collation != "" {
			return coreerrs.Wrapf(ErrUnsupportedOrdering, "%s: collation", k.Name)
		}

		if k.Nulls != NullsDefault && (native == nil || native(k) != k.Nulls) {
			return coreerrs.Wrapf(ErrUnsupportedOrdering, "%s: null placement", k.Name)
		}
	}

	return nil
}

// NullsSmallest is the native null placement of a backend that orders null before every value: first ascending, last
// descending (MongoDB, MariaDB/MySQL, the memory comparator). Pass it to [CheckNativeOrdering].
func NullsSmallest(k Key) NullOrder {
	if k.IsDescending() {
		return NullsLast
	}

	return NullsFirst
}

// NullsAlwaysLast is the native null placement of a backend that orders null and missing values after every value in both
// directions (Meilisearch). Pass it to [CheckNativeOrdering].
func NullsAlwaysLast(Key) NullOrder { return NullsLast }

// Telemetry is the translation timer of one order translator, created by [NewTelemetry]. A nil *Telemetry is
// disabled.
type Telemetry = metrics.TranslateTimer

// NewTelemetry creates the translation timer of an order translator, recorded as the histogram
// orderby_<backend>_translate_duration_seconds. Translators create it from the collector of their WithOrderCollector
// option. A nil meter disables collection and returns nil, whose Start is a no-op.
func NewTelemetry(m metrics.Meter, backend string) *Telemetry {
	return metrics.NewTranslateTimer(m, "orderby_"+backend, "Duration of order_by translation.")
}

// resolveKey maps an authorized API key to its storage key.
func (o *translatorOptions) resolveKey(k Key) (Key, error) {
	var err error
	if o.keyResolver != nil {
		k, err = o.keyResolver.ResolveKey(k)
		if err != nil {
			return Key{}, err
		}
	} else {
		k.Name = o.ApplyFieldMapping(k.Name)
	}

	// A resolver may rewrite every attribute: check them again.
	if err = checkAttributes(k); err != nil {
		return Key{}, err
	}

	if o.storageDenied(k.Name) {
		return Key{}, coreerrs.Wrapf(ErrFieldNotAllowed, "%s", k.Name)
	}

	return k, nil
}

// storageDenied reports whether the storage path overlaps a denied storage path, or a storage path a denied API field of the
// policy reaches through the mapping.
func (o *translatorOptions) storageDenied(name string) bool {
	return slices.ContainsFunc(o.deniedStorage, func(d string) bool { return fieldpolicy.Overlaps(name, d) })
}
