// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package orderby

import (
	"github.com/altessa-s/go-aip/fieldpolicy"
)

//go:generate optgen generate --type=policyOptions --output=policy_options_gen.go --option-type=PolicyOption

// policyOptions is the optgen target of [Policy]: what a client may sort on,
// in API names, independent of any backend. [NewPolicy] validates and
// freezes it.
type policyOptions struct {
	// WithFieldAccess adds a shared field authorization policy. Existing allow-lists remain restrictive.
	fieldAccess fieldpolicy.Access `opt:"FieldAccess" optgen:"notnil"`

	// WithDeniedFields lists API paths a client may never sort on, even when an allow-list wildcard covers them. A key that
	// overlaps a denied path (equal, ancestor or descendant) fails with [ErrFieldNotAllowed]. A translator also denies every
	// storage path such an entry reaches through its mapping, so an allowed alias cannot sort on it.
	deniedFields []string `opt:"DeniedFields" optgen:"allownull" optval:"clone,notrim,nodup"`
	// WithDefaultOrder sets the keys, in API names, used for a request without an order. Each key must pass the policy.
	defaultOrder []Key `opt:"DefaultOrder" optgen:"allownull" optval:"clone"`

	// WithAllowedFields sets a whitelist of allowed field names. Keys are
	// matched against [Key.Name] — the raw dotted API form — so callers
	// configure nested fields exactly as they appear in the DSL
	// ("address.city"), never as storage names.
	//
	// Entries that end in ".*" are treated as wildcard prefixes: every key
	// whose dotted path starts with the matching segment is allowed. A bare
	// "*" matches anything (equivalent to omitting the allow-list).
	// Embedded stars (e.g. "foo.*.bar") match one nonempty segment. The
	// trailing ".*" entry does **not** include the bare prefix itself: to
	// allow "address" in addition to "address.X", list both.
	//
	// Example:
	//
	//	policy, err := orderby.NewPolicy(orderby.WithAllowedFields(
	//	    "createdAt",     // exact
	//	    "slug",          // exact
	//	    "address.*",     // any subkey of address
	//	))
	allowedFields fieldpolicy.AllowList `optfn:"AllowedFields=fieldpolicy.NewAllowList"`
	// WithUntrustedInput marks the order as client input: [NewPolicy] then requires an allow-list that restricts the fields —
	// a non-empty [WithAllowedFields] list or a [fieldpolicy.AllowList] field access policy, neither matching every field.
	untrustedInput bool `opt:"UntrustedInput" optval:"param"`
}
