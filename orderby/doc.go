// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package orderby parses the AIP-132 order_by DSL into a flat AST and translates it to database-specific sort representations.
//
// The package has three layers, each configured by its own options:
//
//   - Parser (ParserOption): NewParser parses comma-separated field paths and optional asc/desc directions into a Spec. Its options cover
//     the syntax and resource limits of the text: lengths, key count, caching, duplicate handling, direction case and numeric path
//     segments.
//   - Policy (PolicyOption): NewPolicy states what a client may sort on, in API names and independent of any backend: the allow-list,
//     a shared field access policy, denied API fields, the default order and the untrusted-input guard. Policy.Validate applies it to a
//     Spec. Build one policy per resource and share it between backends.
//   - Translator (TranslatorOption): a backend translator binds the policy to storage options — exact and prefix field mapping, a
//     KeyResolver, denied storage fields and storage tie-breakers — and renders only orders the policy validated.
//
// # Usage
//
//	p, err := orderby.NewParser()
//	if err != nil { return err }
//	spec, err := p.Parse(ctx, "createdAt desc, name")
//	policy, err := orderby.NewPolicy(orderby.WithUntrustedInput(true), orderby.WithAllowedFields("createdAt", "name"))
//	spec, err = policy.Validate(spec) // INVALID_ARGUMENT on error
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package orderby
