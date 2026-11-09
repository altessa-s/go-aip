// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package listing defines a backend-neutral List request contract with immutable schema metadata and configurable execution profiles.
//
// NewSchema(fields, patterns, profile) separates immutable field and order metadata from execution policy. Pass nil for the portable proto3
// preset or reuse NewProfile(opts...); MustSchema has the same arguments and panics on invalid metadata. Options are ProfileOption values.
// Field lookups return detached enum metadata.
//
// Request.Restriction is a server predicate (an AIP-164 show_deleted rule, a tenant scope) that Compile ANDs onto the client's filter after
// the default equalities; every engine applies it and binds it into the page token.
//
// # Usage
//
//	schema := listing.MustSchema(fields, []string{"createTime desc", "name"}, nil)
//	list, err := memory.NewListEngine(codec, schema, idOf, valuesOf)
//	lreq := listing.RequestFromProto(req)
//	lreq.Restriction = softDelete.Restriction(req.GetShowDeleted())
//	page, err := list.List(ctx, method, lreq, items)
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package listing
