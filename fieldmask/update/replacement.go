// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"strconv"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/internal/behavior"
	"github.com/altessa-s/go-aip/internal/maskmerge"
	"github.com/altessa-s/go-aip/internal/maskpath"
)

// checkReplacement defers a whole collection or atomic value with protected
// descendants when no stored resource is available. A mask cannot express a
// replacement that preserves these descendants while changing membership or
// crossing an atomic boundary. Prepare captures that replacement for completion
// with current state, when the collection's protected values are compared.
func (u *updateCheck) checkReplacement(fd protoreflect.FieldDescriptor, path string) bool {
	md := maskpath.ValueMessage(fd)
	if md == nil || (!fd.IsList() && !fd.IsMap() && !isAtomicValue(fd, u.policy)) ||
		!holdsImmutable(md, map[protoreflect.FullName]bool{}, u.policy) {
		return true
	}

	if !u.verify {
		u.deferReplacement(md, path)
		return false
	}

	return true
}

// checkElementReplacement is checkReplacement for an element named whole.
func (u *updateCheck) checkElementReplacement(
	fd protoreflect.FieldDescriptor, path string,
) bool {
	md := maskpath.ValueMessage(fd)
	if md == nil || !holdsImmutable(md, map[protoreflect.FullName]bool{}, u.policy) {
		return true
	}

	if !u.verify {
		u.deferReplacement(md, path)
		return false
	}

	return true
}

// replacementPairs includes request elements and stored elements removed by a
// whole replacement. elementPairs alone follows only in-place merge targets.
func replacementPairs(fd protoreflect.FieldDescriptor, pairs []msgPair) []msgPair {
	out := elementPairs(fd, maskpath.Wildcard, fieldmask.FieldMask{}, pairs)

	for _, p := range pairs {
		path := behavior.JoinPath(p.path, string(fd.Name()))
		if fd.IsList() {
			r, c := maskmerge.ListOf(p.req, fd), maskmerge.ListOf(p.cur, fd)
			if maskmerge.ListLen(r) == 0 {
				continue // elementPairs already included every stored element
			}

			for i := maskmerge.ListLen(r); i < maskmerge.ListLen(c); i++ {
				out = append(out, msgPair{cur: c.Get(i).Message(), path: behavior.JoinPath(path, strconv.Itoa(i))})
			}

			continue
		}

		r, c := maskmerge.MapOf(p.req, fd), maskmerge.MapOf(p.cur, fd)
		if c == nil {
			continue
		}

		for _, k := range behavior.SortedMapKeys(c) {
			if r == nil || !r.Has(k) {
				out = append(out, msgPair{cur: c.Get(k).Message(), path: elementPath(path, k.String())})
			}
		}
	}

	return out
}
