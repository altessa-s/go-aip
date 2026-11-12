// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/altessa-s/go-aip/etag"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	coreetag "github.com/altessa-s/go-atlas-core/encoding/etag"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// planField declares one string or message field of the plan test schema.
type planField struct {
	name     string
	number   int32
	message  string // type name of a message field, "" for a string
	repeated bool
	behavior annotations.FieldBehavior
}

// planDescriptor builds the plan test schema:
//
//	message Resource {
//	  string id = 1; string etag = 2; string name = 3; string code = 4;
//	  string secret = 5 [INPUT_ONLY]; string create_time = 6 [OUTPUT_ONLY];
//	  Filters filters = 7; Status status = 8; repeated string tags = 9;
//	  string channel = 10 [IMMUTABLE];
//	}
//	message Filters { string keywords = 1; string region = 2; }
//	message Status { string note = 1; string checked = 2 [OUTPUT_ONLY]; Inner inner = 3; string origin = 4 [IMMUTABLE]; }
//	message Inner { string x = 1; string server = 2 [OUTPUT_ONLY]; }
func planDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	msg := func(name string, fields ...planField) *descriptorpb.DescriptorProto {
		d := &descriptorpb.DescriptorProto{Name: new(name)}
		for _, f := range fields {
			fd := &descriptorpb.FieldDescriptorProto{
				Name: new(f.name), Number: new(f.number), JsonName: new(jsonName(f.name)),
				Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:  descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
			}
			if f.repeated {
				fd.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
			}

			if f.message != "" {
				fd.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
				fd.TypeName = new(".plantest." + f.message)
			}

			if f.behavior != annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED {
				fd.Options = &descriptorpb.FieldOptions{}
				proto.SetExtension(fd.Options, annotations.E_FieldBehavior, []annotations.FieldBehavior{f.behavior})
			}

			d.Field = append(d.Field, fd)
		}

		return d
	}
	file := &descriptorpb.FileDescriptorProto{
		Name: new("plantest.proto"), Package: new("plantest"), Syntax: new("proto3"),
		Dependency: []string{"google/api/field_behavior.proto"},
		MessageType: []*descriptorpb.DescriptorProto{
			msg("Resource",
				planField{name: "id", number: 1},
				planField{name: "etag", number: 2},
				planField{name: "name", number: 3},
				planField{name: "code", number: 4},
				planField{name: "secret", number: 5, behavior: annotations.FieldBehavior_INPUT_ONLY},
				planField{name: "create_time", number: 6, behavior: annotations.FieldBehavior_OUTPUT_ONLY},
				planField{name: "filters", number: 7, message: "Filters"},
				planField{name: "status", number: 8, message: "Status"},
				planField{name: "tags", number: 9, repeated: true},
				planField{name: "channel", number: 10, behavior: annotations.FieldBehavior_IMMUTABLE},
			),
			msg("Filters", planField{name: "keywords", number: 1}, planField{name: "region", number: 2}),
			msg("Status",
				planField{name: "note", number: 1},
				planField{name: "checked", number: 2, behavior: annotations.FieldBehavior_OUTPUT_ONLY},
				planField{name: "inner", number: 3, message: "Inner"},
				planField{name: "origin", number: 4, behavior: annotations.FieldBehavior_IMMUTABLE},
			),
			msg("Inner", planField{name: "x", number: 1}, planField{name: "server", number: 2, behavior: annotations.FieldBehavior_OUTPUT_ONLY}),
		},
	}
	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err)

	return fd.Messages().ByName("Resource")
}

// jsonName is the lowerCamelCase JSON name of a snake_case field name.
func jsonName(name string) string {
	out := make([]byte, 0, len(name))
	upper := false

	for i := range len(name) {
		switch c := name[i]; {
		case c == '_':
			upper = true
		case upper && c >= 'a' && c <= 'z':
			out, upper = append(out, c-'a'+'A'), false
		default:
			out, upper = append(out, c), false
		}
	}

	return string(out)
}

// planResource returns a Resource with the given string paths set
// ("status.inner.x" creates the parents).
func planResource(md protoreflect.MessageDescriptor, values map[string]string) *dynamicpb.Message {
	m := dynamicpb.NewMessage(md)
	for path, v := range values {
		cur := m.ProtoReflect()

		segs := splitDots(path)
		for i, seg := range segs {
			fd := cur.Descriptor().Fields().ByName(protoreflect.Name(seg))
			if i == len(segs)-1 {
				if fd.IsList() {
					cur.Mutable(fd).List().Append(protoreflect.ValueOfString(v))
				} else {
					cur.Set(fd, protoreflect.ValueOfString(v))
				}

				break
			}

			cur = cur.Mutable(fd).Message()
		}
	}

	return m
}

