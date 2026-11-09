// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import (
	"errors"
	"fmt"

	"github.com/altessa-s/go-aip/filter"
)

// unknownError marks a per-record error that a null or absent operand causes and that the SQL translators render as SQL's
// unknown: an ordered comparison with null, a bare boolean or a negated operand that is null, size() or substring() of a null
// value that is not an array, a null string-function argument or substring() bound. && and || absorb
// it as SQL's AND and OR absorb unknown (unknown || true holds, unknown && false is false), ! keeps it, and a filter that is
// unknown as a whole does not match: [Evaluator.Evaluate] returns false without an error. It wraps the sentinel of the error it
// stands for, so errors.Is still finds it, and reads "<detail>: <sentinel>" as coreerrs.Wrap would.
//
// An evaluation over sparse data meets one on every record and usually absorbs it, so the unknowns with a fixed detail are built
// once ([newUnknown]) and shared, and the one whose detail holds per-record numbers is formatted only when read.
type unknownError struct {
	sentinel error
	text     string
	// substring holds the start, end and length of an out-of-range substring() when text is empty: Error formats them.
	substring [3]int64
}

func (u *unknownError) Error() string {
	if u.text == "" {
		return fmt.Sprintf("substring(%d, %d) out of range for string of length %d: %v",
			u.substring[0], u.substring[1], u.substring[2], u.sentinel)
	}

	return u.text
}

func (u *unknownError) Unwrap() error { return u.sentinel }

// newUnknown returns the data-driven unknown ([unknownError]) wrapping sentinel with detail.
func newUnknown(sentinel error, detail string) *unknownError {
	return &unknownError{sentinel: sentinel, text: detail + ": " + sentinel.Error()}
}

// substringOutOfRange returns the data-driven unknown of a substring() whose bounds fall outside a string of length runes.
func substringOutOfRange(start, end int64, length int) *unknownError {
	return &unknownError{sentinel: filter.ErrInvalidExpression, substring: [3]int64{start, end, int64(length)}}
}

// The shared unknowns: their detail is fixed, or names one of a few operators or functions.
var (
	errUnknownResult          = newUnknown(filter.ErrInvalidExpression, "expression must evaluate to bool, got null")
	errUnknownNotOperand      = newUnknown(filter.ErrInvalidExpression, "! requires bool operand, got null")
	errUnknownSizeTarget      = newUnknown(filter.ErrUnsupportedOperation, "size() not supported for null")
	errUnknownSubstringTarget = newUnknown(filter.ErrInvalidExpression, "substring() target is null")
	errUnknownIntArgument     = newUnknown(filter.ErrInvalidExpression, "argument is null")
	errUnknownNullComparisons = nullComparisonUnknowns()
)

// logicalOperator is && or ||: its spelling in error messages, the operand value that decides it and the unknown of a null
// operand.
type logicalOperator struct {
	name         string
	shortCircuit bool
	null         *unknownError
}

var (
	logicalAnd = newLogicalOperator("&&", false)
	logicalOr  = newLogicalOperator("||", true)
)

func newLogicalOperator(name string, shortCircuit bool) logicalOperator {
	return logicalOperator{
		name:         name,
		shortCircuit: shortCircuit,
		null:         newUnknown(filter.ErrInvalidExpression, name+" requires bool operands, got null"),
	}
}

// stringFunction labels a string function in error messages and holds the unknown of its null argument.
type stringFunction struct {
	label string
	null  *unknownError
}

var (
	stringPredicate = newStringFunction("string function")
	matchesFunction = newStringFunction("matches()")
)

func newStringFunction(label string) stringFunction {
	return stringFunction{label: label, null: newUnknown(filter.ErrInvalidExpression, label+" argument is null")}
}

// nullComparisonUnknowns returns, by operator, the unknown of a comparison with null that is neither == nor !=.
func nullComparisonUnknowns() []*unknownError {
	unknowns := make([]*unknownError, filter.OpSubstring+1)
	for op := range unknowns {
		unknowns[op] = nullComparison(filter.Operator(op))
	}

	return unknowns
}

// nullComparisonUnknown returns the unknown of an ordered comparison op with null.
func nullComparisonUnknown(op filter.Operator) *unknownError {
	if int(op) < len(errUnknownNullComparisons) {
		return errUnknownNullComparisons[op]
	}

	return nullComparison(op)
}

func nullComparison(op filter.Operator) *unknownError {
	return newUnknown(filter.ErrUnsupportedOperation, "cannot use "+op.String()+" with nil")
}

// isUnknown reports whether err is a data-driven unknown ([unknownError]) that the evaluation absorbs.
func isUnknown(err error) bool {
	if err == nil {
		return false
	}

	if _, ok := err.(*unknownError); ok { //nolint:errorlint // the unknowns are returned unwrapped; errors.AsType covers the rest
		return true
	}

	_, ok := errors.AsType[*unknownError](err)

	return ok
}
