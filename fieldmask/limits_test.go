// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldmask_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	fbpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
	testpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldmasktest/v1"
	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// deepPath returns a valid path of UpdateRequest with n segments: a Struct
// field followed by n-1 keys of its dynamic JSON.
func deepPath(n int) string {
	return "extra_data" + strings.Repeat(".k", n-1)
}

// longPath returns a valid path of UpdateRequest n bytes long.
func longPath(n int) string {
	return "extra_data." + strings.Repeat("k", n-len("extra_data."))
}

// manyPaths returns n distinct valid paths of UpdateRequest.
func manyPaths(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("extra_data.k%d", i)
	}

	return out
}

// limitOptions holds one limit as the options of both the read entry points
// and the update package.
type limitOptions struct {
	read   []fieldmask.Option
	update []update.Option
}

// limitOf returns the limit option of each package as limitOptions.
func limitOf(read fieldmask.Option, upd update.Option) limitOptions {
	return limitOptions{read: []fieldmask.Option{read}, update: []update.Option{upd}}
}

// limitEntryPoint runs one entry point over paths with the mask options.
type limitEntryPoint struct {
	name string
	run  func(paths []string, opts limitOptions) error
	// options reports that the entry point takes the options; otherwise it
	// applies the defaults.
	options bool
}

// limitEntryPoints are the entry points a client mask reaches.
var limitEntryPoints = []limitEntryPoint{
	{name: "ParsePaths", run: func(paths []string, _ limitOptions) error {
		_, err := fieldmask.ParsePaths(paths...)
		return err
	}},
	{name: "FromProtoFieldMask", options: true, run: func(paths []string, opts limitOptions) error {
		_, err := fieldmask.FromProtoFieldMask(&fieldmaskpb.FieldMask{Paths: paths}, opts.read...)
		return err
	}},
	{name: "Resolve", options: true, run: func(paths []string, opts limitOptions) error {
		_, err := fieldmask.FromPaths(paths...).Resolve((&testpb.UpdateRequest{}).ProtoReflect().Descriptor(), opts.read...)
		return err
	}},
	{name: "Validate", options: true, run: func(paths []string, opts limitOptions) error {
		return fieldmask.FromPaths(paths...).Validate(&testpb.UpdateRequest{}, opts.read...)
	}},
	{name: "Filter", options: true, run: func(paths []string, opts limitOptions) error {
		return fieldmask.FromPaths(paths...).Filter(structRequest(), opts.read...)
	}},
	{name: "Prune", options: true, run: func(paths []string, opts limitOptions) error {
		return fieldmask.FromPaths(paths...).Prune(structRequest(), opts.read...)
	}},
	{name: "ApplyUpdateMask", options: true, run: func(paths []string, opts limitOptions) error {
		return update.Apply(fieldmask.FromPaths(paths...), structRequest(), opts.update...)
	}},
	{name: "ApplyUpdateMask/PathValidationDisabled", options: true, run: func(paths []string, opts limitOptions) error {
		return update.Apply(fieldmask.FromPaths(paths...), structRequest(),
			append([]update.Option{update.WithPathValidation(update.PathValidationDisabled)}, opts.update...)...)
	}},
	{name: "ValidateStrictUpdateMask", options: true, run: func(paths []string, opts limitOptions) error {
		_, err := update.ValidateStrict(fieldmask.FromPaths(paths...), structRequest(), opts.update...)
		return err
	}},
	{name: "MergePaths", options: true, run: func(paths []string, opts limitOptions) error {
		return update.MergePaths(structRequest(), structRequest(), paths, opts.update...)
	}},
	{name: "Merge", options: true, run: func(paths []string, opts limitOptions) error {
		return update.Merge(fieldmask.FromPaths(paths...), structRequest(), structRequest(), opts.update...)
	}},
	{name: "Plan", options: true, run: func(paths []string, opts limitOptions) error {
		spec, err := update.NewSpec(&testpb.UpdateRequest{}, opts.update...)
		if err != nil {
			return err
		}

		_, err = spec.Plan(structRequest(), &fieldmaskpb.FieldMask{Paths: paths})

		return err
	}},
}

