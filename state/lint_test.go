// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package state_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/state"

	statetestv1 "github.com/altessa-s/go-aip/internal/proto/gen/statetest/v1"
)

type finding struct {
	Rule    state.Rule
	Element protoreflect.FullName
}

func findings(t *testing.T, vs []state.Violation) []finding {
	t.Helper()

	out := make([]finding, 0, len(vs))
	for _, v := range vs {
		assert.NotEmpty(t, v.Message)

		out = append(out, finding{Rule: v.Rule, Element: v.Element})
	}

	return out
}

func TestLint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		md   protoreflect.MessageDescriptor
		want []finding
	}{
		{name: "conforming", md: (&statetestv1.Book{}).ProtoReflect().Descriptor(), want: []finding{}},
		{name: "top-level enum", md: (&statetestv1.Job{}).ProtoReflect().Descriptor(), want: []finding{
			{Rule: state.RuleEnumNested, Element: "statetest.v1.JobState"},
		}},
		{name: "every value rule", md: (&statetestv1.Shelf{}).ProtoReflect().Descriptor(), want: []finding{
			{Rule: state.RuleOutputOnly, Element: "statetest.v1.Shelf.state"},
			{Rule: state.RuleZeroValue, Element: "statetest.v1.Shelf.State"},
			{Rule: state.RuleValuePrefix, Element: "statetest.v1.Shelf.STATE_ACTIVE"},
			{Rule: state.RuleValueTerm, Element: "statetest.v1.Shelf.READY"},
		}},
		{name: "status and enum name", md: (&statetestv1.Order{}).ProtoReflect().Descriptor(), want: []finding{
			{Rule: state.RuleNotStatus, Element: "statetest.v1.Order.status"},
			{Rule: state.RuleEnumName, Element: "statetest.v1.Order.Lifecycle"},
		}},
		{name: "no state field", md: (&statetestv1.PublishBookRequest{}).ProtoReflect().Descriptor(), want: []finding{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, findings(t, state.Lint(tt.md)))
		})
	}
}

func TestCheckOutputOnly(t *testing.T) {
	t.Parallel()

	require.NoError(t, state.CheckOutputOnly((&statetestv1.Book{}).ProtoReflect().Descriptor().Fields().ByName("state")))

	err := state.CheckOutputOnly((&statetestv1.Shelf{}).ProtoReflect().Descriptor().Fields().ByName("state"))
	require.ErrorIs(t, err, state.ErrNotOutputOnly)
	assert.Contains(t, err.Error(), "statetest.v1.Shelf.state")
}

func TestLintTransitionMethod(t *testing.T) {
	t.Parallel()

	svc := statetestv1.File_statetest_v1_state_proto.Services().ByName("StateTestService")
	book := (&statetestv1.Book{}).ProtoReflect().Descriptor()
	job := (&statetestv1.Job{}).ProtoReflect().Descriptor()
	el := func(m string) protoreflect.FullName {
		return protoreflect.FullName("statetest.v1.StateTestService." + m)
	}

	tests := []struct {
		method   protoreflect.Name
		resource protoreflect.MessageDescriptor
		want     []finding
	}{
		{method: "PublishBook", resource: book, want: []finding{}},
		{method: "SoftDeleteBook", resource: book, want: []finding{}},
		{method: "ArchiveBook", resource: book, want: []finding{
			{Rule: state.RuleHTTPMethod, Element: el("ArchiveBook")},
			{Rule: state.RuleHTTPBody, Element: el("ArchiveBook")},
			{Rule: state.RuleHTTPVerb, Element: el("ArchiveBook")},
		}},
		{method: "Unpublish", resource: book, want: []finding{
			{Rule: state.RuleMethodName, Element: el("Unpublish")},
			{Rule: state.RuleResponse, Element: el("Unpublish")},
			{Rule: state.RuleNameField, Element: "statetest.v1.UnpublishRequest"},
		}},
		{method: "RestoreBook", resource: book, want: []finding{
			{Rule: state.RuleRequestName, Element: el("RestoreBook")},
			{Rule: state.RuleHTTPBody, Element: el("RestoreBook")},
			{Rule: state.RuleHTTPVerb, Element: el("RestoreBook")},
		}},
		{method: "CancelJob", resource: job, want: []finding{
			{Rule: state.RuleNameField, Element: "statetest.v1.CancelJobRequest"},
			{Rule: state.RuleHTTPVerb, Element: el("CancelJob")},
		}},
	}

	for _, tt := range tests {
		t.Run(string(tt.method), func(t *testing.T) {
			t.Parallel()

			md := svc.Methods().ByName(tt.method)
			require.NotNil(t, md)
			assert.Equal(t, tt.want, findings(t, state.LintTransitionMethod(md, tt.resource)))
		})
	}
}
