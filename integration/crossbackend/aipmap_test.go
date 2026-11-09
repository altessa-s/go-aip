// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package crossbackend_test

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/genproto/googleapis/rpc/code"

	"github.com/altessa-s/go-aip/apierror"
	"github.com/altessa-s/go-aip/apierror/aipmap"
	"github.com/altessa-s/go-aip/backend/memory"
	"github.com/altessa-s/go-aip/backend/sqlbase"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// backendSentinel is the code and reason a backend error maps to.
type backendSentinel struct {
	err    error
	code   code.Code
	reason string
}

// backendSentinels lists every exported Err* variable of the backend/ packages, keyed "<package dir>.<name>", with the error
// it is mapped as and the intended code and reason. It completes the core table of apierror/aipmap (coverage_test.go):
// TestEveryBackendSentinelIsListed fails for a backend sentinel missing here, so a new one gets a rule or a deliberate default.
var backendSentinels = map[string]backendSentinel{
	// Wraps listing.ErrWorkBudget.
	"backend/memory.ErrWorkBudget": {
		memory.ErrWorkBudget, code.Code_RESOURCE_EXHAUSTED, aipmap.ReasonWorkBudgetExceeded,
	},
	// Server configuration faults: a storage column or table name the dialect cannot quote.
	"backend/sqlbase.ErrInvalidColumnName": {sqlbase.ErrInvalidColumnName, code.Code_INTERNAL, apierror.ReasonInternal},
	"backend/sqlbase.ErrInvalidSchema":     {sqlbase.ErrInvalidSchema, code.Code_INTERNAL, apierror.ReasonInternal},
	"backend/sqlbase.ErrInvalidTableName":  {sqlbase.ErrInvalidTableName, code.Code_INTERNAL, apierror.ReasonInternal},
	// Server-side faults, a scope that does not pin the parent and a stored row that does not read: INTERNAL as unknown errors.
	"backend/sqlbase.ErrScope":         {sqlbase.ErrScope, code.Code_INTERNAL, apierror.ReasonInternal},
	"backend/sqlbase.ErrCorruptRecord": {sqlbase.ErrCorruptRecord, code.Code_INTERNAL, apierror.ReasonInternal},
	// Returned with coreerrs.ErrUnavailable, or as a FieldError on filter.
	"backend/sqlbase.ErrTimeout": {
		fmt.Errorf("%w: %w", coreerrs.ErrUnavailable, sqlbase.ErrTimeout), code.Code_UNAVAILABLE, apierror.ReasonUnavailable,
	},
}

func TestBackendSentinelMapping(t *testing.T) {
	t.Parallel()

	m, err := apierror.NewMapper(apierror.WithRules(aipmap.Rules()...), apierror.WithDefaultDomain("example.com"))
	require.NoError(t, err, "every rule is valid")

	for _, name := range slices.Sorted(maps.Keys(backendSentinels)) {
		tc := backendSentinels[name]
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := m.Map(fmt.Errorf("op: %w", tc.err))
			assert.Equal(t, tc.code, e.Code())
			assert.Equal(t, tc.reason, e.Reason())

			if tc.code == code.Code_INTERNAL {
				assert.Equal(t, apierror.MessageInternal, e.Message(), "a server fault never exposes its text")
			}
		})
	}
}

// TestEveryBackendSentinelIsListed parses the backend/ packages and fails for an exported Err* variable that
// backendSentinels does not list, for an entry that no longer exists, or for an exported error type (none exists today:
// add a table when one appears). backend/mongo, which this module does not require (it keeps the MongoDB driver out of it), keeps
// its part of the table and the same check in backend/mongo/errors_test.go.
func TestEveryBackendSentinelIsListed(t *testing.T) {
	t.Parallel()

	onlyBackends := func(dir string) bool {
		first, _, _ := strings.Cut(dir, "/")
		return first != "backend" || dir == "backend/mongo"
	}

	vars, types, err := exportedErrors(filepath.Join("..", ".."), onlyBackends)
	require.NoError(t, err)

	assert.ElementsMatch(t, slices.Sorted(maps.Keys(backendSentinels)), vars,
		"every exported backend sentinel needs an entry in backendSentinels (and a rule in aipmap, or a deliberate default)")
	assert.Empty(t, types, "every exported backend error type needs an entry in a mapping table")
}
