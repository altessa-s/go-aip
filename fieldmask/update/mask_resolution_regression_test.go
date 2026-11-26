// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"errors"
	"slices"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
	maskpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldmasktest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func TestResolvedScopeAndWholeWrite(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		section *maskpb.Address
	}{
		{name: "absent"},
		{name: "present", section: &maskpb.Address{City: new("new")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := &maskpb.UpdateRequest{ShippingAddress: tc.section}
			spec, err := update.NewSpec(req)
			require.NoError(t, err)

			mask := maskOf([]string{"shipping_address"})
			resolved, err := spec.ResolveMask(req, mask)
			require.NoError(t, err)

			paths := make([]string, 0, len(resolved.Paths()))
			for _, path := range resolved.Paths() {
				paths = append(paths, path.Path)
			}

			require.Equal(t, []string{"shipping_address.city", "shipping_address.street", "shipping_address.zip"}, paths)

			stored := &maskpb.UpdateRequest{ShippingAddress: &maskpb.Address{City: new("old")}}
			stored.ShippingAddress.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
			field := stored.ProtoReflect().Descriptor().Fields().ByName("shipping_address")
			scoped := proto.Clone(stored)
			require.NoError(t, update.MergePaths(scoped, req, paths))
			require.True(t, scoped.ProtoReflect().Has(field))
			require.NotEmpty(t, scoped.ProtoReflect().Get(field).Message().GetUnknown())
			require.NoError(t, update.MergePaths(stored, req, mask.GetPaths()))
			require.Equal(t, tc.section != nil, stored.ProtoReflect().Has(field))

			if tc.section != nil {
				require.Empty(t, stored.ShippingAddress.ProtoReflect().GetUnknown())
			}
		})
	}
}

func TestValidateResolvedOmittedProtected(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		opts  []update.Option
		field string
		value string
		want  annotations.FieldBehavior
	}{
		{name: "changed", field: "region", value: "new", want: annotations.FieldBehavior_IMMUTABLE},
		{name: "equal", field: "region", value: "old"},
		{name: "changed with allow-list", opts: []update.Option{update.WithAllowedPaths("display_name")}, field: "region", value: "new", want: annotations.FieldBehavior_IMMUTABLE},
		{name: "equal with allow-list", opts: []update.Option{update.WithAllowedPaths("display_name")}, field: "region", value: "old"},
		{name: "identifier with empty allow-list", opts: []update.Option{update.WithAllowedPaths()}, field: "name", value: "new", want: annotations.FieldBehavior_IDENTIFIER},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req, stored := &pb.Account{}, &pb.Account{}
			fd := req.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(tc.field))
			req.ProtoReflect().Set(fd, protoreflect.ValueOfString(tc.value))
			stored.ProtoReflect().Set(fd, protoreflect.ValueOfString("old"))

			spec, err := update.NewSpec(req, tc.opts...)
			require.NoError(t, err)
			resolved, err := spec.ResolveMask(req, nil)
			require.NoError(t, err)
			require.Empty(t, resolved.Paths())
			violations, err := spec.ValidateResolved(req, resolved, update.WithCurrent(stored))
			require.NoError(t, err)

			_, planErr := spec.Plan(req, nil, update.WithCurrent(stored))

			if tc.want == annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED {
				require.Empty(t, violations)
				require.NoError(t, planErr)
			} else {
				require.ErrorIs(t, planErr, update.ErrProtectedField)
				require.Len(t, violations, 1)
				require.Equal(t, tc.field, violations[0].Path)
				require.Equal(t, tc.want, violations[0].Behavior)
			}
		})
	}
}

func TestResolveMaskMapValue(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		key    string
		values map[string]*pb.Owner
		want   string
	}{
		{name: "present", key: "bob", values: map[string]*pb.Owner{"bob": {}}, want: "delegates.bob.email"},
		{name: "absent", key: "bob"},
		{name: "wildcard", key: "*", values: map[string]*pb.Owner{"bob": {}}, want: "delegates.bob.email"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := &pb.Account{Delegates: tc.values}
			path := "delegates." + tc.key
			spec, err := update.NewSpec(req, update.WithAllowedPaths(path, path+".email"))
			require.NoError(t, err)
			resolved, err := spec.ResolveMask(req, maskOf([]string{path}))
			require.NoError(t, err)
			require.Equal(t, []update.ResolvedPath{{Path: path + ".email", Index: 0}}, resolved.Paths())
			violations, err := spec.ValidateResolved(req, resolved)
			require.NoError(t, err)

			if tc.want == "" {
				_, err := spec.Plan(req, maskOf([]string{path}))
				require.NoError(t, err)
				require.Empty(t, violations)

				return
			}

			require.Len(t, violations, 1)
			require.Equal(t, tc.want, violations[0].Path)
		})
	}
}

