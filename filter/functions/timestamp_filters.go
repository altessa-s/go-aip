// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package functions

import filter "github.com/altessa-s/go-aip/filter"

// Standard timestamp field names in the model AST. These are the
// targets [TimestampFilters] expands its custom functions to. If the
// underlying database column uses a different convention (e.g.
// snake_case created_at), translate at the translator layer via
// [filter.WithFieldMapping] — the AST stays canonical.
const (
	TimestampFieldCreatedAt = "createdAt"
	TimestampFieldUpdatedAt = "updatedAt"
	TimestampFieldDeletedAt = "deletedAt"
)

// CEL function names exposed by [TimestampFilters].
const (
	TimestampFuncCreateAfter  = "createAfter"
	TimestampFuncCreateBefore = "createBefore"
	TimestampFuncUpdateAfter  = "updateAfter"
	TimestampFuncUpdateBefore = "updateBefore"
	TimestampFuncDeleteAfter  = "deleteAfter"
	TimestampFuncDeleteBefore = "deleteBefore"
)

// TimestampFilters returns a ready-made set of [filter.CustomFunction]
// handlers that turn semantic timestamp predicates into ordinary
// comparisons against the standard createdAt/updatedAt/deletedAt
// fields:
//
//	createAfter(t)  → createdAt > t
//	createBefore(t) → createdAt < t
//	updateAfter(t)  → updatedAt > t
//	updateBefore(t) → updatedAt < t
//	deleteAfter(t)  → deletedAt > t
//	deleteBefore(t) → deletedAt < t
//
// Pass the returned map explicitly through parser.WithCustomFunctions.
// Use CompareField to choose application-specific field names.
func TimestampFilters() map[string]filter.CustomFunction {
	return map[string]filter.CustomFunction{
		TimestampFuncCreateAfter:  CompareField(TimestampFieldCreatedAt, filter.OpGT),
		TimestampFuncCreateBefore: CompareField(TimestampFieldCreatedAt, filter.OpLT),
		TimestampFuncUpdateAfter:  CompareField(TimestampFieldUpdatedAt, filter.OpGT),
		TimestampFuncUpdateBefore: CompareField(TimestampFieldUpdatedAt, filter.OpLT),
		TimestampFuncDeleteAfter:  CompareField(TimestampFieldDeletedAt, filter.OpGT),
		TimestampFuncDeleteBefore: CompareField(TimestampFieldDeletedAt, filter.OpLT),
	}
}

// SelectTimestampFilters builds a subset of [TimestampFilters]
// containing only the named entries. Names not part of the timestamp
// preset are silently skipped — the helper is for explicit subset
// selection, not validation. Pass the [TimestampFunc*] constants to
// avoid typos.
//
// Useful when a model lacks a soft delete or the API should expose
// only a partial range of predicates:
//
//	filterparser.WithCustomFunctions(functions.SelectTimestampFilters(
//	    functions.TimestampFuncCreateAfter,
//	    functions.TimestampFuncCreateBefore,
//	    functions.TimestampFuncUpdateAfter,
//	    functions.TimestampFuncUpdateBefore,
//	))
//
// Calling with no arguments returns an empty map. Duplicate names are
// deduplicated by the resulting map.
func SelectTimestampFilters(names ...string) map[string]filter.CustomFunction {
	all := TimestampFilters()

	out := make(map[string]filter.CustomFunction, len(names))
	for _, name := range names {
		if h, ok := all[name]; ok {
			out[name] = h
		}
	}

	return out
}
