// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/typepb"

	"github.com/altessa-s/go-aip/fieldbehavior"
)

func TestPolicyIdentitySkipsResolverForUnconfiguredName(t *testing.T) {
	t.Parallel()

	fields := (&typepb.Type{}).ProtoReflect().Descriptor().Fields()
	calls := 0
	o := newOptions(WithIdentityFields("name"), WithResolver(fieldbehavior.ResolverFunc(
		func(protoreflect.FieldDescriptor) []annotations.FieldBehavior {
			calls++
			return nil
		},
	)))

	if isPolicyIdentity(fields.ByName("fields"), o) || calls != 0 {
		t.Errorf("unconfigured field called resolver %d times; want zero", calls)
	}

	calls = 0
	if !isPolicyIdentity(fields.ByName("name"), o) || calls != 1 {
		t.Errorf("configured identity should use the resolver once; got %d calls", calls)
	}
}
