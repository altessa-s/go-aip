// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package state_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/state"

	statetestv1 "github.com/altessa-s/go-aip/internal/proto/gen/statetest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func bookMachine(t *testing.T) *state.Machine[statetestv1.Book_State] {
	t.Helper()

	m, err := state.New(map[statetestv1.Book_State][]statetestv1.Book_State{
		statetestv1.Book_DRAFT:     {statetestv1.Book_PUBLISHED, statetestv1.Book_DELETED},
		statetestv1.Book_PUBLISHED: {statetestv1.Book_ARCHIVED},
		statetestv1.Book_ARCHIVED:  {statetestv1.Book_PUBLISHED, statetestv1.Book_DELETED},
	})
	require.NoError(t, err)

	return m
}

func TestMachineCheck(t *testing.T) {
	t.Parallel()

	m := bookMachine(t)

	tests := []struct {
		name     string
		from, to statetestv1.Book_State
		ok       bool
		terminal bool
	}{
		{name: "publish draft", from: statetestv1.Book_DRAFT, to: statetestv1.Book_PUBLISHED, ok: true},
		{name: "archive published", from: statetestv1.Book_PUBLISHED, to: statetestv1.Book_ARCHIVED, ok: true},
		{name: "republish archived", from: statetestv1.Book_ARCHIVED, to: statetestv1.Book_PUBLISHED, ok: true},
		{name: "publish published", from: statetestv1.Book_PUBLISHED, to: statetestv1.Book_PUBLISHED},
		{name: "delete published", from: statetestv1.Book_PUBLISHED, to: statetestv1.Book_DELETED},
		{name: "from terminal", from: statetestv1.Book_DELETED, to: statetestv1.Book_DRAFT, terminal: true},
		{name: "from unspecified", from: statetestv1.Book_STATE_UNSPECIFIED, to: statetestv1.Book_DRAFT},
		{name: "unknown number", from: statetestv1.Book_DRAFT, to: statetestv1.Book_State(99)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.ok, m.Allowed(tt.from, tt.to))

			err := m.Check(tt.from, tt.to)
			if tt.ok {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, state.ErrTransitionNotAllowed)
			require.ErrorIs(t, err, coreerrs.ErrFailedPrecondition)

			te, ok := errors.AsType[*state.TransitionError](err)
			require.True(t, ok)
			assert.Equal(t, tt.terminal, te.Terminal)
			assert.Empty(t, te.Method)
		})
	}
}

func TestMachineQueries(t *testing.T) {
	t.Parallel()

	m := bookMachine(t)

	assert.Equal(t, []statetestv1.Book_State{
		statetestv1.Book_DRAFT, statetestv1.Book_PUBLISHED, statetestv1.Book_ARCHIVED, statetestv1.Book_DELETED,
	}, m.States())
	assert.Equal(t, []statetestv1.Book_State{statetestv1.Book_PUBLISHED, statetestv1.Book_DELETED},
		m.Targets(statetestv1.Book_DRAFT))
	assert.Nil(t, m.Targets(statetestv1.Book_DELETED))
	assert.Nil(t, m.Targets(statetestv1.Book_STATE_UNSPECIFIED))

	assert.True(t, m.IsTerminal(statetestv1.Book_DELETED))
	assert.False(t, m.IsTerminal(statetestv1.Book_DRAFT))
	assert.False(t, m.IsTerminal(statetestv1.Book_STATE_UNSPECIFIED))
}

func TestMachineExplicitTerminal(t *testing.T) {
	t.Parallel()

	m := state.MustNew(map[statetestv1.Book_State][]statetestv1.Book_State{
		statetestv1.Book_DRAFT:   {statetestv1.Book_DRAFT},
		statetestv1.Book_DELETED: nil,
	})
	assert.True(t, m.IsTerminal(statetestv1.Book_DELETED))
	require.NoError(t, m.Check(statetestv1.Book_DRAFT, statetestv1.Book_DRAFT))
}

func TestNewInvalidTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		table map[statetestv1.Book_State][]statetestv1.Book_State
	}{
		{name: "empty"},
		{name: "unspecified source", table: map[statetestv1.Book_State][]statetestv1.Book_State{
			statetestv1.Book_STATE_UNSPECIFIED: {statetestv1.Book_DRAFT},
		}},
		{name: "unspecified target", table: map[statetestv1.Book_State][]statetestv1.Book_State{
			statetestv1.Book_DRAFT: {statetestv1.Book_STATE_UNSPECIFIED},
		}},
		{name: "undeclared number", table: map[statetestv1.Book_State][]statetestv1.Book_State{
			statetestv1.Book_DRAFT: {statetestv1.Book_State(42)},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := state.New(tt.table)
			require.ErrorIs(t, err, state.ErrInvalidTable)
			assert.Panics(t, func() { state.MustNew(tt.table) })
		})
	}
}

