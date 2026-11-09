// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package projectiontest

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/altessa-s/go-aip/projection"
)

// Result is a backend-neutral reading of a rendered projection, produced by
// the decoder a backend passes to [Adapt]. At most one of All, None, Include
// and Exclude is set.
type Result struct {
	// All reports a projection that returns every stored field.
	All bool
	// None reports a projection that returns no field.
	None bool
	// Include lists the storage paths returned, in any order.
	Include []string
	// Exclude lists the storage paths omitted, in any order.
	Exclude []string
}

// Translator is the shape [RunConformance] drives: a projection translator
// whose output has been decoded into a [Result].
type Translator interface {
	Translate(spec projection.Spec) (Result, error)
	TranslateView(spec projection.Spec, view projection.View) (Result, error)
}

// Source is the shape every projection translator shares, generic in its
// output type.
type Source[Out any] interface {
	Translate(spec projection.Spec) (Out, error)
	TranslateView(spec projection.Spec, view projection.View) (Out, error)
}

// Adapt turns a projection translator into a [Translator], reading each
// rendered output back with decode.
func Adapt[Out any](tr Source[Out], decode func(Out) (Result, error)) Translator {
	return adapter[Out]{tr: tr, decode: decode}
}

type adapter[Out any] struct {
	tr     Source[Out]
	decode func(Out) (Result, error)
}

func (a adapter[Out]) Translate(spec projection.Spec) (Result, error) {
	out, err := a.tr.Translate(spec)
	if err != nil {
		return Result{}, err
	}

	return a.decode(out)
}

func (a adapter[Out]) TranslateView(spec projection.Spec, view projection.View) (Result, error) {
	out, err := a.tr.TranslateView(spec, view)
	if err != nil {
		return Result{}, err
	}

	return a.decode(out)
}

// Capabilities declares what a backend can render beyond an inclusion list.
// A backend without a capability must reject the selection with the shared
// sentinel the suite expects.
type Capabilities struct {
	// Exclusion: every authorized field of a policy without an allow-list
	// renders as "every field but the denied ones". Without it the
	// constructor fails with [projection.ErrAllFieldsUnresolvable].
	Exclusion bool
	// None: a ModeNone selection without required fields renders as no
	// field. Without it Translate fails with
	// [projection.ErrUnsupportedSelection].
	None bool
}

// Backend describes one projection translator under test.
type Backend struct {
	// New builds the translator for policy with the given storage options.
	New func(policy *projection.Policy, storage ...projection.TranslatorOption) (Translator, error)
	// Capabilities declares the optional selections the backend renders.
	Capabilities Capabilities
}

// RunConformance runs the projection behavior every translator shares as
// subtests of t: authorization in API and storage names, field mapping,
// required fields, every authorized field (AIP-157), views, ModeNone,
// exclusion and concurrent use. Paths are compared after collapsing those an
// included ancestor covers, so a backend may or may not drop them.
func RunConformance(t *testing.T, b Backend) {
	t.Helper()

	if b.New == nil {
		t.Fatal("projectiontest: Backend.New is nil")
	}

	cases := []struct {
		name string
		run  func(t *testing.T, b Backend)
	}{
		{"PolicyRequired", runPolicyRequired},
		{"Include", runInclude},
		{"AllAuthorized", runAllAuthorized},
		{"EveryField", runEveryField},
		{"Rejections", runRejections},
		{"DeniedStorage", runDeniedStorage},
		{"Views", runViews},
		{"Nested", runNested},
		{"None", runNone},
		{"Exclusion", runExclusion},
		{"Concurrent", runConcurrent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t, b)
		})
	}
}

// Field names the cases share.
const (
	fieldName       = "name"
	fieldEmail      = "email"
	fieldCreateTime = "createTime"
	fieldToken      = "token"
	// columnCreateTime is the storage name fieldCreateTime maps to.
	columnCreateTime = "created_at"
	// unknownView is a view no policy of the suite serves.
	unknownView projection.View = 99
)

// standard is the policy and storage options most cases share.
func standard(t *testing.T, b Backend) Translator {
	t.Helper()

	return mustNew(t, b, MustPolicy(t,
		projection.WithAllowedFields(fieldName, fieldEmail, fieldCreateTime, "address.*", "password"),
		projection.WithDeniedFields("password"),
		projection.WithView(projection.ViewBasic, fieldName),
		projection.WithDefaultView(projection.ViewBasic),
	),
		projection.WithFieldMapping(map[string]string{fieldCreateTime: columnCreateTime}),
		projection.WithRequiredFields("id"),
	)
}

