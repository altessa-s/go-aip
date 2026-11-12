// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package aipmap_test

import (
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"sync"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

// This seeded input generator uses only base APIs and has no testing dependency.
// The recorder runs it against an archive of a8b17ba, never against HEAD. Each
// input changes two to four protected placements, including several changes
// within one atomic value beside a changed sibling. Additional inputs mix
// REQUIRED and protected violations across list elements. The corpus also
// mixes wildcard and explicit map keys. Indexed list selections
// are recorded as rejections: both the base and HEAD forbid list-index masks.
// Placement indexes also specify the fallback rule: schema order, numeric list indexes,
// then map keys. The actual base diagnostics always precede this fallback.
type generatedRefusalCase struct {
	ID        int
	Layout    string
	Missing   int
	Mode      string
	Changed   []int
	Index     int
	Key       string
	MapInside bool
	Raw       []string
	Keys      []string `json:",omitempty"`
	Named     []int    `json:",omitempty"`
}

type generatedRefusalRecord struct {
	Case            generatedRefusalCase
	Request, Stored string
	Apply           []update.FieldViolation
	Spec, Patch     refusalShape
	Order           []string      // base Apply order followed by new fields in placement order
	Rejection       *refusalShape `json:",omitempty"` // invalid indexed-list selection
}

func generatedRefusalCases() []generatedRefusalCase {
	rng := rand.New(rand.NewPCG(203, 25))
	cases := make([]generatedRefusalCase, 48)

	for i := range cases {
		c := generatedRefusalCase{ID: i, Mode: []string{"explicit", "covering", "all", "implied"}[i%4],
			Changed: slices.Clone(rng.Perm(8)[:2+rng.IntN(3)]), Index: rng.IntN(3), Key: []string{"a", "z", "a-b"}[rng.IntN(3)],
			MapInside: rng.IntN(2) == 0}
		if i%8 == 0 {
			c.Changed = []int{0, 3, 4, 5} // atomic siblings and a top-level sibling, plus a collection change
		}

		paths := generatedRefusalPaths(c)

		for _, placement := range c.Changed {
			path := paths[placement]
			switch placement {
			case 2, 6:
				path = strings.Replace(path, "."+strconv.Itoa(c.Index)+".", ".*.", 1)
				path = strings.TrimSuffix(path, ".value") // explicit atomic leaf, concrete base diagnostic
			case 3, 4, 5:
				path = "atomic" // no path may descend through this boundary
			}

			if c.Mode == "covering" {
				cover, _, _ := strings.Cut(path, ".")
				if !slices.Contains(c.Raw, cover) {
					c.Raw = append(c.Raw, cover)
				}

				if rng.IntN(2) == 0 {
					continue
				}
			}

			if !slices.Contains(c.Raw, path) {
				c.Raw = append(c.Raw, path)
			}
		}

		rng.Shuffle(len(c.Raw), func(a, b int) { c.Raw[a], c.Raw[b] = c.Raw[b], c.Raw[a] })
		slices.Sort(c.Changed)

		switch c.Mode {
		case "all":
			c.Raw = []string{"*"}
		case "implied":
			c.Raw = nil
		}

		cases[i] = c
	}

	// The reviewer's exact Account and FileDescriptorProto inputs are recorded too.
	cases = append(cases, generatedRefusalCase{ID: len(cases), Layout: "atomic-sibling", Mode: "explicit",
		Changed: []int{0, 1, 2}, Raw: []string{"owner", "region"}})
	cases = append(cases, generatedRefusalCase{ID: len(cases), Layout: "required", Mode: "explicit",
		Changed: []int{0, 1}, Index: 0, Missing: 2, Raw: []string{"message_type.*.options"}})

	for range 16 {
		index := rng.IntN(3)
		missing := (index + 1 + rng.IntN(2)) % 3

		c := generatedRefusalCase{ID: len(cases), Layout: "required", Changed: []int{0, 1}, Index: index, Missing: missing,
			Mode: []string{"explicit", "covering", "all", "implied"}[len(cases)%4]}
		switch c.Mode {
		case "explicit":
			c.Raw = []string{"message_type.*.options"}
		case "covering":
			c.Raw = []string{"message_type", "message_type.*.options"}
			rng.Shuffle(len(c.Raw), func(a, b int) { c.Raw[a], c.Raw[b] = c.Raw[b], c.Raw[a] })
		case "all":
			c.Raw = []string{"*"}
		}

		cases = append(cases, c)
	}

	cases = append(cases, generatedRefusalCase{ID: len(cases), Layout: "keyed-map", Mode: "explicit",
		Changed: []int{0, 1}, Keys: []string{"a", "z"}, Named: []int{0}, Raw: []string{"delegates.a.org", "delegates.*.org"}})

	for i := range 16 {
		layout := []string{"keyed-map", "nested-keyed-map", "wildcard-list", "indexed-list"}[i%4]
		c := generatedRefusalCase{ID: len(cases), Layout: layout, Mode: "explicit",
			Keys: []string{"!", "a", "a.b", "b", "z"}, Changed: []int{0, 1, 2, 3, 4}}

		c.Named = slices.Clone(rng.Perm(len(c.Keys))[:1+rng.IntN(3)])
		if layout == "wildcard-list" {
			c.Named = nil
		}

		paths := generatedRefusalPaths(c)

		c.Raw = []string{generatedCollectionPath(c) + ".*.org"}
		for _, index := range c.Named {
			c.Raw = append(c.Raw, paths[index])
		}

		// Alternate explicit/covering selections for every valid layout.
		if layout != "indexed-list" && (i/4)%2 == 1 {
			c.Mode = "covering"
			c.Raw = append(c.Raw, generatedCollectionPath(c))
		}

		rng.Shuffle(len(c.Raw), func(a, b int) { c.Raw[a], c.Raw[b] = c.Raw[b], c.Raw[a] })
		cases = append(cases, c)
	}

	return cases
}

func generatedRefusalPaths(c generatedRefusalCase) []string {
	if len(c.Keys) > 0 {
		paths := make([]string, len(c.Keys))
		for i, key := range c.Keys {
			if c.Layout == "wildcard-list" || c.Layout == "indexed-list" {
				key = strconv.Itoa(i)
			} else if strings.Contains(key, ".") {
				key = "`" + key + "`"
			}

			paths[i] = generatedCollectionPath(c) + "." + key + ".org"
		}

		return paths
	}

	if c.Layout == "atomic-sibling" {
		return []string{"owner.email", "owner.org", "region"}
	}

	if c.Layout == "required" {
		return []string{fmt.Sprintf("message_type.%d.options.deprecated", c.Index), fmt.Sprintf("message_type.%d.options", c.Missing)}
	}

	index := strconv.Itoa(c.Index)

	inside := "members." + index + ".atom.value"
	if c.MapInside {
		inside = "delegates." + c.Key + ".atom.value"
	}

	return []string{"region", "nested.value", "nested." + inside, "atomic.value", "atomic.other",
		"atomic." + inside, "members." + index + ".atom.value", "delegates." + c.Key + ".value"}
}

var generatedRefusalSchema = generatedRefusalDescriptor()

func generatedRefusalFixture(c generatedRefusalCase) (proto.Message, proto.Message, *fieldmaskpb.FieldMask, []update.Option) {
	if len(c.Keys) > 0 {
		return generatedCollectionFixture(c)
	}

	if c.Layout == "required" {
		return generatedRequiredFixture(c)
	}

	if c.Layout == "atomic-sibling" {
		req := &pb.Account{Owner: &pb.Owner{Email: "new", Org: "new"}, Region: "new"}
		stored := &pb.Account{Owner: &pb.Owner{Email: "old", Org: "old"}, Region: "old"}
		opts := []update.Option{update.WithIdentityFields(),
			update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
				if fd.Name() == "email" || fd.Name() == "org" || fd.Name() == "region" {
					return []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
				}

				return nil
			})), update.WithAtomicMessage(func(md protoreflect.MessageDescriptor) bool { return md.Name() == "Owner" }),
		}

		return req, stored, &fieldmaskpb.FieldMask{Paths: slices.Clone(c.Raw)}, opts
	}

	md := generatedRefusalSchema
	req, stored := dynamicpb.NewMessage(md), dynamicpb.NewMessage(md)
	paths := generatedRefusalPaths(c)

	for _, placement := range c.Changed {
		generatedRefusalSet(req, strings.Split(paths[placement], "."), "new")
		generatedRefusalSet(stored, strings.Split(paths[placement], "."), "old")
	}

	var raw *fieldmaskpb.FieldMask
	if c.Raw != nil {
		raw = &fieldmaskpb.FieldMask{Paths: slices.Clone(c.Raw)}
	}

	opts := []update.Option{update.WithIdentityFields(),
		update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if fd.Name() == "value" || fd.Name() == "other" || fd.Name() == "region" {
				return []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
			}

			return nil
		})),
		update.WithAtomicMessage(func(md protoreflect.MessageDescriptor) bool { return md.Name() == "Atomic" || md.Name() == "Leaf" }),
	}

	return req, stored, raw, opts
}