func TestTransition(t *testing.T) {
	t.Parallel()

	m := bookMachine(t)
	book := &statetestv1.Book{Name: "books/b1", State: statetestv1.Book_DRAFT}

	from, err := m.Transition(book, "PublishBook", statetestv1.Book_PUBLISHED)
	require.NoError(t, err)
	assert.Equal(t, statetestv1.Book_DRAFT, from)
	assert.Equal(t, statetestv1.Book_PUBLISHED, book.GetState())

	from, err = m.Transition(book, "PublishBook", statetestv1.Book_PUBLISHED)
	require.ErrorIs(t, err, coreerrs.ErrFailedPrecondition)
	assert.Equal(t, statetestv1.Book_PUBLISHED, from)
	assert.Equal(t, statetestv1.Book_PUBLISHED, book.GetState(), "a refused transition leaves the resource untouched")

	te, ok := errors.AsType[*state.TransitionError](err)
	require.True(t, ok)
	assert.Equal(t, state.TransitionError{Method: "PublishBook", From: "PUBLISHED", To: "PUBLISHED"}, *te)
	assert.Equal(t, "state: PublishBook: cannot move from PUBLISHED to PUBLISHED", err.Error())
}

func TestTransitionTerminalMessage(t *testing.T) {
	t.Parallel()

	m := bookMachine(t)
	book := &statetestv1.Book{State: statetestv1.Book_DELETED}

	_, err := m.Transition(book, "", statetestv1.Book_DRAFT)
	assert.Equal(t, "state: cannot move from DELETED to DRAFT: DELETED is a terminal state", err.Error())

	book.State = statetestv1.Book_State(77)
	_, err = m.Transition(book, "", statetestv1.Book_DRAFT)
	assert.Equal(t, "state: cannot move from 77 to DRAFT", err.Error())
}

func TestTransitionCustomField(t *testing.T) {
	t.Parallel()

	m := state.MustNew(map[statetestv1.JobState][]statetestv1.JobState{
		statetestv1.JobState_RUNNING: {statetestv1.JobState_SUCCEEDED},
	}, state.WithField("job_state"))

	job := &statetestv1.Job{JobState: statetestv1.JobState_RUNNING}
	_, err := m.Transition(job, "CompleteJob", statetestv1.JobState_SUCCEEDED)
	require.NoError(t, err)
	assert.Equal(t, statetestv1.JobState_SUCCEEDED, job.GetJobState())

	empty := state.MustNew(map[statetestv1.Book_State][]statetestv1.Book_State{
		statetestv1.Book_DRAFT: {statetestv1.Book_PUBLISHED},
	}, state.WithField(""))
	_, err = empty.Transition(&statetestv1.Book{State: statetestv1.Book_DRAFT}, "", statetestv1.Book_PUBLISHED)
	require.NoError(t, err)
}

func TestTransitionInvalidField(t *testing.T) {
	t.Parallel()

	m := bookMachine(t)

	cases := map[string]func() error{
		"nil interface": func() error {
			_, err := m.Transition(nil, "", statetestv1.Book_PUBLISHED)
			return err
		},
		"nil pointer": func() error {
			_, err := m.Transition((*statetestv1.Book)(nil), "", statetestv1.Book_PUBLISHED)
			return err
		},
		"no state field": func() error {
			_, err := m.Transition(&statetestv1.PublishBookRequest{}, "", statetestv1.Book_PUBLISHED)
			return err
		},
		"not an enum": func() error {
			_, err := state.MustNew(map[statetestv1.Book_State][]statetestv1.Book_State{
				statetestv1.Book_DRAFT: {statetestv1.Book_PUBLISHED},
			}, state.WithField("title")).Transition(&statetestv1.Book{}, "", statetestv1.Book_PUBLISHED)

			return err
		},
		"other enum": func() error {
			_, err := m.Transition(&statetestv1.Shelf{}, "", statetestv1.Book_PUBLISHED)
			return err
		},
	}

	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.ErrorIs(t, run(), state.ErrInvalidField)
		})
	}
}

func TestMachineConcurrentUse(t *testing.T) {
	t.Parallel()

	m := bookMachine(t)

	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			book := &statetestv1.Book{State: statetestv1.Book_DRAFT}
			_, err := m.Transition(book, "PublishBook", statetestv1.Book_PUBLISHED)
			assert.NoError(t, err)
			assert.NotEmpty(t, m.States())
		})
	}

	wg.Wait()
}