func TestValidateResolvedCoveredCounts(t *testing.T) {
	t.Parallel()

	for _, paths := range [][]string{{"shelves", "shelves.*.street"}, {"shelves.*.street", "shelves"}} {
		t.Run(paths[0], func(t *testing.T) {
			t.Parallel()

			req := &maskpb.Catalog{Shelves: []*maskpb.Shelf{{}}}
			current := &maskpb.Catalog{Shelves: []*maskpb.Shelf{{}, {}}}
			spec, err := update.NewSpec(req)
			require.NoError(t, err)

			mask := maskOf(paths)
			_, err = spec.Plan(req, mask, update.WithCurrent(current))
			require.ErrorIs(t, err, update.ErrElementCountMismatch)
			resolved, err := spec.ResolveMask(req, mask)
			require.NoError(t, err)
			require.Equal(t, []update.ResolvedPath{{Path: "shelves", Index: slices.Index(paths, "shelves")}}, resolved.Paths())
			_, err = spec.ValidateResolved(req, resolved, update.WithCurrent(current))
			require.ErrorIs(t, err, update.ErrElementCountMismatch)
			pathErr, ok := errors.AsType[*update.MaskPathError](err)
			require.True(t, ok)
			require.Equal(t, slices.Index(paths, "shelves.*.street"), pathErr.Index)
		})
	}
}

func TestResolvedRequiredMessages(t *testing.T) {
	t.Parallel()

	md := resolvedTestDescriptor(t)
	for _, tc := range []struct {
		name    string
		paths   []string
		present []protoreflect.Name
		want    []string
	}{
		{name: "inactive branch under star", paths: []string{"*"}, present: []protoreflect.Name{"alternative", "required_empty", "required_output", "required_parent"}},
		{name: "unset branch under star", paths: []string{"*"}, present: []protoreflect.Name{"required_empty", "required_output", "required_parent"}},
		{name: "inactive branch selected whole", paths: []string{"choice"}, present: []protoreflect.Name{"alternative"}},
		{name: "inactive branch under parent", paths: []string{"wrapped"}, present: []protoreflect.Name{"wrapped"}},
		{name: "active branch under parent", paths: []string{"choice"}, present: []protoreflect.Name{"choice"}, want: []string{"choice.leaf"}},
		{name: "ordinary absent parent", paths: []string{"optional_parent"}, want: []string{"optional_parent.leaf"}},
		{name: "required parent suppresses required leaf", paths: []string{"required_parent"}, want: []string{"required_parent"}},
		{name: "required empty and output-only in descriptor order", paths: []string{"required_output", "required_empty"}, want: []string{"required_output", "required_empty"}},
		{name: "present empty and output-only", paths: []string{"required_empty", "required_output"}, present: []protoreflect.Name{"required_empty", "required_output"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := dynamicpb.NewMessage(md)
			for _, name := range tc.present {
				fd := md.Fields().ByName(name)
				if fd.Message() != nil {
					req.Mutable(fd)
				} else {
					req.Set(fd, protoreflect.ValueOfString("selected"))
				}
			}

			if req.Has(md.Fields().ByName("wrapped")) {
				wrapped := req.Mutable(md.Fields().ByName("wrapped")).Message()
				wrapped.Set(wrapped.Descriptor().Fields().ByName("alternative"), protoreflect.ValueOfString("selected"))
			}

			if tc.paths[0] == "*" {
				req.Mutable(md.Fields().ByName("optional_parent")).Message().Set(
					md.Fields().ByName("optional_parent").Message().Fields().ByName("leaf"), protoreflect.ValueOfString("set"))
				parent := req.Mutable(md.Fields().ByName("required_parent")).Message()
				parent.Set(parent.Descriptor().Fields().ByName("leaf"), protoreflect.ValueOfString("set"))
			}

			spec, err := update.NewSpec(req)
			require.NoError(t, err)
			resolved, err := spec.ResolveMask(req, maskOf(tc.paths))
			require.NoError(t, err)
			violations, err := spec.ValidateResolved(req, resolved)
			require.NoError(t, err)

			var got []string

			for _, v := range violations {
				require.Equal(t, annotations.FieldBehavior_REQUIRED, v.Behavior)
				got = append(got, v.Path)
			}

			require.Equal(t, tc.want, got)
		})
	}
}

func resolvedTestDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	field := func(name string, number int32, typ string, behavior annotations.FieldBehavior) *descriptorpb.FieldDescriptorProto {
		fd := &descriptorpb.FieldDescriptorProto{Name: new(name), Number: new(number), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: new(typ)}
		if typ == "" {
			fd.Type = descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
			fd.TypeName = nil
		}

		if behavior != annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED {
			fd.Options = &descriptorpb.FieldOptions{}
			proto.SetExtension(fd.Options, annotations.E_FieldBehavior, []annotations.FieldBehavior{behavior})
		}

		return fd
	}

	const required = annotations.FieldBehavior_REQUIRED

	choice := field("choice", 1, ".resolution.Leaf", 0)
	alternative := field("alternative", 2, "", 0)
	choice.OneofIndex, alternative.OneofIndex = new(int32(0)), new(int32(0))
	rows := field("rows", 7, ".resolution.Row", 0)
	items := field("items", 1, ".resolution.Leaf", 0)
	rows.Label, items.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(), descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name: new("mask_resolution_test.proto"), Package: new("resolution"), Syntax: new("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: new("Root"), OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: new("branch")}},
				Field: []*descriptorpb.FieldDescriptorProto{
					choice, alternative, field("optional_parent", 3, ".resolution.Leaf", 0),
					field("required_parent", 4, ".resolution.Leaf", required), field("required_output", 6, ".resolution.Output", required),
					field("required_empty", 5, ".resolution.Empty", required), rows, field("wrapped", 8, ".resolution.Branch", 0),
				}},
			{Name: new("Leaf"), Field: []*descriptorpb.FieldDescriptorProto{field("leaf", 1, "", required)}},
			{Name: new("Empty")},
			{Name: new("Output"), Field: []*descriptorpb.FieldDescriptorProto{field("server", 1, "", annotations.FieldBehavior_OUTPUT_ONLY)}},
			{Name: new("Row"), Field: []*descriptorpb.FieldDescriptorProto{items}},
			{Name: new("Branch"), OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: new("branch")}},
				Field: []*descriptorpb.FieldDescriptorProto{proto.CloneOf(choice), proto.CloneOf(alternative)}},
		},
	}, nil)
	require.NoError(t, err)

	return file.Messages().ByName("Root")
}

