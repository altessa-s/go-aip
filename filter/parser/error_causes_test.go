// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package parser_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/parser"
)

func TestInjectedErrorCauses(t *testing.T) {
	t.Parallel()

	cause := &time.ParseError{Layout: "custom", Value: "invalid"}

	for _, tc := range []struct {
		name, expression string
		option           parser.Option
	}{
		{"function", "reject()", parser.WithCustomFunctions(map[string]filter.CustomFunction{
			"reject": func([]filter.Node) (filter.Node, error) { return nil, cause },
		})},
		{"timestamp", `timestamp("invalid")`, parser.WithTimestampParser(func(string) (time.Time, error) { return time.Time{}, cause })},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, err := parser.New(tc.option)
			require.NoError(t, err)
			_, err = p.Parse(t.Context(), tc.expression)
			require.ErrorIs(t, err, filter.ErrInvalidExpression)
			require.ErrorIs(t, err, cause)
			typed, ok := errors.AsType[*time.ParseError](err)
			require.True(t, ok)
			require.Same(t, cause, typed)
		})
	}
}
