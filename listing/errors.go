// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing

import (
	"errors"
	"fmt"
)

// ErrInvalidSchema reports a schema declaration its constructor refuses — or
// an engine refuses to serve: a configuration bug surfaced at process start,
// never a client error.
var ErrInvalidSchema = errors.New("listing: invalid schema")

// ErrUnreadableSortField reports an order over a field the client does not
// read: a [Field] declared Hidden in a sort pattern (with [ErrInvalidSchema],
// at start-up) or an order key a request's ReadMask omits (an InvalidArgument
// field error on orderBy). A page token carries the sort values of the last
// item of a page, readable by whoever holds it, so sorting on such a field
// would disclose its values. [WithUnreadableSortAllowed] lifts the check.
var ErrUnreadableSortField = errors.New("listing: sort field is not readable by the client")

// ErrWorkBudget reports a request whose evaluation exhausted an engine's scan,
// materialization, or work limit. Engines wrap it in their own sentinels; an
// error mapper treats it as RESOURCE_EXHAUSTED.
var ErrWorkBudget = errors.New("listing: work budget exceeded")

// ErrInvalidRestriction reports a [Request] Restriction the schema's filter
// policy refuses (an undeclared field, a literal of the wrong kind, a
// malformed AST). The restriction comes from the server, so it is a
// configuration fault: it wraps [ErrInvalidSchema] (INTERNAL in an error
// mapper) and is never blamed on the client's filter.
var ErrInvalidRestriction = fmt.Errorf("listing: invalid restriction: %w", ErrInvalidSchema)