func splitDots(path string) []string {
	var out []string

	start := 0

	for i := range len(path) {
		if path[i] == '.' {
			out, start = append(out, path[start:i]), i+1
		}
	}

	return append(out, path[start:])
}

func TestPlanImpliedMask(t *testing.T) {
	t.Parallel()
	md := planDescriptor(t)
	spec, specErr := update.NewSpec(dynamicpb.NewMessage(md), update.WithImmutableFields("code"))
	require.NoError(t, specErr)

	in := planResource(md, map[string]string{
		"id": "1", "etag": "e", "name": "n", "code": "c", "secret": "s", "create_time": "t",
		"filters.keywords": "k", "status.note": "n", "status.origin": "o", "status.checked": "c",
		"status.inner.server": "srv", "channel": "ch",
	})
	plan, err := spec.Plan(in, nil, update.WithCurrent(proto.Clone(in)))
	require.NoError(t, err)
	// Identity, secret, immutable (by name and annotated) and server-managed
	// fields are left out at every depth; the immutable values equal the
	// stored ones, so they are ignored.
	require.Equal(t, []string{"name", "filters.keywords", "status.note"}, plan.Paths)
	require.Equal(t, "s", *plan.Secret("secret", new("s")))
	require.False(t, plan.Empty())

	// Without the stored resource a set immutable value cannot be verified.
	plan, err = spec.Plan(in, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"name", "filters.keywords", "status.note"}, plan.Paths)

	// A changed one is refused.
	stored := planResource(md, map[string]string{"code": "c", "status.origin": "other", "channel": "ch"})
	_, err = spec.Plan(in, nil, update.WithCurrent(stored))
	require.ErrorIs(t, err, update.ErrProtectedField)

	var fe *coreerrs.FieldError
	require.ErrorAs(t, err, &fe)
	require.Equal(t, `path "status.origin" cannot be changed`, fe.Reason)
}

func TestPlanWildcardKeepsServerManagedFields(t *testing.T) {
	t.Parallel()
	md := planDescriptor(t)
	spec, specErr := update.NewSpec(dynamicpb.NewMessage(md), update.WithImmutableFields("code"))
	require.NoError(t, specErr)

	plan, err := spec.Plan(dynamicpb.NewMessage(md), maskOf([]string{"*"}))
	require.NoError(t, err)
	// Absent status is unresolved until the stored resource is available.
	require.Equal(t, []string{"name", "filters", "tags"}, plan.Paths)
	require.Nil(t, plan.Secret("secret", new("s")), `"*" never changes a secret`)
}

func TestPlanExplicitPaths(t *testing.T) {
	t.Parallel()
	md := planDescriptor(t)
	spec, specErr := update.NewSpec(dynamicpb.NewMessage(md), update.WithImmutableFields("code"))
	require.NoError(t, specErr)

	in := dynamicpb.NewMessage(md)

	plan, err := spec.Plan(in, maskOf([]string{"filters", "status.inner.x", "status.note", "filters", "secret"}))
	require.NoError(t, err)
	require.Equal(t, []string{"filters", "status.note", "status.inner.x"}, plan.Paths)
	require.Equal(t, "", *plan.Secret("secret", nil), "a named unset secret is cleared")

	// Output-only paths are ignored and a message holding server-managed
	// fields is replaced through its other fields (AIP-203).
	accepted := map[string][]string{
		"channel":             nil,
		"code":                nil,
		"status.origin":       nil,
		"status":              {"status.note"},
		"status.inner":        {"status.inner.x"},
		"status.checked":      nil,
		"status.inner.server": nil,
		"createTime":          nil,
	}
	for path, want := range accepted {
		plan, err := spec.Plan(in, maskOf([]string{path}))
		require.NoError(t, err, path)
		require.Equal(t, want, plan.Paths, path)
	}

	tests := []struct {
		path   string
		reason string
	}{
		{"id", `path "id" cannot be updated`},
		{"nope", `unknown path "nope"`},
		{"secret.x", `unknown path "secret.x"`},
		{"tags.x", `unknown path "tags.x"`},
		{"name.", `malformed path "name."`},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()

			_, err := spec.Plan(in, maskOf([]string{tc.path}))

			var fe *coreerrs.FieldError
			require.ErrorAs(t, err, &fe)
			require.Equal(t, update.DefaultUpdateMaskField+".paths[0]", fe.Field)
			require.Equal(t, tc.reason, fe.Reason)
		})
	}

	_, err = spec.Plan(in, maskOf([]string{"*", "name"}))
	require.ErrorIs(t, err, fieldmask.ErrWildcardNotAlone)
}

