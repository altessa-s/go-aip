// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package maskpath holds the field mask path machinery shared by fieldmask
// and fieldmask/update: the AIP-161 path grammar and its schema walk, the
// dynamic JSON rules of google.protobuf.Struct, Value and ListValue, the mask
// limits and their sentinels, and the paths collected from a message.
//
// # Usage
//
//	segs, err := maskpath.Check{Aliases: true}.Resolve(md, raw)
//	if v := (maskpath.Limits{MaxPaths: 1000}).CheckMask(paths); v != nil {
//		return v.Cause
//	}
//
// A rejected path is an *Error; the public packages report it as
// *fieldmask.ValidationError.
package maskpath
