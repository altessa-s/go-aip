// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/fieldpolicy"
	"github.com/altessa-s/go-aip/filter"

	coremaps "github.com/altessa-s/go-atlas-core/collections/maps"
	coreslices "github.com/altessa-s/go-atlas-core/collections/slices"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// TimestampPrecision is the default filter timestamp resolution. Profiles can
// replace it with WithTimestampPrecision; engines configure storage separately.
const TimestampPrecision = time.Millisecond

// storedTime normalizes a literal using the profile resolution (zero preserves precision).
func (s *Schema) storedTime(t time.Time) time.Time {
	return t.UTC().Truncate(s.opts.timestampPrecision)
}

// Query is a validated request, ready for an engine.
type Query struct {
	// Size is the resolved page size.
	Size int
	// Order is the resolved order.
	Order Order
	// Filter is the request filter ANDed with the default equalities it
	// does not override, then with the request's Restriction, validated;
	// nil matches everything. It is the caller's: an engine translates it
	// into its query language and binds its canonical form
	// (filter.Canonical) into the page token.
	Filter filter.Node
	cutoff filter.Node
}

// Cutoff returns the validated, request-owned server predicate to apply to List
// and Count alongside Filter. Only Filter participates in token bindings.
func (q Query) Cutoff() filter.Node { return q.cutoff }

// Compile validates req in field order — pageSize, parent, orderBy, filter
// — and returns the first invalid field's error, an InvalidArgument
// core/errors.FieldError. At the filter step the server restriction
// and cutoff are checked first, then the client's filter is
// parsed, joined with the default equalities it does not override and
// validated, and finally ANDed with the restriction. The page token is checked by the engine when it
// resolves the position. Every engine applies the same rules, so they reject
// the same requests.
func (s *Schema) Compile(ctx context.Context, req Request) (Query, error) {
	return s.CompileWithOrderValidator(ctx, req, nil)
}

// CompileWithOrderValidator compiles a request with engine-specific order checks.
// validate runs after schema order validation and before filter parsing, preserving
// request field precedence. A nil validator adds no engine-specific checks.
func (s *Schema) CompileWithOrderValidator(ctx context.Context, req Request, validate func(Order) error) (Query, error) {
	size, err := s.pageSize(req)
	if err != nil {
		return Query{}, err
	}

	if s.opts.parentRequired && strings.TrimSpace(req.Parent) == "" {
		return Query{}, s.FormatError(FieldParent, "is required", nil)
	}

	order, err := s.Order(ctx, req.OrderBy)
	if err != nil {
		return Query{}, err
	}

	if err = s.checkReadable(order, req.ReadMask); err != nil {
		return Query{}, err
	}

	if validate != nil {
		if err = validate(order); err != nil {
			return Query{}, err
		}
	}

	restriction, err := s.restriction(req.Restriction)
	if err != nil {
		return Query{}, err
	}

	cutoff, err := s.restriction(req.Cutoff)
	if err != nil {
		return Query{}, fmt.Errorf("cutoff: %w", err)
	}

	q := Query{Size: size, Order: order, cutoff: cutoff}
	if strings.TrimSpace(req.Filter) != "" { // a blank filter matches everything
		if q.Filter, err = s.parseFilter(ctx, req.Filter); err != nil {
			return Query{}, err
		}
	}

	q.Filter = s.withDefaults(q.Filter)
	if err = filter.Validate(q.Filter, s.policy); err != nil {
		return Query{}, s.TranslationError(err)
	}

	if restriction != nil {
		q.Filter = and(q.Filter, restriction)
		if err = s.policy.CheckLimits(q.Filter); err != nil {
			return Query{}, s.FormatError(FieldFilter, "is too complex for this list; simplify it", err)
		}
	}

	if cutoff != nil && q.Filter != nil {
		if err = s.policy.CheckLimits(and(q.Filter, cutoff)); err != nil {
			return Query{}, fmt.Errorf("%w: cutoff exceeds combined filter limits: %w", ErrInvalidRestriction, err)
		}
	}

	return q, nil
}

