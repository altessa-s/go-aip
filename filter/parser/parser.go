// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package parser

import (
	"context"
	"time"

	"cel.dev/cel-go/cel"

	"github.com/altessa-s/go-atlas-core/runtime/panics"

	"github.com/altessa-s/go-aip/internal/telemetry"

	filter "github.com/altessa-s/go-aip/filter"
	coremaps "github.com/altessa-s/go-atlas-core/collections/maps"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
	corestrings "github.com/altessa-s/go-atlas-core/text/strings"
)

// Parser parses CEL expressions into filter AST nodes.
type Parser struct {
	orderedEnums        bool
	env                 *cel.Env
	timestampParser     func(string) (time.Time, error)
	cache               *astCache // nil with WithNoCache
	maxExpressionLength int
	metrics             telemetry.Parser
	customFunctions     *coremaps.ImmutableMap[string, filter.CustomFunction]
	allowedFunctions    *coremaps.ImmutableMap[string, struct{}]
	enumNames           *coremaps.ImmutableMap[string, *enumVocabulary] // nil without WithEnumNames
}

// New creates a new CEL expression parser.
func New(opts ...Option) (*Parser, error) {
	cfg := newOptions(opts...)
	if cfg.timestampParser == nil {
		return nil, coreerrs.Wrap(filter.ErrInvalidOptions, "nil timestamp parser")
	}

	if err := validateCustomFunctions(cfg.customFunctions, cfg.allowBuiltinOverride); err != nil {
		return nil, err
	}

	var enumNames *coremaps.ImmutableMap[string, *enumVocabulary]
	if len(cfg.enumNames) > 0 {
		var err error
		if enumNames, err = compileEnumNames(cfg.enumNames); err != nil {
			return nil, err
		}
	}

	env, err := cel.NewEnv(append([]cel.EnvOption{cel.EnableMacroCallTracking(), cel.ParserErrorRecoveryLimit(errorRecoveryLimit)},
		cfg.environmentOptions...)...)
	if err != nil {
		return nil, coreerrs.Wrapf(filter.ErrParseFailed, "%w", err)
	}

	p := &Parser{
		env:                 env,
		orderedEnums:        cfg.orderedEnums,
		timestampParser:     cfg.timestampParser,
		maxExpressionLength: cfg.maxExpressionLength,
		metrics:             telemetry.NewParser(cfg.collector, "filter", "CEL expression"),
		customFunctions:     coremaps.NewImmutableMap(cfg.customFunctions),
		allowedFunctions:    coremaps.NewImmutableMap(cfg.allowedFunctions),
		enumNames:           enumNames,
	}

	if !cfg.noCache {
		if p.cache, err = newASTCache(cfg.cacheSize, cfg.cacheBytes); err != nil {
			return nil, coreerrs.Wrapf(filter.ErrInvalidOptions, "%w", err)
		}
	}

	return p, nil
}

// Parse parses a CEL expression and returns a caller-owned AST. Mutating it
// cannot change future parses or another caller's result. A cache hit is
// copied into one backing array per node kind, so it costs a handful of
// allocations; [Parser.ParseShared] returns the cached tree itself without
// copying, for callers that only read it.
func (p *Parser) Parse(ctx context.Context, expression string) (filter.Node, error) {
	if corestrings.IsEmpty(expression) {
		return nil, filter.ErrEmptyExpression
	}

	if len(expression) > p.maxExpressionLength {
		p.metrics.Errors.Inc()
		return nil, coreerrs.Wrapf(filter.ErrExpressionTooLong, "length %d exceeds maximum %d", len(expression), p.maxExpressionLength)
	}

	stop := p.metrics.Duration.Start()
	defer stop()

	if p.cache == nil {
		node, err := p.parseInternal(expression)
		if err != nil {
			p.metrics.Errors.Inc()
			return nil, err
		}

		return node, nil
	}

	hit, ok := p.cache.get(expression)
	if !ok {
		node, err := p.cache.getOrParse(ctx, expression, func() (filter.Node, error) {
			return p.parseInternal(expression)
		})
		if err != nil {
			p.metrics.Errors.Inc()
			return nil, err
		}

		// The AST may not have been cached (over the per-entry share of the byte budget) or cached by another caller:
		// copy it either way, so the result never aliases a cached tree.
		return filter.Clone(node)
	}

	if hit.err != nil {
		p.metrics.Errors.Inc()
		return nil, hit.err
	}

	if hit.plan != nil {
		return hit.plan.clone(hit.node), nil
	}

	return filter.Clone(hit.node)
}

// ParseShared parses a CEL expression and returns a shared (read-only) AST
// from cache when available. Callers MUST NOT mutate the returned Node tree:
// mutating it corrupts subsequent reads from the parser's cache.
// For a safe, caller-owned AST copy, use [Parser.Parse].
func (p *Parser) ParseShared(ctx context.Context, expression string) (filter.Node, error) {
	if corestrings.IsEmpty(expression) {
		return nil, filter.ErrEmptyExpression
	}

	if len(expression) > p.maxExpressionLength {
		p.metrics.Errors.Inc()
		return nil, coreerrs.Wrapf(filter.ErrExpressionTooLong, "length %d exceeds maximum %d", len(expression), p.maxExpressionLength)
	}

	stop := p.metrics.Duration.Start()
	defer stop()

	node, err := p.parseCached(ctx, expression)
	if err != nil {
		p.metrics.Errors.Inc()
		return nil, err
	}

	return node, nil
}

// parseCached parses expression through the cache when the parser has one: a hit returns the cached AST, or the cached
// error of an expression that failed to parse before.
func (p *Parser) parseCached(ctx context.Context, expression string) (filter.Node, error) {
	if p.cache != nil {
		// Probe first so a hit does not allocate the compute closure.
		if hit, ok := p.cache.get(expression); ok {
			return hit.node, hit.err
		}

		return p.cache.getOrParse(ctx, expression, func() (filter.Node, error) {
			return p.parseInternal(expression)
		})
	}

	return p.parseInternal(expression)
}

// MustParse parses a CEL expression and panics if parsing fails.
// Use this for compile-time expressions to catch errors at startup.
func (p *Parser) MustParse(expression string) filter.Node {
	return panics.MustResult(p.Parse(context.Background(), expression))
}

// MustParseShared parses a CEL expression and returns a shared (read-only) AST,
// panicking if parsing fails. Callers MUST NOT mutate the returned Node tree.
// Use this for compile-time expressions to catch errors at startup.
func (p *Parser) MustParseShared(expression string) filter.Node {
	return panics.MustResult(p.ParseShared(context.Background(), expression))
}

// parseInternal performs the actual parsing without caching.
func (p *Parser) parseInternal(expression string) (filter.Node, error) {
	ast, issues := p.env.Parse(expression)
	if errs := issues.Errors(); len(errs) > 0 {
		return nil, parseError(expression, errs)
	}

	parsedExpr, err := cel.AstToParsedExpr(ast)
	if err != nil {
		return nil, coreerrs.Wrapf(filter.ErrParseFailed, "%w", err)
	}

	c := &conversion{Parser: p, source: []rune(expression), positions: parsedExpr.GetSourceInfo().GetPositions()}

	node, err := c.convertExpr(parsedExpr.GetExpr())
	if err != nil {
		return nil, err
	}

	if p.enumNames != nil {
		// The tree is fresh (custom-function results are copies), so it is rewritten in place before it is cached.
		if err = p.resolveEnumNames(node); err != nil {
			return nil, err
		}
	}

	return node, nil
}
