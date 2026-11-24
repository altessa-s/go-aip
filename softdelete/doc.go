// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package softdelete implements AIP-164 soft delete and undelete and AIP-165
// criteria-based delete (Purge) for protobuf resources.
//
// A [Policy] reads the AIP-148 delete_time and purge_time fields of a resource
// through its descriptor. Delete stamps them (purge_time is delete_time plus
// the retention), Undelete clears them, CheckGet returns a soft-deleted
// resource unless configured otherwise, and Visible, Restriction and Restrict
// apply the show_deleted rule of List to items in memory, as a
// listing.Request restriction and to an AIP-160 filter AST. Policy.Cutoff supplies
// a separate List predicate for resources with a finite restore window.
// Errors match the go-atlas-core canonical errors of their AIP status codes:
// NOT_FOUND for a missing, purged or (on Delete) already deleted resource and
// ALREADY_EXISTS for Undelete of a resource that is not deleted.
//
// A [Purger] validates a Purge request (a required filter parsed and validated
// under a filter policy, an optional wildcard, limits), answers a dry run with
// purgeCount and purgeSample and delegates the deletion to a [Store].
//
// # Usage
//
//	policy := softdelete.New(softdelete.WithRetention(7 * 24 * time.Hour))
//	changed, err := policy.Delete(stored, softdelete.WithPrecondition(checkEtag))
//	err = policy.Undelete(stored, checkEtag)
//	lreq.Restriction = policy.Restriction(req.GetShowDeleted())
//
//	purger, err := softdelete.NewPurger(filterPolicy, filterParser, store)
//	resp, err := purger.Purge(ctx, softdelete.RequestFromProto(req))
//
// See README.md for the complete guide, limits, errors, and the AIP
// conformance table.
package softdelete
