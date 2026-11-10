// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package extract

//go:generate optgen generate --type=extractOptions --option-type=Option --output=options_gen.go

// extractOptions configures the lookup performed by [UpdateMask],
// [ReadMask], [SetUpdateMask], and [IsCanonicalUpdateRequest].
type extractOptions struct {
	maskField     string
	resourceField string
}
