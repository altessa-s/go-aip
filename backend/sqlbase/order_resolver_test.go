// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
)

type orderExpressionResolver struct{}

func (orderExpressionResolver) Table(name string) (string, error) {
	if name == "tier" {
		return "metadata->>'tier'", nil
	}

	return "", orderby.ErrInvalidFieldPath
}

func TestOrderExternalResolver(t *testing.T) {
	t.Parallel()

	c, err := orderby.NewTranslatorContext(orderbytest.MustPolicy(t))
	require.NoError(t, err)
	out, err := sqlbase.TranslateOrder(c, orderExpressionResolver{}, orderby.Spec{Keys: []orderby.Key{{Name: "tier"}}})
	require.NoError(t, err)
	require.Equal(t, "metadata->>'tier' ASC", out)
}
