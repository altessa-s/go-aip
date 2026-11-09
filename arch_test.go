// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package aip_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// rootModule is the import path of the root module.
const rootModule = "github.com/altessa-s/go-aip"

// skippedModuleDirs are module directories the layering check does not load: devtools pins tools and imports no go-aip
// package.
var skippedModuleDirs = []string{"devtools"}

// testOnlyModuleDirs are modules that only test the library and are never released: every package in them is test
// support, so they may import backends and test-support packages, and nothing may require them. They still must not
// import another module's internal packages.
var testOnlyModuleDirs = []string{"integration/crossbackend", "integration/docker"}

// sqlDialects are the backends built on backend/sqlbase. Their modules require the sqlbase module, and the tests of
// sqlbase may import them to check the shared code through each dialect, so the sqlbase module requires them back.
var sqlDialects = []string{"postgres", "mariadb", "clickhouse"}

// driverPrefixes are the import path prefixes of database drivers and clients. The root module renders queries; it never
// talks to a database.
var driverPrefixes = []string{
	"go.mongodb.org/mongo-driver",
	"github.com/jackc/pgx",
	"github.com/lib/pq",
	"github.com/go-sql-driver/mysql",
	"github.com/ClickHouse/clickhouse-go",
	"github.com/redis/go-redis",
	"github.com/redis/rueidis",
	"github.com/meilisearch/meilisearch-go",
}

// goPackage is the part of `go list -json` output the layering rules read.
type goPackage struct {
	Module       *struct{ Path string }
	ImportPath   string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

// goModFile is the part of `go mod edit -json` output the module rules read.
type goModFile struct {
	Module  struct{ Path string }
	Require []struct{ Path string }
}

// goModule is one loaded module.
type goModule struct {
	path     string
	requires []string
	packages []goPackage
	testOnly bool
}

// TestLayering enforces the package and module layering of every module of the repository (see AGENTS.md,
// Architecture):
//
//  1. a core package (the root module) does not import a backend package;
//  2. a backend package imports no other backend except backend/sqlbase (and its subpackages);
//  3. a production package imports no test-support package (an element ending in "test", or internal/testhelpers);
//  4. no root-module package, tests included, imports a database driver;
//  5. no package, tests included, imports an internal package of another module;
//  6. the tests of a core package import no backend package;
//  7. the tests of a backend package import no other backend except backend/sqlbase, and the tests of backend/sqlbase
//     import no backend but the SQL dialects built on it;
//  8. the root module requires no other module of the repository; a backend module requires only the root module,
//     a SQL dialect also backend/sqlbase and backend/sqlbase the SQL dialects; no released module requires a test-only
//     module.
//
// Rules 1 to 3 read the imports of non-test files; rules 6 and 7 read those of test files; rule 8 reads the go.mod
// requirements. A test-only module is exempt from rules 1 to 3, 6 and 7, and may require any module of the repository.
func TestLayering(t *testing.T) {
	t.Parallel()

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the layering check needs the go command: %v", err)
	}

	dirs, err := moduleDirs(".")
	if err != nil {
		t.Fatal(err)
	}

	mods := make([]goModule, 0, len(dirs))
	for _, dir := range dirs {
		mod, err := loadModule(t, goBin, dir)
		if err != nil {
			t.Fatal(err)
		}

		mods = append(mods, mod)
	}

	if !slices.ContainsFunc(mods, func(m goModule) bool { return m.path == rootModule }) {
		t.Fatalf("module %s was not loaded from %v", rootModule, dirs)
	}

	for _, v := range layeringViolations(mods) {
		t.Error(v)
	}
}

// moduleDirs returns the directories under root holding a go.mod, relative to root, without skippedModuleDirs.
func moduleDirs(root string) ([]string, error) {
	var dirs []string

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			name := d.Name()
			if p != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "vendor" ||
				slices.Contains(skippedModuleDirs, filepath.ToSlash(p))) {
				return filepath.SkipDir
			}

			return nil
		}

		if d.Name() == "go.mod" {
			dirs = append(dirs, filepath.ToSlash(filepath.Dir(p)))
		}

		return nil
	})

	return dirs, err
}

