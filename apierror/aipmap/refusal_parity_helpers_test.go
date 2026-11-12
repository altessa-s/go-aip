// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package aipmap_test

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/altessa-s/go-aip/apierror"
	"github.com/altessa-s/go-aip/batch"
	"github.com/altessa-s/go-aip/fieldbehavior"
	"github.com/altessa-s/go-aip/fieldinfo"
	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// These helpers have no testing dependency: the golden recorder compiles them
// with public_refusal_corpus_test.go in an isolated archive of a8b17ba. It calls
// public APIs only and records no result from the implementation under review.
type refusalShape struct {
	Type       string
	Field      string
	Index      int
	Path       string
	Violations []update.FieldViolation
	Payload    []fieldbehavior.BehaviorViolation
	Identities []string
	Children   []refusalShape
}

type refusalRecord struct {
	Case            publicRefusalCase
	Sequence        []int
	Request, Stored string
	Raw             []string
	Shape           refusalShape
	Code            int32
	Fields          []string
}

func refusalTree(err error) refusalShape {
	if err == nil {
		return refusalShape{}
	}

	r := refusalShape{Type: fmt.Sprintf("%T", err)}
	switch e := err.(type) {
	case *coreerrs.FieldError:
		r.Field = e.Field
	case *update.MaskPathError:
		r.Field, r.Index, r.Path = e.Field, e.Index, e.Path
	case *update.BehaviorError:
		r.Violations = e.Violations
	case *update.StrictError:
		r.Violations = e.Violations
	case *fieldbehavior.BehaviorViolationError:
		r.Payload = e.Violations
	case *fieldmask.ValidationError:
		r.Path = e.Path
	}

	for _, identity := range []struct {
		name string
		err  error
	}{
		{"invalid-argument", coreerrs.ErrInvalidArgument},
		{"behavior", update.ErrBehaviorViolation}, {"protected", update.ErrProtectedField},
		{"strict", update.ErrInvalidUpdateMask}, {"mismatch", update.ErrMessageMismatch},
		{"elements", update.ErrElementCountMismatch}, {"invalid-path", fieldmask.ErrInvalidPath},
		{"wildcard", fieldmask.ErrWildcardNotAlone}, {"limit", fieldmask.ErrLimitExceeded},
		{"count", fieldmask.ErrMaxPathsExceeded}, {"length", fieldmask.ErrMaxPathLengthExceeded},
		{"depth", fieldmask.ErrMaxPathDepthExceeded}, {"strip", fieldbehavior.ErrBehaviorViolation},
	} {
		if errors.Is(err, identity.err) {
			r.Identities = append(r.Identities, identity.name)
		}
	}

	switch e := err.(type) {
	case interface{ Unwrap() []error }:
		for _, child := range e.Unwrap() {
			r.Children = append(r.Children, refusalTree(child))
		}
	case interface{ Unwrap() error }:
		if child := e.Unwrap(); child != nil {
			r.Children = append(r.Children, refusalTree(child))
		}
	}

	return r
}

func refusalInput(c publicRefusalCase) refusalRecord {
	req, stored, raw, _, _ := publicRefusalFixture(c)
	wire := func(m proto.Message) string {
		b, err := (proto.MarshalOptions{Deterministic: true}).Marshal(m)
		if err != nil {
			panic(err)
		}

		return hex.EncodeToString(b)
	}

	return refusalRecord{Case: c, Request: wire(req), Stored: wire(stored), Raw: raw.GetPaths()}
}

func refusalFields(mapped *apierror.Error) []string {
	var out []string

	if mapped != nil {
		for _, field := range mapped.FieldViolations() {
			out = append(out, field.GetField())
		}
	}

	return out
}

type baseOpaque struct{ cause error }

func (e baseOpaque) Error() string { return "opaque failure: " + strings.ToUpper(e.cause.Error()) }
func (e baseOpaque) Unwrap() error { return e.cause }
func (baseOpaque) Is(target error) bool {
	return target == coreerrs.ErrInvalidArgument
}

type baseDerived struct{ extra, original error }

func (e baseDerived) Error() string   { return e.original.Error() }
func (e baseDerived) Unwrap() []error { return []error{e.extra, e.original} }