func TestMergePathsNested(t *testing.T) {
	t.Parallel()
	md := planDescriptor(t)
	dst := planResource(md, map[string]string{"name": "old", "filters.keywords": "k0", "filters.region": "r0", "status.inner.server": "srv"})
	src := planResource(md, map[string]string{"filters.keywords": "k1", "status.inner.x": "x1"})

	require.NoError(t, update.MergePaths(dst, src, []string{"filters.keywords", "status.inner.x", "name", "status.note"}))

	want := planResource(md, map[string]string{
		"filters.keywords": "k1", "filters.region": "r0", "status.inner.x": "x1", "status.inner.server": "srv",
	})
	require.True(t, proto.Equal(want, dst), "got %v", dst)

	// A nested path whose parent is set in neither message creates nothing.
	empty := dynamicpb.NewMessage(md)
	require.NoError(t, update.MergePaths(empty, dynamicpb.NewMessage(md), []string{"filters.keywords"}))
	require.False(t, empty.Has(md.Fields().ByName("filters")))

	var ve *fieldmask.ValidationError
	require.ErrorAs(t, update.MergePaths(dst, src, []string{"name", "tags.x"}), &ve)
	require.ErrorIs(t, update.MergePaths(nil, src, []string{"name"}), update.ErrMessageMismatch)
}

// planStore is the stored side of a Patch test: one resource and its version.
type planStore struct {
	cur     *dynamicpb.Message
	version int64
	saved   *dynamicpb.Message
}

func (s *planStore) patch(spec *update.Spec) update.Patch[*dynamicpb.Message, *dynamicpb.Message, *int64] {
	return update.Patch[*dynamicpb.Message, *dynamicpb.Message, *int64]{
		Field: "resource", Spec: spec,
		Read:         func(context.Context) (*dynamicpb.Message, error) { return s.cur, nil },
		Precondition: update.Int64Precondition[*dynamicpb.Message]{Field: "resource.etag", Version: func(*dynamicpb.Message) int64 { return s.version }},
		ToProto:      func(m *dynamicpb.Message) *dynamicpb.Message { return proto.Clone(m).(*dynamicpb.Message) },
		Save: func(_ context.Context, merged *dynamicpb.Message, _ update.Plan, expected *int64) (*dynamicpb.Message, error) {
			if expected != nil && *expected != s.version {
				return nil, coreerrs.ErrConflict
			}

			s.saved = merged

			return merged, nil
		},
	}
}

func TestPatchRun(t *testing.T) {
	t.Parallel()
	md := planDescriptor(t)
	spec, specErr := update.NewSpec(dynamicpb.NewMessage(md), update.WithImmutableFields("code", "name"))
	require.NoError(t, specErr)

	tag := etag.FromRevision[int64](3).String()

	t.Run("applies the mask", func(t *testing.T) {
		t.Parallel()

		s := &planStore{cur: planResource(md, map[string]string{"name": "n", "filters.region": "r"}), version: 3}
		out, err := s.patch(spec).Run(t.Context(), planResource(md, map[string]string{"filters.keywords": "k"}), tag, nil)
		require.NoError(t, err)
		require.True(t, proto.Equal(planResource(md, map[string]string{"name": "n", "filters.region": "r", "filters.keywords": "k"}), out))
		require.NotNil(t, s.saved)
	})
	t.Run("empty plan saves nothing", func(t *testing.T) {
		t.Parallel()

		s := &planStore{cur: planResource(md, map[string]string{"name": "n"}), version: 3}
		out, err := s.patch(spec).Run(t.Context(), dynamicpb.NewMessage(md), tag, nil)
		require.NoError(t, err)
		require.Nil(t, s.saved)
		require.True(t, proto.Equal(s.cur, out))
	})
	t.Run("stale etag is a conflict even when nothing changes", func(t *testing.T) {
		t.Parallel()

		s := &planStore{cur: dynamicpb.NewMessage(md), version: 4}
		_, err := s.patch(spec).Run(t.Context(), dynamicpb.NewMessage(md), tag, nil)
		require.ErrorIs(t, err, coreerrs.ErrConflict)
	})
	t.Run("missing etag when required", func(t *testing.T) {
		t.Parallel()

		s := &planStore{cur: dynamicpb.NewMessage(md), version: 3}
		patch := s.patch(spec)
		patch.Precondition = update.Int64Precondition[*dynamicpb.Message]{Field: "resource.etag", Required: true,
			Version: func(*dynamicpb.Message) int64 { return s.version }}
		_, err := patch.Run(t.Context(), dynamicpb.NewMessage(md), "", nil)

		var fe *coreerrs.FieldError
		require.ErrorAs(t, err, &fe)
		require.Equal(t, "resource.etag", fe.Field)
		require.Equal(t, etag.ReasonRequired, fe.Reason)
		require.ErrorIs(t, err, coreetag.ErrMissing)
	})
	t.Run("mask errors stop before the read", func(t *testing.T) {
		t.Parallel()

		s := &planStore{version: 3}
		_, err := s.patch(spec).Run(t.Context(), dynamicpb.NewMessage(md), tag, &fieldmaskpb.FieldMask{Paths: []string{"id"}})

		var fe *coreerrs.FieldError
		require.ErrorAs(t, err, &fe)
		require.Equal(t, update.DefaultUpdateMaskField+".paths[0]", fe.Field)
	})
	t.Run("changed immutable fields are reported in declaration order", func(t *testing.T) {
		t.Parallel()

		for range 50 {
			s := &planStore{cur: planResource(md, map[string]string{"name": "n0", "code": "c0"}), version: 3}
			_, err := s.patch(spec).Run(t.Context(), planResource(md, map[string]string{"name": "n1", "code": "c1"}), tag, nil)

			var fe *coreerrs.FieldError
			require.ErrorAs(t, err, &fe)
			require.Equal(t, "resource.name", fe.Field)
		}
	})
}