func TestResolvedAllowListConfiguration(t *testing.T) {
	t.Parallel()

	req := &pb.Account{}

	for _, path := range []string{"backup_owner..email", "unknown", "update_time", "uid", "delegates.bob.create_time", "members.*.email", "members.*"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			_, err := update.NewSpec(req, update.WithAllowedPaths(path))
			require.Error(t, err)
			_, isClientError := errors.AsType[*coreerrs.FieldError](err)
			require.False(t, isClientError)
		})
	}

	t.Run("per call is a programming error", func(t *testing.T) {
		t.Parallel()

		spec, err := update.NewSpec(req)
		require.NoError(t, err)
		_, err = spec.ResolveMask(req, maskOf([]string{"display_name"}), update.WithAllowedPaths("display_name"))
		require.Error(t, err)
		require.ErrorIs(t, err, update.ErrAllowedPathsAtCall)
		_, isMaskError := errors.AsType[*update.MaskPathError](err)
		require.False(t, isMaskError)

		_, isClientError := errors.AsType[*coreerrs.FieldError](err)
		require.False(t, isClientError)
	})
}

func TestResolveMaskSelectionBoundaries(t *testing.T) {
	t.Parallel()

	req := &pb.Account{DisplayName: "set"}

	for _, tc := range []struct {
		name string
		opts []update.Option
		mask []string
		want []update.ResolvedPath
		bad  bool
	}{
		{name: "parent only", opts: []update.Option{update.WithAllowedPaths("backup_owner")}, mask: []string{"backup_owner"},
			want: []update.ResolvedPath{{Path: "backup_owner.email", Index: 0}, {Path: "backup_owner.org", Index: 0}, {Path: "backup_owner.note", Index: 0}}},
		{name: "list element", mask: []string{"members.*.email"}, want: []update.ResolvedPath{{Path: "members", Index: 0}}},
		{name: "identifier", mask: []string{"name"}, want: []update.ResolvedPath{{Path: "name", Index: 0}}},
		{name: "output only dropped", mask: []string{"update_time", "uid", "backup_owner.create_time", "delegates.bob.create_time"}},
		{name: "output only rejected", opts: []update.Option{update.WithAllowedPaths("display_name")}, mask: []string{"display_name", "uid"}, bad: true},
		{name: "allow-listed leaf", opts: []update.Option{update.WithAllowedPaths("display_name")}, mask: []string{"display_name"},
			want: []update.ResolvedPath{{Path: "display_name", Index: 0}}},
		{name: "explicit with empty allow-list", opts: []update.Option{update.WithAllowedPaths()}, mask: []string{"display_name"}, bad: true},
		{name: "omitted with empty allow-list", opts: []update.Option{update.WithAllowedPaths()}},
		{name: "star with empty allow-list", opts: []update.Option{update.WithAllowedPaths()}, mask: []string{"*"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			spec, err := update.NewSpec(req, tc.opts...)
			require.NoError(t, err)

			resolved, err := spec.ResolveMask(req, maskOf(tc.mask))
			if tc.bad {
				require.ErrorIs(t, err, fieldmask.ErrInvalidPath)
				pathErr, ok := errors.AsType[*update.MaskPathError](err)
				require.True(t, ok)
				require.Equal(t, len(tc.mask)-1, pathErr.Index)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, resolved.Paths())
		})
	}

	t.Run("recursive field under star", func(t *testing.T) {
		t.Parallel()

		spec, err := update.NewSpec(req)
		require.NoError(t, err)
		resolved, err := spec.ResolveMask(req, maskOf([]string{"*"}))
		require.NoError(t, err)
		require.Contains(t, resolved.Paths(), update.ResolvedPath{Path: "parent", Index: update.SynthesizedPathIndex})
	})
}

func TestValidateResolvedReadOnly(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		paths []string
	}{
		{name: "explicit empty", paths: []string{}},
		{name: "mutable leaf", paths: []string{"description"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := &pb.Resource{Description: "new"}
			current := &pb.Resource{Description: "old"}
			spec, err := update.NewSpec(req)
			require.NoError(t, err)
			resolved, err := spec.ResolveMask(req, maskOf(tc.paths))
			require.NoError(t, err)

			wrap := func(m proto.Message) proto.Message {
				return &resolvedReadOnlyMessage{Message: m.ProtoReflect(), t: t}
			}
			violations, err := spec.ValidateResolved(wrap(req), resolved, update.WithCurrent(wrap(current)))
			require.NoError(t, err)
			require.Empty(t, violations)
		})
	}

	t.Run("empty validates resource type first", func(t *testing.T) {
		t.Parallel()

		req := &pb.Resource{}
		spec, err := update.NewSpec(req)
		require.NoError(t, err)
		resolved, err := spec.ResolveMask(req, maskOf([]string{}))
		require.NoError(t, err)
		_, err = spec.ValidateResolved(&pb.Account{}, resolved)
		require.ErrorIs(t, err, update.ErrMessageMismatch)
		_, err = spec.ValidateResolved(req, resolved, update.WithCurrent(&pb.Account{}))
		require.NoError(t, err)
	})
}

