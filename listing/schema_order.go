// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/pagination/pagetoken"

	coremaps "github.com/altessa-s/go-atlas-core/collections/maps"
)

// OrderKey is one key of a resolved [Order].
type OrderKey struct {
	// Nulls specifies null placement; engines must reject unsupported values.
	Nulls orderby.NullOrder
	// Collation specifies comparison semantics, including cursor comparisons.
	Collation string
	// Field is the public field name.
	Field string
	// Desc reports a descending key.
	Desc bool
}

// Order is an accepted order of a list: a declared pattern or its full
// reversal. Engines append their id tie-breaker in the direction of the
// last key, so every order is total.
type Order struct {
	keys      []OrderKey
	canonical string
	reversed  bool
}

// String returns the canonical orderBy of the order ("kind, name desc"):
// public names, ", " separated, "desc" spelled out and "asc" omitted.
func (o Order) String() string { return o.canonical }

// Keys returns the keys of the order, without a tie-breaker.
func (o Order) Keys() []OrderKey { return slices.Clone(o.keys) }

// Reversed reports whether the order is the reversal of its declared
// pattern.
func (o Order) Reversed() bool { return o.reversed }

// sortClass returns the comparison class of the values of a field of kind;
// a sort pattern accepts only the kinds that have one (scalars MongoDB and
// the in-memory engine order identically). It is [pagetoken.SortClassNone] for
// the others.
func sortClass(kind filter.FieldKind) pagetoken.SortClass {
	switch kind {
	case filter.FieldKindString:
		return pagetoken.SortClassString
	case filter.FieldKindInt, filter.FieldKindFloat, filter.FieldKindDecimal:
		return pagetoken.SortClassNumber
	case filter.FieldKindBool:
		return pagetoken.SortClassBool
	case filter.FieldKindTimestamp:
		return pagetoken.SortClassTime
	default:
		return pagetoken.SortClassNone
	}
}

func (s *Schema) compilePatterns(patterns []string) error {
	byCanonical := make(map[string]Order)

	if len(patterns) == 0 && s.opts.emptyOrderAllowed {
		patterns = []string{""}
	}

	if len(patterns) == 0 && !s.opts.emptyOrderAllowed {
		return invalid("no sort patterns")
	}

	for _, p := range patterns {
		spec, err := s.parsePattern(p)
		if err != nil {
			return fmt.Errorf("%w: pattern %q: %w", ErrInvalidSchema, p, err)
		}

		if err = s.checkPatternKeys(p, spec); err != nil {
			return err
		}

		declared, reversed := newOrder(spec, false), newOrder(spec.Reverse(), true)
		if spec.IsEmpty() {
			if _, dup := byCanonical[declared.canonical]; dup {
				return invalid("duplicate empty order")
			}

			byCanonical[declared.canonical] = declared
			s.orders = append(s.orders, declared, reversed)

			continue
		}

		for _, o := range []Order{declared, reversed} {
			if _, dup := byCanonical[o.canonical]; dup {
				return invalid("pattern %q repeats the order %q", p, o.canonical)
			}

			byCanonical[o.canonical] = o
		}

		s.orders = append(s.orders, declared, reversed)
	}

	s.byCanonical = coremaps.NewImmutableMap(byCanonical)

	return nil
}

// checkPatternKeys rejects a pattern key that is not a declared, scalar,
// always-present field, and a hidden one unless the schema allows it.
func (s *Schema) checkPatternKeys(pattern string, spec orderby.Spec) error {
	for _, k := range spec.Keys {
		f, ok := s.fields.Get(k.Name)
		switch {
		case !ok:
			return invalid("pattern %q: field %q is not declared", pattern, k.Name)
		case f.Hidden && !s.opts.unreadableSortAllowed:
			return fmt.Errorf("%w: %w: pattern %q: field %q is hidden", ErrInvalidSchema, ErrUnreadableSortField, pattern, k.Name)
		}

		switch {
		case len(s.opts.sortableFields) > 0:
			if !slices.Contains(s.opts.sortableFields, k.Name) {
				return fmt.Errorf("%w: pattern %q: field %q does not sort", ErrInvalidSchema, pattern, k.Name)
			}
		case s.opts.sortFieldValidator != nil:
			if err := s.opts.sortFieldValidator(s.fieldMetadata(f)); err != nil {
				return fmt.Errorf("%w: pattern %q: %w", ErrInvalidSchema, pattern, err)
			}
		}
	}

	return nil
}

// newOrder resolves spec into an [Order].
func newOrder(spec orderby.Spec, reversed bool) Order {
	o := Order{reversed: reversed, canonical: spec.String()}
	for _, k := range spec.Keys {
		o.keys = append(o.keys, OrderKey{Field: k.Name, Desc: k.IsDescending(), Nulls: k.Nulls, Collation: k.Collation})
	}

	return o
}

