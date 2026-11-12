// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"context"
	"errors"
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

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// collectionDescriptor builds the collection test schema:
//
//	message Root {
//	  map<string, Elem> items = 1; repeated Elem list = 2;
//	  map<int32, Elem> ports = 3; map<bool, string> flags = 4;
//	}
//	message Elem {
//	  oneof choice { string pinned = 1 [IMMUTABLE]; string free = 2; }
//	  string note = 3;
//	}
func collectionDescriptor(t *testing.T) protoreflect.MessageDescriptor {
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
	entry := func(name string, key descriptorpb.FieldDescriptorProto_Type, value *descriptorpb.FieldDescriptorProto_Type, typeName string) *descriptorpb.DescriptorProto {
		return &descriptorpb.DescriptorProto{
			Name:    new(name),
			Options: &descriptorpb.MessageOptions{MapEntry: new(true)},
			Field:   []*descriptorpb.FieldDescriptorProto{field("key", 1, optional, key.Enum(), ""), field("value", 2, optional, value, typeName)},
		}
	}

	pinned := field("pinned", 1, optional, str, "")
	pinned.OneofIndex = new(int32(0))
	pinned.Options = &descriptorpb.FieldOptions{}
	proto.SetExtension(pinned.Options, annotations.E_FieldBehavior, []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE})

	free := field("free", 2, optional, str, "")
	free.OneofIndex = new(int32(0))

	file := &descriptorpb.FileDescriptorProto{
		Name: new("colltest.proto"), Package: new("colltest"), Syntax: new("proto3"),
		Dependency: []string{"google/api/field_behavior.proto"},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: new("Root"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("items", 1, repeated, message, ".colltest.Root.ItemsEntry"),
					field("list", 2, repeated, message, ".colltest.Elem"),
					field("ports", 3, repeated, message, ".colltest.Root.PortsEntry"),
					field("flags", 4, repeated, message, ".colltest.Root.FlagsEntry"),
				},
				NestedType: []*descriptorpb.DescriptorProto{
					entry("ItemsEntry", descriptorpb.FieldDescriptorProto_TYPE_STRING, message, ".colltest.Elem"),
					entry("PortsEntry", descriptorpb.FieldDescriptorProto_TYPE_INT32, message, ".colltest.Elem"),
					entry("FlagsEntry", descriptorpb.FieldDescriptorProto_TYPE_BOOL, str, ""),
				},
			},
			{
				Name:      new("Elem"),
				OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: new("choice")}},
				Field:     []*descriptorpb.FieldDescriptorProto{pinned, free, field("note", 3, optional, str, "")},
			},
		},
	}
	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	require.NoError(t, err)

	return fd.Messages().ByName("Root")
}

// collectionRoot returns a Root holding the item entries, list elements and
// port entries given as Elem field values.
func collectionRoot(md protoreflect.MessageDescriptor, items map[string]map[string]string, list []map[string]string, ports map[int32]map[string]string) *dynamicpb.Message {
	root := dynamicpb.NewMessage(md)
	elemMD := md.Fields().ByName("list").Message()
	elem := func(values map[string]string) protoreflect.Value {
		e := dynamicpb.NewMessage(elemMD)
		for name, v := range values {
			e.Set(elemMD.Fields().ByName(protoreflect.Name(name)), protoreflect.ValueOfString(v))
		}

		return protoreflect.ValueOfMessage(e)
	}

	for k, values := range items {
		root.Mutable(md.Fields().ByName("items")).Map().Set(protoreflect.ValueOfString(k).MapKey(), elem(values))
	}

	for _, values := range list {
		l := root.Mutable(md.Fields().ByName("list")).List()
		l.Append(elem(values))
	}

	for k, values := range ports {
		root.Mutable(md.Fields().ByName("ports")).Map().Set(protoreflect.ValueOfInt32(k).MapKey(), elem(values))
	}

	return root
}

