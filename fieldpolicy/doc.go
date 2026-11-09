// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package fieldpolicy holds the field-name policy shared by the request DSL packages (filter, orderby, projection): frozen authorization and
// storage-name mappings.
//
// AllowList supports exact names and wildcard prefixes. Mapping applies exact mappings first, then the longest matching prefix; Reach and
// Subtree expose mapped descendants for authorization checks. Constructors capture owned configuration, and accessors return detached values
// where mutation would affect policy.
//
// # Usage
//
//	allow := fieldpolicy.NewAllowList("createdAt", "address.*")
//	allow.Allows("address.city") // true
//	allow.Allows("address")      // false: ".*" matches subpaths only
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package fieldpolicy
