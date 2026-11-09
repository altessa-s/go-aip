// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

// ProjectionDialect describes how one SQL backend renders a SELECT column list: identifier quoting, limits and whether
// it can select every column but some. The built-in dialects are postgres.ProjectionDialect, mariadb.ProjectionDialect
// and clickhouse.ProjectionDialect of the backend packages; another database describes itself with the same fields.
// The zero value rejects every column. A ProjectionDialect is a [ColumnResolver], so a custom resolver can delegate to
// it.
type ProjectionDialect struct {
	// Identifiers validates and quotes column references.
	Identifiers Identifiers
	// Name labels the translation metrics of the dialect, as in projection_<name>_translate_duration_seconds; empty
	// means "sql".
	Name string
	// Except reports that the dialect renders an exclusion — every column but the denied ones — as
	// `* EXCEPT (col, …)`.
	Except bool
	// Qualified reports that a dot in a column name may separate a table qualifier, so a dotted client path must be
	// declared ([QualifiedNamesDialect]).
	Qualified bool
}

// QualifiesNames reports whether a dot in a column name may separate a table qualifier, as in PostgreSQL, MariaDB and
// ClickHouse: then
// [TranslateProjection] refuses a dotted client path the policy does not declare
// ([projection.TranslatorContext.DeclaresField]).
func (d ProjectionDialect) QualifiesNames() bool { return d.Qualified }

// Table validates name as a plain column or qualified column reference of the
// dialect and quotes each part on its own.
func (d ProjectionDialect) Table(name string) (string, error) {
	return d.Identifiers.Column(name)
}
