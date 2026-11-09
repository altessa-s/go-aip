// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package state

import (
	"errors"
	"strings"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Sentinel errors classify failures independently of diagnostic text.
var (
	// ErrTransitionNotAllowed matches every *[TransitionError].
	ErrTransitionNotAllowed = errors.New("state: transition not allowed")
	// ErrInvalidTable reports a transition table [New] rejects: empty, or
	// holding the zero value or a number the enum does not declare.
	ErrInvalidTable = errors.New("state: invalid transition table")
	// ErrInvalidField reports a resource [Machine.Transition] cannot read: nil,
	// or without a singular state field of the Machine's enum.
	ErrInvalidField = errors.New("state: invalid state field")
	// ErrNotOutputOnly reports a state field [CheckOutputOnly] finds writable.
	ErrNotOutputOnly = errors.New("state: state field is not OUTPUT_ONLY")
)

// TransitionError reports a transition the current state does not allow. It
// matches [ErrTransitionNotAllowed] and [coreerrs.ErrFailedPrecondition]
// (FAILED_PRECONDITION, as AIP-216 requires).
type TransitionError struct {
	// Method is the state-changing method, empty for [Machine.Check].
	Method string
	// From is the name of the current state.
	From string
	// To is the name of the requested state.
	To string
	// Terminal is true when the current state allows no transition at all.
	Terminal bool
}

// Error describes the refused transition.
func (e *TransitionError) Error() string {
	var sb strings.Builder

	sb.WriteString("state: ")

	if e.Method != "" {
		sb.WriteString(e.Method)
		sb.WriteString(": ")
	}

	sb.WriteString("cannot move from " + e.From + " to " + e.To)

	if e.Terminal {
		sb.WriteString(": " + e.From + " is a terminal state")
	}

	return sb.String()
}

// Unwrap returns [ErrTransitionNotAllowed] and
// [coreerrs.ErrFailedPrecondition].
func (e *TransitionError) Unwrap() []error {
	return []error{ErrTransitionNotAllowed, coreerrs.ErrFailedPrecondition}
}
