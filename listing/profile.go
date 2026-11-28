// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing

import (
	"fmt"
	"slices"
	"time"

	"github.com/altessa-s/go-aip/pagination/pagetoken"
)

// Profile is the parsing, filtering, sorting and request policy bound to Schema
// metadata. Construct it with NewProfile and reuse it across schemas. Injected
// functions and parsers must be concurrency safe. Version semantic changes with
// WithContractID so existing page tokens cannot silently acquire new semantics.
// A nil Profile selects today's portable proto3 listing behavior.
type Profile struct{ opts *profileOptions }

// NewProfile captures policy options. NewSchema validates the profile against
// its metadata. Options are applied in order; later filter options override
// metadata-derived defaults in both Schema and consuming engines.
func NewProfile(opts ...ProfileOption) *Profile { return &Profile{opts: newProfileOptions(opts...)} }

func defaultBuiltinFunctions() []string {
	return []string{"contains", "startsWith", "endsWith", "size", "timestamp"}
}

func defaultSortFieldValidator(f Field) error {
	switch {
	case f.Array:
		return fmt.Errorf("field %q is an array", f.Name)
	case f.Optional:
		return fmt.Errorf("field %q is optional", f.Name)
	case f.Presence:
		return fmt.Errorf("field %q has explicit presence", f.Name)
	case sortClass(f.Kind) == pagetoken.SortClassNone:
		return fmt.Errorf("field %q of kind %s does not sort", f.Name, f.Kind)
	}

	return nil
}

// sortableField refuses a field of [WithSortableFields] that no order can sort: an array, an optional field, or a kind without order.
func sortableField(f Field) error {
	switch {
	case f.Array:
		return fmt.Errorf("field %q is an array", f.Name)
	case f.Optional:
		return fmt.Errorf("field %q is optional", f.Name)
	case sortClass(f.Kind) == pagetoken.SortClassNone:
		return fmt.Errorf("field %q of kind %s does not sort", f.Name, f.Kind)
	}

	return nil
}

func copyBuiltinFunctions(names ...string) []string    { return slices.Clone(names) }
func timestampPrecision(d time.Duration) time.Duration { return d }
