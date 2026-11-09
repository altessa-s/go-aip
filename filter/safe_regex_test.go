// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
)

// TestCheckPortableRegex pins the one portable subset every translator enforces under [filter.PortableRegex] (NEW-5): QuoteMeta
// text, anchors and .*, with an ASCII-only (?i); groups, alternations, classes and other quantifiers are outside it.
func TestCheckPortableRegex(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		pattern string
		want    error
	}{
		{`^abc$`, nil},
		{`a\.b.*c`, nil},
		{`(?i)^alpha`, nil},
		{`(?i)é`, filter.ErrUnsupportedOperation},
		{`^a[bc]+$`, filter.ErrUnsupportedOperation},
		{`a|b`, filter.ErrUnsupportedOperation},
		{`\d`, filter.ErrUnsupportedOperation},
		{strings.Repeat(`(a|a)`, 25) + "$", filter.ErrUnsupportedOperation},
		{"^" + strings.Repeat(`(a|ab|abc)`, 16) + "$", filter.ErrUnsupportedOperation},
	} {
		err := filter.CheckPortableRegex(tc.pattern)
		if tc.want == nil {
			require.NoError(t, err, tc.pattern)
			continue
		}

		require.ErrorIs(t, err, tc.want, tc.pattern)
	}
}