type resolvedReadOnlyMessage struct {
	protoreflect.Message
	t *testing.T
}

func (m *resolvedReadOnlyMessage) ProtoReflect() protoreflect.Message { return m }
func (m *resolvedReadOnlyMessage) New() protoreflect.Message {
	m.t.Error("validation allocated a copy of a resource")
	return m.Message.New()
}

func TestValidateResolvedConcretePositions(t *testing.T) {
	t.Parallel()

	t.Run("map keys", func(t *testing.T) {
		t.Parallel()

		req := &pb.Account{Delegates: map[string]*pb.Owner{"a.b": {}, "42": {}, "*": {}, "a\x60b": {}}}
		spec, err := update.NewSpec(req)
		require.NoError(t, err)
		resolved, err := spec.ResolveMask(req, maskOf([]string{"delegates"}))
		require.NoError(t, err)
		violations, err := spec.ValidateResolved(req, resolved)
		require.NoError(t, err)
		require.Len(t, violations, 4)

		for i, key := range []string{"*", "42", "a.b", "a\x60b"} {
			v := violations[i]
			require.Empty(t, v.ListIndexes)
			require.Equal(t, "delegates["+strconv.Quote(key)+"].email", v.IndexedPath())
			require.Equal(t, []update.MapKey{{Segment: 0, Key: key}}, v.MapKeys)

			wantPath := []string{"delegates.`*`.email", "delegates.42.email", "delegates.`a.b`.email", "delegates.`a``b`.email"}
			require.Equal(t, wantPath[i], v.Path)
		}
	})

	t.Run("nested lists", func(t *testing.T) {
		t.Parallel()

		md := resolvedTestDescriptor(t)
		req := dynamicpb.NewMessage(md)
		rows := req.Mutable(md.Fields().ByName("rows")).List()
		row := rows.AppendMutable().Message()
		items := row.Mutable(row.Descriptor().Fields().ByName("items")).List()
		items.AppendMutable().Message().Set(items.NewElement().Message().Descriptor().Fields().ByName("leaf"), protoreflect.ValueOfString("set"))
		items.AppendMutable()

		spec, err := update.NewSpec(req)
		require.NoError(t, err)
		resolved, err := spec.ResolveMask(req, maskOf([]string{"rows"}))
		require.NoError(t, err)
		violations, err := spec.ValidateResolved(req, resolved)
		require.NoError(t, err)
		require.Len(t, violations, 1)
		require.Equal(t, []update.ListIndex{{Segment: 0, Index: 0}, {Segment: 1, Index: 1}}, violations[0].ListIndexes)
		require.Equal(t, "rows[0].items[1].leaf", violations[0].IndexedPath())
	})
}

func TestResolveAndValidateSharedSpec(t *testing.T) {
	t.Parallel()

	req := &pb.Account{Delegates: map[string]*pb.Owner{"bob": {}}}
	spec, err := update.NewSpec(req, update.WithAllowedPaths("delegates.bob", "delegates.bob.email", "display_name"))
	require.NoError(t, err)

	mask := maskOf([]string{"delegates.bob"})
	resolved, err := spec.ResolveMask(req, mask)
	require.NoError(t, err)

	want := []update.ResolvedPath{{Path: "delegates.bob.email", Index: 0}}

	for range 16 {
		t.Run("shared", func(t *testing.T) {
			t.Parallel()

			independent, err := spec.ResolveMask(req, mask)
			require.NoError(t, err)
			require.Equal(t, want, independent.Paths())
			paths := resolved.Paths()
			paths[0].Path = "display_name"

			require.Equal(t, want, resolved.Paths())
			violations, err := spec.ValidateResolved(req, resolved)
			require.NoError(t, err)
			require.Len(t, violations, 1)
			require.Equal(t, "delegates.bob.email", violations[0].Path)
		})
	}
}

func TestValidateResolvedExplicitEmptyLeaf(t *testing.T) {
	t.Parallel()

	req := &pb.Account{}
	spec, err := update.NewSpec(req, update.WithAllowedPaths("display_name"))
	require.NoError(t, err)
	resolved, err := spec.ResolveMask(req, maskOf([]string{"display_name"}))
	require.NoError(t, err)
	violations, err := spec.ValidateResolved(req, resolved)
	require.NoError(t, err)
	require.Len(t, violations, 1)
	require.Equal(t, "display_name", violations[0].Path)
	require.Equal(t, annotations.FieldBehavior_REQUIRED, violations[0].Behavior)
}

