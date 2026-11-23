// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter/evaluator"
)

func TestSimpleLower15_1(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ input, lower string }{
		{"ABC ЁЖИК ПрИвЕт", "abc ёжик привет"},
		{"İKẞ", "ikß"},
		{"ΣΟΣ", "σοσ"},
		{"A\u030A Å", "a\u030A å"},
		{"𐐀", "𐐨"},
		{"\u1c89", "\u1c89"}, // gained a lowercase mapping in Unicode 16
		{"x\xffY\xe2\x82Ж\uFFFD", "x\xffy\xe2\x82ж\uFFFD"},
		{"", ""},
	} {
		require.Equal(t, tc.lower, evaluator.SimpleLower15_1(tc.input))
	}
}
