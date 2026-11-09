// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package resourcename implements the resource names of AIP-122, the resource
// types and patterns of AIP-123 and the "-" wildcard reads across collections
// of AIP-159.
//
// A [Pattern] such as "publishers/{publisher}/books/{book}" is compiled once
// by [ParsePattern], which enforces the AIP-122/123 grammar (camelCase
// collection identifiers, snake_case variables, no repeats); it then matches
// names without regular expressions or allocations, formats names from
// values and derives the parent pattern. A [Resource] is a resource type
// with one or more patterns, the first matching one winning; [ResourceOf]
// reads it from the google.api.resource annotation of a message.
//
// A [Validator] holds the registered types and the policy for client names:
// the [IDValidator] of resource ID segments ([PermissiveID] by default,
// [StrictID] for the RFC-1034 recommendation), the length and segment limits
// and whether full resource names ("//service/relative") are accepted.
// [Validator.ValidateRequest] checks the google.api.resource_reference fields
// of a request (type and child_type) and the name of a resource it carries;
// [WithWildcard] opens the "-" wildcard for a List parent. Client errors are
// [coreerrs.FieldError] values on the request field, wrapping a *[NameError].
//
// # Usage
//
//	v := resourcename.NewValidator(resourcename.WithIDValidator(resourcename.StrictID))
//	if err := v.RegisterFile(librarypb.File_library_v1_library_proto); err != nil { return err }
//
//	// Get: name must be a Book, no wildcard.
//	if err := v.ValidateRequest(req); err != nil { return err }
//	// List across publishers: parent "publishers/-" is accepted.
//	if err := v.ValidateRequest(listReq, resourcename.WithWildcard(resourcename.WildcardAny)); err != nil { return err }
//
//	book := resourcename.MustParsePattern("publishers/{publisher}/books/{book}")
//	vals, err := book.Match("publishers/acme/books/les-miserables") // {"publisher": "acme", "book": "les-miserables"}
//	name, err := book.Format(map[string]string{"publisher": "acme", "book": "dune"})
//
// See README.md for the complete guide, the conformance table, errors and
// limits.
package resourcename