func TestValidateResolvedExcludedProtectedScope(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		makeResource func(string) *pb.Account
		path         string
		opts         []update.Option
	}{
		{
			name: "list",
			makeResource: func(org string) *pb.Account {
				return &pb.Account{Members: []*pb.Owner{{Email: "member", Org: org}}}
			},
			path: "members[0].org",
		},
		{
			name: "atomic value",
			makeResource: func(org string) *pb.Account {
				return &pb.Account{BackupOwner: &pb.Owner{Email: "owner", Org: org}}
			},
			path: "backup_owner.org",
			opts: []update.Option{atomicNames("fieldbehaviortest.v1.Owner")},
		},
	} {
		for _, org := range []string{"old", "changed"} {
			t.Run(tc.name+"/"+org, func(t *testing.T) {
				t.Parallel()

				req, current := tc.makeResource(org), tc.makeResource("old")
				spec, err := update.NewSpec(req, append(slices.Clone(tc.opts), update.WithAllowedPaths("display_name"))...)
				require.NoError(t, err)
				resolved, err := spec.ResolveMask(req, nil)
				require.NoError(t, err)
				require.Empty(t, resolved.Paths())

				_, planErr := spec.Plan(req, nil, update.WithCurrent(current))
				violations, err := spec.ValidateResolved(req, resolved, update.WithCurrent(current))
				require.NoError(t, err)

				if org == "old" {
					require.NoError(t, planErr)
					require.Empty(t, violations)

					return
				}

				require.ErrorIs(t, planErr, update.ErrProtectedField)
				require.Len(t, violations, 1)
				require.Equal(t, tc.path, violations[0].IndexedPath())
				require.Equal(t, annotations.FieldBehavior_IMMUTABLE, violations[0].Behavior)
			})
		}
	}
}

func TestResolveAllowedMapSelection(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		paths []string
		index int
		want  []string
	}{
		{name: "explicit", paths: []string{"delegates.bob"}, want: []string{"delegates.bob.email", "delegates.bob.note"}},
		{name: "star", paths: []string{"*"}, index: update.SynthesizedPathIndex, want: []string{"delegates.bob.email", "delegates.bob.note"}},
		{name: "omitted", index: update.SynthesizedPathIndex, want: []string{"delegates.bob.email"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := &pb.Account{Delegates: map[string]*pb.Owner{"bob": {Email: "bob"}, "alice": {Email: "alice", Note: "set"}}}
			spec, err := update.NewSpec(req, update.WithAllowedPaths("delegates.bob", "delegates.bob.email", "delegates.bob.note"))
			require.NoError(t, err)
			resolved, err := spec.ResolveMask(req, maskOf(tc.paths))
			require.NoError(t, err)

			want := make([]update.ResolvedPath, 0, len(tc.want))
			for _, path := range tc.want {
				want = append(want, update.ResolvedPath{Path: path, Index: tc.index})
			}

			require.Equal(t, want, resolved.Paths())
		})
	}
}

func TestResolveOmittedMapWildcard(t *testing.T) {
	t.Parallel()

	req := &pb.Account{Delegates: map[string]*pb.Owner{
		"alice": {Email: "alice"}, "bob": {Note: "unset email"}, "*": {Email: "literal"},
	}}
	spec, err := update.NewSpec(req, update.WithAllowedPaths("delegates.*.email"))
	require.NoError(t, err)
	resolved, err := spec.ResolveMask(req, nil)
	require.NoError(t, err)
	require.ElementsMatch(t, []update.ResolvedPath{
		{Path: "delegates.alice.email", Index: update.SynthesizedPathIndex},
		{Path: "delegates.`*`.email", Index: update.SynthesizedPathIndex},
	}, resolved.Paths())
	violations, err := spec.ValidateResolved(req, resolved)
	require.NoError(t, err)
	require.Empty(t, violations)
}

func TestValidateResolvedExplicitOneofLeaf(t *testing.T) {
	t.Parallel()

	for _, paths := range [][]string{{"choice.leaf"}, {"choice", "choice.leaf"}, {"choice.leaf", "choice"}} {
		t.Run(maskOf(paths).String(), func(t *testing.T) {
			t.Parallel()

			md := resolvedTestDescriptor(t)
			req := dynamicpb.NewMessage(md)
			req.Set(md.Fields().ByName("alternative"), protoreflect.ValueOfString("set"))
			spec, err := update.NewSpec(req)
			require.NoError(t, err)

			mask := maskOf(paths)
			_, err = spec.Plan(req, mask)
			require.ErrorIs(t, err, update.ErrBehaviorViolation)
			pathErr, ok := errors.AsType[*update.MaskPathError](err)
			require.True(t, ok)

			index := slices.Index(paths, "choice.leaf")
			require.Equal(t, index, pathErr.Index)

			resolved, err := spec.ResolveMask(req, mask)
			require.NoError(t, err)
			require.Equal(t, []update.ResolvedPath{{Path: "choice.leaf", Index: index}}, resolved.Paths())
			violations, err := spec.ValidateResolved(req, resolved)
			require.NoError(t, err)
			require.Len(t, violations, 1)
			require.Equal(t, "choice.leaf", violations[0].Path)
			require.Equal(t, index, violations[0].Index)
			require.Equal(t, annotations.FieldBehavior_REQUIRED, violations[0].Behavior)
		})
	}
}

