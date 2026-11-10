// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package extract reads field masks off request messages: the AIP-134 update_mask with its sibling resource, and the legacy
// read_mask of AIP-157 (deprecated by AIP-161). It works on descriptors only and has no gRPC dependency; UpdateFunc and ReadFunc are
// the transport-agnostic shapes a gRPC interceptor or any other caller composes with ChainUpdate and ChainRead.
//
// Default lookup selects a singular google.protobuf.FieldMask declared as update_mask or read_mask. Only when that declared
// name is absent does it try updateMask or readMask, respectively. A snake_case declaration wins even if unset or of the wrong
// type. [WithMaskField] selects an exact declared name without fallback; JSON names are not used for mask-field lookup.
// This rule is shared by [UpdateMask], [SetUpdateMask], [DefaultUpdate], [IsCanonicalUpdateRequest], [ReadMask], and [DefaultRead].
//
// # Usage
//
//	mask, resource, ok := extract.UpdateMask(req)
//	if ok {
//		// apply mask to resource, then write the cleaned mask back
//		err = extract.SetUpdateMask(req, cleaned)
//	}
//
//	read := extract.ChainRead(
//		extract.NewReadFunc(func(r *pb.GetBookRequest) *fieldmaskpb.FieldMask { return r.GetOptions().GetReadMask() }),
//		extract.DefaultRead(),
//	)
//	mask, ok := read(ctx, req)
//
// See README.md for the complete guide.
package extract