// edgeField is one field of the edge-case schema: a string unless message is
// set, optionally in a oneof and with field behaviors.
type edgeField struct {
	name      string
	number    int32
	message   string
	oneof     *int32
	behaviors []annotations.FieldBehavior
}

// edgeDescriptor builds the edge-case schema:
//
//	message Edge {
//	  string id = 1;
//	  Node node = 2;                                  // self-recursive, protected
//	  A a = 3;                                        // mutually recursive with B, protected
//	  oneof choice { Guarded guarded = 4; string plain = 5; }
//	  string credential = 6 [INPUT_ONLY, IMMUTABLE];
//	  string token = 7 [INPUT_ONLY];                  // made immutable by name in the test
//	  oneof free { string left = 8; string right = 9; }
//	}
//	message Node { Node child = 1; string server = 2 [OUTPUT_ONLY]; string value = 3; }
//	message A { B b = 1; string server = 2 [OUTPUT_ONLY]; }
//	message B { A a = 1; string v = 2; }
//	message Guarded { string server = 1 [OUTPUT_ONLY]; string v = 2; }
func edgeDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	zero, one := int32(0), int32(1)
	out := annotations.FieldBehavior_OUTPUT_ONLY
	msg := func(name string, oneofs []string, fields ...edgeField) *descriptorpb.DescriptorProto {
		d := &descriptorpb.DescriptorProto{Name: new(name)}
		for _, o := range oneofs {
			d.OneofDecl = append(d.OneofDecl, &descriptorpb.OneofDescriptorProto{Name: new(o)})
		}

		for _, f := range fields {
			fd := &descriptorpb.FieldDescriptorProto{
				Name: new(f.name), Number: new(f.number), JsonName: new(jsonName(f.name)),
				Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:  descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(), OneofIndex: f.oneof,
			}
			if f.message != "" {
				fd.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
				fd.TypeName = new(".edgetest." + f.message)
			}

			if len(f.behaviors) > 0 {
				fd.Options = &descriptorpb.FieldOptions{}
				proto.SetExtension(fd.Options, annotations.E_FieldBehavior, f.behaviors)
			}

			d.Field = append(d.Field, fd)
		}

		return d
	}
	file := &descriptorpb.FileDescriptorProto{
		Name: new("edgetest.proto"), Package: new("edgetest"), Syntax: new("proto3"),
		Dependency: []string{"google/api/field_behavior.proto"},
		MessageType: []*descriptorpb.DescriptorProto{
			msg("Edge", []string{"choice", "free"},
				edgeField{name: "id", number: 1},
				edgeField{name: "node", number: 2, message: "Node"},
				edgeField{name: "a", number: 3, message: "A"},
				edgeField{name: "guarded", number: 4, message: "Guarded", oneof: &zero},
				edgeField{name: "plain", number: 5, oneof: &zero},
				edgeField{name: "credential", number: 6, behaviors: []annotations.FieldBehavior{
					annotations.FieldBehavior_INPUT_ONLY, annotations.FieldBehavior_IMMUTABLE,
				}},
				edgeField{name: "token", number: 7, behaviors: []annotations.FieldBehavior{annotations.FieldBehavior_INPUT_ONLY}},
				edgeField{name: "left", number: 8, oneof: &one},
				edgeField{name: "right", number: 9, oneof: &one},
			),
			msg("Node", nil,
				edgeField{name: "child", number: 1, message: "Node"},
				edgeField{name: "server", number: 2, behaviors: []annotations.FieldBehavior{out}},
				edgeField{name: "value", number: 3},
			),
			msg("A", nil, edgeField{name: "b", number: 1, message: "B"}, edgeField{name: "server", number: 2, behaviors: []annotations.FieldBehavior{out}}),
			msg("B", nil, edgeField{name: "a", number: 1, message: "A"}, edgeField{name: "v", number: 2}),
			msg("Guarded", nil, edgeField{name: "server", number: 1, behaviors: []annotations.FieldBehavior{out}}, edgeField{name: "v", number: 2}),
		},
	}
	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err)

	return fd.Messages().ByName("Edge")
}