func TestValidateResolvedNestedRequiredEmpty(t *testing.T) {
	t.Parallel()

	file := protodesc.ToFileDescriptorProto(resolvedTestDescriptor(t).ParentFile())
	leaf := file.MessageType[1].Field[0]
	leaf.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
	leaf.TypeName = new(".resolution.Empty")

	rowParent := proto.CloneOf(file.MessageType[0].Field[3])
	rowParent.Number = new(int32(2))
	file.MessageType[4].Field = append(file.MessageType[4].Field, rowParent)

	fd, err := protodesc.NewFile(file, nil)
	require.NoError(t, err)

	md := fd.Messages().ByName("Root")
	req := dynamicpb.NewMessage(md)
	req.Mutable(md.Fields().ByName("required_parent"))
	spec, err := update.NewSpec(req)
	require.NoError(t, err)
	resolved, err := spec.ResolveMask(req, maskOf([]string{"required_parent"}))
	require.NoError(t, err)

	for range 200 {
		violations, err := spec.ValidateResolved(req, resolved)
		require.NoError(t, err)
		require.Len(t, violations, 1)
		require.Equal(t, "required_parent.leaf", violations[0].Path)
		require.Equal(t, annotations.FieldBehavior_REQUIRED, violations[0].Behavior)
	}

	t.Run("per list element", func(t *testing.T) {
		t.Parallel()

		req := dynamicpb.NewMessage(md)
		rows := req.Mutable(md.Fields().ByName("rows")).List()
		rows.AppendMutable()
		present := rows.AppendMutable().Message()
		present.Mutable(present.Descriptor().Fields().ByName("required_parent"))

		resolved, err := spec.ResolveMask(req, maskOf([]string{"rows.*.required_parent"}))
		require.NoError(t, err)
		violations, err := spec.ValidateResolved(req, resolved)
		require.NoError(t, err)
		require.Len(t, violations, 2)
		require.Equal(t, "rows[0].required_parent", violations[0].IndexedPath())
		require.Equal(t, "rows[1].required_parent.leaf", violations[1].IndexedPath())
	})
}

func TestValidateResolvedForeignChild(t *testing.T) {
	t.Parallel()

	for _, incompatible := range []bool{false, true} {
		t.Run(strconv.FormatBool(incompatible), func(t *testing.T) {
			t.Parallel()

			md := resolvedTestDescriptor(t)

			file := protodesc.ToFileDescriptorProto(md.ParentFile())
			if incompatible {
				file.MessageType[1].Field[0].Type = descriptorpb.FieldDescriptorProto_TYPE_BOOL.Enum()
			}

			foreign, err := protodesc.NewFile(file, nil)
			require.NoError(t, err)

			req := dynamicpb.NewMessage(md)
			req.Set(md.Fields().ByName("optional_parent"), protoreflect.ValueOfMessage(dynamicpb.NewMessage(foreign.Messages().ByName("Leaf"))))
			spec, err := update.NewSpec(req)
			require.NoError(t, err)

			mask := maskOf([]string{"optional_parent.leaf"})
			resolved, err := spec.ResolveMask(req, mask)
			require.NoError(t, err)

			for _, opts := range [][]update.Option{nil, {update.WithCurrent(dynamicpb.NewMessage(md))}} {
				violations, err := spec.ValidateResolved(req, resolved, opts...)
				if incompatible {
					require.ErrorIs(t, err, update.ErrMessageMismatch)
					continue
				}

				require.NoError(t, err)
				_, err = spec.Plan(req, mask, opts...)
				require.ErrorIs(t, err, update.ErrBehaviorViolation)
				require.Len(t, violations, 1)
				require.Equal(t, "optional_parent.leaf", violations[0].Path)
			}

			require.Same(t, foreign.Messages().ByName("Leaf"), req.Get(md.Fields().ByName("optional_parent")).Message().Descriptor())
		})
	}
}

func TestValidateResolvedProtectedScalarReadOnly(t *testing.T) {
	t.Parallel()

	for _, region := range []string{"old", "new", ""} {
		t.Run(region, func(t *testing.T) {
			t.Parallel()

			req, current := &pb.Account{Region: region}, &pb.Account{Region: "old"}
			spec, err := update.NewSpec(req)
			require.NoError(t, err)

			mask := maskOf([]string{"region"})
			resolved, err := spec.ResolveMask(req, mask)
			require.NoError(t, err)

			wrap := func(m proto.Message) proto.Message {
				return &resolvedReadOnlyMessage{Message: m.ProtoReflect(), t: t}
			}
			violations, err := spec.ValidateResolved(wrap(req), resolved, update.WithCurrent(wrap(current)))
			require.NoError(t, err)

			_, planErr := spec.Plan(req, mask, update.WithCurrent(current))
			if region == "old" {
				require.NoError(t, planErr)
				require.Empty(t, violations)

				return
			}

			require.ErrorIs(t, planErr, update.ErrProtectedField)
			require.Len(t, violations, 1)
			require.Equal(t, "region", violations[0].Path)
		})
	}
}

func TestValidateResolvedRejectsCallAllowList(t *testing.T) {
	t.Parallel()

	req := &pb.Account{}
	spec, err := update.NewSpec(req)
	require.NoError(t, err)
	resolved, err := spec.ResolveMask(req, maskOf([]string{}))
	require.NoError(t, err)
	_, err = spec.ValidateResolved(req, resolved, update.WithAllowedPaths())
	require.ErrorIs(t, err, update.ErrAllowedPathsAtCall)
}

func TestValidateResolvedAbsentMapEntry(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"delegates.bob", "*"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			req := &pb.Account{}
			if path == "*" {
				req = storedAccount()
			}

			spec, err := update.NewSpec(req, update.WithAllowedPaths("delegates.bob", "delegates.bob.email"))
			require.NoError(t, err)

			mask := maskOf([]string{path})
			_, err = spec.Plan(proto.CloneOf(req), mask)
			require.NoError(t, err)
			resolved, err := spec.ResolveMask(req, mask)
			require.NoError(t, err)
			require.Len(t, resolved.Paths(), 1)
			require.Equal(t, "delegates.bob.email", resolved.Paths()[0].Path)
			violations, err := spec.ValidateResolved(req, resolved)
			require.NoError(t, err)
			require.Empty(t, violations)
		})
	}
}