func generatedRefusalSet(m protoreflect.Message, path []string, value string) {
	fd := m.Descriptor().Fields().ByName(protoreflect.Name(path[0]))
	if len(path) == 1 {
		m.Set(fd, protoreflect.ValueOfString(value))
		return
	}

	switch {
	case fd.IsList():
		index, err := strconv.Atoi(path[1])
		if err != nil {
			panic(err)
		}

		list := m.Mutable(fd).List()
		for list.Len() <= index {
			list.AppendMutable()
		}

		generatedRefusalSet(list.Get(index).Message(), path[2:], value)
	case fd.IsMap():
		mm := m.Mutable(fd).Map()
		key := protoreflect.ValueOfString(path[1]).MapKey()
		generatedRefusalSet(mm.Mutable(key).Message(), path[2:], value)
	default:
		generatedRefusalSet(m.Mutable(fd).Message(), path[1:], value)
	}
}

// Descriptors are immutable; share one across fixtures and the base recorder.
var generatedRefusalDescriptor = sync.OnceValue(buildGeneratedRefusalDescriptor)

func buildGeneratedRefusalDescriptor() protoreflect.MessageDescriptor {
	field := func(name string, number int32, typ string, repeated bool) *descriptorpb.FieldDescriptorProto {
		f := &descriptorpb.FieldDescriptorProto{Name: new(name), Number: new(number),
			Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()}
		if typ != "" {
			f.Type, f.TypeName = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), new(".refusal24."+typ)
		}

		if repeated {
			f.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
		}

		return f
	}
	message := func(name string, fields ...*descriptorpb.FieldDescriptorProto) *descriptorpb.DescriptorProto {
		return &descriptorpb.DescriptorProto{Name: new(name), Field: fields}
	}
	withMap := func(md *descriptorpb.DescriptorProto) *descriptorpb.DescriptorProto {
		entry := message("DelegatesEntry", field("key", 1, "", false), field("value", 2, "Element", false))
		entry.Options = &descriptorpb.MessageOptions{MapEntry: new(true)}
		md.NestedType = []*descriptorpb.DescriptorProto{entry}
		md.Field = append(md.Field, field("delegates", int32(len(md.Field)+1), md.GetName()+".DelegatesEntry", true))

		return md
	}
	root := withMap(message("Resource", field("region", 1, "", false), field("nested", 2, "Nested", false),
		field("atomic", 3, "Atomic", false), field("members", 4, "Element", true)))

	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{Name: new("refusal24.proto"),
		Package: new("refusal24"), Syntax: new("proto3"), MessageType: []*descriptorpb.DescriptorProto{
			root, withMap(message("Nested", field("value", 1, "", false), field("members", 2, "Element", true))),
			withMap(message("Atomic", field("value", 1, "", false), field("other", 2, "", false), field("members", 3, "Element", true))),
			message("Element", field("atom", 1, "Leaf", false), field("value", 2, "", false)),
			message("Leaf", field("value", 1, "", false)),
		}}, nil)
	if err != nil {
		panic(fmt.Sprintf("generated refusal descriptor: %v", err))
	}

	return file.Messages().ByName("Resource")
}

