// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package testhelpers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
)

// ExportedErrors returns the exported Err* variables and the exported types with an Error() string method of the non-test
// Go files under root, as "<package dir>.<name>" with the package directory relative to root. It skips internal, testdata,
// bin and hidden directories, and every directory for which skip (when non-nil) reports true given its path relative to
// root.
func ExportedErrors(root string, skip func(dir string) bool) (vars, types []string, err error) {
	fset := token.NewFileSet()

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}

		rel = filepath.ToSlash(rel)

		if d.IsDir() {
			if n := d.Name(); p != root && (n == "internal" || n == "testdata" || strings.HasPrefix(n, ".") || n == "bin" ||
				skip != nil && skip(rel)) {
				return filepath.SkipDir
			}

			return nil
		}

		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}

		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}

		pkg := filepath.ToSlash(filepath.Dir(rel))
		vars = append(vars, errorVars(pkg, f)...)
		types = append(types, errorTypeNames(pkg, f)...)

		return nil
	})

	return vars, types, err
}

func errorVars(pkg string, f *ast.File) []string {
	var out []string

	for _, decl := range f.Decls {
		g, ok := decl.(*ast.GenDecl)
		if !ok || g.Tok != token.VAR {
			continue
		}

		for _, spec := range g.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}

			for _, n := range vs.Names {
				if n.IsExported() && strings.HasPrefix(n.Name, "Err") {
					out = append(out, pkg+"."+n.Name)
				}
			}
		}
	}

	return out
}

func errorTypeNames(pkg string, f *ast.File) []string {
	var out []string

	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || fn.Name.Name != "Error" || fn.Type.Params.NumFields() != 0 {
			continue
		}

		recv := fn.Recv.List[0].Type
		if star, ok := recv.(*ast.StarExpr); ok {
			recv = star.X
		}

		if id, ok := recv.(*ast.Ident); ok && id.IsExported() {
			out = append(out, pkg+"."+id.Name)
		}
	}

	return out
}
