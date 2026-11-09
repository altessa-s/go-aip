// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldbehavior_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldbehavior"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

type customResolver struct{}

func (customResolver) Resolve(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
	if fd.Name() == "display_name" {
		return []annotations.FieldBehavior{99}
	}

	return nil
}

func TestCustomResolverNestedUnknownBehavior(t *testing.T) {
	t.Parallel()

	msg := &pb.Resource{Profile: &pb.Profile{DisplayName: "private"}}
	err := fieldbehavior.Strip(msg, fieldbehavior.WithResolver(customResolver{}), fieldbehavior.WithBehaviors(99), fieldbehavior.WithStrict(true))
	require.ErrorIs(t, err, fieldbehavior.ErrBehaviorViolation)
	violation, ok := errors.AsType[*fieldbehavior.BehaviorViolationError](err)
	require.True(t, ok)
	require.Equal(t, "profile.display_name", violation.Violations[0].Path)
	require.Equal(t, annotations.FieldBehavior(99), violation.Violations[0].Behavior)
	require.NoError(t, fieldbehavior.Strip(msg, fieldbehavior.WithResolver(customResolver{}), fieldbehavior.WithBehaviors(99)))
	require.Empty(t, msg.Profile.DisplayName)
}

func TestDefaultBehaviorsAreOwnedAndOptionsReversible(t *testing.T) {
	t.Parallel()

	defaults := fieldbehavior.DefaultCreateBehaviors()
	defaults[0] = annotations.FieldBehavior_INPUT_ONLY

	require.Equal(t, annotations.FieldBehavior_OUTPUT_ONLY, fieldbehavior.DefaultCreateBehaviors()[0])

	msg := &pb.Resource{Id: "id"}
	require.NoError(t, fieldbehavior.StripCreate(msg, fieldbehavior.WithBehaviors()))
	require.Equal(t, "id", msg.Id)
	require.NoError(t, fieldbehavior.StripCreate(msg, fieldbehavior.WithStrict(true), fieldbehavior.WithStrict(false)))
	require.Empty(t, msg.Id)
}

func TestCustomResolverQuotedMapPaths(t *testing.T) {
	t.Parallel()

	msg := &pb.Resource{Labels: map[string]*pb.Profile{"a.b": {DisplayName: "private"}}}
	err := fieldbehavior.Strip(msg, fieldbehavior.WithResolver(customResolver{}), fieldbehavior.WithBehaviors(99), fieldbehavior.WithStrict(true))
	require.ErrorIs(t, err, fieldbehavior.ErrBehaviorViolation)
	violation, ok := errors.AsType[*fieldbehavior.BehaviorViolationError](err)
	require.True(t, ok)
	require.Equal(t, "labels.`a.b`.display_name", violation.Violations[0].Path)
}
