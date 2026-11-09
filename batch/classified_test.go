// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package batch_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/protobuf/encoding/prototext"

	"github.com/altessa-s/go-aip/apierror"
	"github.com/altessa-s/go-aip/batch"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

const secret = "secret stored value"

var errCorrupt = errors.New("stored record is invalid")

// assertNoSecret checks no text of the mapped error, its field violations or its google.rpc.Status names the hidden cause.
func assertNoSecret(t testing.TB, e *apierror.Error) {
	t.Helper()

	texts := []string{e.Message(), e.Error(), prototext.Format(e.Proto())}
	for _, v := range e.FieldViolations() {
		texts = append(texts, v.GetField(), v.GetDescription())
	}

	for _, text := range texts {
		assert.NotContains(t, text, secret)
		assert.NotContains(t, text, "storedField")
	}
}

// TestItemErrorKeepsClassifiedCausesHidden checks the field error an ItemError re-roots comes from the classification alone: a
// FieldError, an invalid-argument sentinel or text a classified cause hides yields no field violation and no message, and the
// hidden cause stays reachable through apierror.HiddenCause.
func TestItemErrorKeepsClassifiedCausesHidden(t *testing.T) {
	t.Parallel()

	for name, cause := range map[string]error{
		"field error":         coreerrs.NewFieldError("storedField", secret),
		"wrapped field error": fmt.Errorf("decode: %w", &coreerrs.FieldError{Field: "storedField", Reason: secret}),
		"invalid argument":    fmt.Errorf("%w: storedField: %s", coreerrs.ErrInvalidArgument, secret),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			classified := apierror.Classify(errCorrupt, cause)
			err := batch.NewItemError(batch.FieldRequests, 0, classified)

			e := apierror.FromError(err)
			assert.Equal(t, code.Code_INTERNAL, e.Code())
			assert.Empty(t, e.FieldViolations())
			assertNoSecret(t, e)
			assert.NotErrorIs(t, err, coreerrs.ErrInvalidArgument)
			assert.Same(t, cause, apierror.HiddenCause(err))

			m, mErr := apierror.NewMapper(apierror.WithExposeUnknown(true))
			require.NoError(t, mErr)

			e = m.Map(err)
			assert.Equal(t, "batch: requests[0]: "+errCorrupt.Error(), e.Message())
			assertNoSecret(t, e)
		})
	}
}

// TestItemErrorReRootsTheClassification checks a field error or invalid-argument sentinel the classification itself holds is
// still re-rooted under the item path, without the hidden cause.
func TestItemErrorReRootsTheClassification(t *testing.T) {
	t.Parallel()

	decodeErr := coreerrs.NewFieldError("storedField", secret)

	err := batch.NewItemError(batch.FieldRequests, 2, apierror.Classify(coreerrs.NewFieldError("pageToken", "malformed"), decodeErr))
	e := apierror.FromError(err)
	assert.Equal(t, code.Code_INVALID_ARGUMENT, e.Code())
	require.Len(t, e.FieldViolations(), 1)
	assert.Equal(t, "requests[2].pageToken", e.FieldViolations()[0].GetField())
	assert.Equal(t, "malformed", e.FieldViolations()[0].GetDescription())
	assertNoSecret(t, e)

	category := fmt.Errorf("%w: page token is malformed", coreerrs.ErrInvalidArgument)
	err = batch.NewItemError(batch.FieldRequests, 1, apierror.Classify(category, decodeErr))
	e = apierror.FromError(err)
	assert.Equal(t, code.Code_INVALID_ARGUMENT, e.Code())
	require.Len(t, e.FieldViolations(), 1)
	assert.Equal(t, "requests[1]", e.FieldViolations()[0].GetField())
	assert.Equal(t, category.Error(), e.FieldViolations()[0].GetDescription())
	assertNoSecret(t, e)

	err = batch.NewItemError(batch.FieldRequests, 3,
		apierror.Classify(coreerrs.NewFieldError("pageToken", "malformed"), coreerrs.NewFieldError("storedField", "malformed")))
	e = apierror.FromError(err)
	require.Len(t, e.FieldViolations(), 1)
	assert.Equal(t, "requests[3].pageToken", e.FieldViolations()[0].GetField(), "a hidden FieldError of the same reason")
	assertNoSecret(t, e)
}

// TestAtomicKeepsClassifiedCausesHidden checks the ItemError Atomic wraps a classified decoder error in maps by the category
// alone, through the item error itself and a coreerrs.MapFieldPath above it.
func TestAtomicKeepsClassifiedCausesHidden(t *testing.T) {
	t.Parallel()

	tx := &fakeTx{store: map[string]int{}}
	decode := func(context.Context, int, string) (int, error) {
		return 0, apierror.Classify(errCorrupt, coreerrs.NewFieldError("storedField", secret))
	}

	_, err := batch.Atomic(t.Context(), batch.FieldRequests, []string{"a"}, tx.run, decode)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), secret)
	assert.ErrorIs(t, apierror.HiddenCause(err), coreerrs.ErrInvalidArgument, "the cause stays reachable for logs")

	for name, in := range map[string]error{
		"item error":     err,
		"mapped path":    coreerrs.MapFieldPath(err, func(f string) string { return "batch." + f }),
		"wrapped mapped": fmt.Errorf("create books: %w", coreerrs.MapFieldPath(err, func(f string) string { return f })),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := apierror.FromError(in)
			assert.Equal(t, code.Code_INTERNAL, e.Code())
			assert.Empty(t, e.FieldViolations())
			assertNoSecret(t, e)
		})
	}
}

// TestItemErrorBesideAVisibleSentinel checks an item error joining a classified cause with a visible INVALID_ARGUMENT sentinel is
// re-rooted on the item path with the visible text alone.
func TestItemErrorBesideAVisibleSentinel(t *testing.T) {
	t.Parallel()

	for name, cause := range map[string]error{
		"invalid argument":               fmt.Errorf("%w: storedField: %s", coreerrs.ErrInvalidArgument, secret),
		"field error quoting the result": coreerrs.NewFieldError("storedField", "invalid"),
		"field error without a reason":   coreerrs.NewFieldError("storedField", ""),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := apierror.Classify(errCorrupt, cause)
			err := batch.NewItemError(batch.FieldRequests, 0, errors.Join(c, coreerrs.ErrInvalidArgument))

			e := apierror.FromError(err)
			assert.Equal(t, code.Code_INVALID_ARGUMENT, e.Code())
			require.Len(t, e.FieldViolations(), 1)
			assert.Equal(t, "requests[0]", e.FieldViolations()[0].GetField())
			assertNoSecret(t, e)
			assert.ErrorIs(t, err, c)
		})
	}
}
