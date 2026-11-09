// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projection

import (
	"maps"
	"slices"
	"strconv"
)

// View is an AIP-157 resource view: a named set of fields a request selects when it carries no read mask. Map the -View enum
// of the API onto it. [ViewBasic] and [ViewFull] are the standard views; values above [ViewFull] are free for custom views
// configured with [WithView].
type View int

const (
	// ViewUnspecified selects the policy's default view ([WithDefaultView]). It is valid, never an error (AIP-157).
	ViewUnspecified View = iota
	// ViewBasic selects the fields configured with WithView(ViewBasic, ...).
	ViewBasic
	// ViewFull selects every authorized field. It needs no configuration.
	ViewFull
)

// String returns the AIP-157 name of the standard views and "VIEW(n)" for the others.
func (v View) String() string {
	switch v {
	case ViewUnspecified:
		return "UNSPECIFIED"
	case ViewBasic:
		return "BASIC"
	case ViewFull:
		return "FULL"
	default:
		return "VIEW(" + strconv.Itoa(int(v)) + ")"
	}
}

// Or returns def when v is [ViewUnspecified] and v otherwise. A method whose default view differs from the policy's resolves
// it before selecting: policy.Select(spec, view.Or(ViewBasic)).
func (v View) Or(def View) View {
	if v == ViewUnspecified {
		return def
	}

	return v
}

// viewFields maps each configured view to its API paths.
type viewFields map[View][]string

// With returns a copy of v in which view lists paths.
func (v viewFields) With(view View, paths ...string) viewFields {
	out := maps.Clone(v)
	if out == nil {
		out = make(viewFields, 1)
	}

	out[view] = slices.Clone(paths)

	return out
}
