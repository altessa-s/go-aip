// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projection

import (
	"github.com/altessa-s/go-aip/fieldpolicy"
)

//go:generate optgen generate --type=policyOptions --output=policy_options_gen.go --option-type=PolicyOption

// policyOptions is the optgen target of [Policy]: which fields a client may
// request, in API names, independent of any backend. [NewPolicy] validates
// and freezes it.
type policyOptions struct {
	// WithFieldAccess adds a shared field authorization policy. Existing allow-lists remain restrictive.
	fieldAccess fieldpolicy.Access `opt:"FieldAccess" optgen:"notnil"`

	// WithPathValidator replaces the dotted path grammar a policy checks API paths against: requested paths and the denied and
	// view paths. Storage paths of the translators bound to the policy are checked with WithStoragePathValidator instead.
	// Authorization still applies.
	pathValidator PathValidator `opt:"PathValidator" optgen:"default=checkPath"`

	// WithAllowedFields restricts the paths a client may request. Entries are
	// matched against the API name; an entry ending in ".*" allows every subpath
	// but not the bare parent, and a lone "*" allows anything. A restrictive
	// list also defines "every authorized field" — the selection of an omitted
	// read mask, of "*" and of [ViewFull]: its roots ("address.*" → "address").
	allowedFields fieldpolicy.AllowList `optfn:"AllowedFields=fieldpolicy.NewAllowList"`
	// WithDeniedFields lists API paths that are never returned, even when an
	// allow-list wildcard covers them. A request for a denied path, one of its
	// descendants, or one of its ancestors (which would carry it along) fails
	// with [ErrFieldNotAllowed]. A translator also denies every storage path an
	// entry reaches through its mapping — including targets its descendants are
	// mapped to.
	deniedFields []string `opt:"DeniedFields" optgen:"allownull" optval:"clone,notrim,nodup"`
	//optgen:doc View WithView sets the API paths of view, a named subset of the authorized fields returned when a request carries
	//optgen:doc View no read mask and selects view (AIP-157 resource views). [ViewBasic] is the usual one; values above
	//optgen:doc View [ViewFull] name custom views. [ViewUnspecified] and [ViewFull] are not configurable: FULL is always every
	//optgen:doc View authorized field. [NewPolicy] checks each path against the grammar, the allow-list, the field access policy
	//optgen:doc View and the deny-list. A later call for the same view replaces its paths. Once published, a view must not lose a
	//optgen:doc View field (AIP-157).
	views viewFields `optfn:"View=.With"`
	// WithDefaultView sets the view used when a request carries no read mask and [ViewUnspecified]. AIP-157 recommends
	// [ViewBasic] for List methods and either [ViewBasic] or [ViewFull] for the other methods; a method whose default differs
	// from the policy's passes its own with [View.Or]. The view must be [ViewFull] or one configured with [WithView].
	defaultView View `opt:"DefaultView" optgen:"default=ViewFull"`
	// WithUntrustedInput marks the request as client input: [NewPolicy] then requires an allow-list that restricts the fields —
	// a non-empty [WithAllowedFields] list or a [fieldpolicy.AllowList] field access policy, neither matching every field.
	untrustedInput bool `opt:"UntrustedInput" optval:"param"`
}
