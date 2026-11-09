// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo

import (
	"maps"
	"regexp"
	"slices"
	"time"
)

// bsonPath is the syntax of a BSON path: dotted identifiers.
var bsonPath = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// checkOptions rejects a tie-breaker that is not a BSON path, a cursor precision that is not a whole number of BSON
// milliseconds, a negative time cap, a schema page size beyond [MaxListPageSize] and a parent key that is not a scope key.
func (l *ListEngine[D]) checkOptions() error {
	switch {
	case !bsonPath.MatchString(l.opts.tiebreakPath):
		return invalid("invalid tie-breaker path %q", l.opts.tiebreakPath)
	case l.opts.timestampPrecision < time.Millisecond || l.opts.timestampPrecision%time.Millisecond != 0:
		return invalid("timestamp precision must be a positive multiple of a BSON millisecond")
	case l.opts.maxTime < 0:
		return invalid("negative query timeout")
	case l.schema.MaxPageSize() > MaxListPageSize:
		return invalid("maximum page size %d exceeds %d, the largest page a MongoDB list reads", l.schema.MaxPageSize(), MaxListPageSize)
	case l.opts.parentKey != "" && !slices.Contains(l.opts.scopeKeys, l.opts.parentKey):
		return invalid("parent key must be a declared scope key")
	default:
		return nil
	}
}

// checkScopeKeys rejects a scope key that is not a BSON path or is the id ([WithListScopeKeys] already drops repeats): each
// prefixes every pattern index once, before the sort that ends on the id.
func (l *ListEngine[D]) checkScopeKeys() error {
	for _, k := range l.opts.scopeKeys {
		if !bsonPath.MatchString(k) || k == l.opts.tiebreakPath {
			return invalid("scope key %q", k)
		}
	}

	return nil
}

// checkPaths rejects a field or sort path of an undeclared field or that is not a BSON path, and a sort path on the id. Fields
// are checked in name order, so the same configuration always reports the same error.
func (l *ListEngine[D]) checkPaths() error {
	for _, name := range slices.Sorted(maps.Keys(l.opts.fieldPaths)) {
		p := l.opts.fieldPaths[name]
		if _, ok := l.schema.Field(name); !ok {
			return invalid("field path of %q: the field is not declared", name)
		}

		if !bsonPath.MatchString(p) && p != l.opts.tiebreakPath {
			return invalid("field %q: path %q", name, p)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(l.opts.sortPaths)) {
		p := l.opts.sortPaths[name]
		if _, ok := l.schema.Field(name); !ok {
			return invalid("sort path of %q: the field is not declared", name)
		}

		if !bsonPath.MatchString(p) || p == l.opts.tiebreakPath {
			return invalid("field %q: sort path %q", name, p)
		}
	}

	return nil
}