// TestPlanWildcardExpandsRecursiveProtectedMessages checks that "*" over a
// self- or mutually recursive message holding server-managed fields
// terminates: a recursive sub-message neither the request nor the stored
// resource holds is left out, and an unset oneof is not switched.
func TestPlanWildcardExpandsRecursiveProtectedMessages(t *testing.T) {
	t.Parallel()
	md := edgeDescriptor(t)
	spec, specErr := update.NewSpec(dynamicpb.NewMessage(md))
	require.NoError(t, specErr)

	plan, err := spec.Plan(dynamicpb.NewMessage(md), maskOf([]string{"*"}))
	require.NoError(t, err)
	require.Empty(t, plan.Paths)

	// A value held by the request is expanded down to its last level.
	in := dynamicpb.NewMessage(md)
	node := in.Mutable(md.Fields().ByName("node")).Message()
	node.Mutable(node.Descriptor().Fields().ByName("child")).Message().Set(node.Descriptor().Fields().ByName("value"), protoreflect.ValueOfString("v"))

	plan, err = spec.Plan(in, maskOf([]string{"*"}))
	require.NoError(t, err)
	require.Equal(t, []string{"node.child.value", "node.value"}, plan.Paths)

	// Explicit leaves of the recursive messages stay updatable.
	plan, err = spec.Plan(dynamicpb.NewMessage(md), maskOf([]string{"node.value", "node.child.value", "a.b.v"}))
	require.NoError(t, err)
	require.Equal(t, []string{"node.child.value", "node.value", "a.b.v"}, plan.Paths)
}

// TestPlanOneofSwitchesOverOutputOnlyMembers checks every mask mode: a
// sibling holding only OUTPUT_ONLY fields does not guard its oneof — the
// server owns those values — so setting another member is accepted.
func TestPlanOneofSwitchesOverOutputOnlyMembers(t *testing.T) {
	t.Parallel()
	md := edgeDescriptor(t)
	spec, specErr := update.NewSpec(dynamicpb.NewMessage(md))
	require.NoError(t, specErr)

	plain := dynamicpb.NewMessage(md)
	plain.Set(md.Fields().ByName("plain"), protoreflect.ValueOfString("p"))

	for name, paths := range map[string][]string{"explicit": {"plain"}, "implied": nil} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			plan, err := spec.Plan(proto.Clone(plain), maskOf(paths))
			require.NoError(t, err)
			require.Equal(t, []string{"plain"}, plan.Paths)
		})
	}

	// Selecting the protected member itself clears only an unprotected
	// sibling; a oneof of plain members is free to switch.
	plan, err := spec.Plan(dynamicpb.NewMessage(md), maskOf([]string{"guarded.v", "left"}))
	require.NoError(t, err)
	require.Equal(t, []string{"guarded.v", "left"}, plan.Paths)
}

