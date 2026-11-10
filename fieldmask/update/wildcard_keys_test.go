// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// delegatesAccount returns an Account holding the delegates given as
// email/org pairs.
func delegatesAccount(entries map[string][2]string) *pb.Account {
	a := &pb.Account{Delegates: map[string]*pb.Owner{}}
	for k, v := range entries {
		a.Delegates[k] = &pb.Owner{Email: v[0], Org: v[1]}
	}

	return a
}

// TestWildcardSubPathBesideNoOpKey pins the exact case of a "*" sub-path of a
// map beside a key path the check drops: ["delegates.*.email",
// "delegates.k.org"] with k.org unchanged. The "*" was checked without k, so
// it must not select k once the k branch is gone: it is replaced by the keys
// of the other request entries, and k (whose REQUIRED email is empty) is
// neither written back nor kept in the request.
func TestWildcardSubPathBesideNoOpKey(t *testing.T) {
	t.Parallel()

	stored := delegatesAccount(map[string][2]string{"k": {"k@example.com", "ko"}, "j": {"j@example.com", "jo"}})
	req := delegatesAccount(map[string][2]string{"k": {"", "ko"}, "j": {"new@example.com", "jo"}})
	paths := []string{"delegates.*.email", "delegates.k.org"}

	t.Run("ApplyMask", func(t *testing.T) {
		t.Parallel()

		msg := proto.Clone(req)
		got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: paths}, msg, update.WithCurrent(stored))
		require.NoError(t, err)
		require.Equal(t, []string{"delegates.j.email"}, got.ToPaths())
		requireProtoEqual(t, &pb.Account{Delegates: map[string]*pb.Owner{"j": {Email: "new@example.com"}}}, msg)
	})

	t.Run("Apply and ValidateStrict", func(t *testing.T) {
		t.Parallel()

		mask := fieldmask.FromPaths(paths...)
		require.NoError(t, update.Apply(mask, proto.Clone(req), update.WithCurrent(stored)))
		require.Equal(t, []string{"delegates.j.email"}, mask.ToPaths())

		strict, err := update.ValidateStrict(fieldmask.FromPaths(paths...), req, update.WithCurrent(stored))
		require.NoError(t, err)
		require.Equal(t, []string{"delegates.j.email"}, strict.ToPaths())
	})

	t.Run("Spec.Plan and MergePaths", func(t *testing.T) {
		t.Parallel()

		spec, err := update.NewSpec(&pb.Account{})
		require.NoError(t, err)

		plan, err := spec.Plan(req, &fieldmaskpb.FieldMask{Paths: paths}, update.WithCurrent(stored))
		require.NoError(t, err)
		require.Equal(t, []string{"delegates.j.email"}, plan.Paths)

		merged := proto.Clone(stored)
		require.NoError(t, update.MergePaths(merged, req, plan.Paths))
		requireProtoEqual(t, delegatesAccount(map[string][2]string{"k": {"k@example.com", "ko"}, "j": {"new@example.com", "jo"}}), merged)
	})

	t.Run("no other entry: nothing to update", func(t *testing.T) {
		t.Parallel()

		only := delegatesAccount(map[string][2]string{"k": {"", "ko"}})
		got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: paths}, proto.Clone(only), update.WithCurrent(stored))
		require.NoError(t, err)
		require.Equal(t, []string{}, got.ToPaths())
	})
}