// TestMaskLimits pins the three mask limits in every entry point: the
// defaults, a configured limit, and zero as unlimited.
func TestMaskLimits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		paths   []string
		opts    limitOptions
		custom  bool // the case needs the options
		wantErr error
	}{
		{name: "depth at the default", paths: []string{deepPath(fieldmask.DefaultMaxPathDepth)}},
		{name: "depth over the default", paths: []string{deepPath(fieldmask.DefaultMaxPathDepth + 1)}, wantErr: fieldmask.ErrMaxPathDepthExceeded},
		{name: "depth unlimited", paths: []string{deepPath(100)}, opts: limitOf(fieldmask.WithMaxPathDepth(0), update.WithMaxPathDepth(0)), custom: true},
		{name: "depth configured", paths: []string{deepPath(4)}, opts: limitOf(fieldmask.WithMaxPathDepth(3), update.WithMaxPathDepth(3)), custom: true,
			wantErr: fieldmask.ErrMaxPathDepthExceeded},
		{name: "quoted dots are one segment", paths: []string{"extra_data.`a.b.c.d`"}, opts: limitOf(fieldmask.WithMaxPathDepth(2), update.WithMaxPathDepth(2)),
			custom: true},
		{name: "paths at the default", paths: manyPaths(fieldmask.DefaultMaxPaths)},
		{name: "paths over the default", paths: manyPaths(fieldmask.DefaultMaxPaths + 1), wantErr: fieldmask.ErrMaxPathsExceeded},
		{name: "paths unlimited", paths: manyPaths(fieldmask.DefaultMaxPaths + 1), opts: limitOf(fieldmask.WithMaxPaths(0), update.WithMaxPaths(0)), custom: true},
		{name: "paths configured", paths: manyPaths(3), opts: limitOf(fieldmask.WithMaxPaths(2), update.WithMaxPaths(2)), custom: true,
			wantErr: fieldmask.ErrMaxPathsExceeded},
		{name: "length at the default", paths: []string{longPath(fieldmask.DefaultMaxPathLength)}},
		{name: "length over the default", paths: []string{longPath(fieldmask.DefaultMaxPathLength + 1)}, wantErr: fieldmask.ErrMaxPathLengthExceeded},
		{name: "length unlimited", paths: []string{longPath(4096)}, opts: limitOf(fieldmask.WithMaxPathLength(0), update.WithMaxPathLength(0)), custom: true},
		{name: "length configured", paths: []string{longPath(20)}, opts: limitOf(fieldmask.WithMaxPathLength(16), update.WithMaxPathLength(16)), custom: true,
			wantErr: fieldmask.ErrMaxPathLengthExceeded},
		{name: "negative keeps the default", paths: []string{deepPath(fieldmask.DefaultMaxPathDepth + 1)},
			opts: limitOf(fieldmask.WithMaxPathDepth(-1), update.WithMaxPathDepth(-1)), custom: true, wantErr: fieldmask.ErrMaxPathDepthExceeded},
	}

	for _, tc := range tests {
		for _, ep := range limitEntryPoints {
			if tc.custom && !ep.options {
				continue
			}

			t.Run(tc.name+"/"+ep.name, func(t *testing.T) {
				t.Parallel()

				err := ep.run(tc.paths, tc.opts)
				if tc.wantErr == nil {
					require.NoError(t, err)
					return
				}

				require.ErrorIs(t, err, tc.wantErr)
				require.ErrorIs(t, err, fieldmask.ErrLimitExceeded)
				require.ErrorIs(t, err, fieldmask.ErrInvalidPath)
			})
		}
	}
}

