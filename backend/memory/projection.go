// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package memory

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/projection"
)

// Projection is a resolved selection ready to apply to documents: a
// map[string]any tree with [Projection.Apply] or a proto message with
// [Projection.ApplyProto]. It follows the MongoDB projection rules, so an
// in-memory store returns what a MongoDB collection would. A Projection is
// immutable and safe for concurrent use.
type Projection struct {
	sel  projection.Selection
	tree *node
	// mask is the fieldmask of an inclusion or exclusion, built on the
	// first ApplyProto.
	mask func() fieldmask.FieldMask
}

// node is one segment of the selected paths; a leaf selects its whole
// subtree.
type node struct {
	leaf     bool
	children map[string]*node
}

func newProjection(sel projection.Selection) *Projection {
	p := &Projection{sel: sel}

	paths := sel.Include
	if len(sel.Exclude) > 0 {
		paths = sel.Exclude
	}

	if len(paths) > 0 {
		p.tree = buildTree(paths)
		p.mask = sync.OnceValue(func() fieldmask.FieldMask { return fieldmask.FromPaths(paths...) })
	}

	return p
}

// buildTree builds the path tree; a path under a leaf adds nothing.
func buildTree(paths []string) *node {
	root := &node{}

	for _, p := range paths {
		n := root
		for seg := range strings.SplitSeq(p, ".") {
			if n.leaf {
				break
			}

			if n.children == nil {
				n.children = make(map[string]*node)
			}

			child, ok := n.children[seg]
			if !ok {
				child = &node{}
				n.children[seg] = child
			}

			n = child
		}

		n.leaf, n.children = true, nil
	}

	return root
}

// Selection returns the resolved selection in storage names.
func (p *Projection) Selection() projection.Selection {
	return projection.Selection{Mode: p.sel.Mode, Include: slices.Clone(p.sel.Include), Exclude: slices.Clone(p.sel.Exclude)}
}

// Apply returns the projection of doc as a new map; doc is never modified.
// Unselected values are not copied: the result shares the selected
// subtrees with doc.
//
//   - Inclusion keeps the selected paths. A dotted path into a nested map
//     keeps that key and an empty map when it is missing; into a list it
//     selects per element, keeping the map elements (nested lists are
//     walked) and dropping the others; into any other value it drops the
//     value.
//   - Exclusion removes the selected paths, the same way into maps and
//     lists, and leaves a value of another shape as it is.
//   - ModeNone returns an empty map; every field returns a copy of doc.
//
// Lists are []any or []map[string]any; any other value is a leaf. A nil doc
// returns nil.
func (p *Projection) Apply(doc map[string]any) map[string]any {
	switch {
	case doc == nil:
		return nil
	case p.sel.Mode == projection.ModeNone:
		return map[string]any{}
	case len(p.sel.Include) > 0:
		return include(doc, p.tree)
	case len(p.sel.Exclude) > 0:
		return exclude(doc, p.tree)
	default:
		return maps.Clone(doc)
	}
}

// ApplyProto applies the projection to msg in place with the fieldmask
// package: [fieldmask.FieldMask.Filter] for an inclusion,
// [fieldmask.FieldMask.Prune] for an exclusion and
// [fieldmask.FieldMask.Select] with an empty mask for ModeNone, which
// clears msg; every field leaves msg as it is. Storage names must then be
// proto field paths of msg; a path msg does not declare fails with an error
// matching [projection.ErrInvalidFieldPath] and leaves msg untouched.
func (p *Projection) ApplyProto(msg proto.Message) error {
	var err error

	switch {
	case msg == nil:
		return nil
	case p.sel.Mode == projection.ModeNone:
		err = fieldmask.FieldMask{}.Select(msg)
	case len(p.sel.Include) > 0:
		err = p.mask().Filter(msg)
	case len(p.sel.Exclude) > 0:
		err = p.mask().Prune(msg)
	}

	if err != nil {
		return fmt.Errorf("%w: %w", projection.ErrInvalidFieldPath, err)
	}

	return nil
}

// include keeps the paths of n in doc.
func include(doc map[string]any, n *node) map[string]any {
	out := make(map[string]any, len(n.children))

	for key, child := range n.children {
		v, ok := doc[key]
		if !ok {
			continue
		}

		if child.leaf {
			out[key] = v
			continue
		}

		if sub, ok := includeValue(v, child); ok {
			out[key] = sub
		}
	}

	return out
}

// includeValue keeps the paths of n in v, reporting false for a value the
// paths cannot address.
func includeValue(v any, n *node) (any, bool) {
	switch v := v.(type) {
	case map[string]any:
		return include(v, n), true
	case []map[string]any:
		out := make([]map[string]any, len(v))
		for i, e := range v {
			out[i] = include(e, n)
		}

		return out, true
	case []any:
		out := make([]any, 0, len(v))
		for _, e := range v {
			if sub, ok := includeValue(e, n); ok {
				out = append(out, sub)
			}
		}

		return out, true
	default:
		return nil, false
	}
}

// exclude removes the paths of n from a copy of doc.
func exclude(doc map[string]any, n *node) map[string]any {
	out := maps.Clone(doc)

	for key, child := range n.children {
		v, ok := doc[key]
		if !ok {
			continue
		}

		if child.leaf {
			delete(out, key)
			continue
		}

		out[key] = excludeValue(v, child)
	}

	return out
}

// excludeValue removes the paths of n from v, copying what it changes.
func excludeValue(v any, n *node) any {
	switch v := v.(type) {
	case map[string]any:
		return exclude(v, n)
	case []map[string]any:
		out := make([]map[string]any, len(v))
		for i, e := range v {
			out[i] = exclude(e, n)
		}

		return out
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = excludeValue(e, n)
		}

		return out
	default:
		return v
	}
}
