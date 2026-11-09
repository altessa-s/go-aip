// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package state implements the resource states of AIP-216: a transition table for a generated state enum, the core of state-changing
// custom methods, and descriptor checks for consumer tests.
//
// A Machine holds the transitions a caller declares for an enum such as Book_State: only declared transitions are allowed, a state into
// itself included, and a state without targets is terminal. Check refuses any other transition with a *TransitionError, which maps to
// FAILED_PRECONDITION as AIP-216 requires. Transition reads the state field of a resource, checks the move and sets the new state, so a
// custom method such as PublishBook is a load, a Transition and a save.
//
// Lint reports the AIP-216 rules the state fields of a resource break: OUTPUT_ONLY behavior, the State enum name and nesting, the
// STATE_UNSPECIFIED zero value, unprefixed values, preferred terms and State instead of Status. LintTransitionMethod checks a state transition
// method: its name, request, response, name field and HTTP binding. CheckOutputOnly checks one field.
//
// # Usage
//
//	var bookStates = state.MustNew(map[pb.Book_State][]pb.Book_State{
//		pb.Book_DRAFT:     {pb.Book_PUBLISHED},
//		pb.Book_PUBLISHED: {pb.Book_ARCHIVED},
//	})
//
//	if _, err := bookStates.Transition(book, "PublishBook", pb.Book_PUBLISHED); err != nil {
//		return nil, err // FAILED_PRECONDITION
//	}
//
// See README.md for the complete guide, the AIP-216 conformance table and errors.
package state
