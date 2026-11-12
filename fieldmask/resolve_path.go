// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldmask

import (
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/internal/maskpath"
)

// ResolvePath returns path of md spelled by declared names
// ([FieldMask.Resolve]: any field segment may use its declared, JSON or
// snake_case spelling, map keys and "*" are kept), or "" when the path
// grammar or md's schema rejects path (AIP-161), breaks a limit of the
// options ([WithMaxPathLength], [WithMaxPathDepth]) or is the
// full-replacement path "*", which names no field.
func ResolvePath(md protoreflect.MessageDescriptor, path string, opts ...Option) string {
	policy := newOptions(opts...)
	if path == maskpath.Wildcard || policy.limits().CheckPath(path) != nil {
		return ""
	}

	r, err := maskpath.ResolvePath(md, path, policy.strictDynamicPaths)
	if err != nil {
		return ""
	}

	return r
}
