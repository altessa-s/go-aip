// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"sync"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-mongox/bsonbound"

	"github.com/altessa-s/go-aip/filter"
)

// translate translates a validated node into a MongoDB filter. TranslateD emits ordered documents, so the page token, which
// binds the filter's BSON bytes, does not depend on map order. The presence rewrite runs inside the translator, after the limits
// and the allow-list are checked on node: the nodes it adds are not the client's, so they do not count against the operation
// budget [listing.Schema.Compile] and the other engines enforce.
func (l *ListEngine[D]) translate(node filter.Node) (bson.D, error) {
	if node == nil {
		return nil, nil
	}

	var (
		d   bson.D
		err error
	)

	if len(l.presence) != 0 {
		d, err = l.translator.translateRewritten(node, func(node filter.Node) filter.Node {
			return presenceForm(filter.NegationNormalForm(node, l.arrays, l.presence), l.presence)
		})
	} else {
		d, err = l.translator.TranslateD(node)
	}

	if err != nil {
		return nil, l.schema.TranslationError(err)
	}

	return d, nil
}

// presenceForm rewrites node, a filter in negation normal form over the presence fields (filter.NegationNormalForm, which
// complements a negated comparison of a presence field and turns a negated has() into a null test), into the predicates whose
// MongoDB meaning is the listing contract of a Presence field: null equals null and differs from every value, is in no list and
// holds no string — the MongoDB meaning of ==, !=, in and the string functions, negated or not — while an ordering comparison
// with null is unknown, also under negation — the MongoDB meaning of a comparison once its negation is complemented away. The
// remaining has() of a presence field becomes `field != null`, which a null value fails. node is a fresh copy, rewritten in
// place.
func presenceForm(node filter.Node, presence map[string]bool) filter.Node {
	switch n := node.(type) {
	case *filter.BinaryOpNode:
		n.Left = presenceForm(n.Left, presence)
		n.Right = presenceForm(n.Right, presence)
	case *filter.UnaryOpNode:
		n.Operand = presenceForm(n.Operand, presence)
	case *filter.CallNode:
		if ident, ok := n.Target.(*filter.IdentNode); ok && (n.Op == filter.OpHas || n.Op == filter.OpExists) && presence[ident.Name] {
			return &filter.BinaryOpNode{Op: filter.OpNotEqual, Left: ident, Right: &filter.LiteralNode{}}
		}
	}

	return node
}

// and returns the conjunction of scope and filter; nil when both are empty.
func and(scope, filter bson.D) bson.D {
	switch {
	case len(scope) == 0:
		return filter
	case len(filter) == 0:
		return scope
	default:
		return bson.D{{Key: "$and", Value: bson.A{scope, filter}}}
	}
}

// listRegistry is the registry an engine without [WithListRegistry] encodes its filters and scope pins with.
var listRegistry = sync.OnceValue(NewRegistry)

// marshal encodes doc with reg, bounded to [maxDocumentBytes] and [maxWriterNesting] levels: a value that expands as it encodes
// fails as soon as it passes the bound instead of being built in memory.
func marshal(doc bson.D, reg *bson.Registry) (bson.Raw, error) {
	return bsonbound.Marshal(doc, maxDocumentBytes, maxWriterNesting, func(enc *bson.Encoder) { enc.SetRegistry(reg) })
}
