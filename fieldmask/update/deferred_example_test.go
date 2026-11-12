// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask/update"
)

func ExamplePrepare() {
	ctx := context.Background()
	resource := &descriptorpb.FileDescriptorProto{Name: new("example.proto"), Dependency: []string{"v2.proto"}}
	raw := &fieldmaskpb.FieldMask{Paths: []string{"name", "dependency"}}
	policy := update.WithImmutableFields("name")

	// Interceptor: normalize in place and attach the plan for this resource.
	pending, err := update.Prepare(raw, resource, policy)
	if err != nil {
		panic(err)
	}

	ctx = update.ContextWithPlan(ctx, pending)
	raw = pending.Mask().ToProtoFieldMask()

	// Handler: load stored state. Patch.Run uses its context automatically;
	// direct Spec.Plan callers pass WithPlanContext.
	stored := &descriptorpb.FileDescriptorProto{Name: new("example.proto"), Dependency: []string{"v1.proto"}}

	spec, err := update.NewSpec(resource, policy)
	if err != nil {
		panic(err)
	}

	plan, err := spec.Plan(resource, raw, update.WithCurrent(stored), update.WithPlanContext(ctx))
	if err != nil {
		panic(err)
	}

	merged := proto.Clone(stored).(*descriptorpb.FileDescriptorProto)
	if err := update.MergePaths(merged, resource, plan.Paths, policy); err != nil {
		panic(err)
	}

	fmt.Println(merged.GetName(), merged.GetDependency())
	// Output: example.proto [v2.proto]
}

func ExampleKeepIdentity_deferredValidation() {
	resource := &descriptorpb.FileDescriptorProto{Name: new("changed.proto")}
	policy := update.WithImmutableFields("name")

	pending, err := update.Prepare(&fieldmaskpb.FieldMask{Paths: []string{"name"}}, resource, policy)
	if err != nil {
		panic(err)
	}

	fmt.Println("effective paths:", len(pending.Mask()))

	// An empty effective mask still needs completion. Do not call KeepIdentity
	// before the handler validates the retained plan and consumes the request.
	_, err = pending.Complete(&descriptorpb.FileDescriptorProto{Name: new("stored.proto")})
	fmt.Println("changed immutable value refused:", err != nil)
	// Output:
	// effective paths: 0
	// changed immutable value refused: true
}
