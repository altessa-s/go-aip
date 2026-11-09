// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import (
	"sync"

	"github.com/altessa-s/go-aip/filter"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// evaluationPool recycles per-call state so Evaluate does not allocate it.
var evaluationPool = sync.Pool{New: func() any { return new(evaluation) }}

// Lookup returns the value of field in data as a predicate of [Evaluator.Evaluate] reads it, for a caller that needs the same
// value outside a filter (an in-memory backend sorting by the field): the field's mapped path is resolved under the evaluator's
// options. Under [WithProfile] the profile reads it when it implements [FieldAccessor]. Otherwise Lookup reads the field as the
// default Evaluate does: a field declared by [filter.WithArrayFields] through lists of documents (the list of the values found),
// any other through map[string]any only. Either way value and present are what the field's predicates and has() see; present
// reports whether the path exists (a null value included). It fails with [filter.ErrFieldNotAllowed] for a field outside the
// allow-list and with [filter.ErrDataLimitExceeded] when reading the data exceeds the budget ([WithMaxDataBudget]).
func (e *Evaluator) Lookup(data map[string]any, field string) (value any, present bool, err error) {
	if e.config.accessor != nil {
		return e.config.accessor.Lookup(e.config.Policy, data, field, e.config.maxDataBudget, e.config.valueDecoder)
	}

	if !e.config.IsFieldAllowed(field) {
		return nil, false, coreerrs.Wrapf(filter.ErrFieldNotAllowed, "%s", field)
	}

	ev := newEvaluation(e.config, data)
	// As VisitIdent and evalHas read a field: a declared array field through lists of documents, any other through maps.
	mapped := e.config.ApplyFieldMapping(field)
	if e.config.IsArrayField(field) {
		value, present, _ = ev.ResolvePath(ev.data, mapped)
	} else {
		value, present = ev.WalkField(mapped)
	}

	err = ev.Err()
	ev.release()

	if err != nil {
		return nil, false, err
	}

	return value, present, nil
}

// evalHas checks if a field exists (a null value included) in the data; the path of an array field ([filter.WithArrayFields]) exists
// when a document of a list on the way holds the rest of it, as $exists reads it.
func (e *evaluation) evalHas(n *filter.CallNode) (any, error) {
	ident, ok := n.Target.(*filter.IdentNode)
	if !ok {
		return nil, coreerrs.Wrap(filter.ErrInvalidExpression, "has() requires an identifier")
	}

	field := ident.Name
	if !e.config.IsFieldAllowed(field) {
		return nil, coreerrs.Wrapf(filter.ErrFieldNotAllowed, "%s", field)
	}

	mapped := e.config.ApplyFieldMapping(field)

	var present bool

	if e.config.IsArrayField(field) {
		value, exists, crossed := e.ResolvePath(e.data, mapped) // through lists of documents, as $exists

		present = exists
		if e.config.PortableContract() && !crossed {
			present = exists && hasArrayFirstElement(value)
		}
	} else {
		_, present = e.WalkField(mapped)
	}

	return present, e.Err()
}
