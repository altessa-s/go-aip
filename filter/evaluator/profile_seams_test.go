// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
)

// fakeBinder is a profile whose compiler depends on the evaluator's value encoding ([evaluator.Binder]).
type fakeBinder struct {
	bindErr  error
	nilBound bool
	got      *evaluator.ValueEncoding
}

func (fakeBinder) ValidatePolicy(*filter.Policy) error { return nil }

func (p fakeBinder) BindEvaluator(_ *filter.Policy, enc evaluator.ValueEncoding) (evaluator.Compiler, error) {
	if p.got != nil {
		*p.got = enc
	}

	if p.bindErr != nil || p.nilBound {
		return nil, p.bindErr
	}

	return fakePostgresProfile{program: fakeProgram{}, lookupValue: "bound", lookupFound: true}, nil
}

// nopEncoding is a value encoding that reads every value by its kind.
type nopEncoding struct{}

func (nopEncoding) Stored(v any, _ evaluator.Budget) (any, bool) { return v, false }
func (nopEncoding) Encode(v any, _ evaluator.Budget) any         { return v }
func (nopEncoding) EncodesItself(reflect.Type) bool              { return false }
func (nopEncoding) Owns(reflect.Type) bool                       { return false }

func TestBinderProfileInjection(t *testing.T) {
	t.Parallel()

	policy := filtertest.MustPolicy(t, filter.WithAnyField(true))

	var got evaluator.ValueEncoding

	eval, err := evaluator.New(policy, evaluator.WithProfile(fakeBinder{got: &got}), evaluator.WithValueEncoding(nopEncoding{}))
	require.NoError(t, err)
	require.Equal(t, nopEncoding{}, got, "the binder receives the evaluator's encoding")

	matched, err := eval.Evaluate(filtertest.MustParseFilter(t, `enabled == true`), map[string]any{})
	require.NoError(t, err)
	require.True(t, matched, "the bound compiler's program decides")

	value, found, err := eval.Lookup(map[string]any{}, "name")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "bound", value, "Lookup reads through the bound compiler")

	bindErr := errors.New("profile bind failed")
	_, err = evaluator.New(policy, evaluator.WithProfile(fakeBinder{bindErr: bindErr}))
	require.ErrorIs(t, err, bindErr)

	_, err = evaluator.New(policy, evaluator.WithProfile(fakeBinder{nilBound: true}))
	require.ErrorIs(t, err, filter.ErrInvalidOptions, "a binder must bind a compiler")

	var nilProfile evaluator.Profile

	_, err = evaluator.New(policy, evaluator.WithProfile(nilProfile))
	require.NoError(t, err, "nil selects the default evaluator")

	var typedNil *fakePostgresProfile

	_, err = evaluator.New(policy, evaluator.WithProfile(typedNil))
	require.NoError(t, err, "typed nil selects the default evaluator")

	_, err = evaluator.New(policy, evaluator.WithProfile(validatingProfile{}))
	require.ErrorIs(t, err, filter.ErrInvalidOptions, "a profile must compile or bind")

	_, err = evaluator.New(policy, evaluator.WithProfile(fakePostgresProfile{}), evaluator.WithValueEncoding(nopEncoding{}))
	require.ErrorIs(t, err, filter.ErrInvalidOptions, "a compiled profile reads through a decoder")

	_, err = evaluator.New(policy, evaluator.WithValueDecoder(nopDecoder{}))
	require.ErrorIs(t, err, filter.ErrInvalidOptions, "a decoder requires a profile")
}

// validatingProfile validates but neither compiles nor binds.
type validatingProfile struct{}

func (validatingProfile) ValidatePolicy(*filter.Policy) error { return nil }

// nopDecoder decodes every value as it is.
type nopDecoder struct{}

func (nopDecoder) Decode(v any) (any, error) { return v, nil }

type fakePostgresProfile struct {
	validateErr error
	compileErr  error
	program     evaluator.Program
	lookupValue any
	lookupFound bool
	lookupErr   error
}

func (p fakePostgresProfile) ValidatePolicy(*filter.Policy) error { return p.validateErr }
func (p fakePostgresProfile) CompileEvaluator(*filter.Policy, filter.Node) (evaluator.Program, error) {
	return p.program, p.compileErr
}

func (p fakePostgresProfile) Lookup(*filter.Policy, map[string]any, string, int, evaluator.ValueDecoder) (any, bool, error) {
	return p.lookupValue, p.lookupFound, p.lookupErr
}

type fakeProgram struct{ err error }

func (p fakeProgram) Evaluate(map[string]any, int, evaluator.ValueDecoder) (bool, error) {
	return true, p.err
}

func TestPostgresProfileInjection(t *testing.T) {
	t.Parallel()

	compileErr := errors.New("compile failed")
	compileEval, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAnyField(true)),
		evaluator.WithProfile(fakePostgresProfile{compileErr: compileErr}),
	)
	require.NoError(t, err)
	_, err = compileEval.Evaluate(nil, map[string]any{})
	require.ErrorIs(t, err, compileErr)
	prepared, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAnyField(true)), evaluator.WithProfile(fakePostgresProfile{program: fakeProgram{}}))
	require.NoError(t, err)
	got, err := prepared.Evaluate(nil, map[string]any{})
	require.NoError(t, err)
	require.True(t, got)

	lookupEval, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAnyField(true)),
		evaluator.WithProfile(fakePostgresProfile{lookupValue: "value", lookupFound: true}),
	)
	require.NoError(t, err)
	value, found, err := lookupEval.Lookup(map[string]any{}, "name")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "value", value)

	missingEval, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAnyField(true)), evaluator.WithProfile(fakePostgresProfile{}))
	require.NoError(t, err)
	value, found, err = missingEval.Lookup(map[string]any{}, "name")
	require.NoError(t, err)
	require.False(t, found)
	require.Nil(t, value)

	lookupErr := errors.New("lookup failed")
	errorEval, err := evaluator.New(filtertest.MustPolicy(t, filter.WithAnyField(true)), evaluator.WithProfile(fakePostgresProfile{lookupErr: lookupErr}))
	require.NoError(t, err)
	_, _, err = errorEval.Lookup(map[string]any{}, "name")
	require.ErrorIs(t, err, lookupErr)

	_, err = evaluator.New(filtertest.MustPolicy(t, filter.WithAnyField(true)), evaluator.WithProfile(fakePostgresProfile{validateErr: compileErr}))
	require.ErrorIs(t, err, compileErr)
}
