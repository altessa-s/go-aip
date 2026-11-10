// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package behavior_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/altessa-s/go-aip/internal/behavior"

	testpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// TestIsEmpty pins the falsy values of every scalar kind, of lists, maps and
// messages, with and without a message.
func TestIsEmpty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		msg   proto.Message
		field protoreflect.Name
		want  bool
	}{
		{"zero double", wrapperspb.Double(0), "value", true},
		{"double", wrapperspb.Double(1.5), "value", false},
		{"zero float", wrapperspb.Float(0), "value", true},
		{"float", wrapperspb.Float(1), "value", false},
		{"zero int64", wrapperspb.Int64(0), "value", true},
		{"int64", wrapperspb.Int64(-1), "value", false},
		{"zero uint64", wrapperspb.UInt64(0), "value", true},
		{"uint64", wrapperspb.UInt64(1), "value", false},
		{"zero uint32", wrapperspb.UInt32(0), "value", true},
		{"uint32", wrapperspb.UInt32(1), "value", false},
		{"false", wrapperspb.Bool(false), "value", true},
		{"true", wrapperspb.Bool(true), "value", false},
		{"empty bytes", wrapperspb.Bytes(nil), "value", true},
		{"bytes", wrapperspb.Bytes([]byte{0}), "value", false},
		{"empty string", wrapperspb.String(""), "value", true},
		{"string", wrapperspb.String("s"), "value", false},
		{"zero enum", &testpb.Account{}, "state", true},
		{"enum", &testpb.Account{State: testpb.State_ACTIVE}, "state", false},
		{"explicit zero", &testpb.Account{Quota: new(int32(0))}, "quota", true},
		{"empty list", &testpb.Account{Emails: []string{}}, "emails", true},
		{"list", &testpb.Account{Emails: []string{""}}, "emails", false},
		{"empty map", &testpb.Account{}, "attributes", true},
		{"map", &testpb.Account{Attributes: map[string]string{"": ""}}, "attributes", false},
		{"unset message", &testpb.Account{}, "owner", true},
		{"set empty message", &testpb.Account{Owner: &testpb.Owner{}}, "owner", false},
		{"set zero enum of a oneof", structpb.NewNullValue(), "null_value", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := tt.msg.ProtoReflect()
			fd := m.Descriptor().Fields().ByName(tt.field)
			require.NotNil(t, fd)
			assert.Equal(t, tt.want, behavior.IsEmpty(m, fd))
		})
	}

	fd := (&testpb.Account{}).ProtoReflect().Descriptor().Fields().ByName("display_name")
	assert.True(t, behavior.IsEmpty(nil, fd), "a nil message holds nothing")
	assert.True(t, behavior.RequiredUnmet(nil, fd))
}

// TestRequiredViolationsDepth pins that the walk reports exceeding its depth
// and that the skip set removes fields from the check.
func TestRequiredViolationsDepth(t *testing.T) {
	t.Parallel()

	a := &testpb.Account{Parent: &testpb.Account{Parent: &testpb.Account{}}}
	_, exceeded := behavior.RequiredViolations(a.ProtoReflect(), "", behavior.Get, nil, 1)
	assert.True(t, exceeded)

	v, exceeded := behavior.RequiredViolations((&testpb.Owner{}).ProtoReflect(), "x", behavior.Get,
		[]annotations.FieldBehavior{annotations.FieldBehavior_REQUIRED}, 4)
	assert.False(t, exceeded)
	assert.Empty(t, v, "a skipped behavior is not checked")

	v, _ = behavior.RequiredViolations(nil, "", behavior.Get, nil, 4)
	assert.Empty(t, v)
}

// TestSameValueAndChild pins presence-aware comparison and sub-message access.
func TestSameValueAndChild(t *testing.T) {
	t.Parallel()

	md := (&testpb.Account{}).ProtoReflect().Descriptor()
	region := md.Fields().ByName("region")
	owner := md.Fields().ByName("owner")
	members := md.Fields().ByName("members")

	set := (&testpb.Account{Region: "eu", Owner: &testpb.Owner{}}).ProtoReflect()
	other := (&testpb.Account{Region: "us"}).ProtoReflect()
	empty := (&testpb.Account{}).ProtoReflect()

	assert.True(t, behavior.SameValue(set, set, region))
	assert.False(t, behavior.SameValue(set, other, region))
	assert.False(t, behavior.SameValue(set, nil, region))
	assert.True(t, behavior.SameValue(empty, nil, region), "an unset field matches an absent message")
	assert.True(t, behavior.SameValue(nil, nil, region))
	assert.False(t, behavior.SameValue(set, empty, owner), "a set empty message differs from an unset one")

	assert.NotNil(t, behavior.Child(set, owner))
	assert.Nil(t, behavior.Child(empty, owner))
	assert.Nil(t, behavior.Child(nil, owner))
	assert.Nil(t, behavior.Child(set, region), "a scalar has no child")
	assert.Nil(t, behavior.Child(set, members), "a list has no single child")
}

