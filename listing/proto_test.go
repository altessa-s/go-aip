// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/listing"
)

// listRequest mirrors the getters of a generated top-level List request.
type listRequest struct {
	pageSize                         int32
	pageToken, filterExpr, orderExpr string
}

func (r *listRequest) GetPageSize() int32   { return r.pageSize }
func (r *listRequest) GetPageToken() string { return r.pageToken }
func (r *listRequest) GetFilter() string    { return r.filterExpr }
func (r *listRequest) GetOrderBy() string   { return r.orderExpr }

// childListRequest is a List request of a child list.
type childListRequest struct {
	listRequest

	parent string
}

func (r *childListRequest) GetParent() string { return r.parent }

func TestRequestFromProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  listing.ProtoRequest
		want listing.Request
	}{
		{
			name: "top-level list",
			msg:  &listRequest{pageSize: 7, pageToken: "t", filterExpr: "isActive", orderExpr: "name desc"},
			want: listing.Request{PageSize: 7, PageToken: "t", Filter: "isActive", OrderBy: "name desc"},
		},
		{
			name: "child list",
			msg:  &childListRequest{pageSize: 3, filterExpr: "version > 2", parent: "p1"},
			want: listing.Request{PageSize: 3, Filter: "version > 2", Parent: "p1"},
		},
		{name: "empty", msg: &listRequest{}, want: listing.Request{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, listing.RequestFromProto(tc.msg))
		})
	}
}