func TestValidateResolvedProtectedWholeMessage(t *testing.T) {
	t.Parallel()

	req := &pb.Account{BackupOwner: &pb.Owner{Email: "valid"}}
	current := proto.CloneOf(req)
	current.BackupOwner.ProtoReflect().SetUnknown([]byte{0xa0, 6, 1})

	spec, err := update.NewSpec(req, update.WithImmutableFields("backup_owner"))
	require.NoError(t, err)

	mask := maskOf([]string{"backup_owner"})
	withCurrent := update.WithCurrent(current)
	_, err = spec.Plan(proto.CloneOf(req), mask, withCurrent)
	require.ErrorIs(t, err, update.ErrProtectedField)
	require.Zero(t, planRefusalIndex(t, err))

	resolved, err := spec.ResolveMask(req, mask)
	require.NoError(t, err)
	violations, err := spec.ValidateResolved(req, resolved, withCurrent)
	require.NoError(t, err)
	require.Len(t, violations, 1)
	require.Equal(t, "backup_owner", violations[0].Path)
	require.Zero(t, violations[0].Index)
	require.Equal(t, annotations.FieldBehavior_IMMUTABLE, violations[0].Behavior)
}

func TestValidateResolvedOmittedOneofSwitch(t *testing.T) {
	t.Parallel()

	file := protodesc.ToFileDescriptorProto(resolvedTestDescriptor(t).ParentFile())
	proto.SetExtension(file.MessageType[1].Field[0].Options, annotations.E_FieldBehavior,
		[]annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE})
	fd, err := protodesc.NewFile(file, nil)
	require.NoError(t, err)

	md := fd.Messages().ByName("Root")
	req := dynamicpb.NewMessage(md)
	req.Set(md.Fields().ByName("alternative"), protoreflect.ValueOfString("set"))

	spec, err := update.NewSpec(req)
	require.NoError(t, err)
	_, err = spec.Plan(proto.CloneOf(req), nil)
	require.ErrorIs(t, err, update.ErrProtectedField)
	require.Equal(t, update.SynthesizedPathIndex, planRefusalIndex(t, err))

	resolved, err := spec.ResolveMask(req, nil)
	require.NoError(t, err)
	violations, err := spec.ValidateResolved(req, resolved)
	require.NoError(t, err)
	require.Len(t, violations, 1)
	require.Equal(t, "alternative", violations[0].Path)
	require.Equal(t, update.SynthesizedPathIndex, violations[0].Index)
	require.Equal(t, annotations.FieldBehavior_IMMUTABLE, violations[0].Behavior)
}

func TestValidateResolvedDescriptorScope(t *testing.T) {
	t.Parallel()

	md := (&pb.Account{}).ProtoReflect().Descriptor()
	req := dynamicpb.NewMessage(md)
	req.Set(md.Fields().ByName("display_name"), protoreflect.ValueOfString("valid"))

	parent := req.ProtoReflect()
	for range 35 {
		parent = parent.Mutable(md.Fields().ByName("parent")).Message()
	}

	spec, err := update.NewSpec(req)
	require.NoError(t, err)

	mask := maskOf([]string{"display_name"})
	_, err = spec.Plan(proto.CloneOf(req), mask)
	require.NoError(t, err)

	deep, err := spec.ResolveMask(req, maskOf([]string{"parent"}))
	require.NoError(t, err)
	_, err = spec.ValidateResolved(req, deep)
	require.ErrorIs(t, err, fieldbehavior.ErrMaxDepthExceeded)

	file := protodesc.ToFileDescriptorProto(md.ParentFile())
	for _, message := range file.MessageType {
		if message.GetName() == "Owner" {
			message.Field[0].Type = descriptorpb.FieldDescriptorProto_TYPE_BYTES.Enum()
		}
	}

	foreign, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err)
	req.Set(md.Fields().ByName("backup_owner"), protoreflect.ValueOfMessage(dynamicpb.NewMessage(foreign.Messages().ByName("Owner"))))

	for _, tc := range []struct {
		name  string
		paths []string
		want  error
	}{
		{name: "unselected", paths: []string{"display_name"}},
		{name: "selected", paths: []string{"parent", "backup_owner"}, want: update.ErrMessageMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resolved, err := spec.ResolveMask(req, maskOf(tc.paths))
			require.NoError(t, err)

			for range 200 {
				violations, err := spec.ValidateResolved(req, resolved)
				if tc.want == nil {
					require.NoError(t, err)
					require.Empty(t, violations)
				} else {
					require.ErrorIs(t, err, tc.want)
					require.NotErrorIs(t, err, fieldbehavior.ErrMaxDepthExceeded)
				}
			}
		})
	}
}

func TestValidateResolvedForeignEnum(t *testing.T) {
	t.Parallel()

	md := (&pb.Account{}).ProtoReflect().Descriptor()
	file := protodesc.ToFileDescriptorProto(md.ParentFile())

	for _, enum := range file.EnumType {
		if enum.GetName() == "State" {
			enum.Value[1].Name = new("OTHER_MEANING")
		}
	}

	foreign, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err)

	child := dynamicpb.NewMessage(foreign.Messages().ByName("Account"))
	child.Set(child.Descriptor().Fields().ByName("state"), protoreflect.ValueOfEnum(1))

	req := dynamicpb.NewMessage(md)
	req.Set(md.Fields().ByName("parent"), protoreflect.ValueOfMessage(child))
	spec, err := update.NewSpec(req)
	require.NoError(t, err)
	resolved, err := spec.ResolveMask(req, maskOf([]string{"parent.state"}))
	require.NoError(t, err)
	_, err = spec.ValidateResolved(req, resolved)
	require.ErrorIs(t, err, update.ErrMessageMismatch)
}

