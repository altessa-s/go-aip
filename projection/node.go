// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projection

import (
	"slices"
	"strings"
)

// Mode distinguishes an omitted read mask, explicit all, and explicit none.
// ModeDefault with paths means an explicit field list.
type Mode uint8

const (
	// ModeDefault without paths is an omitted read mask: every authorized
	// field ([Policy.Validate]) or the requested view ([Policy.Select]). With
	// paths it requests the listed paths.
	ModeDefault Mode = iota
	// ModeAll requests every authorized field ("*"), whatever the view.
	ModeAll
	// ModeNone selects no field.
	ModeNone
)

// Spec is the parsed result of a fields expression: the requested API field
// paths, deduplicated and sorted. The zero value is an omitted read mask, which
// returns every authorized field unless a view applies; ModeAll explicitly
// requests all authorized fields, and ModeNone selects none.
//
// Paths are API names. Parent/child pairs ("address", "address.city") are
// kept as written: collapsing them is only safe on storage names, after the
// translator has applied the field mapping.
type Spec struct {
	Mode  Mode
	Paths []string
}

// IsEmpty reports whether the Spec is an omitted read mask, so a view (or,
// without one, every authorized field) decides the selection.
func (s Spec) IsEmpty() bool { return len(s.Paths) == 0 && s.Mode == ModeDefault }

// Clone returns a copy of s whose Paths slice is independent of the
// original.
func (s Spec) Clone() Spec {
	return Spec{Mode: s.Mode, Paths: slices.Clone(s.Paths)}
}

// Contains reports whether path is selected in full: an empty Spec selects
// everything (when a view decides instead, inspect the selected [Fields]),
// otherwise path or one of its ancestors must be listed. A path
// whose descendants alone are listed ("address" when only "address.city"
// was requested) is selected only partially and reports false.
func (s Spec) Contains(path string) bool {
	if s.Mode == ModeNone {
		return false
	}

	if s.Mode == ModeAll || s.IsEmpty() {
		return true
	}

	for _, p := range s.Paths {
		if p == path || (strings.HasPrefix(path, p) && path[len(p)] == '.') {
			return true
		}
	}

	return false
}

// Relative re-roots s at prefix: it keeps the paths under prefix with the
// prefix removed and drops every other path. Use it when the mask addresses
// a response envelope rather than the stored resource — a List response
// mask "items.name, next_page_token" becomes "name" for the items.
//
// It returns ok=false when s names nothing under prefix, so the collection
// is not requested at all. An s that selects prefix in full — empty, or
// listing prefix or one of its ancestors — selects the whole resource and
// returns ModeAll for an explicit parent, preserving omission only for an omitted spec.
func (s Spec) Relative(prefix string) (Spec, bool) {
	if s.Contains(prefix) {
		if len(s.Paths) > 0 {
			return Spec{Mode: ModeAll}, true
		}

		return Spec{Mode: s.Mode}, true
	}

	var out []string

	for _, p := range s.Paths {
		if rest, ok := strings.CutPrefix(p, prefix+"."); ok {
			out = append(out, rest)
		}
	}

	if len(out) == 0 {
		return Spec{}, false
	}

	slices.Sort(out)

	return Spec{Paths: slices.Compact(out)}, true
}

// Selection is a resolved projection in storage names, ready for a backend
// to render. At most one of Include and Exclude is non-empty; both empty
// means every stored field. Both slices are sorted and deduplicated.
type Selection struct {
	Mode Mode
	// Include lists the storage paths to return, required fields included.
	Include []string
	// Exclude lists the storage paths to omit. It is only produced for every
	// authorized field — an omitted mask, "*" or ViewFull — when the policy
	// has no allow-list.
	Exclude []string
}

// IsAll reports whether the selection returns every stored field.
func (s Selection) IsAll() bool {
	return s.Mode != ModeNone && len(s.Include) == 0 && len(s.Exclude) == 0
}

// clone returns a copy of s whose slices are independent of the original.
func (s Selection) clone() Selection {
	return Selection{Mode: s.Mode, Include: slices.Clone(s.Include), Exclude: slices.Clone(s.Exclude)}
}
