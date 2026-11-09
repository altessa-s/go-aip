// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"github.com/altessa-s/go-aip/metrics"
	"github.com/altessa-s/go-aip/orderby"
)

//go:generate optgen generate --type=orderOptions --option-type=OrderOption --output=order_options_gen.go

type orderOptions struct {
	// WithOrderOptions binds the shared policy to the storage names of this backend: [orderby.WithFieldMapping],
	// [orderby.WithKeyResolver], [orderby.WithDeniedStorageFields] and [orderby.WithTiebreaker].
	orderOptions []orderby.TranslatorOption `opt:"OrderOptions" optgen:"allownull" optval:"clone"`
	// WithOrderCollector records the translation duration as the histogram orderby_<name>_translate_duration_seconds, where
	// name is the [OrderDialect] Name (orderby_postgres_…, orderby_mariadb_…, orderby_clickhouse_…; "sql" when empty).
	// Without it nothing is recorded.
	collector metrics.Meter `opt:"OrderCollector" optgen:"notnil"`
}
