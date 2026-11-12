// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhouse

import (
	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/projection"
)

// ProjectionDialect returns the ClickHouse SELECT-list dialect: backtick-quoted parts of at most 255 bytes, metrics
// labeled "clickhouse", and an exclusion — every column but the denied ones — rendered as `* EXCEPT (col, …)`. A dotted
// client path must be declared ([sqlbase.QualifiedNamesDialect]), since its dot may name a joined table. Pass it to
// [sqlbase.NewProjectionTranslator] or delegate an application [sqlbase.ColumnResolver] to it.
func ProjectionDialect() sqlbase.ProjectionDialect {
	return sqlbase.ProjectionDialect{Identifiers: identifiers, Name: dialectName, Except: true, Qualified: true}
}

// NewProjectionTranslator creates a SELECT column-list translator for
// ClickHouse bound to policy, the client-facing [projection.Policy] shared by
// every backend of the resource. It is [sqlbase.NewProjectionTranslator] with
// [ProjectionDialect] fixed: backtick-quoted column parts of at most 255
// bytes, and an exclusion rendered as `* EXCEPT (col, …)`.
// [sqlbase.WithProjectionOptions] sets mapping, denied storage fields and
// required fields; [sqlbase.WithProjectionCollector] records metrics.
func NewProjectionTranslator(policy *projection.Policy, opts ...sqlbase.ProjectionOption) (*sqlbase.ProjectionTranslator, error) {
	return sqlbase.NewProjectionTranslator(policy, ProjectionDialect(), opts...)
}
