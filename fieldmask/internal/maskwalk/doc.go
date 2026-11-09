// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package maskwalk applies a field mask tree to a protobuf message: the
// filter and prune walks shared by fieldmask (Filter, Prune, Select) and
// fieldmask/update (the cleaning of an update request), and the insertion of
// path segments into a tree. It is generic over the mask type, a recursive
// map such as fieldmask.FieldMask.
//
// # Usage
//
//	maskwalk.Insert(mask, maskpath.Split(path))
//	maskwalk.Filter(mask, msg, true) // a read mask
//	maskwalk.Prune(mask, msg)
package maskwalk