// generatedRefusalOrder writes the independent deterministic rule into the
// golden: retain every base diagnostic in its recorded order, then append all
// remaining changed placements in declaration order. The atomic walk's first
// difference and the REQUIRED group precheck are recorded by the base itself.
// No case where the base reports a violation has a parity exemption.
func generatedRefusalOrder(c generatedRefusalCase, base []update.FieldViolation) []string {
	if c.Layout == "indexed-list" {
		return nil // invalid before the behavior walk, on both branches
	}

	var order []string
	for _, v := range generatedRefusalPrefix(c, base) {
		order = append(order, v.Path)
	}

	paths := generatedRefusalPaths(c)
	for _, placement := range c.Changed {
		if path := paths[placement]; !slices.Contains(order, path) {
			order = append(order, path)
		}
	}

	return order
}

func generatedCollectionPath(c generatedRefusalCase) string {
	switch c.Layout {
	case "nested-keyed-map":
		return "parent.delegates"
	case "wildcard-list", "indexed-list":
		return "members"
	default:
		return "delegates"
	}
}

func generatedCollectionFixture(c generatedRefusalCase) (proto.Message, proto.Message, *fieldmaskpb.FieldMask, []update.Option) {
	req, stored := &pb.Account{}, &pb.Account{}

	r, s := req, stored
	if c.Layout == "nested-keyed-map" {
		req.Parent, stored.Parent = &pb.Account{}, &pb.Account{}
		r, s = req.Parent, stored.Parent
	}

	r.Delegates, s.Delegates = map[string]*pb.Owner{}, map[string]*pb.Owner{}

	for i, key := range c.Keys {
		value := "old"
		if slices.Contains(c.Changed, i) {
			value = "new"
		}

		if c.Layout == "wildcard-list" || c.Layout == "indexed-list" {
			r.Members = append(r.Members, &pb.Owner{Org: value})
			s.Members = append(s.Members, &pb.Owner{Org: "old"})
		} else {
			r.Delegates[key], s.Delegates[key] = &pb.Owner{Org: value}, &pb.Owner{Org: "old"}
		}
	}

	opts := []update.Option{update.WithIdentityFields(),
		update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			if fd.Name() == "org" {
				return []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
			}

			return nil
		})),
	}

	return req, stored, &fieldmaskpb.FieldMask{Paths: slices.Clone(c.Raw)}, opts
}

