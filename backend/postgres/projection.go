// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgres

import (
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/projection"
)

// ProjectionDialect returns the PostgreSQL SELECT-list dialect: double-quoted `col` or `table.col` parts of at most 63
// bytes, metrics labeled "postgres", and no exclusion projection. A dotted client path must be declared
// ([sqlbase.QualifiedNamesDialect]). Pass it to [sqlbase.NewProjectionTranslator] or delegate an application
// [sqlbase.ColumnResolver] to it.
func ProjectionDialect() sqlbase.ProjectionDialect {
	return sqlbase.ProjectionDialect{Identifiers: identifiers, Name: dialectName, Qualified: true}
}

// NewProjectionTranslator creates a SELECT column-list translator for
// PostgreSQL bound to policy, the client-facing [projection.Policy] shared by
// every backend of the resource. It is [sqlbase.NewProjectionTranslator] with
// [ProjectionDialect] fixed: double-quoted column parts of at most 63 bytes.
// PostgreSQL has no exclusion projection: a policy whose every authorized
// field would mean "every column but the denied ones" fails with
// [projection.ErrAllFieldsUnresolvable]; list the allowed fields with
// [projection.WithAllowedFields]. [sqlbase.WithProjectionOptions] sets mapping,
// denied storage fields and required fields; [sqlbase.WithProjectionCollector]
// records metrics.
func NewProjectionTranslator(policy *projection.Policy, opts ...sqlbase.ProjectionOption) (*sqlbase.ProjectionTranslator, error) {
	return sqlbase.NewProjectionTranslator(policy, ProjectionDialect(), opts...)
}
