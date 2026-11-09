// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package functions

import "github.com/altessa-s/go-aip/filter"

//go:generate optgen generate

type options struct {
	// WithOperator chooses the text-search predicate: matches, contains, startsWith, or endsWith.
	operator filter.Operator `optgen:"default=filter.OpMatches"`
	// WithCaseInsensitive controls the matches preset's leading (?i). Other operators keep backend string semantics.
	caseInsensitive bool `optgen:"default=true" optval:"param"`
}
