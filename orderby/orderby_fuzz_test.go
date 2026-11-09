// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package orderby_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/orderby"
)

func FuzzParse(f *testing.F) {
	seeds := []string{
		"",
		" ",
		"slug",
		"slug desc",
		"create_time desc, slug",
		"address.city asc, name desc",
		"a,b,c",
		",",
		",a",
		"a,",
		"a,,b",
		"foo bar baz",
		"foo$",
		"foo..bar",
		"1foo",
		"DESC",
		"\t\n",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	p, err := orderby.NewParser(orderby.WithNoCache(true))
	require.NoError(f, err)

	f.Fuzz(func(t *testing.T, in string) {
		// Parse must never panic for any input.
		_, _ = p.Parse(t.Context(), in)
	})
}
