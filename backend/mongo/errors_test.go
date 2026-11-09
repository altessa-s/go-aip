// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"fmt"
	"go/ast"
	"go/parser"
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
	"github.com/altessa-s/go-aip/backend/mongo"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
	gotoken "go/token"
)

// sentinel is the code and reason an exported error of the package maps to, wrapped the way the package returns it.
type sentinel struct {
	err    error
	code   code.Code
	reason string
}

// sentinels lists every exported Err* variable of the package with the code and reason it maps to. It is the backend/mongo part
// of the backend sentinel table of integration/crossbackend, which does not require this module (it keeps the MongoDB driver
// out of that module); TestEveryErrorIsListed fails for a sentinel missing here.
var sentinels = map[string]sentinel{
	// Server-side faults: a scope that does not pin the parent, a stored document that does not decode, a value the codecs
	// cannot store or read. INTERNAL as an unknown error.
	"ErrScope":             {mongo.ErrScope, code.Code_INTERNAL, apierror.ReasonInternal},
	"ErrCorruptRecord":     {mongo.ErrCorruptRecord, code.Code_INTERNAL, apierror.ReasonInternal},
	"ErrInvalidDate":       {mongo.ErrInvalidDate, code.Code_INTERNAL, apierror.ReasonInternal},
	"ErrNotADateType":      {mongo.ErrNotADateType, code.Code_INTERNAL, apierror.ReasonInternal},
	"ErrDecimalOutOfRange": {mongo.ErrDecimalOutOfRange, code.Code_INTERNAL, apierror.ReasonInternal},
	"ErrDecimalNotFinite":  {mongo.ErrDecimalNotFinite, code.Code_INTERNAL, apierror.ReasonInternal},
	// Returned with coreerrs.ErrUnavailable, or as a FieldError on filter.
	"ErrTimeout": {fmt.Errorf("%w: %w", coreerrs.ErrUnavailable, mongo.ErrTimeout), code.Code_UNAVAILABLE, apierror.ReasonUnavailable},
}

func TestSentinelMapping(t *testing.T) {
	t.Parallel()

	m, err := apierror.NewMapper(apierror.WithRules(aipmap.Rules()...), apierror.WithDefaultDomain("example.com"))
	require.NoError(t, err)

	for _, name := range slices.Sorted(maps.Keys(sentinels)) {
		tc := sentinels[name]
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

// TestEveryErrorIsListed parses the package and fails for an exported Err* variable sentinels does not list, or for an entry
// that no longer exists.
func TestEveryErrorIsListed(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("*.go")
	require.NoError(t, err)

	var vars []string

	fset := gotoken.NewFileSet()

	for _, p := range files {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}

		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		require.NoError(t, err)

		for _, decl := range f.Decls {
			g, ok := decl.(*ast.GenDecl)
			if !ok || g.Tok != gotoken.VAR {
				continue
			}

			for _, spec := range g.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}

				for _, n := range vs.Names {
					if n.IsExported() && strings.HasPrefix(n.Name, "Err") {
						vars = append(vars, n.Name)
					}
				}
			}
		}
	}

	assert.ElementsMatch(t, slices.Sorted(maps.Keys(sentinels)), vars, "every exported sentinel needs an entry in sentinels")
}