// restriction returns a validated copy of the server restriction of a
// request, its timestamp literals cut to the profile precision; nil for no
// restriction. A refused restriction is [ErrInvalidRestriction] and never a
// field error.
func (s *Schema) restriction(node filter.Node) (filter.Node, error) {
	if nilcheck.IsNil(node) {
		return nil, nil //nolint:nilnil // no restriction
	}

	restriction, err := filter.Clone(node)
	if err == nil {
		err = s.checkContract(restriction)
	}

	if err == nil {
		err = filter.Validate(restriction, s.policy)
	}

	if err == nil {
		err = s.checkStoredEnums(restriction)
	}

	if err != nil {
		if fe, ok := errors.AsType[*coreerrs.FieldError](err); ok {
			err = fmt.Errorf("%s: %w", fe.Reason, fe.Err)
		}

		return nil, fmt.Errorf("%w: %w", ErrInvalidRestriction, err)
	}

	return restriction, nil
}

// checkStoredEnums rejects a comparison of an enum field with a literal that
// is none of its stored values: a server-built AST skips the parser, which
// rewrites proto enum names, so an enum name there would silently match
// nothing.
func (s *Schema) checkStoredEnums(root filter.Node) error {
	var err error

	filter.Walk(root, func(n filter.Node) bool {
		cmp, ok := n.(*filter.BinaryOpNode)
		if !ok {
			return true
		}

		id, ok := cmp.Left.(*filter.IdentNode)
		if !ok {
			return true
		}

		f, declared := s.fields.Get(id.Name)
		if !declared || f.enum == nil {
			return true
		}

		literals := []filter.Node{cmp.Right}
		if list, isList := cmp.Right.(*filter.ListNode); isList {
			literals = list.Elements
		}

		for _, l := range literals {
			lit, isLit := l.(*filter.LiteralNode)
			if !isLit || lit.Value == nil {
				continue
			}

			value, _, valid := fieldpolicy.NormalizeEnumValue(lit.Value)
			if !valid || !storesValue(f.enum.Values(), value) {
				err = coreerrs.Wrapf(filter.ErrEnumValueNotAllowed, "%s takes stored enum values, not proto enum names", id.Name)
				return false
			}
		}

		return true
	})

	return err
}

// storesValue reports whether value is one of the stored enum values.
func storesValue(values iter.Seq[any], value any) bool {
	for v := range values {
		if v == value {
			return true
		}
	}

	return false
}

// and returns left && right; a nil left is right alone.
func and(left, right filter.Node) filter.Node {
	if left == nil {
		return right
	}

	return &filter.BinaryOpNode{Op: filter.OpAnd, Left: left, Right: right}
}

// parseFilter parses a non-blank expr into a caller-owned AST. The filter
// parser turns the global form of a string function into the method form
// and enum value names into stored values; [Schema.checkContract] then
// applies the profile precision and the enabled AST checks before
// validation.
func (s *Schema) parseFilter(ctx context.Context, expr string) (filter.Node, error) {
	node, err := s.filterParser.Parse(ctx, expr)
	if err != nil {
		return nil, s.parseError(err)
	}

	if len(s.opts.serverOnlyFields) != 0 {
		if err = s.policy.CheckLimits(node); err != nil {
			return nil, s.TranslationError(err)
		}

		for n := range filter.AllNodes(node) {
			if id, ok := n.(*filter.IdentNode); ok && id != nil && slices.Contains(s.opts.serverOnlyFields, id.Name) {
				return nil, s.TranslationError(coreerrs.Wrapf(filter.ErrFieldNotAllowed, "%s", id.Name))
			}
		}
	}

	if err = s.checkContract(node); err != nil {
		return nil, s.formatFilterError(err)
	}

	return node, nil
}

// checkContract cuts the timestamp literals of a caller-owned AST to the
// profile precision in place and runs the enabled AST checks: string
// function targets, size() comparisons and presence tests.
func (s *Schema) checkContract(node filter.Node) error {
	var callErr error

	filter.Walk(node, func(n filter.Node) bool {
		switch n := n.(type) {
		case *filter.CallNode:
			if s.opts.callValidator != nil {
				callErr = s.opts.callValidator(n)
			}
		case *filter.LiteralNode:
			if t, ok := n.Value.(time.Time); ok {
				n.Value = s.storedTime(t)
			}
		}

		return callErr == nil
	})

	if callErr != nil {
		return callErr
	}

	if s.opts.sizeValidator != nil {
		if err := s.opts.sizeValidator(node, s); err != nil {
			return err
		}
	}

	if s.opts.presenceValidator != nil {
		return s.opts.presenceValidator(node, s)
	}

	return nil
}

