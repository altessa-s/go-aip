// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package etag implements AIP-154 resource freshness validation with RFC 9110
// entity tags.
//
// A coreetag.Tag is an entity tag, strong ("value") or weak (W/"value"); it is the
// go-atlas-core entity tag, so the same values drive HTTP conditional
// requests. Parse decodes the etag a client sends: quoted and weak forms, and
// leniently a bare unquoted value as a strong tag (WithStrictQuotes refuses
// it). FromRevision issues the weak tag of an integer revision and Revision
// reads it back; FromMessage issues the content tag of a resource, the
// SHA-256 digest of its deterministic encoding without the etag field,
// optionally over a field subset or without OUTPUT_ONLY fields.
//
// A Precondition checks the etag an Update, Delete or custom method sends:
// an absent etag is no precondition unless WithRequired, a malformed one is a
// core/errors.FieldError (INVALID_ARGUMENT) and one that does not match the
// current tag is ErrMismatch, a core/errors.ErrConflict that the API maps to
// ABORTED. Matching is exact by default (the client echoes the tag it read),
// with RFC 9110 strong and weak comparison available. Get reads the etag
// field of a request and Stamp writes the current tag into a response.
//
// # Usage
//
//	pre := etag.NewPrecondition(etag.WithField("book.etag"))
//	cur := etag.FromRevision(stored.Revision)
//	if err := pre.Check(req.GetBook().GetEtag(), cur); err != nil {
//		return nil, err // FieldError or ErrMismatch (ABORTED)
//	}
//	// ... apply the update ...
//	if err := etag.Stamp(resp, etag.FromRevision(saved.Revision)); err != nil {
//		return nil, err
//	}
//
// See README.md for the complete guide, the AIP-154 conformance table and the
// errors.
package etag
