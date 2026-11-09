// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package sqlbase

import (
	"strings"

	"github.com/altessa-s/go-atlas-core/types/nilcheck"

	"github.com/altessa-s/go-aip/projection"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// ProjectionTranslator converts a [projection.Spec] into the SELECT column list of one
// SQL [ProjectionDialect]. It is safe for concurrent use after construction.
type ProjectionTranslator struct {
	config   *projection.TranslatorContext
	dialect  ProjectionDialect
	duration *projection.Telemetry
}

// NewProjectionTranslator creates a projection translator for dialect bound to policy,
// the client-facing [projection.Policy] shared by every backend of the
// resource. Storage options go through [WithProjectionOptions]. Beyond the
// construction errors of [NewProjectionContext], it returns
// [projection.ErrInvalidFieldPath] for the zero Dialect.
func NewProjectionTranslator(policy *projection.Policy, dialect ProjectionDialect, opts ...ProjectionOption) (*ProjectionTranslator, error) {
	if dialect.Identifiers.Quote == 0 {
		return nil, coreerrs.Wrap(projection.ErrInvalidFieldPath, "zero sqlbase.ProjectionDialect")
	}

	o := newProjectionOptions(opts...)

	ctx, err := NewProjectionContext(policy, dialect, o.projectionOptions...)
	if err != nil {
		return nil, err
	}

	name := dialect.Name
	if name == "" {
		name = unnamedDialect
	}

	return &ProjectionTranslator{config: ctx, dialect: dialect, duration: projection.NewTelemetry(o.collector, name)}, nil
}

// Translate resolves spec against the policy and renders the quoted column
// list, without the SELECT keyword, in lexical order of the column names.
// A selection of every column renders "", for which the caller writes `*`;
// on ClickHouse an exclusion renders `* EXCEPT (col, …)`.
func (t *ProjectionTranslator) Translate(spec projection.Spec) (string, error) {
	defer t.duration.Start()()

	return TranslateProjection(t.config, t.dialect, spec)
}

// TranslateView resolves spec and view against the policy (see
// [projection.Policy.Select]) and renders the quoted column list like
// [ProjectionTranslator.Translate].
func (t *ProjectionTranslator) TranslateView(spec projection.Spec, view projection.View) (string, error) {
	defer t.duration.Start()()

	return TranslateProjectionView(t.config, t.dialect, spec, view)
}

// columnSizeHint is the expected rendered size of one column: a quoted
// identifier of a dozen characters and a separator.
const columnSizeHint = 16

// NewProjectionContext binds policy to the storage options of a SQL backend. A policy
// whose "every authorized field" — the projection of an omitted read mask,
// "*" and FULL — would resolve to "everything except the denied fields" is
// rejected with [projection.ErrAllFieldsUnresolvable] unless style is a
// [ProjectionDialect] with Except set (ClickHouse), which renders it as `* EXCEPT (col, …)` when every
// denied storage path is a top-level column. Otherwise the caller must list
// the allowed fields with [projection.WithAllowedFields]. The column list of
// every view the policy serves is rendered once in style, so a view that maps
// to a non-column fails here rather than on every request.
func NewProjectionContext(policy *projection.Policy, style ColumnResolver, opts ...projection.TranslatorOption) (*projection.TranslatorContext, error) {
	if nilcheck.IsNil(style) {
		return nil, projection.ErrInvalidFieldPath
	}

	ctx, err := projection.NewTranslatorContext(policy, opts...)
	if err != nil {
		return nil, err
	}

	for _, view := range policy.Views() {
		sel, err := ctx.ViewSelection(view)
		if err != nil {
			return nil, err
		}

		if len(sel.Exclude) > 0 {
			if _, err := renderExcept(style, sel.Exclude); err != nil {
				return nil, coreerrs.Wrapf(projection.ErrAllFieldsUnresolvable,
					"cannot exclude denied columns %v: %w", sel.Exclude, err)
			}

			continue
		}

		if _, err := renderProjection(style, sel.Include); err != nil {
			return nil, err
		}
	}

	return ctx, nil
}

// TranslateProjection resolves a read mask against bound and renders the column list of
// a SELECT — `col, …` without the keyword — through style, which is a
// [ProjectionDialect] or an application resolver. An omitted mask and "*" render every
// authorized column (AIP-157).
// Columns appear in lexical order of their storage name, without aliases,
// so callers scan rows by column name (rows.Columns) rather than position.
// Every storage name must resolve through style; anything it rejects fails
// with [projection.ErrInvalidFieldPath] rather than being spliced in. Under a
// [QualifiedNamesDialect] a dotted path of the read mask must be declared
// ([projection.TranslatorContext.DeclaresField]) or it fails with
// [projection.ErrFieldNotAllowed]. A selection
// of every field renders "", for which the caller writes `*`.
func TranslateProjection(bound *projection.TranslatorContext, style ColumnResolver, spec projection.Spec) (string, error) {
	if bound == nil || nilcheck.IsNil(style) {
		return "", projection.ErrInvalidFieldPath
	}

	sel, err := bound.Resolve(spec)
	if err != nil {
		return "", err
	}

	if err := checkDeclaredPaths(bound, style, spec); err != nil {
		return "", err
	}

	return renderSelection(style, sel)
}

// TranslateProjectionView is [TranslateProjection] for a method with a resource view: a present
// read mask decides the columns, an omitted one defers to view, and
// [projection.ViewUnspecified] to the policy's default view (see
// [projection.Policy.Select]).
func TranslateProjectionView(bound *projection.TranslatorContext, style ColumnResolver, spec projection.Spec, view projection.View) (string, error) {
	if bound == nil || nilcheck.IsNil(style) {
		return "", projection.ErrInvalidFieldPath
	}

	sel, err := bound.ResolveView(spec, view)
	if err != nil {
		return "", err
	}

	if err := checkDeclaredPaths(bound, style, spec); err != nil {
		return "", err
	}

	return renderSelection(style, sel)
}

// checkDeclaredPaths refuses, with [projection.ErrFieldNotAllowed], a dotted path of a client read mask the policy does not
// declare when style reads a dot as a table qualifier (see checkDeclared). Views and every authorized field are configured
// by the service and need no check.
func checkDeclaredPaths(bound *projection.TranslatorContext, style ColumnResolver, spec projection.Spec) error {
	if spec.Mode != projection.ModeDefault {
		return nil
	}

	for _, p := range spec.Paths {
		if err := checkDeclared(style, p, bound.DeclaresField); err != nil {
			return coreerrs.Wrapf(projection.ErrFieldNotAllowed, "%s: %w", p, err)
		}
	}

	return nil
}

// renderSelection renders an inclusion selection, and an exclusion on a
// dialect that can express it; SQL cannot express ModeNone.
func renderSelection(style ColumnResolver, sel projection.Selection) (string, error) {
	switch {
	case sel.Mode == projection.ModeNone:
		return "", coreerrs.Wrap(projection.ErrUnsupportedSelection, "SQL cannot select no column")
	case len(sel.Exclude) > 0:
		return renderExcept(style, sel.Exclude)
	default:
		return renderProjection(style, sel.Include)
	}
}

// renderExcept renders `* EXCEPT (col, …)` for a dialect that supports it.
// Only top-level columns can be excluded: `*` never expands a subcolumn, so
// excluding `a.b` would still return it inside `a`.
func renderExcept(style ColumnResolver, columns []string) (string, error) {
	d, ok := style.(ProjectionDialect)
	if !ok || !d.Except {
		return "", coreerrs.Wrap(projection.ErrUnsupportedSelection, "the SQL backend has no exclusion projection")
	}

	for _, c := range columns {
		if strings.Contains(c, ".") {
			return "", coreerrs.Wrapf(projection.ErrUnsupportedSelection, "%s is a subcolumn, which * EXCEPT cannot exclude", c)
		}
	}

	cols, err := renderProjection(style, columns)
	if err != nil {
		return "", err
	}

	return "* EXCEPT (" + cols + ")", nil
}

// renderProjection quotes each column name and joins them with ", ".
func renderProjection(style ColumnResolver, columns []string) (string, error) {
	var b strings.Builder
	b.Grow(len(columns) * columnSizeHint)

	for i, name := range columns {
		col, err := style.Table(name)
		if err != nil {
			return "", coreerrs.Wrapf(projection.ErrInvalidFieldPath, "%s is not a SQL column name: %w", name, err)
		}

		if i > 0 {
			b.WriteString(", ")
		}

		b.WriteString(col)
	}

	return b.String(), nil
}
