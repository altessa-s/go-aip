// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package orderby_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/internal/testhelpers"
	"github.com/altessa-s/go-aip/orderby"
)

func TestCheckNativeOrdering(t *testing.T) {
	t.Parallel()

	asc, desc := orderby.DirectionAscending, orderby.DirectionDescending
	spec := func(dir orderby.Direction, nulls orderby.NullOrder, collation string) orderby.Spec {
		return orderby.Spec{Keys: []orderby.Key{{Name: "a"}, {Name: "b", Direction: dir, Nulls: nulls, Collation: collation}}}
	}

	for _, tc := range []struct {
		name   string
		native func(orderby.Key) orderby.NullOrder
		spec   orderby.Spec
		ok     bool
	}{
		{name: "default", native: orderby.NullsSmallest, spec: spec(asc, orderby.NullsDefault, ""), ok: true},
		{name: "smallest asc first", native: orderby.NullsSmallest, spec: spec(asc, orderby.NullsFirst, ""), ok: true},
		{name: "smallest asc last", native: orderby.NullsSmallest, spec: spec(asc, orderby.NullsLast, "")},
		{name: "smallest desc last", native: orderby.NullsSmallest, spec: spec(desc, orderby.NullsLast, ""), ok: true},
		{name: "smallest desc first", native: orderby.NullsSmallest, spec: spec(desc, orderby.NullsFirst, "")},
		{name: "always last asc", native: orderby.NullsAlwaysLast, spec: spec(asc, orderby.NullsLast, ""), ok: true},
		{name: "always last desc", native: orderby.NullsAlwaysLast, spec: spec(desc, orderby.NullsLast, ""), ok: true},
		{name: "always last first", native: orderby.NullsAlwaysLast, spec: spec(desc, orderby.NullsFirst, "")},
		{name: "collation", native: orderby.NullsSmallest, spec: spec(asc, orderby.NullsDefault, "C")},
		{name: "no native", spec: spec(asc, orderby.NullsFirst, "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := orderby.CheckNativeOrdering(tc.spec, tc.native)
			if tc.ok {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, orderby.ErrUnsupportedOrdering)
		})
	}

	require.ErrorIs(t, orderby.CheckBasicOrdering(spec(asc, orderby.NullsFirst, "")), orderby.ErrUnsupportedOrdering)
	require.NoError(t, orderby.CheckBasicOrdering(spec(desc, orderby.NullsDefault, "")))
}

func TestTelemetry(t *testing.T) {
	t.Parallel()

	var disabled *orderby.Telemetry
	disabled.Start()()
	require.Nil(t, orderby.NewTelemetry(nil, "x"))

	m := testhelpers.NewRecordingMeter()
	tel := orderby.NewTelemetry(m, "sql")
	tel.Start()()
	tel.Start()()
	require.Equal(t, map[string]int{"orderby_sql_translate_duration_seconds": 2}, m.Counts())
}
