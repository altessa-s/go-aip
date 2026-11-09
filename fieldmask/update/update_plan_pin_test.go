// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/altessa-s/go-aip/fieldmask/update"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// TestPlanRefusesPathsBelowSecretsPin checks that a secret is named only as
// a whole: a path into it is unknown.
func TestPlanRefusesPathsBelowSecretsPin(t *testing.T) {
	t.Parallel()
	md := edgeDescriptor(t)
	spec, specErr := update.NewSpec(dynamicpb.NewMessage(md))
	require.NoError(t, specErr)

	_, err := spec.Plan(dynamicpb.NewMessage(md), maskOf([]string{"token.x"}))
	fe, ok := errors.AsType[*coreerrs.FieldError](err)
	require.True(t, ok, "want a field error, got %v", err)
	require.Equal(t, update.DefaultUpdateMaskField+".paths[0]", fe.Field)
	require.Equal(t, `unknown path "token.x"`, fe.Reason)

	plan, err := spec.Plan(dynamicpb.NewMessage(md), maskOf([]string{"token", "token"}))
	require.NoError(t, err)
	require.Empty(t, plan.Paths)
}
