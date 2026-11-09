// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
)

func TestOptionsCurrentBehavior(t *testing.T) {
	t.Parallel()

	var nilProfile Profile

	o := newOptions(WithProfile(nilProfile), WithProfile(nil))
	require.Nil(t, o.profile)

	binding := []filter.TranslatorOption{filter.WithFieldMapping(map[string]string{"active": "a"})}
	option := WithFilterOptions(binding...)
	binding[0] = filter.WithFieldMapping(map[string]string{"active": "b"})
	o = newOptions(option)
	require.Len(t, o.filterOptions, 1)

	binding[0] = nil

	require.NotNil(t, o.filterOptions[0])

	newOptions(WithFilterOptions())
}
