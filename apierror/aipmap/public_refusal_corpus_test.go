// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package aipmap_test

import (
	"context"
	"slices"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
	infopb "github.com/altessa-s/go-aip/internal/proto/gen/fieldinfotest/v1"
)

// This input corpus uses only a8b17ba APIs. The standalone golden recorder can
// call it without testing.T or executing any tests. Deferred entries are added
// separately because Prepare and Complete did not exist at that revision.
type publicRefusalCase struct {
	Entry, Kind string
	Strict      bool
}

func publicRefusalCases() []publicRefusalCase {
	var out []publicRefusalCase

	for _, strict := range []bool{false, true} {
		for entryIndex, entry := range []string{"ApplyMask", "Apply", "ValidateStrict", "Spec", "Patch"} {
			for _, kind := range []string{"unknown", "malformed", "count", "length", "depth", "wildcard", "required", "immutable", "immutable-implied", "immutable-all", "identifier", "elements", "oneof", "mismatch", "typed-current", "clear", "collection", "map", "removed", "atomic", "multiple", "mixed-required", "element", "map-element", "deferred-length", "deferred-depth", "deferred-count"} {
				out = append(out, publicRefusalCase{entry, kind, strict})
			}

			for layoutIndex, layout := range []string{"atomic-after", "atomic-between", "atomic-collection", "nested-between", "nested-deep", "nested-late", "lexical-mixed", "atomic-priority", "atomic-fields", "nested-fields", "list", "map", "list-sibling"} {
				for maskIndex, mask := range []string{"all", "implied", "declared", "reversed", "shuffled", "covering"} {
					// Every layout/selection pair remains, rotated across entry points.
					if entryIndex != (layoutIndex+maskIndex)%5 {
						continue
					}

					out = append(out, publicRefusalCase{entry, "order/" + layout + "/" + mask, strict})
				}
			}

			for _, layout := range []string{"list-nested", "list-atomic"} {
				out = append(out, publicRefusalCase{entry, "order/" + layout + "/declared", strict})
			}

			if entry != "Apply" && entry != "ValidateStrict" {
				for _, mode := range []string{"declared", "reversed", "shuffled"} {
					out = append(out, publicRefusalCase{entry, "order/covered-clear/" + mode, strict})
				}
			}
		}

		for _, kind := range []string{"unknown", "malformed", "count", "length", "depth", "wildcard", "elements", "mismatch", "typed-current", "dynamic-wildcard", "dynamic-malformed"} {
			out = append(out, publicRefusalCase{"MergePaths", kind, strict})
		}
		// StripUpdate has no mask and does not check REQUIRED. These are its
		// actual refusal shapes: aggregate behavior/format, depth and mismatch.
		for _, kind := range []string{"immutable", "identifier", "oneof", "format", "walk-depth", "mismatch"} {
			out = append(out, publicRefusalCase{"StripUpdate", kind, strict})
		}
	}

	return out
}