// maskPathError returns the *MaskPathError of a Plan or Patch refusal and
// its field error.
func maskPathError(t *testing.T, err error) (*coreerrs.FieldError, *update.MaskPathError) {
	t.Helper()

	fe, ok := errors.AsType[*coreerrs.FieldError](err)
	require.True(t, ok, "want a field error, got %v", err)
	mp, ok := errors.AsType[*update.MaskPathError](err)
	require.True(t, ok, "want a *MaskPathError, got %v", err)

	return fe, mp
}

// TestPlanCollectionPaths pins that Plan takes the map key and wildcard
// grammar Apply takes: keys and "*" on repeated and map fields,
// any field spelling, a trailing "*" naming the collection whole.
func TestPlanCollectionPaths(t *testing.T) {
	t.Parallel()

	spec, err := update.NewSpec(&pb.Resource{})
	require.NoError(t, err)

	stored := &pb.Resource{
		Name:    "n",
		Labels:  map[string]*pb.Profile{"k": {DisplayName: "old"}, "a.b": {DisplayName: "dot"}},
		Aliases: []*pb.Profile{{DisplayName: "x"}, {DisplayName: "y"}},
	}
	req := &pb.Resource{
		Name:    "n",
		Labels:  map[string]*pb.Profile{"k": {DisplayName: "new", UpdatedAt: "client"}, "a.b": {DisplayName: "dot2"}},
		Aliases: []*pb.Profile{{DisplayName: "x2"}, {DisplayName: "y2"}},
	}

	tests := []struct {
		name  string
		paths []string
		want  []string
	}{
		{name: "map key", paths: []string{"labels.k"}, want: []string{"labels.k"}},
		{name: "quoted map key", paths: []string{"labels.`a.b`"}, want: []string{"labels.`a.b`"}},
		{name: "map wildcard names the map whole", paths: []string{"labels.*"}, want: []string{"labels"}},
		{name: "list wildcard names the list whole", paths: []string{"aliases.*"}, want: []string{"aliases"}},
		{name: "sub-path through a key", paths: []string{"labels.k.display_name"}, want: []string{"labels.k.display_name"}},
		{name: "sub-path through a key in JSON spelling", paths: []string{"labels.k.displayName"}, want: []string{"labels.k.display_name"}},
		{name: "sub-path through a list wildcard", paths: []string{"aliases.*.display_name"}, want: []string{"aliases.*.display_name"}},
		{name: "sub-path through a map wildcard", paths: []string{"labels.*.display_name"}, want: []string{"labels.*.display_name"}},
		{name: "output only inside an entry is dropped", paths: []string{"labels.k.updated_at"}, want: nil},
		{name: "whole collection covers its keys", paths: []string{"labels.k.display_name", "labels"}, want: []string{"labels"}},
		{name: "key and wildcard together", paths: []string{"labels.k", "labels.*.display_name"}, want: []string{"labels.*.display_name", "labels.k"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			plan, err := spec.Plan(proto.Clone(req), maskOf(tt.paths), update.WithCurrent(stored))
			require.NoError(t, err)
			require.Equal(t, tt.want, plan.Paths)
		})
	}
}

