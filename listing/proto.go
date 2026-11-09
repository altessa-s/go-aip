// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing

// ProtoRequest is the getter set every generated List request message shares
// (AIP-132, AIP-158, AIP-160).
type ProtoRequest interface {
	GetPageSize() int32
	GetPageToken() string
	GetFilter() string
	GetOrderBy() string
}

// parentGetter is a List request of a child list.
type parentGetter interface {
	GetParent() string
}

// RequestFromProto converts a generated List request message to a [Request]:
// pageSize, pageToken, filter, orderBy and, when the message has a
// GetParent method (a child list), parent. The handler passes the gRPC full
// method next to it as the list subject.
func RequestFromProto(msg ProtoRequest) Request {
	req := Request{
		PageSize:  msg.GetPageSize(),
		PageToken: msg.GetPageToken(),
		Filter:    msg.GetFilter(),
		OrderBy:   msg.GetOrderBy(),
	}
	if p, ok := msg.(parentGetter); ok {
		req.Parent = p.GetParent()
	}

	return req
}
