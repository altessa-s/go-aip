// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import "github.com/altessa-s/go-aip/filter"

// ValidateAST checks compatibility with this translator's policy and encoding.
// It renders and discards the result without contacting the backend.
func (t *FilterTranslator) ValidateAST(n filter.Node) error { _, _, err := t.Translate(n); return err }

// ValidateAST checks compatibility with this translator's schema, policy and encoding.
// It renders and discards the result without contacting the backend.
func (t *SchemaFilterTranslator) ValidateAST(n filter.Node) error {
	_, _, err := t.Translate(n)
	return err
}
