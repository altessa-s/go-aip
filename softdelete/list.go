// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package softdelete

import "github.com/altessa-s/go-aip/filter"

// Restriction returns the AIP-164 show_deleted rule of a List as a
// server restriction: "<field> == null" (the [WithFilterField] name,
// deleteTime by default), which excludes soft-deleted resources, or nil
// when showDeleted is true. Pass it as listing.Request.Restriction: the
// listing schema ANDs it onto the client's filter after the default
// equalities (a blank filter included), validates it, and every listing
// engine applies it and binds it into the page token, so a token of one
// show_deleted value is rejected with the other (AIP-158).
//
//	req := listing.RequestFromProto(msg)
//	req.Restriction = policy.Restriction(msg.GetShowDeleted())
//	page, err := books.List(ctx, req)
//
// Declare the field in the list schema as a nullable timestamp
// (listing.Field{Name: "deleteTime", Kind: filter.FieldKindTimestamp,
// Presence: true}, or Optional); otherwise listing refuses the restriction
// with listing.ErrInvalidRestriction. The node is new on every call and
// the caller may keep or share it: listing never modifies a restriction.
func (p *Policy) Restriction(showDeleted bool) filter.Node {
	return p.Restrict(nil, showDeleted)
}

// Restrict applies the AIP-164 show_deleted rule to a List filter AST the
// caller holds (its own translator or evaluator, a store adapter): unless
// showDeleted is true it returns node ANDed with "<field> == null" (the
// [WithFilterField] name, deleteTime by default), which excludes
// soft-deleted resources; with showDeleted it returns node unchanged. A nil
// node is the empty filter, so the result is the predicate alone. With the
// listing engines use [Policy.Restriction] instead.
//
// node is the validated AST of the client's AIP-160 filter; it is not
// modified, only referenced by the new root. Run the result through
// [filter.Validate] or the engine's translator as usual: the field must be
// allowed by the filter policy and declared nullable. Bind the page token to
// the restricted filter (its canonical form changes with showDeleted), so a
// token of one show_deleted value is not accepted with the other (AIP-158).
func (p *Policy) Restrict(node filter.Node, showDeleted bool) filter.Node {
	if showDeleted {
		return node
	}

	notDeleted := &filter.BinaryOpNode{
		Op:    filter.OpEqual,
		Left:  &filter.IdentNode{Name: p.opts.filterField},
		Right: &filter.LiteralNode{Value: nil},
	}
	if node == nil {
		return notDeleted
	}

	return &filter.BinaryOpNode{Op: filter.OpAnd, Left: node, Right: notDeleted}
}