func publicRefusalFixture(c publicRefusalCase) (proto.Message, proto.Message, *fieldmaskpb.FieldMask, []update.Option, []fieldbehavior.Option) {
	if strings.HasPrefix(c.Kind, "order/") {
		return orderedRefusalFixture(c)
	}

	var req, stored proto.Message = &pb.Account{}, &pb.Account{}

	paths := []string{"name"}
	behavior := annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED
	field := "name"

	var (
		extra      []update.Option
		stripExtra []fieldbehavior.Option
	)

	switch c.Kind {
	case "dynamic-wildcard", "dynamic-malformed":
		req, stored = &structpb.Struct{}, &structpb.Struct{}

		paths = []string{"*", "name"}
		if c.Kind == "dynamic-malformed" {
			paths = []string{"name..bad"}
		}
	case "unknown":
		req, stored = &descriptorpb.FileDescriptorProto{}, &descriptorpb.FileDescriptorProto{}
		paths = []string{"does_not_exist"}
	case "malformed":
		paths = []string{"name..bad"}
	case "count":
		paths = []string{"name", "region"}

		extra = append(extra, update.WithMaxPaths(1))
	case "length":
		extra = append(extra, update.WithMaxPathLength(3))
	case "depth":
		paths = []string{"parent.name"}

		extra = append(extra, update.WithMaxPathDepth(1))
	case "wildcard":
		paths = []string{"*", "name"}
	case "required":
		behavior = annotations.FieldBehavior_REQUIRED
	case "immutable", "immutable-implied", "immutable-all", "identifier":
		behavior = annotations.FieldBehavior_IMMUTABLE
		if c.Kind == "identifier" {
			behavior = annotations.FieldBehavior_IDENTIFIER
		}

		req, stored = &pb.Account{Name: "new"}, &pb.Account{Name: "old"}

		if c.Kind == "immutable-implied" {
			paths = nil
		}

		if c.Kind == "immutable-all" {
			paths = []string{"*"}
		}
	case "elements":
		req = &pb.Account{Members: []*pb.Owner{{Note: "new"}}}
		stored = &pb.Account{Members: []*pb.Owner{{Note: "old"}, {Note: "other"}}}
		paths = []string{"members.*.note"}
	case "oneof":
		field, behavior = "card", annotations.FieldBehavior_IMMUTABLE
		req = &pb.Account{Billing: &pb.Account_InvoiceEmail{InvoiceEmail: "new"}, Region: "same"}
		stored = &pb.Account{Billing: &pb.Account_Card{Card: "old"}, Region: "same"}
		paths = []string{"invoice_email", "region"}
	case "format":
		req, stored = &infopb.Host{Uid: "not-a-uuid"}, &infopb.Host{}
	case "walk-depth":
		behavior = annotations.FieldBehavior_IMMUTABLE
		req = &pb.Account{Parent: &pb.Account{Parent: &pb.Account{Name: "deep"}}}

		stripExtra = append(stripExtra, fieldbehavior.WithMaxDepth(1))
	case "deferred-length", "deferred-depth", "deferred-count":
		req = &pb.Resource{Name: "resource", Profile: &pb.Profile{DisplayName: "new"}}
		stored = &pb.Resource{Name: "resource", Profile: &pb.Profile{DisplayName: "old"}}
		paths = []string{"profile"}

		switch c.Kind {
		case "deferred-length":
			extra = append(extra, update.WithMaxPathLength(11))
		case "deferred-depth":
			extra = append(extra, update.WithMaxPathDepth(1))
		case "deferred-count":
			extra = append(extra, update.WithMaxPaths(1))
		}
	case "clear":
		behavior = annotations.FieldBehavior_IMMUTABLE
		stored = &pb.Account{Name: "old"}
	case "collection", "map", "removed", "atomic", "element", "map-element":
		field, behavior = "org", annotations.FieldBehavior_IMMUTABLE
		r, st := &pb.Owner{Org: "new"}, &pb.Owner{Org: "old"}

		switch c.Kind {
		case "collection", "removed", "element":
			req, stored = &pb.Account{Members: []*pb.Owner{r}}, &pb.Account{Members: []*pb.Owner{st}}
			paths = []string{"members"}

			if c.Kind == "removed" {
				req = &pb.Account{}
			}
		case "map", "map-element":
			req, stored = &pb.Account{Delegates: map[string]*pb.Owner{"a.b": r}}, &pb.Account{Delegates: map[string]*pb.Owner{"a.b": st}}
			paths = []string{"delegates"}
		case "atomic":
			req, stored = &pb.Account{Owner: r}, &pb.Account{Owner: st}
			paths = []string{"owner"}

			extra = append(extra, update.WithAtomicMessage(func(md protoreflect.MessageDescriptor) bool { return md.Name() == "Owner" }))
		}

		if c.Kind == "element" {
			paths = []string{"members.*.org"}
		}

		if c.Kind == "map-element" {
			paths = []string{"delegates.*.org"}
		}
	case "multiple", "mixed-required":
		field, behavior = "name", annotations.FieldBehavior_IMMUTABLE
		req, stored = &pb.Account{Name: "new", Region: "new"}, &pb.Account{Name: "old", Region: "old"}
		paths = []string{"name", "region"}

		if c.Kind == "mixed-required" {
			req = &pb.Account{Name: "new"}
		}
	case "typed-current":
		behavior = annotations.FieldBehavior_IMMUTABLE
		req = &pb.Account{Name: "new"}
		stored = (*pb.Account)(nil)
	case "mismatch":
		behavior = annotations.FieldBehavior_IMMUTABLE
		req = &pb.Account{Name: "new"}
		stored = &descriptorpb.FileDescriptorProto{}
	default:
		panic(c.Kind)
	}

	resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		if c.Kind == "deferred-length" || c.Kind == "deferred-depth" || c.Kind == "deferred-count" {
			return fieldbehavior.AnnotationResolver{}.Resolve(fd)
		}

		if c.Kind == "mixed-required" && fd.Name() == "region" {
			return []annotations.FieldBehavior{annotations.FieldBehavior_REQUIRED}
		}

		if (string(fd.Name()) == field || (c.Kind == "multiple" && fd.Name() == "region")) && behavior != annotations.FieldBehavior_FIELD_BEHAVIOR_UNSPECIFIED {
			return []annotations.FieldBehavior{behavior}
		}
		// An equal protected field makes this switch deferrable in Prepare.
		if c.Kind == "oneof" && fd.Name() == "region" {
			return []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
		}

		return nil
	})
	opts := append([]update.Option{update.WithIdentityFields(), update.WithResolver(resolver), update.WithStrict(c.Strict)}, extra...)
	strip := append([]fieldbehavior.Option{fieldbehavior.WithResolver(resolver), fieldbehavior.WithStrict(c.Strict)}, stripExtra...)

	var raw *fieldmaskpb.FieldMask
	if paths != nil {
		raw = &fieldmaskpb.FieldMask{Paths: paths}
	}

	return req, stored, raw, opts, strip
}