// TestMaskLimitErrorsAreStructured pins where a limit error points: Plan
// names the offending path, or the bare mask field for the path count, and
// the read entry points return a *ValidationError naming the path.
func TestMaskLimitErrorsAreStructured(t *testing.T) {
	t.Parallel()

	spec, err := update.NewSpec(&testpb.UpdateRequest{}, update.WithMaxPaths(2), update.WithMaxPathDepth(3))
	require.NoError(t, err)

	_, err = spec.Plan(structRequest(), &fieldmaskpb.FieldMask{Paths: []string{"description", deepPath(4)}})
	fe, ok := errors.AsType[*coreerrs.FieldError](err)
	require.True(t, ok, "want a FieldError, got %v", err)
	require.Equal(t, "updateMask.paths[1]", fe.Field)

	mp, ok := errors.AsType[*update.MaskPathError](err)
	require.True(t, ok)
	require.Equal(t, 1, mp.Index)
	require.Equal(t, deepPath(4), mp.Path)
	require.ErrorIs(t, mp.Err, fieldmask.ErrMaxPathDepthExceeded)

	_, err = spec.Plan(structRequest(), &fieldmaskpb.FieldMask{Paths: manyPaths(3)})
	fe, ok = errors.AsType[*coreerrs.FieldError](err)
	require.True(t, ok)
	require.Equal(t, "updateMask", fe.Field)

	mp, ok = errors.AsType[*update.MaskPathError](err)
	require.True(t, ok)
	require.Equal(t, -1, mp.Index)
	require.Empty(t, mp.Path)
	require.Empty(t, mp.Field)
	require.ErrorIs(t, err, fieldmask.ErrMaxPathsExceeded)

	_, err = fieldmask.ParsePaths(deepPath(fieldmask.DefaultMaxPathDepth + 1))
	requireInvalidPath(t, err, deepPath(fieldmask.DefaultMaxPathDepth+1))

	md := (&testpb.UpdateRequest{}).ProtoReflect().Descriptor()
	require.Empty(t, fieldmask.ResolvePath(md, deepPath(4), fieldmask.WithMaxPathDepth(3)))
	require.Empty(t, update.ResolvePath(md, deepPath(4), update.WithMaxPathDepth(3)))
	require.Equal(t, deepPath(3), fieldmask.ResolvePath(md, deepPath(3), fieldmask.WithMaxPathDepth(3)))
}

// TestSetFieldsStopAtTheDepthLimit pins that FromSetFields and FromMessage
// stop a recursive message at DefaultMaxPathDepth segments, and the
// policy-driven implied masks at WithMaxPathDepth, keeping the cut path.
func TestSetFieldsStopAtTheDepthLimit(t *testing.T) {
	t.Parallel()

	settings := &fbpb.Settings{Theme: "leaf"}
	for range 40 {
		settings = &fbpb.Settings{Defaults: settings}
	}

	msg := &fbpb.Account{Settings: settings}

	for _, paths := range [][]string{fieldmask.FromSetFields[[]string](msg), fieldmask.FromMessage[[]string](msg)} {
		deepest := 0
		for _, p := range paths {
			deepest = max(deepest, strings.Count(p, ".")+1)
		}

		require.Equal(t, fieldmask.DefaultMaxPathDepth, deepest)
		require.NoError(t, fieldmask.FromPaths(paths...).Validate(msg))
	}

	catalog := &testpb.Catalog{ShippingAddress: &testpb.Shelf{Street: "main", Position: 2}}

	got := update.FromSetFields(catalog, update.WithMaxPathDepth(1))
	require.Equal(t, []string{"shipping_address"}, got.ToPaths())
	require.Equal(t, []string{"shipping_address"}, update.ImpliedPaths(catalog, update.WithMaxPathDepth(1)))
	require.ElementsMatch(t, []string{"shipping_address.street", "shipping_address.position"}, update.ImpliedPaths(catalog))
}
