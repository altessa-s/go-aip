// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMapOptionsSnapshotAndEmpty(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		set  func(*translatorOptions, map[string]string)
		get  func(*translatorOptions) map[string]string
	}{
		{"field mapping", func(o *translatorOptions, v map[string]string) { WithFieldMapping(v)(o) }, func(o *translatorOptions) map[string]string {
			result := make(map[string]string)

			if o.fieldMapping != nil {
				maps.Insert(result, o.fieldMapping.All())
			}

			return result
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			input := map[string]string{" ": " storage ", "api": "storage"}
			op := func(o *translatorOptions) { tc.set(o, input) }
			input["api"] = "before apply"
			o := newTranslatorOptions(op)
			input["api"] = "after apply"

			require.Equal(t, map[string]string{" ": " storage ", "api": "before apply"}, tc.get(o))
			tc.set(o, nil)
			require.Empty(t, tc.get(o))
		})
	}

	o := newOptions(WithFieldTypes(map[string]FieldKind{"age": FieldKindInt}), WithFieldTypes(nil))
	require.Equal(t, 0, o.fieldTypes.Len())
}