// checkPresence rejects a null test (x == null, x != null) or has(x) on
// a required field: such a field is always stored, so the test has no
// contract meaning, and backends would read it differently (MongoDB by key,
// the others by value).
func checkPresence(node filter.Node, s *Schema) error {
	var err error

	filter.Walk(node, func(n filter.Node) bool {
		var target filter.Node

		switch x := n.(type) {
		case *filter.BinaryOpNode:
			if x.Op == filter.OpEqual || x.Op == filter.OpNotEqual {
				switch {
				case isNullLiteral(x.Right):
					target = x.Left
				case isNullLiteral(x.Left):
					target = x.Right
				}
			}
		case *filter.CallNode:
			if x.Op == filter.OpHas || x.Op == filter.OpExists {
				target = x.Target
			}
		}

		if id, ok := target.(*filter.IdentNode); ok {
			if f, declared := s.fields.Get(id.Name); declared && !f.Optional && !f.Array && !f.Presence {
				err = fieldError(FieldFilter, fmt.Sprintf("%s is always set; compare it with its zero value instead", id.Name), filter.ErrUnsupportedOperation)
			}
		}

		return err == nil
	})

	return err
}

// isNullLiteral reports whether n is the null literal.
func isNullLiteral(n filter.Node) bool {
	lit, ok := n.(*filter.LiteralNode)
	return ok && lit.Value == nil
}

// parseError reports a parse failure. The parser echoes the source line in
// its diagnostics, and the reason reaches the client status and the request
// logs; a filter may carry personal data (an INN), so only a generic reason
// is reported. An enum violation is the exception: its message names the
// field and the operator, never the literal.
func (s *Schema) parseError(err error) error {
	if errors.Is(err, filter.ErrEnumValueNotAllowed) || errors.Is(err, filter.ErrFieldTypeMismatch) ||
		errors.Is(err, filter.ErrUnsupportedOperation) {
		return s.FormatError(FieldFilter, err.Error(), err)
	}

	return s.FormatError(FieldFilter, "is not a valid filter expression", err)
}

// TranslationError reports a filter the validation or an engine's
// translator refuses as an InvalidArgument field error on filter. A
// translator echoes literal operands in its diagnostics (a malformed regex
// carries the pattern, which may be an INN); only a generic reason reaches
// the client status and the request logs, and err stays in the chain.
func (s *Schema) TranslationError(err error) error {
	return s.FormatError(FieldFilter, "cannot be translated into a query; check the field types and literals", err)
}

// checkSize enforces the size() contract on every comparison reached
// through !, && and ||: size() of a field is compared with an integer
// literal using ==, !=, <, <=, > or >=, and stands on the left. The
// MongoDB translator also takes a float or an unsigned operand; the contract
// keeps to integers. Both engines measure an absent or null value as size 0
// under every operator; an empty string or list of an Optional field is stored
// absent.
func checkSize(n filter.Node, schema *Schema) error {
	switch x := n.(type) {
	case *filter.UnaryOpNode:
		return checkSize(x.Operand, schema)
	case *filter.BinaryOpNode:
		if x.Op == filter.OpAnd || x.Op == filter.OpOr {
			if err := checkSize(x.Left, schema); err != nil {
				return err
			}

			return checkSize(x.Right, schema)
		}

		return checkSizeComparison(x, schema)
	default:
		return nil
	}
}

// checkSizeComparison checks one comparison (see [checkSize]); a comparison
// without size() passes.
func checkSizeComparison(n *filter.BinaryOpNode, schema *Schema) error {
	if isSizeCall(n.Right) {
		return fieldError(FieldFilter, sizeContract+" (size() on the left)", filter.ErrInvalidExpression)
	}

	call, ok := n.Left.(*filter.CallNode)
	if !ok || call.Op != filter.OpSize {
		return nil
	}

	if !n.Op.IsComparison() {
		return fieldError(FieldFilter, sizeContract, filter.ErrUnsupportedOperation)
	}

	_, isIdent := call.Target.(*filter.IdentNode)

	lit, isLit := n.Right.(*filter.LiteralNode)
	if !isIdent || !isLit {
		return fieldError(FieldFilter, sizeContract, filter.ErrInvalidExpression)
	}

	ident := call.Target.(*filter.IdentNode) //nolint:errcheck // checked to be an identifier above

	field, exists := schema.fields.Get(ident.Name)
	if exists && !field.Array && field.Kind != filter.FieldKindString {
		return fieldError(FieldFilter, sizeContract, filter.ErrUnsupportedOperation)
	}

	size, isInt := lit.Value.(int64)
	if !isInt {
		return fieldError(FieldFilter, sizeContract, filter.ErrFieldTypeMismatch)
	}

	if size < 0 {
		return fieldError(FieldFilter, sizeContract+" (operand must be non-negative)", filter.ErrInvalidExpression)
	}

	if size > math.MaxInt32 {
		return fieldError(FieldFilter, sizeContract+" (operand exceeds 32-bit range)", filter.ErrInvalidExpression)
	}

	return nil
}

