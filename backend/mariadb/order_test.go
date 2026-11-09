// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/metrics"
	"github.com/altessa-s/go-aip/orderby"
	"github.com/altessa-s/go-aip/orderby/orderbytest"
)

func TestNewOrderTranslator(t *testing.T) {
	t.Parallel()

	tr, err := mariadb.NewOrderTranslator(orderbytest.MustPolicy(t, orderby.WithAllowedFields("createTime", "t.b", "a")),
		sqlbase.WithOrderOptions(orderby.WithFieldMapping(map[string]string{"createTime": "created_at"})),
		sqlbase.WithOrderCollector(metrics.Noop()))
	require.NoError(t, err)

	for _, tc := range []struct {
		name, in, want string
	}{
		{name: "mapped and qualified", in: "createTime desc, t.b", want: "`created_at` DESC, `t`.`b` ASC"},
		{name: "default direction", in: "a", want: "`a` ASC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := tr.Translate(orderbytest.MustParseOrderBy(t, tc.in))
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}

	_, err = mariadb.NewOrderTranslator(nil)
	require.ErrorIs(t, err, orderby.ErrPolicyRequired)
}
