// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import "github.com/altessa-s/go-aip/filter"

// ValidateAST checks compatibility with this evaluator's policy and profile, as [Evaluator.Validate] does, so an Evaluator
// satisfies the Compatibility interface of the filter translators. It reads no data.
func (e *Evaluator) ValidateAST(n filter.Node) error { return e.Validate(n) }