// TestPlanRefusesCollectionPaths pins the paths the update grammar refuses:
// list indexes, the implicit per-element form, keys not of the key kind and
// paths into values without fields. Each refusal names the offending path.
func TestPlanRefusesCollectionPaths(t *testing.T) {
	t.Parallel()

	md := collectionDescriptor(t)
	spec, err := update.NewSpec(dynamicpb.NewMessage(md))
	require.NoError(t, err)

	tests := []struct {
		path   string
		reason string
	}{
		{path: "list.0", reason: `addresses an element of repeated field "list" by index`},
		{path: "list.0.note", reason: `addresses an element of repeated field "list" by index`},
		{path: "list.note", reason: `names a field of the elements of repeated field "list" without "*"; use "list.*.note"`},
		{path: "ports.x", reason: `"x" is not a int32 key of map "ports"`},
		{path: "ports.0443", reason: `"0443" is not a int32 key of map "ports"`},
		{path: "flags.yes", reason: `"yes" is not a bool key of map "flags"`},
		{path: "flags.true.x", reason: `addresses a field inside the elements of "flags"`},
		{path: "items.k.nope", reason: `unknown path "items.k.nope"`},
		{path: "items.*.*", reason: `malformed path "items.*.*"`},
		{path: "items..note", reason: `malformed path "items..note"`},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()

			_, err := spec.Plan(dynamicpb.NewMessage(md), maskOf([]string{"items.k", tt.path}))
			require.ErrorIs(t, err, fieldmask.ErrInvalidPath)

			fe, mp := maskPathError(t, err)
			require.Equal(t, update.DefaultUpdateMaskField+".paths[1]", fe.Field)
			require.Contains(t, fe.Reason, tt.reason)
			require.Equal(t, 1, mp.Index)
			require.Equal(t, tt.path, mp.Path)
			require.Empty(t, update.ResolvePath(md, tt.path))
		})
	}

	for path, want := range map[string]string{
		"ports.443": "ports.443", "ports.-1": "ports.-1", "flags.true": "flags.true", "items.*": "items",
		"list.*.note": "list.*.note", "items.`a.b`.free": "items.`a.b`.free",
	} {
		require.Equal(t, want, update.ResolvePath(md, path), path)
	}
}