func (s *Schema) resolveDefault() error {
	if s.opts.defaultOrderBy == "" {
		if len(s.orders) > 0 {
			s.def = s.orders[0]
		}

		return nil
	}

	spec, err := s.parsePattern(s.opts.defaultOrderBy)
	if err != nil {
		return fmt.Errorf("%w: default order %q: %w", ErrInvalidSchema, s.opts.defaultOrderBy, err)
	}

	o, ok := s.byCanonical.Get(spec.String())
	if !ok && s.opts.unrestrictedOrder {
		if err := s.checkPatternKeys(s.opts.defaultOrderBy, spec); err != nil {
			return err
		}

		o, ok = newOrder(spec, false), true
	}

	if !ok {
		return invalid("default order %q is neither a declared pattern nor its reversal", s.opts.defaultOrderBy)
	}

	s.def = o

	return nil
}

// parsePattern parses a declared order (a pattern or the default order) with
// the orderby parser; its context only feeds the parser's cache, so a
// declaration needs none. The parser accepts a blank order as the empty Spec,
// accepted only when the profile enables empty orders.
func (s *Schema) parsePattern(pattern string) (orderby.Spec, error) {
	spec, err := s.orderParser.Parse(context.Background(), pattern)
	if err != nil {
		return orderby.Spec{}, err
	}

	if spec.IsEmpty() && !s.opts.emptyOrderAllowed {
		return orderby.Spec{}, errors.New("empty order")
	}

	return spec, nil
}

// Order resolves a request orderBy: empty is the default order, otherwise it
// must spell a declared pattern or its full reversal (whitespace and the
// case of asc/desc are free; an explicit "asc" equals none). Anything else is
// an InvalidArgument core/errors.FieldError on orderBy listing the accepted
// orders.
func (s *Schema) Order(ctx context.Context, orderBy string) (Order, error) {
	if strings.TrimSpace(orderBy) == "" {
		return s.def, nil
	}

	spec, err := s.orderParser.Parse(ctx, orderBy)
	if err != nil {
		return Order{}, s.FormatError(FieldOrderBy, err.Error(), err)
	}

	if o, ok := s.byCanonical.Get(spec.String()); ok {
		return o, nil
	}

	if s.opts.unrestrictedOrder {
		if spec.IsEmpty() && !s.opts.emptyOrderAllowed {
			return Order{}, s.FormatError(FieldOrderBy, "empty order is not allowed", nil)
		}

		if err := s.checkPatternKeys(orderBy, spec); err != nil {
			return Order{}, s.FormatError(FieldOrderBy, err.Error(), err)
		}

		return newOrder(spec, false), nil
	}

	return Order{}, s.FormatError(FieldOrderBy,
		fmt.Sprintf("unsupported order; use one of: %s", strings.Join(s.orderNames(), "; ")), nil)
}

// OrderOptions returns the order policy options of the schema, in public names: untrusted input, the fields a request may sort
// on and the default order. A field may be sorted on when it is a key of an accepted order or, under an unrestricted profile,
// when it passes the pattern key checks; a schema with no such field refuses every key through a field access policy instead
// of an allow-list. Translators bind the policy to their storage mapping (orderby.NewTranslatorContext), so one policy serves
// every engine of the list.
func (s *Schema) OrderOptions() []orderby.PolicyOption {
	sortable := s.SortFields()

	defaults := make([]orderby.Key, 0, len(s.def.keys))
	for _, k := range s.def.keys {
		key := orderby.Key{Name: k.Field, Nulls: k.Nulls, Collation: k.Collation}
		if k.Desc {
			key.Direction = orderby.DirectionDescending
		}

		defaults = append(defaults, key)
	}

	if len(sortable) == 0 {
		// The access policy refuses every key, so there is no allow-list for the untrusted-input guard to require.
		return []orderby.PolicyOption{orderby.WithDefaultOrder(defaults...), orderby.WithFieldAccess(noFields{})}
	}

	return []orderby.PolicyOption{orderby.WithUntrustedInput(true), orderby.WithDefaultOrder(defaults...),
		orderby.WithAllowedFields(sortable...)}
}

// SortFields returns the sorted names of the fields a request may sort on: the keys of the accepted orders and, under an
// unrestricted profile, every field that passes the pattern key checks.
func (s *Schema) SortFields() []string {
	sortable := make(map[string]struct{})

	for _, o := range s.orders {
		for _, k := range o.keys {
			sortable[k.Field] = struct{}{}
		}
	}

	if s.opts.unrestrictedOrder {
		for name := range s.fields.Keys() {
			if s.checkPatternKeys("", orderby.Spec{Keys: []orderby.Key{{Name: name}}}) == nil {
				sortable[name] = struct{}{}
			}
		}
	}

	return slices.Sorted(maps.Keys(sortable))
}

// OrderPolicy returns the order policy built from [Schema.OrderOptions].
func (s *Schema) OrderPolicy() *orderby.Policy { return s.orderPolicy }

// Default returns the order of a request without orderBy.
func (s *Schema) Default() Order { return s.def }

// Orders returns every accepted order: each declared pattern followed by its
// reversal, in declaration order. Unrestricted request orders are not enumerated.
func (s *Schema) Orders() []Order { return slices.Clone(s.orders) }

func (s *Schema) orderNames() []string {
	names := make([]string, len(s.orders))
	for i, o := range s.orders {
		names[i] = o.canonical
	}

	return names
}

// UnrestrictedOrder reports whether request orders may be absent from Orders.
// Engines that precompile a fixed index plan must reject this profile.
func (s *Schema) UnrestrictedOrder() bool { return s.opts.unrestrictedOrder }
