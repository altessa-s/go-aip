// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing

import (
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/projection"

	filterparser "github.com/altessa-s/go-aip/filter/parser"
	coremaps "github.com/altessa-s/go-atlas-core/collections/maps"
	coreslices "github.com/altessa-s/go-atlas-core/collections/slices"
)

// Schema is the validated query surface of one list: its filter fields,
// custom filter functions and accepted orders. It is immutable and safe for
// concurrent use; build it once (package level or in a constructor) and
// share it between the engines that serve the list.
//
// With the default Profile, filters are treated as untrusted input: only the declared fields
// are allowed, and of the built-in string functions only contains(),
// startsWith() and endsWith() are exposed — never matches() (a client regex)
// or substring(). Custom functions may expand into matches() over declared
// fields (functions.TextSearch quotes its argument).
type Schema struct {
	opts                          *profileOptions
	fields                        *coremaps.ImmutableMap[string, compiledField]
	filterParser                  FilterParser
	orderParser                   OrderParser
	policy                        *filter.Policy
	combinedFilterPolicy          *filter.Policy
	orderPolicy                   *orderby.Policy
	orders                        []Order                               // declared, then reversed, per pattern
	byCanonical                   *coremaps.ImmutableMap[string, Order] // canonical orderBy -> order
	def                           Order
	contractID                    string
	caseInsensitiveContainsFields []string
	defaults                      []defaultEquality
}

// NewSchema binds fields and patterns to a Profile; nil selects the portable
// default. Profile options may replace the validation rules described below. A pattern
// is a complete order in AIP-132 syntax over public names, e.g.
// "createTime desc" or "kind, name"; its full reversal is accepted too.
//
// It fails with [ErrInvalidSchema] for a default page size above the
// maximum, a field without a name or kind, a duplicate field, an enum on a
// kind other than string or int, and for a pattern that does not parse,
// names an undeclared, array, optional or non-scalar field (only string,
// int, float, decimal, bool and timestamp sort), or repeats another pattern
// or its reversal. A pattern over a Hidden field fails with
// [ErrUnreadableSortField] too, unless [WithUnreadableSortAllowed] is set. The default order must be a declared pattern or its
// reversal. [WithServerOnlyFields] must name declared fields.
// NewSchema needs no context: patterns are parsed by the
// orderby parser, as request orders are.
func NewSchema(fields []Field, patterns []string, profile *Profile) (*Schema, error) {
	if profile == nil || profile.opts == nil {
		profile = NewProfile()
	}

	s := &Schema{opts: profile.opts}
	if s.opts.defaultLiteral == nil || s.opts.errorFormatter == nil {
		return nil, invalid("default literal converter and error formatter are required")
	}

	if s.opts.timestampPrecision < 0 {
		return nil, invalid("timestamp precision must be nonnegative")
	}

	if s.opts.pageSizeResolver == nil && s.opts.defaultPageSize > s.opts.maxPageSize {
		return nil, invalid("default page size %d exceeds the maximum %d", s.opts.defaultPageSize, s.opts.maxPageSize)
	}

	if err := s.compileFields(fields); err != nil {
		return nil, err
	}

	for _, name := range s.opts.serverOnlyFields {
		if !s.fields.Contains(name) {
			return nil, invalid("server-only field %q is not declared", name)
		}
	}

	if err := s.compileContainsFields(); err != nil {
		return nil, err
	}

	if err := s.buildParsers(); err != nil {
		return nil, err
	}

	if err := s.compilePatterns(patterns); err != nil {
		return nil, err
	}

	if err := s.resolveDefault(); err != nil {
		return nil, err
	}

	policy, err := filter.NewPolicy(s.FilterOptions()...)
	if err != nil {
		return nil, fmt.Errorf("%w: filter policy: %w", ErrInvalidSchema, err)
	}

	s.policy = policy
	s.combinedFilterPolicy = policy.WithoutContractRule(filter.NonemptyIn)

	if err = s.compileDefaults(); err != nil {
		return nil, err
	}

	if s.orderPolicy, err = orderby.NewPolicy(s.OrderOptions()...); err != nil {
		return nil, fmt.Errorf("%w: order policy: %w", ErrInvalidSchema, err)
	}

	return s, nil
}

// MustSchema is [NewSchema] for package-level schema declarations
// (var widgets = listing.MustSchema(fields, patterns, nil)): it returns the schema NewSchema
// builds and panics on an invalid declaration. The panic value is an error
// prefixed with "listing.MustSchema: " that wraps the NewSchema error, so
// errors.Is(recovered, [ErrInvalidSchema]) holds. Use NewSchema where the
// declaration comes from configuration rather than code.
func MustSchema(fields []Field, patterns []string, profile *Profile) *Schema {
	s, err := NewSchema(fields, patterns, profile)
	if err != nil {
		panic(fmt.Errorf("listing.MustSchema: %w", err))
	}

	return s
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSchema, fmt.Sprintf(format, args...))
}

func (s *Schema) buildParsers() error {
	names := slices.Concat(s.opts.builtinFunctions, slices.Sorted(s.opts.functions.Keys()))
	// The parser interprets an empty allowlist as unrestricted. A non-callable
	// name keeps an explicitly empty profile allowlist restrictive.
	if len(names) == 0 {
		names = []string{"__listing_no_functions__"}
	}

	enums := map[string]map[string]any{}

	for name, f := range s.fields.All() {
		if f.enum != nil {
			enums[name] = maps.Collect(f.enum.All())
		}
	}

	parserOptions := []filterparser.Option{
		filterparser.WithCustomFunctions(maps.Collect(s.opts.functions.All())),
		filterparser.WithAllowedFunctions(names...),
		filterparser.WithEnumNames(enums),
	}

	s.filterParser = s.opts.filterParser
	if nilcheck.IsNil(s.filterParser) {
		fp, err := filterparser.New(append(slices.Clone(s.opts.filterParserOptions), parserOptions...)...)
		if err != nil {
			return fmt.Errorf("%w: filter parser: %w", ErrInvalidSchema, err)
		}

		s.filterParser = fp
	}

	s.orderParser = s.opts.orderParser
	if nilcheck.IsNil(s.orderParser) {
		op, err := orderby.NewParser(s.opts.orderParserOptions...)
		if err != nil {
			return fmt.Errorf("%w: orderBy parser: %w", ErrInvalidSchema, err)
		}

		s.orderParser = op
	}

	return nil
}

// ProjectionOptions returns the projection policy options the schema implies, in public names: its Hidden fields are denied, as
// the client never reads them. A list's fields are its filter fields, not every field of the resource, so the schema cannot
// state the allow-list: append it, and any resource views (projection.WithView), before calling projection.NewPolicy.
func (s *Schema) ProjectionOptions() []projection.PolicyOption {
	hidden := make([]string, 0)
	for name, f := range s.fields.All() {
		hidden = coreslices.AppendIf(hidden, f.Hidden, name)
	}

	slices.Sort(hidden)

	return []projection.PolicyOption{projection.WithDeniedFields(hidden...)}
}

// noFields is the field access policy of a schema with no sortable field.
type noFields struct{}

func (noFields) Allows(string) bool { return false }

// MaxPageSize returns the largest page the schema serves ([WithMaxPageSize]).
func (s *Schema) MaxPageSize() int { return s.opts.maxPageSize }

// TimestampPrecision returns the resolution used for filter timestamps.
// Zero preserves nanoseconds.
func (s *Schema) TimestampPrecision() time.Duration { return s.opts.timestampPrecision }
