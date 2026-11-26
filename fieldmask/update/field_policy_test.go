// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"errors"
	"sync/atomic"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldmask/update"
)

func TestSpecFieldPolicy(t *testing.T) {
	t.Parallel()

	md := planDescriptor(t)
	msg := dynamicpb.NewMessage(md)

	defaultSpec, err := update.NewSpec(msg)
	if err != nil {
		t.Fatal(err)
	}

	resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		switch fd.Name() {
		case "secret", "keywords":
			return []annotations.FieldBehavior{annotations.FieldBehavior_INPUT_ONLY, annotations.FieldBehavior_REQUIRED}
		case "region":
			return []annotations.FieldBehavior{annotations.FieldBehavior_IDENTIFIER, annotations.FieldBehavior_REQUIRED}
		case "note":
			return []annotations.FieldBehavior{annotations.FieldBehavior_REQUIRED}
		case "origin":
			return []annotations.FieldBehavior{annotations.FieldBehavior_IMMUTABLE, annotations.FieldBehavior_REQUIRED}
		case "create_time":
			return []annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY, annotations.FieldBehavior_IDENTIFIER, annotations.FieldBehavior_REQUIRED}
		default:
			return nil
		}
	})

	customSpec, err := update.NewSpec(msg, update.WithResolver(resolver), update.WithIdentityFields("code"),
		update.WithImmutableFields("secret", "region"), update.WithAllowedPaths("name"),
		update.WithAtomicMessage(func(md protoreflect.MessageDescriptor) bool { return md.Name() == "Status" }))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		spec *update.Spec
		path string
		want update.FieldPolicy
	}{
		{"default id", defaultSpec, "id", update.FieldPolicy{Identity: true}},
		{"default etag", defaultSpec, "etag", update.FieldPolicy{Identity: true}},
		{"default write-only", defaultSpec, "secret", update.FieldPolicy{WriteOnly: true}},
		{"default immutable", defaultSpec, "channel", update.FieldPolicy{Immutable: true}},
		{"nested output-only", defaultSpec, "status.inner.server", update.FieldPolicy{OutputOnly: true}},
		{"identity default replaced", customSpec, "id", update.FieldPolicy{}},
		{"configured identity", customSpec, "code", update.FieldPolicy{Identity: true}},
		{"configured immutable input-only", customSpec, "secret", update.FieldPolicy{Immutable: true, Required: true}},
		{"resolver replaces annotations", customSpec, "channel", update.FieldPolicy{}},
		{"nested input-only", customSpec, "filters.keywords", update.FieldPolicy{Required: true}},
		{"nested identifier", customSpec, "filters.region", update.FieldPolicy{Identity: true}},
		{"output-only outside allow-list", customSpec, "create_time", update.FieldPolicy{OutputOnly: true}},
		{"required in atomic value", customSpec, "status.note", update.FieldPolicy{Required: true}},
		{"required immutable in atomic value", customSpec, "status.origin", update.FieldPolicy{Immutable: true, Required: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := tc.spec.FieldPolicy(policyRoute(t, md, tc.path)...)
			if err != nil || got != tc.want {
				t.Errorf("FieldPolicy(%s) = %+v, %v; want %+v", tc.path, got, err, tc.want)
			}
		})
	}

	for _, tc := range []struct {
		name  string
		route []protoreflect.FieldDescriptor
	}{
		{"empty", nil},
		{"nil descriptor", []protoreflect.FieldDescriptor{nil}},
		{"foreign root of same name", policyRoute(t, planDescriptor(t), "name")},
		{"foreign child", []protoreflect.FieldDescriptor{md.Fields().ByName("filters"), md.Fields().ByName("name")}},
		{"foreign equivalent child", []protoreflect.FieldDescriptor{md.Fields().ByName("filters"),
			policyRoute(t, planDescriptor(t), "filters.keywords")[1]}},
		{"descent through scalar", []protoreflect.FieldDescriptor{md.Fields().ByName("name"), md.Fields().ByName("code")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := customSpec.FieldPolicy(tc.route...); !errors.Is(err, update.ErrMessageMismatch) {
				t.Errorf("FieldPolicy error = %v; want %v", err, update.ErrMessageMismatch)
			}
		})
	}
}