// orderedRefusalFixture combines protected changes that the base encountered
// in different traversal phases. Only the resolver's named fields are protected.
func orderedRefusalFixture(c publicRefusalCase) (proto.Message, proto.Message, *fieldmaskpb.FieldMask, []update.Option, []fieldbehavior.Option) {
	parts := strings.Split(c.Kind, "/")

	layout, mode := parts[1], parts[2]
	if layout == "list-nested" || layout == "list-atomic" {
		return orderedElementMessageFixture(c)
	}

	req, stored := &pb.Account{}, &pb.Account{}

	var paths, covering []string

	protected := map[protoreflect.Name]annotations.FieldBehavior{"org": annotations.FieldBehavior_IMMUTABLE}

	switch layout {
	case "atomic-after", "atomic-between", "atomic-priority", "nested-between":
		req.Owner, stored.Owner = &pb.Owner{Org: "new"}, &pb.Owner{Org: "old"}
		req.Region, stored.Region = "new", "old"
		protected["region"] = annotations.FieldBehavior_IMMUTABLE

		paths = []string{"owner.org", "region"}
		if strings.HasPrefix(layout, "atomic-") {
			paths[0] = "owner"
		}

		covering = []string{"owner", "region"}

		if layout != "atomic-after" {
			req.Name, stored.Name = "new", "old"

			protected["name"] = annotations.FieldBehavior_IDENTIFIER
			if layout == "atomic-priority" {
				protected["name"] = annotations.FieldBehavior_IMMUTABLE
			}

			paths = append([]string{"name"}, paths...)
			covering = append([]string{"name"}, covering...)
		}
	case "nested-deep", "nested-late":
		req.Region, stored.Region = "new", "old"
		req.Parent, stored.Parent = &pb.Account{Owner: &pb.Owner{Org: "new"}}, &pb.Account{Owner: &pb.Owner{Org: "old"}}
		protected["region"] = annotations.FieldBehavior_IMMUTABLE
		paths = []string{"region", "parent.owner.org"}
		covering = []string{"region", "parent"}

		if layout == "nested-deep" {
			req.Name, stored.Name = "new", "old"
			protected["name"] = annotations.FieldBehavior_IDENTIFIER

			paths = append([]string{"name"}, paths...)
			covering = append([]string{"name"}, covering...)
		}
	case "atomic-collection":
		req.Region, stored.Region = "new", "old"
		req.Parent = &pb.Account{Members: []*pb.Owner{{Org: "new"}}}
		stored.Parent = &pb.Account{Members: []*pb.Owner{{Org: "old"}}}
		protected["region"] = annotations.FieldBehavior_IMMUTABLE
		paths, covering = []string{"region", "parent"}, []string{"region", "parent"}
	case "covered-clear":
		req.Region, stored.Region = "new", "old"
		stored.Parent = &pb.Account{Owner: &pb.Owner{Org: "old"}}
		protected["region"] = annotations.FieldBehavior_IMMUTABLE
		paths = []string{"parent", "region", "parent.owner.org"}
	case "lexical-mixed":
		req.Owner, stored.Owner = &pb.Owner{Note: "new"}, &pb.Owner{Note: "old"}
		req.Region, stored.Region = "new", "old"
		req.Parent, stored.Parent = &pb.Account{Owner: &pb.Owner{Org: "new"}}, &pb.Account{Owner: &pb.Owner{Org: "old"}}
		protected["note"], protected["region"] = annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_IMMUTABLE
		paths = []string{"owner.note", "region", "parent.owner.org"}
		covering = []string{"owner", "region", "parent"}
	case "atomic-fields", "nested-fields":
		req.Owner, stored.Owner = &pb.Owner{Email: "new", Org: "new", Note: "new"}, &pb.Owner{Email: "old", Org: "old", Note: "old"}
		protected["email"], protected["note"] = annotations.FieldBehavior_IDENTIFIER, annotations.FieldBehavior_IMMUTABLE

		paths = []string{"owner.email", "owner.org", "owner.note"}
		if layout == "atomic-fields" {
			paths = []string{"owner"}
		}

		covering = []string{"owner"}
	case "list-sibling":
		req.Members, stored.Members = []*pb.Owner{{Org: "new"}}, []*pb.Owner{{Org: "old"}}
		req.Region, stored.Region = "new", "old"
		protected["region"] = annotations.FieldBehavior_IMMUTABLE
		paths, covering = []string{"members", "region"}, []string{"members", "region"}
	case "list":
		protected["email"] = annotations.FieldBehavior_IDENTIFIER

		for range 11 {
			req.Members = append(req.Members, &pb.Owner{Email: "same", Org: "same"})
			stored.Members = append(stored.Members, &pb.Owner{Email: "same", Org: "same"})
		}

		req.Members[2].Org = "new"
		req.Members[10].Email, req.Members[10].Org = "new", "new"
		paths, covering = []string{"members.*.email", "members.*.org"}, []string{"members"}
	case "map":
		protected["email"] = annotations.FieldBehavior_IDENTIFIER
		req.Delegates = map[string]*pb.Owner{"z": {Org: "new"}, "a.b": {Org: "new"}, "a-b": {Email: "new"}}
		stored.Delegates = map[string]*pb.Owner{"z": {Org: "old"}, "a.b": {Org: "old"}, "a-b": {Email: "old"}}
		paths, covering = []string{"delegates.*.email", "delegates.*.org"}, []string{"delegates"}
	default:
		panic(layout)
	}

	switch mode {
	case "all":
		paths = []string{"*"}
	case "implied":
		paths = nil
	case "declared":
	case "reversed":
		slices.Reverse(paths)
	case "shuffled":
		paths = append(slices.Clone(paths[1:]), paths[0])
	case "covering":
		paths = append(covering, paths...)
	default:
		panic(mode)
	}

	resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		if b, ok := protected[fd.Name()]; ok {
			return []annotations.FieldBehavior{b}
		}

		return nil
	})

	opts := []update.Option{update.WithIdentityFields(), update.WithResolver(resolver), update.WithStrict(c.Strict)}
	if strings.HasPrefix(layout, "atomic-") {
		opts = append(opts, update.WithAtomicMessage(func(md protoreflect.MessageDescriptor) bool {
			if layout == "atomic-collection" {
				return md.Name() == "Account"
			}

			return md.Name() == "Owner"
		}))
	}

	var raw *fieldmaskpb.FieldMask
	if paths != nil {
		raw = &fieldmaskpb.FieldMask{Paths: paths}
	}

	return req, stored, raw, opts, nil
}

