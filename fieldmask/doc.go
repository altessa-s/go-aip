// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package fieldmask provides hierarchical field mask utilities for Protocol Buffers.
//
// FieldMask is a hierarchical path selection with filtering, pruning, selection, union, intersection, difference and resolution
// operations on protobuf messages. Every path is read by one AIP-161 grammar: field segments in their declared, JSON or snake_case
// spelling, map keys typed by the map's key kind, backtick-quoted segments with a literal backtick doubled, and "*" for every element of a
// repeated field or every value of a map. An index into a repeated field and a field of its elements without "*" are rejected; "*" alone
// is the full-replacement mask and "*" with other paths is an error. FromProtoFieldMask keeps the presence of a mask (AIP-134): nil is an
// omitted mask, an empty mask was sent with no paths.
//
// Updates live in the update subpackage (AIP-134, AIP-154, AIP-163 and AIP-203: Apply, ValidateStrict, Spec.Plan, Merge, Patch), and
// the reading of masks off request messages in the extract subpackage.
//
// # Usage
//
//	mask, err := fieldmask.ParsePaths("user.name", "user.address.city")
//	if err != nil {
//		return err
//	}
//	err = mask.Filter(protoMessage)
//
//	readMask, err := fieldmask.FromProtoFieldMask(req.GetReadMask(), fieldmask.WithMaxPaths(100))
//
//	combined := mask1.Union(mask2)
//	common := mask1.Intersection(mask2)
//
// See README.md for the complete guide, limits and errors.
package fieldmask