func TestSpecFieldPolicyParity(t *testing.T) {
	t.Parallel()

	md := planDescriptor(t)
	resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		switch fd.Name() {
		case "secret", "keywords":
			return []annotations.FieldBehavior{annotations.FieldBehavior_INPUT_ONLY, annotations.FieldBehavior_REQUIRED}
		case "note":
			return []annotations.FieldBehavior{annotations.FieldBehavior_REQUIRED}
		default:
			return (fieldbehavior.AnnotationResolver{}).Resolve(fd)
		}
	})

	spec, err := update.NewSpec(dynamicpb.NewMessage(md), update.WithResolver(resolver), update.WithIdentityFields("code"),
		update.WithImmutableFields("name"), update.WithAtomicMessage(func(md protoreflect.MessageDescriptor) bool { return md.Name() == "Status" }))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name     string
		path     string
		maskPath string
		input    map[string]string
	}{
		{name: "identity", path: "code", maskPath: "code"},
		{name: "output-only", path: "create_time", maskPath: "create_time"},
		{name: "write-only", path: "secret", maskPath: "secret", input: map[string]string{"secret": "new"}},
		{name: "immutable", path: "name", maskPath: "name", input: map[string]string{"name": "new"}},
		{name: "required nested input-only", path: "filters.keywords", maskPath: "filters.keywords"},
		{name: "required inside atomic value", path: "status.note", maskPath: "status", input: map[string]string{"status.note": ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			policy, err := spec.FieldPolicy(policyRoute(t, md, tc.path)...)
			if err != nil {
				t.Fatal(err)
			}

			req := planResource(md, tc.input)
			current := update.WithCurrent(dynamicpb.NewMessage(md))
			mask := maskOf([]string{tc.maskPath})

			resolved, err := spec.ResolveMask(req, mask)
			if err != nil {
				t.Fatal(err)
			}

			violations, err := spec.ValidateResolved(req, resolved, current)
			if err != nil {
				t.Fatal(err)
			}

			plan, planErr := spec.Plan(req, mask, current)

			switch {
			case policy.Identity:
				if len(violations) != 0 {
					t.Errorf("ValidateResolved = %+v; want no value violations for the routing field", violations)
				}

				if !errors.Is(planErr, update.ErrProtectedField) {
					t.Errorf("Plan error = %v; want %v", planErr, update.ErrProtectedField)
				}
			case policy.Immutable || policy.Required:
				want := annotations.FieldBehavior_REQUIRED
				wantErr := update.ErrBehaviorViolation

				if policy.Immutable {
					want = annotations.FieldBehavior_IMMUTABLE
					wantErr = update.ErrProtectedField
				}

				if len(violations) != 1 || violations[0].Path != tc.path || violations[0].Behavior != want {
					t.Errorf("ValidateResolved = %+v; want %s violation for %s", violations, want, tc.path)
				}

				if !errors.Is(planErr, wantErr) {
					t.Errorf("Plan error = %v; want %v", planErr, wantErr)
				}
			default:
				if policy.OutputOnly && len(resolved.Paths()) != 0 {
					t.Errorf("ResolveMask = %+v; want empty scope for an output-only field", resolved.Paths())
				}

				if planErr != nil || len(violations) != 0 || len(plan.Paths) != 0 {
					t.Errorf("Plan = %+v, %v; violations = %+v", plan, planErr, violations)
				}

				if got := plan.Secret(tc.path, new("new")) != nil; got != policy.WriteOnly {
					t.Errorf("Plan names secret %t; policy = %+v", got, policy)
				}
			}
		})
	}
}

func TestSpecFieldPolicyCollectionRoutes(t *testing.T) {
	t.Parallel()

	md := collectionDescriptor(t)

	spec, err := update.NewSpec(dynamicpb.NewMessage(md))
	if err != nil {
		t.Fatal(err)
	}

	items := md.Fields().ByName("items")
	list := md.Fields().ByName("list")

	for _, tc := range []struct {
		field protoreflect.FieldDescriptor
		child protoreflect.FieldDescriptor
	}{
		{items, items.MapValue().Message().Fields().ByName("pinned")},
		{list, list.Message().Fields().ByName("pinned")},
	} {
		t.Run(string(tc.field.Name()), func(t *testing.T) {
			t.Parallel()

			want := update.FieldPolicy{Immutable: true}
			if got, err := spec.FieldPolicy(tc.field, tc.child); err != nil || got != want {
				t.Errorf("FieldPolicy(%s, %s) = %+v, %v; want %+v", tc.field.Name(), tc.child.Name(), got, err, want)
			}
		})
	}
}