// loadModule lists the packages of the module in dir.
func loadModule(t *testing.T, goBin, dir string) (goModule, error) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), goBin, "list", "-json", "./...")
	cmd.Dir = dir

	cmd.Env = append(os.Environ(), "GOWORK=off")

	var stderr strings.Builder

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return goModule{}, fmt.Errorf("go list in %s: %w\n%s", dir, err, stderr.String())
	}

	mod := goModule{testOnly: slices.Contains(testOnlyModuleDirs, dir)}

	gomod, err := readGoMod(t, goBin, dir)
	if err != nil {
		return goModule{}, err
	}

	mod.path = gomod.Module.Path
	for _, r := range gomod.Require {
		mod.requires = append(mod.requires, r.Path)
	}

	dec := json.NewDecoder(bytes.NewReader(out))

	for {
		var p goPackage

		err := dec.Decode(&p)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return goModule{}, fmt.Errorf("decode go list output of %s: %w", dir, err)
		}

		if p.Module == nil || p.Module.Path != mod.path {
			return goModule{}, fmt.Errorf("package %s of %s is not in module %s", p.ImportPath, dir, mod.path)
		}

		mod.packages = append(mod.packages, p)
	}

	if len(mod.packages) == 0 {
		return goModule{}, fmt.Errorf("no package listed in %s", dir)
	}

	return mod, nil
}

// readGoMod reads the go.mod file of the module in dir.
func readGoMod(t *testing.T, goBin, dir string) (goModFile, error) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), goBin, "mod", "edit", "-json")
	cmd.Dir = dir

	var stderr strings.Builder

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return goModFile{}, fmt.Errorf("go mod edit -json in %s: %w\n%s", dir, err, stderr.String())
	}

	var f goModFile
	if err := json.Unmarshal(out, &f); err != nil {
		return goModFile{}, fmt.Errorf("decode go.mod of %s: %w", dir, err)
	}

	if f.Module.Path == "" {
		return goModFile{}, fmt.Errorf("go.mod of %s names no module", dir)
	}

	return f, nil
}

// layeringViolations applies the rules of [TestLayering] to mods and returns one sorted message per violation.
func layeringViolations(mods []goModule) []string {
	paths := make([]string, len(mods))
	for i, m := range mods {
		paths[i] = m.path
	}

	var out []string

	for _, m := range mods {
		out = append(out, requireViolations(m, mods)...)

		for _, p := range m.packages {
			for _, imp := range slices.Concat(p.Imports, p.TestImports, p.XTestImports) {
				if owner := moduleOf(imp, paths); owner != "" && owner != m.path && hasElementBelow(imp, owner, isInternal) {
					out = append(out, fmt.Sprintf("package %s of module %s imports internal package %s of module %s",
						p.ImportPath, m.path, imp, owner))
				}

				if m.path == rootModule && slices.ContainsFunc(driverPrefixes, func(d string) bool { return underPath(imp, d) }) {
					out = append(out, fmt.Sprintf("root module package %s imports database driver %s", p.ImportPath, imp))
				}
			}

			if m.testOnly {
				continue
			}

			out = append(out, productionViolations(p)...)
			out = append(out, testViolations(p)...)
		}
	}

	slices.Sort(out)

	return slices.Compact(out)
}

// requireViolations applies rule 8 to the requirements of m on the other modules of mods.
func requireViolations(m goModule, mods []goModule) []string {
	var out []string

	for _, req := range m.requires {
		i := slices.IndexFunc(mods, func(o goModule) bool { return o.path == req })
		if i < 0 || m.testOnly {
			continue
		}

		target := mods[i]
		backend, targetBackend := backendOf(m.path), backendOf(target.path)

		switch {
		case target.testOnly:
			out = append(out, fmt.Sprintf("module %s requires test-only module %s", m.path, req))
		case req == rootModule && m.path != rootModule:
		case slices.Contains(sqlDialects, backend) && targetBackend == "sqlbase":
		case backend == "sqlbase" && slices.Contains(sqlDialects, targetBackend):
		default:
			out = append(out, fmt.Sprintf("module %s requires module %s", m.path, req))
		}
	}

	return out
}

