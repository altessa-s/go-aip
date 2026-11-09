// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package projection parses a client-supplied field list — the AIP-157 partial-response "fields" parameter or an AIP-161 read mask —
// and translates it into a database-specific projection, so unrequested fields are never read from storage instead of being trimmed
// off the response afterwards. It also serves AIP-157 resource views (View).
//
// The package has three layers, each configured by its own options:
//
//   - Parser (ParserOption): NewParser parses a field list into a Spec. Its options cover the syntax and resource limits of the text:
//     the path grammar, duplicate handling, lengths, path count and caching.
//   - Policy (PolicyOption): NewPolicy states which fields a client may request, in API names and independent of any backend: the
//     allow-list, a shared field access policy, denied API fields, resource views, the API path grammar and the untrusted-input
//     guard. Policy.Validate applies it to a read mask, Policy.Select to a read mask and a view. Build one policy per resource and
//     share it between backends.
//   - Translator (TranslatorOption): a backend translator binds the policy to storage options — exact and prefix field mapping,
//     denied storage fields, required storage fields and the storage path grammar — and renders only projections the policy
//     validated.
//
// # Selection rule
//
// An omitted read mask returns every authorized field (AIP-157): the allow-list minus the denied fields. A method with a view
// parameter calls Policy.Select (or a translator's TranslateView): a present mask, including "*", decides the fields; an omitted
// one defers to the view, and ViewUnspecified to the policy's default view (WithDefaultView, FULL unless set; AIP-157 recommends
// BASIC for List methods). An index path such as "items.0" fails with ErrUnsupportedPath, which maps to INVALID_ARGUMENT as AIP-161
// requires; so do the other request-time errors.
//
// # Usage
//
//	p, err := projection.NewParser()
//	if err != nil { return err }
//	spec, err := p.Parse(ctx, "name, address.city")
//	policy, err := projection.NewPolicy(projection.WithUntrustedInput(true), projection.WithAllowedFields("name", "address.*"))
//	fields, err := policy.Validate(spec) // INVALID_ARGUMENT on error
//	fields, err = policy.Select(spec, projection.ViewBasic)
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package projection
