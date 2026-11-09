// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package bsonstruct reads struct types the way the MongoDB driver's default struct codec describes them: which fields it
// encodes, under which key, and which struct types it describes in place (`bson:",inline"`). The value encoding of the MongoDB
// backend sizes stored struct values with it.
//
// # Usage
//
//	for f := range t.Fields() {
//		field := bsonstruct.ReadField(f)
//		if !field.Encoded {
//			continue
//		}
//		// field.Key, field.Inlined
//	}
package bsonstruct
