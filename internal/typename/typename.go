// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package typename

import "reflect"

// maxTypeNameBytes bounds the name of a type an error message spells for a value, which may come from the data: the name of a struct
// type constructed at run time (reflect.StructOf) lists all its fields and their tags.
const maxTypeNameBytes = 128

// Of returns the name of the type of v, as %T formats it, cut to [maxTypeNameBytes]; reading it does not copy it.
func Of(v any) string {
	if v == nil {
		return "<nil>"
	}

	name := reflect.TypeOf(v).String()
	if len(name) > maxTypeNameBytes {
		return name[:maxTypeNameBytes] + "..."
	}

	return name
}