// TestImmutableSecretsAreNeverUpdated checks that a credential that is both
// INPUT_ONLY and IMMUTABLE, or INPUT_ONLY and declared immutable, is an
// immutable field: a mask naming it is refused and Secret never sets or
// clears it.
func TestImmutableSecretsAreNeverUpdated(t *testing.T) {
	t.Parallel()
	md := edgeDescriptor(t)
	spec, specErr := update.NewSpec(dynamicpb.NewMessage(md), update.WithImmutableFields("token"))
	require.NoError(t, specErr)

	in := dynamicpb.NewMessage(md)
	in.Set(md.Fields().ByName("credential"), protoreflect.ValueOfString("c"))
	in.Set(md.Fields().ByName("token"), protoreflect.ValueOfString("t"))
	in.Set(md.Fields().ByName("left"), protoreflect.ValueOfString("l"))

	plan, err := spec.Plan(in, nil, update.WithCurrent(proto.Clone(in)))
	require.NoError(t, err)
	require.Equal(t, []string{"left"}, plan.Paths)
	require.Nil(t, plan.Secret("credential", new("c")))
	require.Nil(t, plan.Secret("token", new("t")))

	for _, path := range []string{"credential", "token"} {
		plan, err := spec.Plan(in, maskOf([]string{path}))
		require.NoError(t, err)
		require.True(t, plan.Empty())
	}
}

// TestPlanRefusesOneofSwitchesOverClassifiedMembers checks that a oneof
// switch never clears a top-level member the spec classifies as immutable,
// identity or secret, in every mask mode, and that a merge of an accepted
// plan keeps such a stored member.
func TestPlanRefusesOneofSwitchesOverClassifiedMembers(t *testing.T) {
	t.Parallel()
	md := edgeDescriptor(t)
	in := dynamicpb.NewMessage(md)
	in.Set(md.Fields().ByName("right"), protoreflect.ValueOfString("r"))
	// left is immutable by name: switching the free oneof to right would
	// clear a stored left.
	spec, specErr := update.NewSpec(dynamicpb.NewMessage(md), update.WithImmutableFields("left"))
	require.NoError(t, specErr)

	for name, paths := range map[string][]string{"explicit": {"right"}, "implied": nil} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := spec.Plan(in, maskOf(paths))

			want := update.DefaultUpdateMaskField
			if paths != nil {
				want += ".paths[0]"
			}

			var fe *coreerrs.FieldError
			require.ErrorAs(t, err, &fe)
			require.Equal(t, want, fe.Field)
			require.Contains(t, fe.Reason, `would clear "left" of its oneof`)
		})
	}
}

// collDescriptor builds the collection schema:
//
//	message Coll {
//	  repeated Item items = 1;          // elements hold server-managed fields
//	  map<string, Item> by_key = 2;     // values hold server-managed fields
//	  Holder holder = 3;                // a singular parent of such a collection
//	  repeated Plain plains = 4;        // free to replace
//	  string secret = 5 [INPUT_ONLY];
//	}
//	message Item { string server = 1 [OUTPUT_ONLY]; string v = 2; }
//	message Holder { repeated Item items = 1; string note = 2; }
//	message Plain { string v = 1; }
func collDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	str := descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
	msgT := descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
	opt := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	rep := descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	field := func(name string, n int32, label *descriptorpb.FieldDescriptorProto_Label, typ *descriptorpb.FieldDescriptorProto_Type,
		typeName string, b annotations.FieldBehavior,
	) *descriptorpb.FieldDescriptorProto {
		fd := &descriptorpb.FieldDescriptorProto{Name: new(name), Number: new(n), JsonName: new(jsonName(name)), Label: label, Type: typ}
		if typeName != "" {
			fd.TypeName = new(typeName)
		}

		if b != annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED {
			fd.Options = &descriptorpb.FieldOptions{}
			proto.SetExtension(fd.Options, annotations.E_FieldBehavior, []annotations.FieldBehavior{b})
		}

		return fd
	}
	none := annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED
	file := &descriptorpb.FileDescriptorProto{
		Name: new("colltest.proto"), Package: new("colltest"), Syntax: new("proto3"),
		Dependency: []string{"google/api/field_behavior.proto"},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: new("Coll"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("items", 1, rep, msgT, ".colltest.Item", none),
					field("by_key", 2, rep, msgT, ".colltest.Coll.ByKeyEntry", none),
					field("holder", 3, opt, msgT, ".colltest.Holder", none),
					field("plains", 4, rep, msgT, ".colltest.Plain", none),
					field("secret", 5, opt, str, "", annotations.FieldBehavior_INPUT_ONLY),
				},
				NestedType: []*descriptorpb.DescriptorProto{{
					Name:    new("ByKeyEntry"),
					Field:   []*descriptorpb.FieldDescriptorProto{field("key", 1, opt, str, "", none), field("value", 2, opt, msgT, ".colltest.Item", none)},
					Options: &descriptorpb.MessageOptions{MapEntry: new(true)},
				}},
			},
			{Name: new("Item"), Field: []*descriptorpb.FieldDescriptorProto{
				field("server", 1, opt, str, "", annotations.FieldBehavior_OUTPUT_ONLY), field("v", 2, opt, str, "", none),
			}},
			{Name: new("Holder"), Field: []*descriptorpb.FieldDescriptorProto{
				field("items", 1, rep, msgT, ".colltest.Item", none), field("note", 2, opt, str, "", none),
			}},
			{Name: new("Plain"), Field: []*descriptorpb.FieldDescriptorProto{field("v", 1, opt, str, "", none)}},
		},
	}
	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err)

	return fd.Messages().ByName("Coll")
}

