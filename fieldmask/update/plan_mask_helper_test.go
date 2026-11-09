// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update_test

import "google.golang.org/protobuf/types/known/fieldmaskpb"

// maskOf returns the update mask of paths: nil (an omitted update_mask) for
// nil paths, a present mask otherwise, empty for an empty non-nil slice.
func maskOf(paths []string) *fieldmaskpb.FieldMask {
	if paths == nil {
		return nil
	}

	return &fieldmaskpb.FieldMask{Paths: paths}
}