func TestResolveMaskDynamicRootAllowList(t *testing.T) {
	t.Parallel()

	req, err := structpb.NewStruct(map[string]any{"x": "ok", "y": "extra"})
	require.NoError(t, err)
	spec, err := update.NewSpec(req, update.WithAllowedPaths("x"))
	require.NoError(t, err)

	for _, tc := range []struct {
		name  string
		mask  *fieldmaskpb.FieldMask
		index int
	}{
		{name: "explicit", mask: maskOf([]string{"x"}), index: 0},
		{name: "wildcard", mask: maskOf([]string{"*"}), index: update.SynthesizedPathIndex},
		{name: "omitted", index: update.SynthesizedPathIndex},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resolved, err := spec.ResolveMask(req, tc.mask)
			require.NoError(t, err)
			require.Equal(t, []update.ResolvedPath{{Path: "x", Index: tc.index}}, resolved.Paths())
		})
	}
}
func TestValidateResolvedNestedProtectedScalarReadOnly(t *testing.T) {
	t.Parallel()

	for _, org := range []string{"old", "new", ""} {
		t.Run(org, func(t *testing.T) {
			t.Parallel()

			req, current := &pb.Account{BackupOwner: &pb.Owner{Org: org}}, &pb.Account{BackupOwner: &pb.Owner{Org: "old"}}
			spec, err := update.NewSpec(req)
			require.NoError(t, err)

			mask := maskOf([]string{"backup_owner.org"})
			resolved, err := spec.ResolveMask(req, mask)
			require.NoError(t, err)

			wrap := func(m proto.Message) proto.Message {
				return &resolvedReadOnlyMessage{Message: m.ProtoReflect(), t: t}
			}
			violations, err := spec.ValidateResolved(wrap(req), resolved, update.WithCurrent(wrap(current)))
			require.NoError(t, err)

			_, planErr := spec.Plan(req, mask, update.WithCurrent(current))
			if org == "old" {
				require.NoError(t, planErr)
				require.Empty(t, violations)

				return
			}

			require.ErrorIs(t, planErr, update.ErrProtectedField)
			require.Len(t, violations, 1)
			require.Equal(t, "backup_owner.org", violations[0].Path)
		})
	}
}

func TestValidateResolvedForeignMapValueEnum(t *testing.T) {
	t.Parallel()

	file := protodesc.ToFileDescriptorProto(resolvedTestDescriptor(t).ParentFile())
	file.EnumType = []*descriptorpb.EnumDescriptorProto{{
		Name: new("State"),
		Value: []*descriptorpb.EnumValueDescriptorProto{
			{Name: new("UNSPECIFIED"), Number: new(int32(0))},
			{Name: new("ACTIVE"), Number: new(int32(1))},
		},
	}}
	leaf := file.MessageType[1]
	leaf.NestedType = []*descriptorpb.DescriptorProto{{
		Name:    new("StatesEntry"),
		Options: &descriptorpb.MessageOptions{MapEntry: new(true)},
		Field: []*descriptorpb.FieldDescriptorProto{
			{Name: new("key"), Number: new(int32(1)), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
			{Name: new("value"), Number: new(int32(2)), Type: descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(), TypeName: new(".resolution.State")},
		},
	}}
	leaf.Field = append(leaf.Field, &descriptorpb.FieldDescriptorProto{
		Name: new("states"), Number: new(int32(2)), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
		Label: descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(), TypeName: new(".resolution.Leaf.StatesEntry"),
	})

	declared, err := protodesc.NewFile(file, nil)
	require.NoError(t, err)

	md := declared.Messages().ByName("Root")

	for _, tc := range []struct {
		enumName string
		want     error
	}{
		{enumName: "ACTIVE"},
		{enumName: "OTHER_MEANING", want: update.ErrMessageMismatch},
	} {
		t.Run(tc.enumName, func(t *testing.T) {
			t.Parallel()

			copyFile := proto.CloneOf(file)
			copyFile.EnumType[0].Value[1].Name = new(tc.enumName)
			actual, err := protodesc.NewFile(copyFile, nil)
			require.NoError(t, err)

			child := dynamicpb.NewMessage(actual.Messages().ByName("Leaf"))
			states := child.Descriptor().Fields().ByName("states")
			child.Mutable(states).Map().Set(protoreflect.ValueOfString("key").MapKey(), protoreflect.ValueOfEnum(1))

			req := dynamicpb.NewMessage(md)
			req.Set(md.Fields().ByName("optional_parent"), protoreflect.ValueOfMessage(child))

			spec, err := update.NewSpec(req)
			require.NoError(t, err)
			resolved, err := spec.ResolveMask(req, maskOf([]string{"optional_parent.states"}))
			require.NoError(t, err)
			violations, err := spec.ValidateResolved(req, resolved)
			require.ErrorIs(t, err, tc.want)
			require.Empty(t, violations)
		})
	}
}
