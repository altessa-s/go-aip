// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/pagination/pagetoken"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Request fields a list error names: every invalid request is an
// InvalidArgument [coreerrs.FieldError] on one of them.
const (
	FieldFilter  = "filter"
	FieldOrderBy = "orderBy"
	FieldParent  = "parent"
)

// Request is the common shape of a List request (AIP-132, AIP-158, AIP-160).
type Request struct {
	// PageSize is the requested page size: 0 is the schema's default
	// ([WithDefaultPageSize]), more than its maximum ([WithMaxPageSize]) is
	// clamped, negative is invalid.
	PageSize int32
	// PageToken continues a previous page; empty reads the first page. It
	// is bound to the RPC, the parent, the filter (with the restriction)
	// and the order.
	PageToken string
	// Filter is a CEL expression over the schema's fields (AIP-160); empty
	// matches everything.
	Filter string
	// OrderBy is a declared sort pattern or its full reversal (AIP-132);
	// empty is the schema's default.
	OrderBy string
	// Parent is the id of the parent resource of a child list; empty for a
	// top-level list.
	Parent string
	// ReadMask lists the public field paths the response returns to the
	// caller (an AIP-157 read mask, e.g. aip/projection Spec.Paths); a
	// path covers itself and the fields below it. Empty or "*" returns
	// every field. When a resource view selects the fields instead — no
	// mask, aip/projection Policy.Select — pass the paths of the selected
	// Fields, so the check below sees what the response returns. Unless the
	// schema allows it ([WithUnreadableSortAllowed]), an order with a key the
	// mask does not cover is a field error on orderBy
	// ([ErrUnreadableSortField]): the page token would disclose it.
	ReadMask []string
	// Restriction is a server-side predicate over the schema's public field
	// names that every item of the list must satisfy, whatever the client
	// asks for: an AIP-164 show_deleted rule (softdelete.Policy.Restriction),
	// a tenant or caller scope. Compile ANDs it onto the client's filter
	// after the default equalities, so a client filter can only narrow it,
	// never widen it, and a blank filter still gets it. The handler sets it;
	// it never comes from the request message ([RequestFromProto] leaves it
	// nil). nil (or a typed nil node) restricts nothing.
	//
	// The restriction is trusted server input, built as an AST that skips
	// the filter parser: enum fields take their stored values, not proto
	// enum names. It obeys the same contract as a client filter — the
	// schema's filter policy (declared fields, kinds, contract rules) and the
	// profile's call, size() and presence checks — but a refusal is
	// [ErrInvalidRestriction] (a server fault, INTERNAL), never a field
	// error on filter. It is cloned and its timestamp literals are cut to the
	// profile precision; the caller's node is never modified, so one node may
	// serve concurrent requests. The combined filter must stay within the
	// filter depth and operation limits; exceeding them only together with
	// the client's filter is a field error on filter. Use only operations
	// every engine of the list translates (equalities, null tests, ranges).
	//
	// The page token binds the canonical combined filter, so a token issued
	// under one restriction (show_deleted false) is rejected under another
	// (show_deleted true). The restriction must therefore be the same for
	// every page of a walk: derive it from the request and the caller, never
	// from the clock.
	Restriction filter.Node
	// Cutoff is a trusted server predicate that may narrow the visible rows between
	// pages, for example as a restore window expires. List and Count apply it before
	// pagination. It has the validation and copy semantics of Restriction. A nil
	// or typed nil cutoff admits every row. Tokens omit it because removing rows
	// changes neither the order keys nor the continuation position. Use Restriction
	// for any change that must invalidate an existing walk, including caller scope.
	Cutoff filter.Node
}

// pageSize resolves the page size of req under the schema's limits.
func (s *Schema) pageSize(req Request) (int, error) {
	resolve := s.opts.pageSizeResolver
	if resolve == nil {
		resolve = pagetoken.ResolvePageSize
	}

	size, err := resolve(req.PageSize, s.opts.defaultPageSize, s.opts.maxPageSize)
	if err != nil {
		if e, ok := errors.AsType[*coreerrs.FieldError](err); ok {
			return 0, s.FormatError(e.Field, e.Reason, e.Err)
		}

		return 0, s.FormatError(pagetoken.SizeField, err.Error(), err)
	}

	if size <= 0 || size == int(^uint(0)>>1) {
		return 0, s.FormatError(pagetoken.SizeField, "resolved page size must be positive and permit a lookahead row", ErrInvalidSchema)
	}

	return size, nil
}

// checkReadable rejects an order with a key the read mask does not cover.
func (s *Schema) checkReadable(order Order, mask []string) error {
	if len(mask) == 0 || s.opts.unreadableSortAllowed || slices.Contains(mask, "*") {
		return nil
	}

	for _, k := range order.keys {
		if !slices.ContainsFunc(mask, func(p string) bool { return covers(p, k.Field) }) {
			return s.FormatError(FieldOrderBy,
				fmt.Sprintf("orders by %s, which the read mask omits; read it or choose another order", k.Field), ErrUnreadableSortField)
		}
	}

	return nil
}

// covers reports whether the read-mask path covers the field: it is the field
// or one of its parents.
func covers(path, field string) bool {
	rest, ok := strings.CutPrefix(field, path)
	return ok && (rest == "" || rest[0] == '.')
}

// fieldError returns an InvalidArgument field error on field keeping cause in
// the chain (errors.Is still matches the sentinel).
func fieldError(field, reason string, cause error) error {
	return &coreerrs.FieldError{Field: field, Reason: reason, Err: cause}
}
