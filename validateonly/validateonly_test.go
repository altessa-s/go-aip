// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package validateonly_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"

	"github.com/altessa-s/go-aip/validateonly"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/etagtest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func TestRequested(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		m    proto.Message
		want bool
		err  error
	}{
		{name: "set", m: &pb.DeleteBookRequest{ValidateOnly: true}, want: true},
		{name: "unset", m: &pb.CreateBookRequest{}},
		{name: "typed nil", m: (*pb.CreateBookRequest)(nil)},
		{name: "nil", m: nil},
		{name: "no field", m: &pb.Book{}},
		{name: "not a bool", m: &pb.Malformed{ValidateOnly: "true"}, err: validateonly.ErrInvalidField},
		{name: "repeated", m: &pb.Lists{ValidateOnly: []bool{true}}, err: validateonly.ErrInvalidField},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := validateonly.Requested(tt.m)
			require.ErrorIs(t, err, tt.err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestRun(t *testing.T) {
	t.Parallel()

	errDenied := errors.New("permission denied")

	tests := []struct {
		name         string
		validateOnly bool
		validateErr  error
		commitErr    error
		want         string
		committed    bool
		err          error
	}{
		{name: "live", want: "stored", committed: true},
		{name: "validate only", validateOnly: true, want: "would-be"},
		{name: "validation fails live", validateErr: errDenied, err: errDenied},
		{name: "validation fails validate only", validateOnly: true, validateErr: coreerrs.ErrConflict, err: coreerrs.ErrConflict},
		{name: "commit fails", commitErr: coreerrs.ErrUnavailable, committed: true, err: coreerrs.ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			committed := false
			got, err := validateonly.Run(t.Context(), tt.validateOnly,
				func(context.Context) (string, error) {
					if tt.validateErr != nil {
						return "partial", tt.validateErr
					}

					return "would-be", nil
				},
				func(_ context.Context, in string) (string, error) {
					committed = true

					require.Equal(t, "would-be", in)

					if tt.commitErr != nil {
						return "", tt.commitErr
					}

					return "stored", nil
				})
			require.ErrorIs(t, err, tt.err)
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.committed, committed)
		})
	}
}

func TestRunMissingStep(t *testing.T) {
	t.Parallel()

	validate := func(context.Context) (int, error) { return 1, nil }
	commit := func(_ context.Context, v int) (int, error) { return v, nil }

	for _, validateOnly := range []bool{false, true} {
		_, err := validateonly.Run(t.Context(), validateOnly, nil, commit)
		require.ErrorIs(t, err, validateonly.ErrMissingStep)

		_, err = validateonly.Run(t.Context(), validateOnly, validate, nil)
		require.ErrorIs(t, err, validateonly.ErrMissingStep)
	}
}

func TestOmitGenerated(t *testing.T) {
	t.Parallel()

	b := &pb.Book{Name: "n", Title: "t", UpdateTime: "server", Shelf: &pb.Shelf{Theme: "sf", Audit: "server"}}
	require.NoError(t, validateonly.OmitGenerated(b))
	require.True(t, proto.Equal(&pb.Book{Name: "n", Title: "t", Shelf: &pb.Shelf{Theme: "sf"}}, b), "got %v", b)

	require.NoError(t, validateonly.OmitGenerated(nil))
	require.NoError(t, validateonly.OmitGenerated((*pb.Book)(nil)))
}