// generatedRefusalPrefix expands only the collection fixture's known wildcard
// group, using its explicit-key exclusions. A covering collection makes each
// covered path a separate selection, so its wildcard then includes every key.
// This derives concrete expectations from fixture placements, never from HEAD
// diagnostics or path-pattern matching against evaluated violations.
func generatedRefusalPrefix(c generatedRefusalCase, base []update.FieldViolation) []update.FieldViolation {
	var prefix []update.FieldViolation

	for _, v := range base {
		if len(c.Keys) == 0 || v.Path != generatedCollectionPath(c)+".*.org" {
			if !slices.Contains(prefix, v) {
				prefix = append(prefix, v)
			}

			continue
		}

		paths := generatedRefusalPaths(c)
		for _, index := range c.Changed {
			if c.Mode != "covering" && slices.Contains(c.Named, index) {
				continue
			}

			concrete := v

			concrete.Path = paths[index]
			if !slices.Contains(prefix, concrete) {
				prefix = append(prefix, concrete)
			}
		}
	}

	return prefix
}

func generatedRefusalWire(m proto.Message) string {
	wire, err := (proto.MarshalOptions{Deterministic: true}).Marshal(m)
	if err != nil {
		panic(err)
	}

	return hex.EncodeToString(wire)
}

// generatedRequiredFixture keeps the absent REQUIRED field identical in request
// and stored state. Only the selected atomic descendant differs. Thus the base's
// REQUIRED group precheck must precede (and suppress) its atomic comparison.
func generatedRequiredFixture(c generatedRefusalCase) (proto.Message, proto.Message, *fieldmaskpb.FieldMask, []update.Option) {
	req := &descriptorpb.FileDescriptorProto{}

	for i := range 3 {
		element := &descriptorpb.DescriptorProto{}
		if i != c.Missing {
			element.Options = &descriptorpb.MessageOptions{}
		}

		req.MessageType = append(req.MessageType, element)
	}

	req.MessageType[c.Index].Options.Deprecated = new(true)
	stored := proto.Clone(req).(*descriptorpb.FileDescriptorProto)
	stored.MessageType[c.Index].Options.Deprecated = new(false)
	opts := []update.Option{update.WithIdentityFields(),
		update.WithResolver(fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			switch fd.FullName() {
			case "google.protobuf.DescriptorProto.options":
				return []annotations.FieldBehavior{annotations.FieldBehavior_REQUIRED}
			case "google.protobuf.MessageOptions.deprecated":
				return []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE}
			}

			return nil
		})), update.WithAtomicMessage(func(md protoreflect.MessageDescriptor) bool { return md.Name() == "MessageOptions" }),
	}

	var raw *fieldmaskpb.FieldMask
	if c.Raw != nil {
		raw = &fieldmaskpb.FieldMask{Paths: slices.Clone(c.Raw)}
	}

	return req, stored, raw, opts
}