// TestPlanReplacesCollectionsWhole checks that a repeated or map field whose
// elements hold an OUTPUT_ONLY field is replaced whole — explicitly, through a
// singular parent, by "*" or by an implied mask — and never refused: its
// elements are new values whose OUTPUT_ONLY fields the server owns.
func TestPlanReplacesCollectionsWhole(t *testing.T) {
	t.Parallel()
	md := collDescriptor(t)
	spec, specErr := update.NewSpec(dynamicpb.NewMessage(md))
	require.NoError(t, specErr)

	empty := dynamicpb.NewMessage(md)

	for _, path := range []string{"items", "byKey", "holder", "holder.items"} {
		plan, err := spec.Plan(empty, maskOf([]string{path}))
		require.NoError(t, err, path)
		require.Equal(t, []string{fieldmask.ResolvePath(md, path)}, plan.Paths, path)
	}

	plan, err := spec.Plan(empty, maskOf([]string{"*"}))
	require.NoError(t, err)
	require.Equal(t, []string{"items", "by_key", "holder", "plains"}, plan.Paths)

	withItems := dynamicpb.NewMessage(md)
	item := dynamicpb.NewMessage(md.Fields().ByName("items").Message())
	item.Set(item.Descriptor().Fields().ByName("v"), protoreflect.ValueOfString("v"))
	withItems.Mutable(md.Fields().ByName("items")).List().Append(protoreflect.ValueOfMessage(item))
	plan, err = spec.Plan(withItems, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"items"}, plan.Paths)
}

// TestPlanWildcardSetsASuppliedSecret checks the secret contract of "*": a
// secret the request sets is set, one it leaves unset is kept (never
// cleared).
func TestPlanWildcardSetsASuppliedSecret(t *testing.T) {
	t.Parallel()
	md := planDescriptor(t)
	spec, specErr := update.NewSpec(dynamicpb.NewMessage(md), update.WithImmutableFields("code"))
	require.NoError(t, specErr)

	in := planResource(md, map[string]string{"secret": "s"})
	plan, err := spec.Plan(in, maskOf([]string{"*"}))
	require.NoError(t, err)
	require.Equal(t, "s", *plan.Secret("secret", new("s")))

	plan, err = spec.Plan(dynamicpb.NewMessage(md), maskOf([]string{"*"}))
	require.NoError(t, err)
	require.Nil(t, plan.Secret("secret", nil), `"*" never clears an unset secret`)
}

// TestPlanChecksOneofSwitchesOfSecrets checks that a secret the request sets
// or the mask names is refused when it belongs to a oneof with a guarded
// sibling: writing it would clear that sibling.
func TestPlanChecksOneofSwitchesOfSecrets(t *testing.T) {
	t.Parallel()
	sd := secretDescriptor(t)
	spec, specErr := update.NewSpec(dynamicpb.NewMessage(sd))
	require.NoError(t, specErr)

	in := dynamicpb.NewMessage(sd)
	in.Set(sd.Fields().ByName("key"), protoreflect.ValueOfString("k"))

	for name, paths := range map[string][]string{"supplied, implied": nil, "supplied, wildcard": {"*"}, "named": {"key"}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := spec.Plan(in, maskOf(paths))

			var fe *coreerrs.FieldError
			require.ErrorAs(t, err, &fe)
			require.Contains(t, fe.Reason, `would clear "pinned" of its oneof`)
		})
	}
}

// TestPlanNestedInputOnlyIsAnOrdinaryLeaf pins the narrowed secret contract:
// a nested INPUT_ONLY field is a mutable leaf, implied when set.
func TestPlanNestedInputOnlyIsAnOrdinaryLeaf(t *testing.T) {
	t.Parallel()
	sd := secretDescriptor(t)
	spec, specErr := update.NewSpec(dynamicpb.NewMessage(sd))
	require.NoError(t, specErr)

	in := dynamicpb.NewMessage(sd)
	prof := in.Mutable(sd.Fields().ByName("profile")).Message()
	prof.Set(prof.Descriptor().Fields().ByName("token"), protoreflect.ValueOfString("t"))

	plan, err := spec.Plan(in, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"profile.token"}, plan.Paths)
	require.Nil(t, plan.Secret("token", new("t")), "only top-level INPUT_ONLY fields are secrets")
}

