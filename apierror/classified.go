// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package apierror

import (
	"errors"
	"fmt"
	"log/slog"
)

// Classified is implemented by an error that hides a cause from everything
// that classifies or renders it: its text and the errors it unwraps to
// (errors.Is, errors.As) are those of its classification alone, and the
// cause is reachable only through HiddenCause, for logs and diagnostics. A
// [Mapper], coreerrs.MapFieldPath, batch.ItemError or any other code that
// walks the error chain therefore never sees the cause, a FieldError,
// sentinel or text it holds included.
type Classified interface {
	error
	// HiddenCause returns the cause the error hides.
	HiddenCause() error
}

// Classify returns a [Classified] error that reads as category, unwraps to
// category alone and hides cause: a stored record that does not decode stays
// a server fault although its decoder returned a page-token, deadline or
// field error, and its stored values never reach a client. errors.Is(err,
// context.DeadlineExceeded) is false for such an error even when its cause
// is a deadline; [HiddenCause] returns the cause. The error is a
// slog.LogValuer that renders "<category>: <cause>"; every fmt verb and flag,
// %#v included, formats the category text alone. A nil cause returns
// category, a nil category cause.
func Classify(category, cause error) error {
	switch {
	case cause == nil:
		return category
	case category == nil:
		return cause
	}

	return &classifiedError{category: category, hidden: &hiddenCause{err: cause}}
}

// HiddenCause returns the cause hidden by the first [Classified] error in
// the tree of err, in the order errors.As searches it, or nil when err holds
// none. The mapped [*Error] keeps the original error as its cause, so
// HiddenCause reaches through it as well.
func HiddenCause(err error) error {
	if c, ok := errors.AsType[Classified](err); ok {
		return c.HiddenCause()
	}

	return nil
}

// classifiedError is the classified error of [Classify].
type classifiedError struct {
	category error
	// hidden holds the cause behind a pointer: fmt reflects the error's fields
	// when it reports a bad verb (%w outside fmt.Errorf), bypassing Format, and
	// renders a nested pointer as its address, so no cause value is printed.
	hidden *hiddenCause
}

// hiddenCause boxes the cause of a [classifiedError].
type hiddenCause struct{ err error }

func (e *classifiedError) Error() string      { return e.category.Error() }
func (e *classifiedError) Unwrap() error      { return e.category }
func (e *classifiedError) HiddenCause() error { return e.hidden.err }

// Format formats the category text alone with the verb, flags, width and
// precision of state, so %v, %+v, %#v, %q, %x and every other verb render no
// part of the cause: a value-type cause is never reflected.
func (e *classifiedError) Format(state fmt.State, verb rune) {
	_, _ = fmt.Fprintf(state, fmt.FormatString(state, verb), e.category.Error())
}

// GoString renders the category text as a Go string literal, as %#v does.
func (e *classifiedError) GoString() string { return fmt.Sprintf("%#v", e.category.Error()) }

// LogValue renders the category and the hidden cause for a server log.
func (e *classifiedError) LogValue() slog.Value {
	return slog.StringValue(e.category.Error() + ": " + e.hidden.err.Error())
}
