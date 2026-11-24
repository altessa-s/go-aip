// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation

import (
	"strings"
	"time"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
)

// contractEvaluator is the state of one evaluation: the program's policy and reader and the record's budget. The budget is held
// by value: escape analysis does not tell the fields of a struct apart, so a pointer to it next to the reader would move it to the
// heap on every record.
type contractEvaluator struct {
	policy    *filter.Policy
	reader    FieldReader
	budget    readBudget
	precision time.Duration
	bounded   bool // whether budget applies
}

// charge charges n units of work to a bounded evaluation.
func (e *contractEvaluator) charge(n int) bool { return !e.bounded || e.budget.Charge(n) }

type truth uint8

const (
	falseValue truth = iota
	trueValue
	unknownValue
)

// not is the Kleene negation: unknown stays unknown.
func (t truth) not() truth {
	switch t {
	case trueValue:
		return falseValue
	case falseValue:
		return trueValue
	default:
		return unknownValue
	}
}

func (e *contractEvaluator) matches(x *expr, data map[string]any) (truth, error) {
	if !e.charge(1) {
		return falseValue, filter.ErrDataLimitExceeded
	}

	switch x.kind {
	case exprNot:
		v, err := e.matches(x.left, data)
		return v.not(), err
	case exprAnd:
		return e.junction(x, data, falseValue)
	case exprOr:
		return e.junction(x, data, trueValue)
	case exprIn:
		return e.in(x.in, data)
	case exprCompare:
		return e.compare(x.compare, data)
	case exprIdent:
		value, err := e.field(x.ref, data)
		if value == nil {
			return unknownValue, err
		}

		if value == true {
			return trueValue, err
		}

		return falseValue, err
	case exprCall:
		return e.call(x.call, data)
	default:
		return falseValue, x.err
	}
}

// junction evaluates AND (absorbing is false) or OR (absorbing is true) in Kleene logic. The right operand is not evaluated when
// the left one is absorbing or fails; an unknown operand absorbs the other's identity value.
func (e *contractEvaluator) junction(x *expr, data map[string]any, absorbing truth) (truth, error) {
	left, err := e.matches(x.left, data)
	if err != nil || left == absorbing {
		return left, err
	}

	right, err := e.matches(x.right, data)
	if err != nil {
		return falseValue, err
	}

	if left == unknownValue && right != absorbing {
		return unknownValue, nil
	}

	return right, nil
}

func (e *contractEvaluator) call(x *callExpr, data map[string]any) (truth, error) {
	value, err := e.field(x.ref, data)
	if err != nil {
		return falseValue, err
	}

	field := x.ref.info
	if x.op == filter.OpHas || x.op == filter.OpExists {
		if field.Array {
			if len(elements(value)) != 0 {
				return trueValue, nil
			}

			return falseValue, nil
		}

		if !field.Presence {
			if !equal(value, x.ref.zero) {
				return trueValue, nil
			}

			return falseValue, nil
		}

		if value == nil {
			return falseValue, nil
		}

		return trueValue, nil
	}

	if x.err != nil {
		return falseValue, x.err
	}

	if field.Presence && value == nil {
		// A string function finds nothing in a null value, as in the default evaluator, so its negation holds.
		return falseValue, nil
	}

	if e.anyElement(value, field.Array, func(item any) bool { return e.stringMatch(x, item) }) {
		return trueValue, nil
	}

	return falseValue, nil
}

// stringMatch applies the string function of x to one value, charging the length of both strings.
func (e *contractEvaluator) stringMatch(x *callExpr, item any) bool {
	text, ok := item.(string)
	if !e.charge(len(text) + x.needleLength) {
		return false
	}

	if !ok {
		return false
	}

	switch x.op {
	case filter.OpContains:
		if x.fold {
			text = evaluator.SimpleLower15_1(text)
		}

		return strings.Contains(text, x.needle)
	case filter.OpStartsWith:
		return strings.HasPrefix(text, x.needle)
	case filter.OpEndsWith:
		return strings.HasSuffix(text, x.needle)
	case filter.OpMatches:
		return x.matcher.MatchString(text)
	default:
		return false
	}
}

func truthOf(ok bool) truth {
	if ok {
		return trueValue
	}

	return falseValue
}

func (e *contractEvaluator) in(x *inExpr, data map[string]any) (truth, error) {
	if x.array {
		if x.err != nil {
			return falseValue, x.err
		}

		array, err := e.field(x.ref, data)
		if err != nil {
			return falseValue, err
		}

		return truthOf(e.anyElement(array, true, func(item any) bool { return scalarCompare(filter.OpEqual, item, x.value) })), nil
	}

	value, err := e.field(x.ref, data)
	if err != nil {
		return falseValue, err
	}

	if x.listErr != nil {
		return falseValue, x.listErr
	}

	field := x.ref.info
	if field.Presence && value == nil {
		// A null value is in a list only with a null element, as in the default evaluator.
		for i := range x.elements {
			if el := &x.elements[i]; el.literalErr != nil || el.literal == nil {
				return trueValue, el.literalErr
			}
		}

		return falseValue, nil
	}

	for i := range x.elements {
		el := &x.elements[i]
		if el.candidateErr != nil {
			return falseValue, el.candidateErr
		}

		var found bool
		if field.Array {
			found = e.anyElement(value, true, func(item any) bool { return scalarCompare(filter.OpEqual, item, el.candidate) })
		} else {
			found = scalarCompare(filter.OpEqual, value, el.candidate)
		}

		if found {
			return trueValue, nil
		}
	}

	return falseValue, nil
}

func (e *contractEvaluator) anyElement(value any, array bool, predicate func(any) bool) bool {
	if array {
		for _, item := range elements(value) {
			if !e.charge(1) {
				return false
			}

			if predicate(item) {
				return true
			}
		}

		return false
	}

	return predicate(value)
}
