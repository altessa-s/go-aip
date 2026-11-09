// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package meili

import (
	"github.com/altessa-s/go-aip/metrics"
	"github.com/altessa-s/go-aip/orderby"
)

//go:generate optgen generate --type=orderOptions --option-type=OrderOption --output=order_options_gen.go

type orderOptions struct {
	// WithOrderOptions binds the shared policy to the storage names of this backend: [orderby.WithFieldMapping],
	// [orderby.WithKeyResolver], [orderby.WithDeniedStorageFields] and [orderby.WithTiebreaker].
	orderOptions []orderby.TranslatorOption `opt:"OrderOptions" optgen:"allownull" optval:"clone"`
	// WithOrderCollector records the translation duration as the histogram orderby_meili_translate_duration_seconds. Without it
	// nothing is recorded.
	collector metrics.Meter `opt:"OrderCollector" optgen:"notnil"`
}
