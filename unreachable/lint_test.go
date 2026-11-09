// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package unreachable_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/unreachable"

	unreachabletestv1 "github.com/altessa-s/go-aip/internal/proto/gen/unreachabletest/v1"
)

func TestLintMethod(t *testing.T) {
	t.Parallel()

	svc := unreachabletestv1.File_unreachabletest_v1_unreachable_proto.Services().ByName("UnreachableTestService")

	tests := []struct {
		method protoreflect.Name
		want   map[unreachable.Rule]protoreflect.FullName
	}{
		{method: "ListBooks", want: map[unreachable.Rule]protoreflect.FullName{}},
		{method: "ListShelves", want: map[unreachable.Rule]protoreflect.FullName{
			unreachable.RuleUnreachableUnordered: "unreachabletest.v1.ListShelvesResponse.unreachable",
		}},
		{method: "ListAuthors", want: map[unreachable.Rule]protoreflect.FullName{
			unreachable.RuleUnreachableType:      "unreachabletest.v1.ListAuthorsResponse.unreachable",
			unreachable.RuleUnreachableUnordered: "unreachabletest.v1.ListAuthorsResponse.unreachable",
		}},
		{method: "ListPublishers", want: map[unreachable.Rule]protoreflect.FullName{
			unreachable.RulePartialSuccessType: "unreachabletest.v1.ListPublishersRequest.return_partial_success",
			unreachable.RulePartialSuccessPair: "unreachabletest.v1.UnreachableTestService.ListPublishers",
		}},
	}

	for _, tt := range tests {
		t.Run(string(tt.method), func(t *testing.T) {
			t.Parallel()

			got := map[unreachable.Rule]protoreflect.FullName{}

			for _, v := range unreachable.LintMethod(svc.Methods().ByName(tt.method)) {
				assert.NotEmpty(t, v.Message)
				got[v.Rule] = v.Element
			}

			assert.Equal(t, tt.want, got)
		})
	}
}