// TestWholeWildcardBesideKey pins that a key path beside a "*" naming every
// entry whole ("delegates.*") is covered by it, as Spec.Plan and MergePaths
// read "delegates.*": the "*" replaces every entry, k included, so k's
// REQUIRED fields are checked, the key path is checked as if sent alone, and
// the request entry is kept whole (its IMMUTABLE org is not cleared for the
// write-back).
func TestWholeWildcardBesideKey(t *testing.T) {
	t.Parallel()

	stored := delegatesAccount(map[string][2]string{"k": {"a@corp.com", "corp"}})

	runApplyMaskCases(t, []applyMaskCase{
		{name: "REQUIRED of a key beside the wildcard", paths: []string{"delegates.*", "delegates.k.org"},
			req: delegatesAccount(map[string][2]string{"k": {"", "corp"}}), current: stored,
			violations: []FieldViolation{{Path: "delegates.k.email", Behavior: annotations.FieldBehavior_REQUIRED, Reason: reasonRequired}}},
		{name: "IMMUTABLE kept with the entry", paths: []string{"delegates.*", "delegates.k.email"},
			req: delegatesAccount(map[string][2]string{"k": {"b@corp.com", "corp"}}), current: stored,
			want: []string{"delegates.*"}, wantMsg: delegatesAccount(map[string][2]string{"k": {"b@corp.com", "corp"}})},
		{name: "covered key checked alone", paths: []string{"delegates.*", "delegates.k.org"},
			req: delegatesAccount(map[string][2]string{"k": {"b@corp.com", "other"}}), current: stored,
			violations: []FieldViolation{{Path: "delegates.k.org", Behavior: annotations.FieldBehavior_IMMUTABLE,
				Reason: "immutable field cannot be changed from its stored value"}}},
	})

	mask := fieldmask.FromPaths("delegates.*", "delegates.k.email")
	msg := delegatesAccount(map[string][2]string{"k": {"b@corp.com", "corp"}})
	require.NoError(t, update.Apply(mask, msg, update.WithCurrent(stored)))
	require.Equal(t, []string{"delegates.*"}, mask.ToPaths())
	requireProtoEqual(t, delegatesAccount(map[string][2]string{"k": {"b@corp.com", "corp"}}), msg)
}

// nestedMapDescriptor builds Outer{repeated Inner inners}, Inner{map<string,
// Leaf> m}, Leaf{string a [IMMUTABLE]; string b}: a map below the elements of
// a list.
func nestedMapDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	optional := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	repeated := descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
	message := descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
	field := func(name string, number int32, label *descriptorpb.FieldDescriptorProto_Label, typ *descriptorpb.FieldDescriptorProto_Type,
		typeName string,
	) *descriptorpb.FieldDescriptorProto {
		fd := &descriptorpb.FieldDescriptorProto{Name: new(name), Number: new(number), JsonName: new(jsonName(name)), Label: label, Type: typ}
		if typeName != "" {
			fd.TypeName = new(typeName)
		}

		return fd
	}

	a := field("a", 1, optional, str, "")
	a.Options = &descriptorpb.FieldOptions{}
	proto.SetExtension(a.Options, annotations.E_FieldBehavior, []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE})

	file := &descriptorpb.FileDescriptorProto{
		Name: new("nestedmap.proto"), Package: new("nestedmap"), Syntax: new("proto3"),
		Dependency: []string{"google/api/field_behavior.proto"},
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: new("Outer"), Field: []*descriptorpb.FieldDescriptorProto{field("inners", 1, repeated, message, ".nestedmap.Inner")}},
			{
				Name:  new("Inner"),
				Field: []*descriptorpb.FieldDescriptorProto{field("m", 1, repeated, message, ".nestedmap.Inner.MEntry")},
				NestedType: []*descriptorpb.DescriptorProto{{
					Name:    new("MEntry"),
					Options: &descriptorpb.MessageOptions{MapEntry: new(true)},
					Field: []*descriptorpb.FieldDescriptorProto{
						field("key", 1, optional, str, ""), field("value", 2, optional, message, ".nestedmap.Leaf"),
					},
				}},
			},
			{Name: new("Leaf"), Field: []*descriptorpb.FieldDescriptorProto{a, field("b", 2, optional, str, "")}},
		},
	}
	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err)

	return fd.Messages().ByName("Outer")
}