// TestPlanCollectionBehaviors pins the AIP-203 rules inside collection
// elements: an IMMUTABLE value is compared with the stored entry of the same
// key, a REQUIRED field of a replaced entry is checked, an IDENTIFIER of a
// list element updated in place is compared with the stored element of the
// same index, and a "*" sub-path needs as many elements as stored.
func TestPlanCollectionBehaviors(t *testing.T) {
	t.Parallel()

	spec, err := update.NewSpec(&pb.Account{})
	require.NoError(t, err)

	current := update.WithCurrent(storedAccount())

	t.Run("unchanged immutable in an entry is ignored", func(t *testing.T) {
		t.Parallel()

		req := storedAccount()
		req.Delegates["k"].Note = "n"
		plan, err := spec.Plan(req, maskOf([]string{"delegates.k.org", "delegates.k.note"}), current)
		require.NoError(t, err)
		require.Equal(t, []string{"delegates.k.note"}, plan.Paths)
	})

	refused := []struct {
		name   string
		paths  []string
		mutate func(*pb.Account)
		field  string // MaskPathError.Field
		index  int
		cause  error
	}{
		{name: "changed immutable in an entry", paths: []string{"display_name", "delegates.k.org"}, mutate: func(a *pb.Account) { a.Delegates["k"].Org = "x" },
			field: "delegates.k.org", index: 1, cause: update.ErrProtectedField},
		{name: "changed immutable through a map wildcard", paths: []string{"delegates.*.org"}, mutate: func(a *pb.Account) { a.Delegates["k"].Org = "x" },
			field: "delegates.k.org", index: 0, cause: update.ErrProtectedField},
		{name: "changed immutable through a list wildcard", paths: []string{"members.*.org"}, mutate: func(a *pb.Account) { a.Members[0].Org = "x" },
			field: "members.0.org", index: 0, cause: update.ErrProtectedField},
		{name: "replaced entry without a required field", paths: []string{"delegates.k"}, mutate: func(a *pb.Account) { a.Delegates["k"].Email = "" },
			field: "delegates.k.email", index: 0, cause: update.ErrBehaviorViolation},
		{name: "list wildcard with fewer elements than stored", paths: []string{"members.*.note"},
			mutate: func(a *pb.Account) { a.Members = append(a.Members, &pb.Owner{Email: "e"}) },
			field:  "members", index: 0, cause: update.ErrElementCountMismatch},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := storedAccount()
			tt.mutate(req)

			_, err := spec.Plan(req, maskOf(tt.paths), current)
			require.ErrorIs(t, err, tt.cause)

			fe, mp := maskPathError(t, err)
			require.Equal(t, tt.index, mp.Index)
			require.Equal(t, tt.paths[tt.index], mp.Path)
			require.Equal(t, tt.field, mp.Field)

			require.Equal(t, update.DefaultUpdateMaskField+".paths["+strconv.Itoa(tt.index)+"]", fe.Field)
		})
	}

	t.Run("identifier of a list element updated in place", func(t *testing.T) {
		t.Parallel()

		rs, err := update.NewSpec(&pb.Resource{})
		require.NoError(t, err)

		stored := &pb.Resource{Aliases: []*pb.Profile{{Id: "a1", DisplayName: "x"}}}
		req := &pb.Resource{Aliases: []*pb.Profile{{Id: "a2", DisplayName: "y"}}}
		_, err = rs.Plan(req, maskOf([]string{"aliases.*.display_name"}), update.WithCurrent(stored))
		require.ErrorIs(t, err, update.ErrProtectedField)

		_, mp := maskPathError(t, err)
		require.Equal(t, 0, mp.Index)
		require.Equal(t, "aliases.*.display_name", mp.Path)
		require.Contains(t, mp.Field, "id")

		req.Aliases[0].Id = "a1"
		plan, err := rs.Plan(req, maskOf([]string{"aliases.*.display_name"}), update.WithCurrent(stored))
		require.NoError(t, err)
		require.Equal(t, []string{"aliases.*.display_name"}, plan.Paths)
	})

	t.Run("identifier of a message updated through a sibling path", func(t *testing.T) {
		t.Parallel()

		rs, err := update.NewSpec(&pb.Resource{})
		require.NoError(t, err)

		stored := &pb.Resource{
			Profile: &pb.Profile{Id: "p1", DisplayName: "x"},
			Labels:  map[string]*pb.Profile{"k": {Id: "l1", DisplayName: "x"}},
		}

		for _, tc := range []struct{ path, field string }{
			{path: "profile.display_name", field: "profile.id"},
			{path: "labels.k.display_name", field: "labels.k.id"},
		} {
			req := &pb.Resource{
				Profile: &pb.Profile{Id: "p2", DisplayName: "y"},
				Labels:  map[string]*pb.Profile{"k": {Id: "l2", DisplayName: "y"}},
			}
			_, err = rs.Plan(req, maskOf([]string{"description", tc.path}), update.WithCurrent(stored))
			require.ErrorIs(t, err, update.ErrProtectedField, tc.path)

			fe, mp := maskPathError(t, err)
			require.Equal(t, 1, mp.Index, tc.path)
			require.Equal(t, tc.path, mp.Path)
			require.Equal(t, tc.field, mp.Field)
			require.Equal(t, update.DefaultUpdateMaskField+".paths[1]", fe.Field)
		}
	})
}

// TestPlanCollectionOneofSwitches pins that a sub-path into an element may
// not switch the element's oneof away from a stored IMMUTABLE member: the
// stored entry of the key, or every stored element below "*", is checked.
func TestPlanCollectionOneofSwitches(t *testing.T) {
	t.Parallel()

	md := collectionDescriptor(t)
	spec, err := update.NewSpec(dynamicpb.NewMessage(md))
	require.NoError(t, err)

	for _, path := range []string{"items.a.free", "items.*.free", "list.*.free"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			req := collectionRoot(md, map[string]map[string]string{"a": {"free": "f"}}, []map[string]string{{"free": "f"}}, nil)
			pinned := collectionRoot(md, map[string]map[string]string{"a": {"pinned": "p"}}, []map[string]string{{"pinned": "p"}}, nil)
			_, err := spec.Plan(req, maskOf([]string{path}), update.WithCurrent(pinned))
			require.ErrorIs(t, err, update.ErrProtectedField)

			fe, mp := maskPathError(t, err)
			require.Equal(t, update.DefaultUpdateMaskField+".paths[0]", fe.Field)
			require.Equal(t, path, mp.Path)

			changed := "items.a.pinned"
			if path == "list.*.free" {
				changed = "list.0.pinned"
			}

			require.Equal(t, changed, mp.Field)
			require.Equal(t, `path "`+changed+`" cannot be changed`, fe.Reason)

			free := collectionRoot(md, map[string]map[string]string{"a": {"note": "n"}}, []map[string]string{{"note": "n"}}, nil)
			_, err = spec.Plan(req, maskOf([]string{path}), update.WithCurrent(free))
			require.NoError(t, err)
		})
	}
}