func mustNew(t *testing.T, b Backend, p *projection.Policy, storage ...projection.TranslatorOption) Translator {
	t.Helper()

	tr, err := b.New(p, storage...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return tr
}

func runPolicyRequired(t *testing.T, b Backend) {
	_, err := b.New(nil)
	wantErr(t, err, projection.ErrPolicyRequired)
}

func runInclude(t *testing.T, b Backend) {
	tr := standard(t, b)

	got, err := tr.Translate(projection.Spec{Paths: []string{fieldCreateTime, fieldName}})
	wantResult(t, got, err, Result{Include: []string{columnCreateTime, "id", fieldName}})
}

func runAllAuthorized(t *testing.T, b Backend) {
	tr := mustNew(t, b, MustPolicy(t,
		projection.WithAllowedFields(fieldName, fieldEmail, fieldCreateTime),
		projection.WithView(projection.ViewBasic, fieldName),
		projection.WithDefaultView(projection.ViewBasic),
	),
		projection.WithFieldMapping(map[string]string{fieldCreateTime: columnCreateTime}),
		projection.WithRequiredFields("id"),
	)

	want := Result{Include: []string{columnCreateTime, fieldEmail, "id", fieldName}}

	for _, spec := range []projection.Spec{{}, {Mode: projection.ModeAll}} {
		got, err := tr.Translate(spec)
		wantResult(t, got, err, want)
	}

	got, err := tr.TranslateView(projection.Spec{}, projection.ViewFull)
	wantResult(t, got, err, want)

	got, err = tr.TranslateView(projection.Spec{Mode: projection.ModeAll}, projection.ViewBasic)
	wantResult(t, got, err, want)
}

func runEveryField(t *testing.T, b Backend) {
	tr := mustNew(t, b, MustPolicy(t))

	got, err := tr.Translate(projection.Spec{})
	wantResult(t, got, err, Result{All: true})

	got, err = tr.Translate(projection.Spec{Paths: []string{fieldName}})
	wantResult(t, got, err, Result{Include: []string{fieldName}})
}

func runRejections(t *testing.T, b Backend) {
	tr := standard(t, b)

	for _, c := range []struct {
		paths []string
		want  error
	}{
		{[]string{"unknown"}, projection.ErrFieldNotAllowed},
		{[]string{"password"}, projection.ErrFieldNotAllowed},
		{[]string{"password.hash"}, projection.ErrFieldNotAllowed},
		{[]string{"address.lines.0"}, projection.ErrUnsupportedPath},
		{[]string{"name;drop"}, projection.ErrInvalidFieldPath},
	} {
		_, err := tr.Translate(projection.Spec{Paths: c.paths})
		wantErr(t, err, c.want)

		_, err = tr.TranslateView(projection.Spec{Paths: c.paths}, projection.ViewFull)
		wantErr(t, err, c.want)
	}
}

func runDeniedStorage(t *testing.T, b Backend) {
	tr := mustNew(t, b, MustPolicy(t, projection.WithAllowedFields(fieldName, fieldToken)),
		projection.WithFieldMapping(map[string]string{fieldToken: "secret"}),
		projection.WithDeniedStorageFields("secret"),
	)

	_, err := tr.Translate(projection.Spec{Paths: []string{fieldToken}})
	wantErr(t, err, projection.ErrFieldNotAllowed)

	got, err := tr.Translate(projection.Spec{})
	wantResult(t, got, err, Result{Include: []string{fieldName}})

	_, err = b.New(MustPolicy(t, projection.WithAllowedFields(fieldName)),
		projection.WithDeniedStorageFields("id"), projection.WithRequiredFields("id"))
	wantErr(t, err, projection.ErrConflictingPolicy)
}

func runViews(t *testing.T, b Backend) {
	tr := standard(t, b)

	got, err := tr.TranslateView(projection.Spec{}, projection.ViewUnspecified)
	wantResult(t, got, err, Result{Include: []string{"id", fieldName}})

	got, err = tr.TranslateView(projection.Spec{}, projection.ViewBasic)
	wantResult(t, got, err, Result{Include: []string{"id", fieldName}})

	got, err = tr.TranslateView(projection.Spec{Paths: []string{fieldEmail}}, projection.ViewBasic)
	wantResult(t, got, err, Result{Include: []string{fieldEmail, "id"}})

	_, err = tr.TranslateView(projection.Spec{}, unknownView)
	wantErr(t, err, projection.ErrInvalidView)

	_, err = tr.TranslateView(projection.Spec{Paths: []string{fieldEmail}}, unknownView)
	wantErr(t, err, projection.ErrInvalidView)
}

func runNested(t *testing.T, b Backend) {
	// Nested paths are declared exactly: an SQL dialect reads the dot as a table qualifier and refuses a path only a wildcard
	// admits.
	tr := mustNew(t, b, MustPolicy(t, projection.WithAllowedFields(fieldName, "address.city", "address.zip")),
		projection.WithRequiredFields("id"))

	got, err := tr.Translate(projection.Spec{Paths: []string{"address.city", "address.zip"}})
	wantResult(t, got, err, Result{Include: []string{"address.city", "address.zip", "id"}})
}

func runNone(t *testing.T, b Backend) {
	tr := mustNew(t, b, MustPolicy(t, projection.WithAllowedFields(fieldName)))

	got, err := tr.Translate(projection.Spec{Mode: projection.ModeNone})
	if b.Capabilities.None {
		wantResult(t, got, err, Result{None: true})
	} else {
		wantErr(t, err, projection.ErrUnsupportedSelection)
	}

	tr = mustNew(t, b, MustPolicy(t, projection.WithAllowedFields(fieldName)), projection.WithRequiredFields("id"))

	got, err = tr.Translate(projection.Spec{Mode: projection.ModeNone})
	wantResult(t, got, err, Result{Include: []string{"id"}})
}

func runExclusion(t *testing.T, b Backend) {
	p := MustPolicy(t,
		projection.WithDeniedFields("password", fieldToken),
		projection.WithView(projection.ViewBasic, fieldName),
	)
	storage := []projection.TranslatorOption{projection.WithFieldMapping(map[string]string{fieldToken: "api_key"})}

	tr, err := b.New(p, storage...)
	if !b.Capabilities.Exclusion {
		wantErr(t, err, projection.ErrAllFieldsUnresolvable)
		return
	}

	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	want := Result{Exclude: []string{"api_key", "password"}}

	got, err := tr.Translate(projection.Spec{})
	wantResult(t, got, err, want)

	got, err = tr.TranslateView(projection.Spec{}, projection.ViewFull)
	wantResult(t, got, err, want)

	got, err = tr.TranslateView(projection.Spec{}, projection.ViewBasic)
	wantResult(t, got, err, Result{Include: []string{fieldName}})

	_, err = tr.Translate(projection.Spec{Paths: []string{fieldToken}})
	wantErr(t, err, projection.ErrFieldNotAllowed)
}

func runConcurrent(t *testing.T, b Backend) {
	tr := standard(t, b)
	spec := projection.Spec{Paths: []string{fieldCreateTime, fieldEmail}}
	want := Result{Include: []string{columnCreateTime, fieldEmail, "id"}}

	const workers = 8

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	for range workers {
		wg.Go(func() {
			got, err := tr.Translate(spec)
			if err == nil && !equal(got, want) {
				err = errors.New("unexpected result")
			}

			if err != nil {
				mu.Lock()

				errs = append(errs, err)

				mu.Unlock()
			}
		})
	}

	wg.Wait()

	if err := errors.Join(errs...); err != nil {
		t.Fatalf("concurrent Translate: %v", err)
	}
}

func wantErr(t *testing.T, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func wantResult(t *testing.T, got Result, err error, want Result) {
	t.Helper()

	if err != nil {
		t.Fatalf("error = %v, want %+v", err, want)
	}

	if !equal(got, want) {
		t.Fatalf("result = %+v, want %+v", normalize(got), normalize(want))
	}
}

func equal(a, b Result) bool {
	a, b = normalize(a), normalize(b)

	return a.All == b.All && a.None == b.None && slices.Equal(a.Include, b.Include) && slices.Equal(a.Exclude, b.Exclude)
}

// normalize sorts the paths and drops those an included ancestor covers.
func normalize(r Result) Result {
	r.Include = collapse(r.Include)
	r.Exclude = collapse(r.Exclude)

	return r
}

func collapse(paths []string) []string {
	sorted := slices.Clone(paths)
	slices.Sort(sorted)

	out := make([]string, 0, len(sorted))
	for _, p := range slices.Compact(sorted) {
		if !slices.ContainsFunc(out, func(kept string) bool { return strings.HasPrefix(p, kept+".") }) {
			out = append(out, p)
		}
	}

	return out
}
