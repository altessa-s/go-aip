// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

//go:generate optgen generate --type=countOptions --option-type=CountOption --option-prefix=Count --output=list_count_options_gen.go

import "time"

// DefaultCountLimit is the largest total [ListEngine.BoundedCount] reports exactly by default.
const DefaultCountLimit = 1000

type countOptions struct {
	// limit is the largest total reported exactly; a larger collection reports an estimate.
	limit int `optgen:"default=DefaultCountLimit"`
	// probeTime bounds the exact count, which reports an estimate when it expires. Zero leaves it to the engine's time cap; a
	// positive value requires a dialect implementing ListCountDialect.
	probeTime time.Duration `optval:"nonpositive"`
}