// secretDescriptor builds the secret schema:
//
//	message Sec {
//	  oneof cred { string key = 1 [INPUT_ONLY]; string pinned = 2 [IMMUTABLE]; }
//	  Profile profile = 3;
//	}
//	message Profile { string token = 1 [INPUT_ONLY]; }
func secretDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	zero := int32(0)
	field := func(name string, n int32, oneof *int32, typeName string, b annotations.FieldBehavior) *descriptorpb.FieldDescriptorProto {
		fd := &descriptorpb.FieldDescriptorProto{
			Name: new(name), Number: new(n), JsonName: new(jsonName(name)), OneofIndex: oneof,
			Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
		}
		if typeName != "" {
			fd.Type, fd.TypeName = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), new(typeName)
		}

		if b != annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED {
			fd.Options = &descriptorpb.FieldOptions{}
			proto.SetExtension(fd.Options, annotations.E_FieldBehavior, []annotations.FieldBehavior{b})
		}

		return fd
	}
	file := &descriptorpb.FileDescriptorProto{
		Name: new("sectest.proto"), Package: new("sectest"), Syntax: new("proto3"),
		Dependency: []string{"google/api/field_behavior.proto"},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name:      new("Sec"),
				OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: new("cred")}},
				Field: []*descriptorpb.FieldDescriptorProto{
					field("key", 1, &zero, "", annotations.FieldBehavior_INPUT_ONLY),
					field("pinned", 2, &zero, "", annotations.FieldBehavior_IMMUTABLE),
					field("profile", 3, nil, ".sectest.Profile", annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED),
				},
			},
			{Name: new("Profile"), Field: []*descriptorpb.FieldDescriptorProto{
				field("token", 1, nil, "", annotations.FieldBehavior_INPUT_ONLY),
			}},
		},
	}
	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err)

	return fd.Messages().ByName("Sec")
}

// TestImpliedPathsNeverBringsBackASkippedField checks that a skip inside a
// well-known type leaves its whole leaf out instead of letting the collapsed
// leaf replace the skipped field.
func TestImpliedPathsNeverBringsBackASkippedField(t *testing.T) {
	t.Parallel()

	ts := timestamppb.New(time.Unix(10, 5))
	md := tsDescriptor(t)
	m := dynamicpb.NewMessage(md)
	m.Set(md.Fields().ByName("scheduled_at"), protoreflect.ValueOfMessage(ts.ProtoReflect()))
	m.Set(md.Fields().ByName("name"), protoreflect.ValueOfString("n"))
	require.Equal(t, []string{"scheduled_at", "name"}, update.ImpliedPaths(m))
	require.Equal(t, []string{"name"}, update.ImpliedPaths(m, update.WithImpliedSkipFields("scheduled_at.seconds")))
	require.Equal(t, []string{"name"}, update.ImpliedPaths(m, update.WithImpliedSkipFields("scheduled_at")))
}

// tsDescriptor builds message Sched { google.protobuf.Timestamp scheduled_at = 1; string name = 2; }.
func tsDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	file := &descriptorpb.FileDescriptorProto{
		Name: new("tstest.proto"), Package: new("tstest"), Syntax: new("proto3"),
		Dependency: []string{"google/protobuf/timestamp.proto"},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: new("Sched"),
			Field: []*descriptorpb.FieldDescriptorProto{
				{
					Name: new("scheduled_at"), Number: proto.Int32(1), JsonName: new("scheduledAt"),
					Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
					TypeName: new(".google.protobuf.Timestamp"),
				},
				{
					Name: new("name"), Number: proto.Int32(2), JsonName: new("name"),
					Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
				},
			},
		}},
	}
	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err)

	return fd.Messages().ByName("Sched")
}

func TestImpliedPathsIdentityAndSecrets(t *testing.T) {
	t.Parallel()
	m := planResource(planDescriptor(t), map[string]string{"id": "1", "etag": "tag", "name": "name", "code": "fixed", "secret": "password"})
	require.Equal(t, []string{"name", "code"}, update.ImpliedPaths(m))
	require.Equal(t, []string{"etag", "name"}, update.ImpliedPaths(m, update.WithIdentityFields("id"), update.WithImmutableFields("code")))
}