// TestMergePathsCollections pins how MergePaths writes collection paths.
func TestMergePathsCollections(t *testing.T) {
	t.Parallel()

	dst := func() *pb.Resource {
		return &pb.Resource{
			Labels:  map[string]*pb.Profile{"k": {DisplayName: "old", Secret: "s"}, "keep": {DisplayName: "kept"}, "gone": {DisplayName: "g"}},
			Aliases: []*pb.Profile{{Id: "1", DisplayName: "x"}, {Id: "2", DisplayName: "y"}},
		}
	}
	src := &pb.Resource{
		Labels:  map[string]*pb.Profile{"k": {DisplayName: "new"}, "add": {DisplayName: "added"}},
		Aliases: []*pb.Profile{{DisplayName: "x2"}, {DisplayName: "y2"}},
	}

	tests := []struct {
		name  string
		paths []string
		want  *pb.Resource
	}{
		{name: "key replaces the entry", paths: []string{"labels.k"}, want: &pb.Resource{
			Labels:  map[string]*pb.Profile{"k": {DisplayName: "new"}, "keep": {DisplayName: "kept"}, "gone": {DisplayName: "g"}},
			Aliases: dst().Aliases,
		}},
		{name: "key src lacks deletes the entry", paths: []string{"labels.gone"}, want: &pb.Resource{
			Labels:  map[string]*pb.Profile{"k": {DisplayName: "old", Secret: "s"}, "keep": {DisplayName: "kept"}},
			Aliases: dst().Aliases,
		}},
		{name: "sub-path keeps the siblings", paths: []string{"labels.k.display_name"}, want: &pb.Resource{
			Labels:  map[string]*pb.Profile{"k": {DisplayName: "new", Secret: "s"}, "keep": {DisplayName: "kept"}, "gone": {DisplayName: "g"}},
			Aliases: dst().Aliases,
		}},
		{name: "sub-path creates an entry only src holds", paths: []string{"labels.add.display_name", "labels.none.display_name"}, want: &pb.Resource{
			Labels: map[string]*pb.Profile{
				"k": {DisplayName: "old", Secret: "s"}, "keep": {DisplayName: "kept"}, "gone": {DisplayName: "g"}, "add": {DisplayName: "added"},
			},
			Aliases: dst().Aliases,
		}},
		{name: "map wildcard updates the src entries only", paths: []string{"labels.*.display_name"}, want: &pb.Resource{
			Labels: map[string]*pb.Profile{
				"k": {DisplayName: "new", Secret: "s"}, "keep": {DisplayName: "kept"}, "gone": {DisplayName: "g"}, "add": {DisplayName: "added"},
			},
			Aliases: dst().Aliases,
		}},
		{name: "list wildcard updates elements in place", paths: []string{"aliases.*.display_name"}, want: &pb.Resource{
			Labels:  dst().Labels,
			Aliases: []*pb.Profile{{Id: "1", DisplayName: "x2"}, {Id: "2", DisplayName: "y2"}},
		}},
		{name: "trailing wildcard replaces the collection", paths: []string{"labels.*", "aliases.*"}, want: &pb.Resource{
			Labels: src.Labels, Aliases: src.Aliases,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := dst()
			require.NoError(t, update.MergePaths(got, src, tt.paths))
			require.True(t, proto.Equal(tt.want, got), "got %v", got)

			viaMask := dst()
			require.NoError(t, update.Merge(fieldmask.FromPaths(tt.paths...), viaMask, src))
			require.True(t, proto.Equal(tt.want, viaMask), "Merge got %v", viaMask)
		})
	}

	t.Run("empty source list clears the sub-path in every element", func(t *testing.T) {
		t.Parallel()

		got := dst()
		require.NoError(t, update.MergePaths(got, &pb.Resource{}, []string{"aliases.*.display_name"}))
		require.True(t, proto.Equal(&pb.Resource{Labels: dst().Labels, Aliases: []*pb.Profile{{Id: "1"}, {Id: "2"}}}, got), "got %v", got)
	})

	t.Run("element count mismatch changes nothing", func(t *testing.T) {
		t.Parallel()

		got := dst()
		err := update.MergePaths(got, &pb.Resource{Aliases: []*pb.Profile{{DisplayName: "only"}}}, []string{"labels.k", "aliases.*.display_name"})
		require.ErrorIs(t, err, update.ErrElementCountMismatch)
		require.True(t, proto.Equal(dst(), got), "dst must be unchanged, got %v", got)
	})

	t.Run("typed and scalar map keys", func(t *testing.T) {
		t.Parallel()

		md := collectionDescriptor(t)
		d := collectionRoot(md, nil, nil, map[int32]map[string]string{443: {"note": "old"}, 80: {"note": "http"}})
		s := collectionRoot(md, nil, nil, map[int32]map[string]string{443: {"note": "new"}})
		require.NoError(t, update.MergePaths(d, s, []string{"ports.443.note", "ports.80"}))
		require.True(t, proto.Equal(collectionRoot(md, nil, nil, map[int32]map[string]string{443: {"note": "new"}}), d), "got %v", d)

		acc := &pb.Account{Attributes: map[string]string{"a": "1", "b": "2"}}
		require.NoError(t, update.MergePaths(acc, &pb.Account{Attributes: map[string]string{"a": "9"}}, []string{"attributes.a", "attributes.b"}))
		require.Equal(t, map[string]string{"a": "9"}, acc.GetAttributes())
	})
}