// productionViolations applies rules 1 to 3 to the non-test imports of p.
func productionViolations(p goPackage) []string {
	var out []string

	backend := backendOf(p.ImportPath)
	testSupport := isTestSupport(p.ImportPath)

	for _, imp := range p.Imports {
		if !underPath(imp, rootModule) {
			continue
		}

		target := backendOf(imp)

		switch {
		case backend == "" && target != "":
			out = append(out, fmt.Sprintf("core package %s imports backend package %s", p.ImportPath, imp))
		case backend != "" && target != "" && target != backend && target != "sqlbase":
			out = append(out, fmt.Sprintf("backend package %s imports backend package %s", p.ImportPath, imp))
		}

		if !testSupport && isTestSupport(imp) {
			out = append(out, fmt.Sprintf("production package %s imports test-support package %s", p.ImportPath, imp))
		}
	}

	return out
}

// testViolations applies rules 6 and 7 to the test imports of p.
func testViolations(p goPackage) []string {
	var out []string

	backend := backendOf(p.ImportPath)

	for _, imp := range slices.Concat(p.TestImports, p.XTestImports) {
		target := backendOf(imp)
		if target == "" || target == backend {
			continue
		}

		switch {
		case backend == "":
			out = append(out, fmt.Sprintf("tests of core package %s import backend package %s", p.ImportPath, imp))
		case backend == "sqlbase" && !slices.Contains(sqlDialects, target):
			out = append(out, fmt.Sprintf("tests of backend package %s import backend package %s", p.ImportPath, imp))
		case backend != "" && backend != "sqlbase" && target != "sqlbase":
			out = append(out, fmt.Sprintf("tests of backend package %s import backend package %s", p.ImportPath, imp))
		}
	}

	return out
}

// backendOf returns the backend a go-aip package belongs to (the element after backend/), or "" for a core package.
func backendOf(importPath string) string {
	rest, ok := strings.CutPrefix(importPath, rootModule+"/backend/")
	if !ok {
		return ""
	}

	name, _, _ := strings.Cut(rest, "/")

	return name
}

// isTestSupport reports a go-aip package that exists for tests: an element ending in "test" (filtertest, postgrestest, the
// proto fixtures) or internal/testhelpers.
func isTestSupport(importPath string) bool {
	return underPath(importPath, rootModule) &&
		hasElement(importPath, func(e string) bool { return strings.HasSuffix(e, "test") || e == "testhelpers" })
}

// hasElement reports whether an element of importPath below the root module path satisfies match.
func hasElement(importPath string, match func(string) bool) bool {
	return hasElementBelow(importPath, rootModule, match)
}

// hasElementBelow reports whether an element of importPath below the module path modulePath satisfies match.
func hasElementBelow(importPath, modulePath string, match func(string) bool) bool {
	rest, ok := strings.CutPrefix(importPath, modulePath+"/")
	if !ok {
		return false
	}

	return slices.ContainsFunc(strings.Split(rest, "/"), match)
}

// isInternal reports an internal path element.
func isInternal(element string) bool { return element == "internal" }

// moduleOf returns the module of modulePaths that owns importPath (the longest matching path), or "" for none.
func moduleOf(importPath string, modulePaths []string) string {
	owner := ""

	for _, m := range modulePaths {
		if underPath(importPath, m) && len(m) > len(owner) {
			owner = m
		}
	}

	return owner
}

// underPath reports whether importPath is prefix or a package below it.
func underPath(importPath, prefix string) bool {
	return importPath == prefix || strings.HasPrefix(importPath, prefix+"/")
}

