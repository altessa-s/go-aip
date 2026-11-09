// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package update applies a field mask to an update request: AIP-134 update masks, AIP-203 field behaviors, AIP-154 etag
// preconditions and AIP-163 validate_only, on the masks of the fieldmask package.
//
// Apply, ValidateStrict and Spec.Plan apply the field behaviors of AIP-203 to an update mask: OUTPUT_ONLY paths are ignored at any
// depth, IMMUTABLE and IDENTIFIER values are compared with the stored resource passed with WithCurrent (unchanged ignored, changed
// rejected; rejected without WithCurrent), REQUIRED paths must hold non-empty values, and a message named whole keeps its stored
// server-managed values. They and Patch keep the presence of the update mask (AIP-134), as fieldmask.FromProtoFieldMask builds it: nil
// is the implied mask of the populated fields, an empty mask is a no-op that leaves the message untouched and "*" is a full
// replacement. An empty cleaned mask (fieldmask.FieldMask.IsEmpty) or plan (Plan.Empty) means the update changes nothing. Merge and
// MergePaths copy the fields of update paths in a read-modify-write, and Patch runs the whole read-modify-write with an optional
// AIP-154 etag precondition (Int64Precondition, TagPrecondition), allow_missing and AIP-163 validate_only.
//
// # Usage
//
//	mask, err := fieldmask.FromProtoFieldMask(req.GetUpdateMask())
//	if err != nil {
//		return err
//	}
//	err = update.Apply(mask, req.GetUser(), update.WithCurrent(stored))
//
//	spec, err := update.NewSpec(&pb.User{}, update.WithImmutableFields("email"))
//	plan, err := spec.Plan(req.GetUser(), req.GetUpdateMask(), update.WithCurrent(stored))
//	err = update.MergePaths(merged, req.GetUser(), plan.Paths)
//
// See README.md for the complete guide, options and errors.
package update