func TestSpecFieldPolicySnapshot(t *testing.T) {
	t.Parallel()

	md := planDescriptor(t)

	var (
		changed   atomic.Bool
		rootCalls atomic.Int32
	)

	resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		if fd.ContainingMessage() == md {
			rootCalls.Add(1)
		}

		switch fd.Name() {
		case "name", "secret", "create_time":
			if changed.Load() {
				return nil
			}

			if fd.Name() == "name" {
				return []annotations.FieldBehavior{annotations.FieldBehavior_REQUIRED}
			}
		case "keywords":
			if changed.Load() {
				return []annotations.FieldBehavior{annotations.FieldBehavior_REQUIRED}
			}
		}

		return (fieldbehavior.AnnotationResolver{}).Resolve(fd)
	})

	spec, err := update.NewSpec(dynamicpb.NewMessage(md), update.WithResolver(resolver))
	if err != nil {
		t.Fatal(err)
	}

	changed.Store(true)

	for _, tc := range []struct {
		path string
		want update.FieldPolicy
	}{
		{"secret", update.FieldPolicy{WriteOnly: true}},
		{"name", update.FieldPolicy{Required: true}},
		{"create_time", update.FieldPolicy{OutputOnly: true}},
		{"filters.keywords", update.FieldPolicy{Required: true}},
	} {
		if got, err := spec.FieldPolicy(policyRoute(t, md, tc.path)...); err != nil || got != tc.want {
			t.Errorf("FieldPolicy(%s) = %+v, %v; want %+v", tc.path, got, err, tc.want)
		}
	}

	req := planResource(md, map[string]string{"secret": "new", "create_time": "ignored"})
	plan, err := spec.Plan(req, maskOf([]string{"secret", "create_time"}))

	secret := plan.Secret("secret", new("new"))
	if err != nil || len(plan.Paths) != 0 || secret == nil || *secret != "new" {
		t.Errorf("Plan = %+v, %v; want a secret operation and no write paths", plan, err)
	}

	for _, path := range []string{"name", "filters.keywords"} {
		if _, err := spec.Plan(req, maskOf([]string{path})); !errors.Is(err, update.ErrBehaviorViolation) {
			t.Errorf("Plan(%s) error = %v; want %v", path, err, update.ErrBehaviorViolation)
		}
	}

	resolved, err := spec.ResolveMask(req, maskOf([]string{"create_time"}))
	if err != nil {
		t.Fatal(err)
	}

	if len(resolved.Paths()) != 0 {
		t.Errorf("ResolveMask(create_time) = %v; want empty selection", resolved.Paths())
	}

	resolved, err = spec.ResolveMask(req, maskOf([]string{"name"}))
	if err != nil {
		t.Fatal(err)
	}

	violations, err := spec.ValidateResolved(req, resolved)
	if err != nil || len(violations) != 1 || violations[0].Behavior != annotations.FieldBehavior_REQUIRED {
		t.Errorf("ValidateResolved(name) = %v, %v; want a REQUIRED violation", violations, err)
	}

	if got := int(rootCalls.Load()); got != md.Fields().Len() {
		t.Errorf("resolver called %d times for root fields; want %d", got, md.Fields().Len())
	}
}

func TestSpecFieldPolicyExtension(t *testing.T) {
	t.Parallel()

	schema := &descriptorpb.FileDescriptorProto{
		Name: new("extension.proto"), Package: new("policytest"), Syntax: new("proto2"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: new("Resource"),
			ExtensionRange: []*descriptorpb.DescriptorProto_ExtensionRange{{
				Start: new(int32(100)), End: new(int32(101)),
			}},
		}},
		Extension: []*descriptorpb.FieldDescriptorProto{{
			Name: new("server_data"), Number: new(int32(100)), Extendee: new(".policytest.Resource"),
			Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			Type:  descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
		}},
	}

	file, err := protodesc.NewFile(schema, nil)
	if err != nil {
		t.Fatal(err)
	}

	md := file.Messages().Get(0)
	ext := dynamicpb.NewExtensionType(file.Extensions().Get(0)).TypeDescriptor()
	msg := dynamicpb.NewMessage(md)
	msg.Set(ext, protoreflect.ValueOfString("server value"))

	resolver := fieldbehavior.ResolverFunc(func(fd protoreflect.FieldDescriptor) []annotations.FieldBehavior {
		if fd == ext {
			return []annotations.FieldBehavior{annotations.FieldBehavior_OUTPUT_ONLY}
		}

		return nil
	})

	spec, err := update.NewSpec(msg, update.WithResolver(resolver))
	if err != nil {
		t.Fatal(err)
	}

	msg.Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		want := update.FieldPolicy{OutputOnly: true}
		if got, err := spec.FieldPolicy(fd); err != nil || got != want {
			t.Errorf("FieldPolicy(%s) = %+v, %v; want %+v", fd.FullName(), got, err, want)
		}

		return true
	})

	foreign, err := protodesc.NewFile(schema, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := spec.FieldPolicy(foreign.Extensions().Get(0)); !errors.Is(err, update.ErrMessageMismatch) {
		t.Errorf("FieldPolicy(foreign extension) error = %v; want %v", err, update.ErrMessageMismatch)
	}
}

func policyRoute(t testing.TB, md protoreflect.MessageDescriptor, path string) []protoreflect.FieldDescriptor {
	t.Helper()

	var route []protoreflect.FieldDescriptor

	for _, name := range splitDots(path) {
		if md == nil {
			t.Fatalf("path %q descends through a scalar", path)
		}

		fd := md.Fields().ByName(protoreflect.Name(name))
		if fd == nil {
			t.Fatalf("path %q has no field %q in %s", path, name, md.FullName())
		}

		route = append(route, fd)
		md = fd.Message()
	}

	return route
}