// TestPatchRunCollections pins a Patch through collection paths: the merge
// writes the planned entry paths, and a changed IMMUTABLE value inside an
// entry is reported on the resource field of that entry.
func TestPatchRunCollections(t *testing.T) {
	t.Parallel()

	spec, err := update.NewSpec(&pb.Account{})
	require.NoError(t, err)

	var saved *pb.Account

	patch := update.Patch[*pb.Account, *pb.Account, *int64]{
		Field: "account", Spec: spec,
		Precondition: update.Int64Precondition[*pb.Account]{Field: "account.etag", Version: func(*pb.Account) int64 { return 1 }},
		Read:         func(context.Context) (*pb.Account, error) { return storedAccount(), nil },
		ToProto:      func(a *pb.Account) *pb.Account { return proto.Clone(a).(*pb.Account) },
		Save: func(_ context.Context, merged *pb.Account, _ update.Plan, _ *int64) (*pb.Account, error) {
			saved = merged
			return merged, nil
		},
	}

	req := &pb.Account{Delegates: map[string]*pb.Owner{"k": {Note: "n", CreateTime: "client"}, "new": {Email: "e@example.com"}}}
	out, err := patch.Run(t.Context(), req, "", &fieldmaskpb.FieldMask{Paths: []string{"delegates.k.note", "delegates.new"}})
	require.NoError(t, err)
	require.NotNil(t, saved)

	want := storedAccount()
	want.Delegates["k"].Note = "n"
	want.Delegates["new"] = &pb.Owner{Email: "e@example.com"}
	require.True(t, proto.Equal(want, out), "got %v", out)

	req = &pb.Account{Delegates: map[string]*pb.Owner{"k": {Org: "other"}}}
	_, err = patch.Run(t.Context(), req, "", &fieldmaskpb.FieldMask{Paths: []string{"delegates.k.org"}})

	var fe *coreerrs.FieldError
	require.ErrorAs(t, err, &fe)
	require.Equal(t, "account.delegates.k.org", fe.Field)
	require.ErrorIs(t, err, update.ErrProtectedField)
}