// sizeContract is the reason of every refused size() comparison.
const sizeContract = "size() compares a field's size with an integer literal using ==, !=, <, <=, > or >="

// isSizeCall reports whether n is a size() call.
func isSizeCall(n filter.Node) bool {
	call, ok := n.(*filter.CallNode)
	return ok && call.Op == filter.OpSize
}

// stringFunctions are the CEL names of the string functions a filter may
// call in either form.
var stringFunctions = coremaps.NewImmutableMap(map[filter.Operator]string{
	filter.OpContains:   "contains",
	filter.OpStartsWith: "startsWith",
	filter.OpEndsWith:   "endsWith",
})

// checkGlobalCall rejects a string function that is not called on a field —
// a global call whose first argument is not a field, which the parser turns
// into a call on that argument — with a reason naming both forms.
func checkGlobalCall(n *filter.CallNode) error {
	name, ok := stringFunctions.Get(n.Op)
	if _, onField := n.Target.(*filter.IdentNode); onField || !ok {
		return nil
	}

	return fieldError(FieldFilter,
		fmt.Sprintf("%s() takes a field and a string: name.%s(\"x\") or %s(name, \"x\")", name, name, name), filter.ErrInvalidExpression)
}

// withDefaults ANDs node with the [WithDefaultEqualities] restrictions whose
// field node does not refer to; nil node is the empty filter.
func (s *Schema) withDefaults(node filter.Node) filter.Node {
	for _, d := range s.defaults {
		if node != nil && references(node, d.field) {
			continue
		}

		eq := &filter.BinaryOpNode{Op: filter.OpEqual, Left: &filter.IdentNode{Name: d.field}, Right: &filter.LiteralNode{Value: d.value}}
		if node == nil {
			node = eq
			continue
		}

		node = &filter.BinaryOpNode{Op: filter.OpAnd, Left: node, Right: eq}
	}

	return node
}

// references reports whether the AST refers to the public field name.
func references(root filter.Node, name string) bool {
	found := false

	filter.Walk(root, func(n filter.Node) bool {
		if id, ok := n.(*filter.IdentNode); ok && id.Name == name {
			found = true
		}

		return !found
	})

	return found
}

// FilterPolicy returns the filter policy the schema built from [Schema.FilterOptions]: the shared, backend-independent
// field rules in public names, including server-only fields. [Schema.Compile] enforces client access before
// combining predicates. Engines pass the policy to their translator or evaluator and bind it to their storage names
// there (filter.WithFieldMapping); it is immutable and safe to share.
func (s *Schema) FilterPolicy() *filter.Policy { return s.policy }

// FilterOptions returns the shared filter policy options of the schema:
// declared fields and kinds, the portable contract, safe regex patterns, array rules and AST limits.
// [Schema.FilterPolicy] is built from them; storage names are not among them.
func (s *Schema) FilterOptions() []filter.PolicyOption {
	names := slices.Sorted(s.fields.Keys())
	types := make(map[string]filter.FieldKind, s.fields.Len())

	var arrays []string

	for _, name := range names {
		f, _ := s.fields.Get(name)
		types[name] = f.Kind
		arrays = coreslices.AppendIf(arrays, f.Array, name)
	}

	return slices.Concat([]filter.PolicyOption{
		filter.WithPortableContract(true),
		filter.WithUntrustedInput(true),
		filter.WithSafeRegex(true),
		filter.WithAllowedFields(names...),
		filter.WithFieldTypes(types),
		filter.WithArrayFields(arrays...),
		filter.WithMaxDepth(s.opts.maxFilterDepth),
		filter.WithMaxOperations(s.opts.maxFilterOperations),
	}, s.opts.filterOptions)
}

// FormatError applies the profile hook to schema and engine request failures.
func (s *Schema) FormatError(field, reason string, cause error) error {
	return s.opts.errorFormatter(field, reason, cause)
}

func (s *Schema) formatFilterError(err error) error {
	if e, ok := errors.AsType[*coreerrs.FieldError](err); ok {
		return s.FormatError(e.Field, e.Reason, e.Err)
	}

	return s.FormatError(FieldFilter, err.Error(), err)
}