// The base expands a non-atomic singular message across every element pair,
// but visits an atomic message one pair at a time. The changed fields cross
// element order so that the two traversals have different first violations.
func orderedElementMessageFixture(c publicRefusalCase) (proto.Message, proto.Message, *fieldmaskpb.FieldMask, []update.Option, []fieldbehavior.Option) {
	req := &descriptorpb.FileDescriptorProto{MessageType: []*descriptorpb.DescriptorProto{
		{Options: &descriptorpb.MessageOptions{Deprecated: new(true)}},
		{Options: &descriptorpb.MessageOptions{MessageSetWireFormat: new(true)}},
	}}
	stored := &descriptorpb.FileDescriptorProto{MessageType: []*descriptorpb.DescriptorProto{
		{Options: &descriptorpb.MessageOptions{Deprecated: new(false)}},
		{Options: &descriptorpb.MessageOptions{MessageSetWireFormat: new(false)}},
	}}
	resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		if fd.Name() == "deprecated" || fd.Name() == "message_set_wire_format" {
			return []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
		}

		return nil
	})
	opts := []update.Option{update.WithIdentityFields(), update.WithResolver(resolver), update.WithStrict(c.Strict),
		update.WithAtomicMessage(func(md protoreflect.MessageDescriptor) bool {
			return strings.Contains(c.Kind, "/list-atomic/") && md.Name() == "MessageOptions"
		}),
	}

	return req, stored, &fieldmaskpb.FieldMask{Paths: []string{"message_type.*.options"}}, opts, nil
}

