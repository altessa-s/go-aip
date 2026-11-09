// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package telemetry_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/altessa-s/go-aip/internal/telemetry"
	"github.com/altessa-s/go-aip/internal/testhelpers"
)

func TestNewParser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		subsystem, noun string
		wantHelp        map[string]string
	}{
		{"filter", "CEL expression", map[string]string{
			"filter_parse_duration_seconds": "Duration of CEL expression parse operations in seconds.",
			"filter_parse_errors_total":     "Total number of CEL expression parse failures.",
		}},
		{"orderby", "order_by", map[string]string{
			"orderby_parse_duration_seconds": "Duration of order_by parse operations in seconds.",
			"orderby_parse_errors_total":     "Total number of order_by parse failures.",
		}},
		{"projection", "fields", map[string]string{
			"projection_parse_duration_seconds": "Duration of fields parse operations in seconds.",
			"projection_parse_errors_total":     "Total number of fields parse failures.",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.subsystem, func(t *testing.T) {
			t.Parallel()

			m := testhelpers.NewRecordingMeter()
			p := telemetry.NewParser(m, tt.subsystem, tt.noun)
			p.Duration.Start()()
			p.Errors.Inc()
			p.Errors.Inc()

			require.Equal(t, tt.wantHelp, m.Help())
			require.Equal(t, map[string]int{
				tt.subsystem + "_parse_duration_seconds": 1,
				tt.subsystem + "_parse_errors_total":     2,
			}, m.Counts())
		})
	}
}

func TestNewParserNilMeter(t *testing.T) {
	t.Parallel()

	p := telemetry.NewParser(nil, "filter", "CEL expression")
	require.NotNil(t, p.Duration)
	require.NotNil(t, p.Errors)
	p.Duration.Start()()
	p.Errors.Inc()
}