// TestWildcardBesideNoOpKeyBelowList pins the map below the elements of a
// list: one element is pinned as a single map, several share one mask and
// cannot be, so the mask is refused instead of widening the wildcard.
func TestWildcardBesideNoOpKeyBelowList(t *testing.T) {
	t.Parallel()

	md := nestedMapDescriptor(t)
	parse := func(text string) *dynamicpb.Message {
		m := dynamicpb.NewMessage(md)
		require.NoError(t, prototext.Unmarshal([]byte(text), m))

		return m
	}
	paths := []string{"inners.*.m.*.b", "inners.*.m.k.a"}

	one := `inners { m { key: "k" value { a: "x" b: "" } } m { key: "j" value { a: "y" b: "new" } } }`
	got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: paths}, parse(one), update.WithCurrent(parse(one)))
	require.NoError(t, err)
	require.Equal(t, []string{"inners.*.m.j.b"}, got.ToPaths())

	// Several elements share the mask: refused as a path error, classified
	// as one by every entry point.
	two := one + ` inners { m { key: "k" value { a: "x" } } m { key: "z" value { b: "new" } } }`
	_, err = update.ApplyMask(&fieldmaskpb.FieldMask{Paths: paths}, parse(two), update.WithCurrent(parse(two)))
	requireValidationPath(t, err, "inners.*.m.*")

	_, err = update.ApplyMask(&fieldmaskpb.FieldMask{Paths: paths}, parse(two), update.WithCurrent(parse(two)), update.WithStrict(true))
	require.ErrorIs(t, err, update.ErrInvalidUpdateMask)
	require.ErrorIs(t, err, fieldmask.ErrInvalidPath)

	_, err = update.ValidateStrict(fieldmask.FromPaths(paths...), parse(two), update.WithCurrent(parse(two)))
	require.ErrorIs(t, err, fieldmask.ErrInvalidPath)

	spec, err := update.NewSpec(dynamicpb.NewMessage(md))
	require.NoError(t, err)

	_, err = spec.Plan(parse(two), &fieldmaskpb.FieldMask{Paths: paths}, update.WithCurrent(parse(two)))
	require.ErrorIs(t, err, fieldmask.ErrInvalidPath)
	require.NotErrorIs(t, err, update.ErrProtectedField)

	mp, ok := errors.AsType[*update.MaskPathError](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, 0, mp.Index)
	require.Contains(t, mp.Error(), "cannot be kept out of the wildcard")

	// The "*" addresses no request entry: dropping it is exact when the
	// elements' maps hold only the named key.
	stored := `inners { m { key: "k" value { a: "x" } } } inners { m { key: "k" value { a: "x" } } }`
	got, err = update.ApplyMask(&fieldmaskpb.FieldMask{Paths: paths}, parse(stored), update.WithCurrent(parse(stored)))
	require.NoError(t, err)
	require.Equal(t, []string{}, got.ToPaths())

	// With no request elements at all, the merge updates every stored element
	// from nothing and would clear the IMMUTABLE k.a in each: refused.
	_, err = update.ApplyMask(&fieldmaskpb.FieldMask{Paths: paths}, parse(``), update.WithCurrent(parse(stored)))
	require.ErrorIs(t, err, update.ErrBehaviorViolation)
}

// TestWildcardExclusionsIgnoreKeyOrder pins that the entries a "*" sub-path
// addresses are those no key names in the mask as sent, whatever the key
// sorts before "*": ["delegates.*.email", "delegates.<key>.org"] with the
// org unchanged and the email of that entry empty, beside an entry j with a
// new email, succeeds for the empty key, punctuation keys and a plain key
// alike, and writes back j's email only.
func TestWildcardExclusionsIgnoreKeyOrder(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"", "!", "#k", ")", "k"} {
		t.Run("key "+key, func(t *testing.T) {
			t.Parallel()

			stored := delegatesAccount(map[string][2]string{key: {"x@example.com", "o"}, "j": {"j@example.com", "jo"}})
			req := delegatesAccount(map[string][2]string{key: {"", "o"}, "j": {"new@example.com", "jo"}})
			paths := []string{"delegates.*.email", delegatePath(key) + ".org"}

			got, err := update.ApplyMask(&fieldmaskpb.FieldMask{Paths: paths}, proto.Clone(req), update.WithCurrent(stored))
			require.NoError(t, err)
			require.Equal(t, []string{"delegates.j.email"}, got.ToPaths())

			spec, err := update.NewSpec(&pb.Account{})
			require.NoError(t, err)

			plan, err := spec.Plan(req, &fieldmaskpb.FieldMask{Paths: paths}, update.WithCurrent(stored))
			require.NoError(t, err)
			require.Equal(t, []string{"delegates.j.email"}, plan.Paths)
		})
	}
}

// delegatePath returns the mask path of the delegates entry key, quoted as the
// path grammar requires.
func delegatePath(key string) string {
	if key == "k" {
		return "delegates.k"
	}

	return "delegates.`" + key + "`"
}