type baseAs struct {
	cause, projection error
	match             bool
}

func (e baseAs) Error() string { return e.cause.Error() }
func (e baseAs) Unwrap() error { return e.cause }
func (e baseAs) As(target any) bool {
	return errors.As(e.projection, target)
}
func (e baseAs) Is(target error) bool {
	return e.match && errors.Is(e.projection, target)
}

// baseWrappers includes the update wrapper matrix, the base apierror tests'
// custom wrappers, and multi-%w. Compositions exercise selection through more
// than one wrapper without executing any update diagnostics.
func baseWrappers() []func(error) error {
	return []func(error) error{
		func(err error) error {
			return coreerrs.MapFieldPath(err, func(p string) string { return "mapped." + p })
		},
		func(err error) error {
			return coreerrs.MapFieldPath(err, func(p string) string { return "renamed[" + strings.ReplaceAll(p, ".", "/") + "]" })
		},
		func(err error) error { return batch.NewItemError("requests", 2, err) },
		func(err error) error { return batch.Each("requests", []int{2}, func(_, i int) error { return err }) },
		func(err error) error {
			_, got := batch.Atomic(context.Background(), "requests", []int{0},
				func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) },
				func(context.Context, int, int) (int, error) { return 0, err })

			return got
		},
		func(err error) error {
			_, got := batch.Partial(context.Background(), "requests", []int{0},
				func(context.Context, int, int) (int, error) { return 0, err },
				batch.WithConcurrency(1), batch.WithFatal(func(error) bool { return true }))

			return got
		},
		func(err error) error {
			result, got := batch.Partial(context.Background(), "requests", []int{0}, func(context.Context, int, int) (int, error) { return 0, err })
			if result.Failed[0] == nil {
				return got
			}

			return result.Failed[0]
		},
		func(err error) error { return errors.Join(errors.New("context"), err, errors.New("other context")) },
		func(err error) error { return fmt.Errorf("update request: %w", err) },
		func(err error) error { return apierror.Classify(err, errors.New("hidden storage cause")) },
		func(err error) error { return baseOpaque{cause: err} },
		func(err error) error { return &baseOpaque{cause: err} },
		func(err error) error {
			return baseDerived{extra: coreerrs.NewFieldError("other", "invalid"), original: err}
		},
		func(err error) error {
			return &baseDerived{extra: coreerrs.NewFieldError("other", "invalid"), original: err}
		},
		func(err error) error {
			return fmt.Errorf("requests: %w; %w", err, coreerrs.NewFieldError("other", "invalid"))
		},
		func(err error) error { return &coreerrs.FieldError{Field: "container", Reason: "invalid", Err: err} },
		func(err error) error {
			return baseAs{cause: err, projection: &fieldinfo.ViolationError{Violations: []fieldinfo.Violation{
				{Path: "book.uid", Format: "UUID4", Reason: "must be a UUID4"},
			}}}
		},
		func(err error) error {
			return fmt.Errorf("update: %w", errors.Join(coreerrs.NewFieldError("a", "invalid"), coreerrs.NewFieldError("b", "invalid"), err))
		},
		func(err error) error {
			return coreerrs.MapFieldPath(baseAs{cause: err, projection: errors.Join(
				coreerrs.NewFieldError("book.title", "invalid"),
				&fieldinfo.ViolationError{Violations: []fieldinfo.Violation{{Path: "book.uid", Format: "UUID4", Reason: "must be a UUID4"}}},
			)}, func(p string) string { return "mapped." + p })
		},
	}
}

func baseSequences() [][]int {
	out := [][]int{nil}
	for i := range baseWrappers() {
		out = append(out, []int{i})
	}

	// Representative compositions: mapping before/after a batch, nested
	// batches, field/batch nesting in both orders, and composed mappings.
	return append(out, []int{2, 0}, []int{0, 2}, []int{2, 2}, []int{15, 2}, []int{2, 15}, []int{0, 1})
}

func baseWrap(err error, sequence []int) error {
	wrappers := baseWrappers()
	for _, i := range sequence {
		err = wrappers[i](err)
	}

	return err
}