// TestLayeringViolations pins every rule of [TestLayering] on synthetic module listings.
func TestLayeringViolations(t *testing.T) {
	t.Parallel()

	const (
		r       = rootModule
		sqlbase = rootModule + "/backend/sqlbase"
		pg      = rootModule + "/backend/postgres"
		memory  = rootModule + "/backend/memory"
		mongo   = rootModule + "/backend/mongo"
		cross   = rootModule + "/integration/crossbackend"
		dock    = rootModule + "/integration/docker"
	)

	pkg := func(path string, imports, testImports []string) goPackage {
		return goPackage{ImportPath: path, Imports: imports, XTestImports: testImports}
	}
	root := func(pkgs ...goPackage) goModule { return goModule{path: r, packages: pkgs} }
	module := func(path string, requires []string, pkgs ...goPackage) goModule {
		return goModule{path: path, requires: requires, packages: pkgs}
	}
	testOnly := func(path string, requires []string, pkgs ...goPackage) goModule {
		return goModule{path: path, requires: requires, packages: pkgs, testOnly: true}
	}

	tests := []struct {
		name string
		mods []goModule
		want []string
	}{
		{
			name: "clean graph",
			mods: []goModule{
				root(
					pkg(r+"/listing", []string{r + "/filter", r + "/internal/lru", "fmt"}, []string{r + "/filter/filtertest"}),
					pkg(r+"/listing/listingtest", []string{r + "/internal/testhelpers"}, nil),
				),
				module(sqlbase, []string{r, pg, "github.com/shopspring/decimal"},
					pkg(sqlbase, []string{r + "/listing"}, []string{pg, sqlbase + "/sqlbasetest"}),
					pkg(sqlbase+"/sqltype", []string{sqlbase, sqlbase + "/internal/x"}, nil),
				),
				module(pg, []string{r, sqlbase},
					pkg(pg, []string{sqlbase, sqlbase + "/sqltype", pg + "/internal/x"}, nil),
					pkg(pg+"/postgrestest", []string{pg, r + "/filter/filtertest"}, nil),
				),
				module(memory, []string{r}, pkg(memory, nil, []string{memory + "/internal/x", r + "/listing/listingtest"})),
				module(mongo, []string{r}, pkg(mongo, []string{r + "/filter", r + "/filter/regexanchor"}, []string{r + "/filter/filtertest"})),
				testOnly(cross, []string{r, sqlbase, pg, memory},
					pkg(cross, nil, []string{pg, memory, sqlbase + "/sqlbasetest", r + "/listing"})),
				testOnly(dock, []string{r, sqlbase, pg, cross},
					pkg(dock+"/postgres", []string{pg, pg + "/postgrestest", dock + "/internal/live"}, nil)),
			},
		},
		{
			name: "core imports backend",
			mods: []goModule{root(pkg(r+"/listing", []string{sqlbase}, nil))},
			want: []string{"core package " + r + "/listing imports backend package " + sqlbase},
		},
		{
			name: "backend imports sibling backend",
			mods: []goModule{module(memory, nil, pkg(memory, []string{r + "/backend/mariadb"}, nil))},
			want: []string{"backend package " + memory + " imports backend package " + r + "/backend/mariadb"},
		},
		{
			name: "backend imports a shared non-sqlbase package",
			mods: []goModule{module(pg, nil, pkg(pg, []string{r + "/backend/internal/sqlhelpers"}, nil))},
			want: []string{"backend package " + pg + " imports backend package " + r + "/backend/internal/sqlhelpers"},
		},
		{
			name: "production imports test support",
			mods: []goModule{
				root(
					pkg(r+"/listing", []string{r + "/filter/filtertest"}, nil),
					pkg(r+"/fieldmask", []string{r + "/internal/proto/gen/fieldmasktest/v1"}, nil),
				),
				module(sqlbase, nil, pkg(sqlbase, []string{sqlbase + "/sqlbasetest"}, nil)),
			},
			want: []string{
				"production package " + r + "/fieldmask imports test-support package " + r + "/internal/proto/gen/fieldmasktest/v1",
				"production package " + r + "/listing imports test-support package " + r + "/filter/filtertest",
				"production package " + sqlbase + " imports test-support package " + sqlbase + "/sqlbasetest",
			},
		},
		{
			name: "core tests import backends",
			mods: []goModule{root(
				goPackage{ImportPath: r + "/filter", TestImports: []string{r + "/backend/lua"}},
				pkg(r+"/listing", nil, []string{sqlbase}),
				pkg(r+"/internal/crossbackend", nil, []string{memory}),
			)},
			want: []string{
				"tests of core package " + r + "/filter import backend package " + r + "/backend/lua",
				"tests of core package " + r + "/internal/crossbackend import backend package " + memory,
				"tests of core package " + r + "/listing import backend package " + sqlbase,
			},
		},
		{
			name: "backend tests import sibling backends",
			mods: []goModule{
				module(memory, nil, pkg(memory, nil, []string{r + "/backend/mariadb"})),
				module(pg, nil, pkg(pg, nil, []string{sqlbase, r + "/backend/clickhouse"})),
				module(sqlbase, nil, pkg(sqlbase, nil, []string{r + "/backend/mariadb", memory})),
			},
			want: []string{
				"tests of backend package " + memory + " import backend package " + r + "/backend/mariadb",
				"tests of backend package " + pg + " import backend package " + r + "/backend/clickhouse",
				"tests of backend package " + sqlbase + " import backend package " + memory,
			},
		},
		{
			name: "root imports driver in a test",
			mods: []goModule{root(pkg(r+"/filter", nil, []string{"github.com/jackc/pgx/v5"}))},
			want: []string{"root module package " + r + "/filter imports database driver github.com/jackc/pgx/v5"},
		},
		{
			name: "module imports internal package of another module",
			mods: []goModule{
				root(),
				module(mongo, nil, pkg(mongo, []string{r + "/internal/lru"}, []string{r + "/filter/internal/regexcache"})),
				module(sqlbase, nil, pkg(sqlbase+"/sqltype", []string{sqlbase + "/internal/x"}, nil)),
				module(pg, nil, pkg(pg, nil, []string{sqlbase + "/internal/x"})),
				testOnly(dock, nil, pkg(dock+"/postgres", nil, []string{r + "/internal/testhelpers", dock + "/internal/live"})),
				testOnly(cross, nil, pkg(cross, nil, []string{sqlbase + "/internal/x"})),
			},
			want: []string{
				"package " + cross + " of module " + cross + " imports internal package " + sqlbase + "/internal/x of module " + sqlbase,
				"package " + dock + "/postgres of module " + dock + " imports internal package " + r + "/internal/testhelpers of module " + r,
				"package " + mongo + " of module " + mongo + " imports internal package " + r + "/filter/internal/regexcache of module " + r,
				"package " + mongo + " of module " + mongo + " imports internal package " + r + "/internal/lru of module " + r,
				"package " + pg + " of module " + pg + " imports internal package " + sqlbase + "/internal/x of module " + sqlbase,
			},
		},
		{
			name: "module requirements",
			mods: []goModule{
				module(r, []string{sqlbase, "github.com/shopspring/decimal"}),
				module(sqlbase, []string{r, pg, memory}),
				module(pg, []string{r, sqlbase, memory}),
				module(memory, []string{r, sqlbase, cross}),
				module(mongo, []string{r, pg}),
				testOnly(cross, []string{r, sqlbase, pg, memory, mongo}),
			},
			want: []string{
				"module " + memory + " requires module " + sqlbase,
				"module " + memory + " requires test-only module " + cross,
				"module " + mongo + " requires module " + pg,
				"module " + pg + " requires module " + memory,
				"module " + r + " requires module " + sqlbase,
				"module " + sqlbase + " requires module " + memory,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got, want := layeringViolations(tt.mods), slices.Sorted(slices.Values(tt.want)); !slices.Equal(got, want) {
				t.Errorf("violations:\n got %q\nwant %q", got, want)
			}
		})
	}
}
