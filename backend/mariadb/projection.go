// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb

import (
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/projection"
)

// ProjectionOption configures [NewProjectionTranslator]: [sqlbase.WithProjectionOptions] and
// [sqlbase.WithProjectionCollector].
type ProjectionOption = sqlbase.ProjectionOption

// ProjectionDialect returns the MariaDB and MySQL SELECT-list dialect: backtick-quoted `col` or `table.col` parts of at
// most 64 bytes, metrics labeled "mariadb", and no exclusion projection. A dotted client path must be declared
// ([sqlbase.QualifiedNamesDialect]). Pass it to [sqlbase.NewProjectionTranslator] or delegate an application
// [sqlbase.ColumnResolver] to it.
func ProjectionDialect() sqlbase.ProjectionDialect {
	return sqlbase.ProjectionDialect{Identifiers: identifiers, Name: dialectName, Qualified: true}
}

// NewProjectionTranslator creates a SELECT column-list translator with the MariaDB dialect fixed
// ([ProjectionDialect], backtick-quoted columns). It is [sqlbase.NewProjectionTranslator] without
// the dialect argument; use that one directly for an application [sqlbase.ColumnResolver]. Returns
// [projection.ErrPolicyRequired] for a nil policy. A policy whose
// "every authorized field" would resolve to "everything except the denied fields" fails with
// [projection.ErrAllFieldsUnresolvable]: list the allowed fields with [projection.WithAllowedFields].
func NewProjectionTranslator(policy *projection.Policy, opts ...ProjectionOption) (*sqlbase.ProjectionTranslator, error) {
	return sqlbase.NewProjectionTranslator(policy, ProjectionDialect(), opts...)
}
