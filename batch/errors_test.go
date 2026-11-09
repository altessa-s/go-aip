// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package batch_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/batch"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func TestNewItemError(t *testing.T) {
	t.Parallel()

	require.NoError(t, batch.NewItemError(batch.FieldNames, 3, nil))

	err := batch.NewItemError(batch.FieldNames, 3, coreerrs.ErrNotFound)
	ie, ok := errors.AsType[*batch.ItemError](err)
	require.True(t, ok)
	assert.Equal(t, "names[3]", ie.Path())
	assert.Equal(t, "batch: names[3]: not found", err.Error())
}

func TestItemErrorUnwrap(t *testing.T) {
	t.Parallel()

	t.Run("field error is re-rooted", func(t *testing.T) {
		t.Parallel()

		cause := errors.New("too long")
		err := batch.NewItemError(batch.FieldRequests, 2, fmt.Errorf("wrapped: %w",
			&coreerrs.FieldError{Field: "book.title", Reason: "is too long", Err: cause}))

		fe, ok := errors.AsType[*coreerrs.FieldError](err)
		require.True(t, ok)
		assert.Equal(t, "requests[2].book.title", fe.Field)
		assert.Equal(t, "is too long", fe.Reason)
		require.ErrorIs(t, err, cause)
		assert.Equal(t, "batch: requests[2]: wrapped: invalid argument: book.title: is too long", err.Error())
	})

	t.Run("invalid argument without field", func(t *testing.T) {
		t.Parallel()

		err := batch.NewItemError(batch.FieldRequests, 1, fmt.Errorf("%w: bad book", coreerrs.ErrInvalidArgument))

		fe, ok := errors.AsType[*coreerrs.FieldError](err)
		require.True(t, ok)
		assert.Equal(t, "requests[1]", fe.Field)
		assert.Equal(t, "invalid argument: bad book", fe.Reason)
		require.ErrorIs(t, err, coreerrs.ErrInvalidArgument)
	})

	t.Run("other error keeps its class", func(t *testing.T) {
		t.Parallel()

		err := batch.NewItemError(batch.FieldRequests, 0, coreerrs.ErrConflict)
		require.ErrorIs(t, err, coreerrs.ErrConflict)
		require.NotErrorIs(t, err, coreerrs.ErrInvalidArgument)

		_, ok := errors.AsType[*coreerrs.FieldError](err)
		assert.False(t, ok)
	})

	t.Run("nil err", func(t *testing.T) {
		t.Parallel()

		ie := &batch.ItemError{Index: 4, Field: batch.FieldNames}
		assert.Nil(t, ie.Unwrap())
		assert.Equal(t, "batch: names[4]: failed", ie.Error())
	})
}

func TestErrNotFound(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, batch.ErrNotFound, coreerrs.ErrNotFound)
}

func TestAllFailedMessage(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		"None of the requests succeeded, refer to the BatchCreateBooksOperationMetadata.failed_requests for individual error details",
		batch.AllFailedMessage("BatchCreateBooksOperationMetadata"))
}