func publicRefusal(c publicRefusalCase) error {
	req, stored, raw, opts, strip := publicRefusalFixture(c)
	opts = append(opts, update.WithCurrent(stored))

	switch c.Entry {
	case "MergePaths":
		return update.MergePaths(stored, req, raw.GetPaths(), opts...)
	case "ApplyMask":
		_, err := update.ApplyMask(raw, req, opts...)
		return err
	case "Apply":
		return update.Apply(refusalMask(raw), req, opts...)
	case "ValidateStrict":
		_, err := update.ValidateStrict(refusalMask(raw), req, opts...)
		return err
	case "StripUpdate":
		return fieldbehavior.StripUpdate(req, append(strip, fieldbehavior.WithCurrent(stored))...)
	}

	spec, err := update.NewSpec(req, opts...)
	if err != nil {
		panic(err)
	}

	if c.Entry == "Spec" {
		_, err := spec.Plan(req, raw, update.WithCurrent(stored))
		return err
	}

	patch := refusalPatch(spec, stored)
	_, err = patch.Run(context.Background(), req, "", raw)

	return err
}

func refusalPatch(spec *update.Spec, stored proto.Message) update.Patch[proto.Message, proto.Message, *int64] {
	return update.Patch[proto.Message, proto.Message, *int64]{Field: "resource", Spec: spec,
		Precondition: update.Int64Precondition[proto.Message]{Version: func(proto.Message) int64 { return 1 }},
		Read:         func(context.Context) (proto.Message, error) { return stored, nil },
		ToProto:      func(m proto.Message) proto.Message { return m },
		Save: func(_ context.Context, m proto.Message, _ update.Plan, _ *int64) (proto.Message, error) {
			return m, nil
		},
	}
}

func refusalMask(raw *fieldmaskpb.FieldMask) fieldmask.FieldMask {
	if raw == nil {
		return nil
	}

	return fieldmask.FromPaths(raw.Paths...)
}