// TestProtectedViolationAndSortedMapKeys pins the reasons of protected
// violations and the deterministic map order.
func TestProtectedViolationAndSortedMapKeys(t *testing.T) {
	t.Parallel()

	assert.Equal(t, behavior.ReasonImmutableChanged, behavior.ProtectedViolation("a", annotations.FieldBehavior_IMMUTABLE, true).Reason)
	assert.Equal(t, behavior.ReasonImmutableUnverified, behavior.ProtectedViolation("a", annotations.FieldBehavior_IMMUTABLE, false).Reason)
	assert.Equal(t, behavior.ReasonIdentifierChanged, behavior.ProtectedViolation("a", annotations.FieldBehavior_IDENTIFIER, true).Reason)
	assert.Equal(t, behavior.ReasonIdentifierUnverified, behavior.ProtectedViolation("a", annotations.FieldBehavior_IDENTIFIER, false).Reason)

	a := &testpb.Account{Attributes: map[string]string{"b": "", "a": "", "c": ""}}
	m := a.ProtoReflect()
	keys := behavior.SortedMapKeys(m.Get(m.Descriptor().Fields().ByName("attributes")).Map())

	got := make([]string, 0, len(keys))
	for _, k := range keys {
		got = append(got, k.String())
	}

	assert.Equal(t, []string{"a", "b", "c"}, got)
}

// TestCheckedRequiredViolations pins that the field check runs for every
// populated, unskipped field in walk order, after the REQUIRED check, and
// never for a field reported as REQUIRED.
func TestCheckedRequiredViolations(t *testing.T) {
	t.Parallel()

	r := &testpb.Resource{
		Id:          "id",
		Description: "d",
		Profile:     &testpb.Profile{DisplayName: "p"},
		Aliases:     []*testpb.Profile{{DisplayName: "a"}},
	}

	var visited []string

	check := func(_ protoreflect.Message, fd protoreflect.FieldDescriptor, path func() string) []behavior.Violation {
		visited = append(visited, path())
		if fd.Name() == "display_name" {
			return []behavior.Violation{{Path: path(), Reason: "checked"}}
		}

		return nil
	}

	got, exceeded := behavior.CheckedRequiredViolations(r.ProtoReflect(), "", behavior.Get,
		[]annotations.FieldBehavior{annotations.FieldBehavior_IDENTIFIER}, 8, check)
	require.False(t, exceeded)

	assert.NotContains(t, visited, "id", "skipped fields are not checked")
	assert.NotContains(t, visited, "name", "a field reported as REQUIRED is not checked")
	assert.Contains(t, visited, "description")
	assert.Contains(t, got, behavior.Violation{Path: "profile.display_name", Reason: "checked"})
	assert.Contains(t, got, behavior.Violation{Path: "aliases.0.display_name", Reason: "checked"})
	assert.Contains(t, got, behavior.Violation{Path: "name", Behavior: annotations.FieldBehavior_REQUIRED, Reason: behavior.ReasonRequired})
}

// TestReplacedRequiredViolations pins that the element skip governs the
// elements of repeated and map fields, the skip the message and its singular
// sub-messages.
func TestReplacedRequiredViolations(t *testing.T) {
	t.Parallel()

	resolve := func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		if fd.FullName() == "fieldbehaviortest.v1.Owner.org" {
			return []annotations.FieldBehavior{annotations.FieldBehavior_REQUIRED, annotations.FieldBehavior_IMMUTABLE}
		}

		return behavior.Shared(fd)
	}
	skip := []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
	elementSkip := []annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY}

	msg := &testpb.Account{
		DisplayName: "d", Key: []byte{1}, Enabled: true, State: testpb.State_ACTIVE, Quota: new(int32(1)),
		Owner:  &testpb.Owner{Email: "o"},
		Emails: []string{"e"}, Attributes: map[string]string{"k": "v"}, Billing: &testpb.Account_Card{Card: "c"},
		Members:   []*testpb.Owner{{Email: "m"}},
		Delegates: map[string]*testpb.Owner{"k": {Email: "d"}},
	}

	got, exceeded := behavior.ReplacedRequiredViolations(msg.ProtoReflect(), "", resolve, skip, elementSkip, 8)
	require.False(t, exceeded)
	assert.Equal(t, []string{"members.0.org", "delegates.k.org"}, violationPathsOf(got), "owner.org keeps its stored value")

	got, _ = behavior.ReplacedRequiredViolations(msg.ProtoReflect(), "", resolve, skip, nil, 8)
	assert.Empty(t, got, "a nil element skip keeps the skip")
}

// violationPathsOf returns the paths of violations.
func violationPathsOf(violations []behavior.Violation) []string {
	out := make([]string, 0, len(violations))
	for _, v := range violations {
		out = append(out, v.Path)
	}

	return out
}
