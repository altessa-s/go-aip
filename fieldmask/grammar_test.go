// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldmask_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/fieldmask/update"

	pb "github.com/altessa-s/go-aip/internal/proto/gen/fieldmasktest/v1"
)

// newCatalog returns a Catalog with every collection populated.
func newCatalog() *pb.Catalog {
	return &pb.Catalog{
		DisplayName:     "c",
		ShippingAddress: &pb.Shelf{DisplayName: "addr", Street: "main"},
		Shelves:         []*pb.Shelf{{DisplayName: "s0", Street: "a"}, {DisplayName: "s1", Street: "b"}},
		Tags:            []string{"t0", "t1"},
		ShelvesById:     map[int32]*pb.Shelf{0: {DisplayName: "zero"}, 7: {DisplayName: "seven"}},
		NotesById:       map[int64]string{1: "one"},
		Codes:           map[uint32]string{3: "three"},
		Flags:           map[bool]string{true: "yes"},
		ShelvesByName:   map[string]*pb.Shelf{"x`y.z": {DisplayName: "odd"}, "plain": {DisplayName: "p"}},
	}
}

// requireInvalidPath asserts that err is a *ValidationError naming path and
// classified as ErrInvalidPath.
func requireInvalidPath(t *testing.T, err error, path string) {
	t.Helper()

	ve, ok := errors.AsType[*fieldmask.ValidationError](err)
	require.True(t, ok, "want *ValidationError, got %v", err)
	require.Equal(t, path, ve.Path)
	require.ErrorIs(t, err, fieldmask.ErrInvalidPath)
}

// TestGrammarRejectsInvalidPathsEverywhere pins that a path the grammar
// rejects is INVALID_ARGUMENT on every entry point, read and write alike, and
// that the message is left untouched.
func TestGrammarRejectsInvalidPathsEverywhere(t *testing.T) {
	t.Parallel()

	paths := []string{
		"tags.0",                    // index into a repeated scalar field
		"shelves.0",                 // index into a repeated message field
		"shelves.0.street",          // index then a field
		"shelves.street",            // implicit per-element form, no "*"
		"shelves.displayName",       // implicit form with a JSON name
		"display_name.*",            // wildcard after a scalar
		"shipping_address.*",        // wildcard after a singular message
		"tags.*.x",                  // field beyond a scalar list wildcard
		"codes.3.x",                 // field beyond a scalar map value
		"shelves_by_id.abc",         // non-numeric int32 key
		"shelves_by_id.07",          // non-canonical int32 key
		"shelves_by_id.99999999999", // int32 key out of range
		"codes.-1",                  // negative uint32 key
		"flags.TRUE",                // non-canonical bool key
		"flags.1",                   // bool key as a number
		"nonexistent",               // unknown field
		"shipping_address.nope",     // unknown nested field
		"display_name.x",            // field of a scalar
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			mask := fieldmask.FromPaths(path)
			want := newCatalog()

			requireInvalidPath(t, mask.Validate(&pb.Catalog{}), path)

			_, err := mask.Resolve((&pb.Catalog{}).ProtoReflect().Descriptor())
			requireInvalidPath(t, err, path)

			for name, apply := range map[string]func(*pb.Catalog) error{
				"Filter": func(m *pb.Catalog) error { return mask.Filter(m) },
				"Prune":  func(m *pb.Catalog) error { return mask.Prune(m) },
				"Select": func(m *pb.Catalog) error { return mask.Select(m) },
				"ApplyUpdateMask": func(m *pb.Catalog) error {
					return update.Apply(fieldmask.FromPaths(path), m)
				},
			} {
				msg := newCatalog()
				requireInvalidPath(t, apply(msg), path)
				require.True(t, proto.Equal(want, msg), "%s mutated the message", name)
			}

			_, err = update.ValidateStrict(fieldmask.FromPaths(path), newCatalog())
			require.ErrorIs(t, err, fieldmask.ErrInvalidPath)
		})
	}
}

// TestGrammarAcceptsValidPaths pins the paths the grammar accepts: declared,
// JSON and snake_case spellings, "*" over repeated and map fields, map keys
// of every key kind in canonical form (a numeric segment after a map is a
// key, not an index), quoted keys and the entries of a Struct.
func TestGrammarAcceptsValidPaths(t *testing.T) {
	t.Parallel()

	md := (&pb.Catalog{}).ProtoReflect().Descriptor()

	for path, want := range map[string]string{
		"display_name":                    "display_name",
		"displayName":                     "display_name",
		"shippingAddress.street":          "shipping_address.street",
		"shelves":                         "shelves",
		"shelves.*":                       "shelves.*",
		"shelves.*.displayName":           "shelves.*.display_name",
		"tags.*":                          "tags.*",
		"shelves_by_id.0":                 "shelves_by_id.0",
		"shelves_by_id.-7.street":         "shelves_by_id.-7.street",
		"shelvesById.*.displayName":       "shelves_by_id.*.display_name",
		"notes_by_id.99999999999":         "notes_by_id.99999999999",
		"codes.4294967295":                "codes.4294967295",
		"flags.false":                     "flags.false",
		"shelves_by_name.0":               "shelves_by_name.0",
		"shelves_by_name.`x``y.z`.street": "shelves_by_name.`x``y.z`.street",
		"shelves_by_name.`plain`":         "shelves_by_name.plain",
		"shelves_by_name.``":              "shelves_by_name.``",
		"extra_data":                      "extra_data",
		"extra_data.*":                    "extra_data.*",
		"extra_data.any":                  "extra_data.any",
		"extraData.`a.b`":                 "extra_data.`a.b`",
		"*":                               "*",
	} {
		got, err := fieldmask.FromPaths(path).Resolve(md)
		require.NoError(t, err, path)
		require.Equal(t, []string{want}, got.ToPaths(), path)
		require.NoError(t, fieldmask.FromPaths(path).Validate(&pb.Catalog{}), path)
	}
}

// TestGrammarFilterSpellingsAndWildcards pins that Filter and Prune apply a
// mask by any spelling Resolve accepts, and that "*" selects every element.
func TestGrammarFilterSpellingsAndWildcards(t *testing.T) {
	t.Parallel()

	msg := newCatalog()
	require.NoError(t, fieldmask.FromPaths("shippingAddress.street", "shelves.*.displayName", "shelvesById.7").Filter(msg))
	require.True(t, proto.Equal(&pb.Shelf{Street: "main"}, msg.GetShippingAddress()))
	require.Equal(t, []string{"s0", "s1"}, []string{msg.GetShelves()[0].GetDisplayName(), msg.GetShelves()[1].GetDisplayName()})
	require.Empty(t, msg.GetShelves()[0].GetStreet())
	require.Len(t, msg.GetShelvesById(), 1)
	require.Empty(t, msg.GetTags())

	msg = newCatalog()
	require.NoError(t, fieldmask.FromPaths("shelves.*.street", "shelvesByName.`x``y.z`").Prune(msg))
	require.Empty(t, msg.GetShelves()[1].GetStreet())
	require.Equal(t, "s1", msg.GetShelves()[1].GetDisplayName())
	require.NotContains(t, msg.GetShelvesByName(), "x`y.z")
	require.Contains(t, msg.GetShelvesByName(), "plain")

	msg = newCatalog()
	require.NoError(t, fieldmask.FromPaths("*").Filter(msg))
	require.True(t, proto.Equal(newCatalog(), msg), `Filter "*" keeps everything`)

	require.NoError(t, fieldmask.FromPaths("*").Prune(msg))
	require.True(t, proto.Equal(&pb.Catalog{}, msg), `Prune "*" clears everything`)

	require.ErrorIs(t, fieldmask.FromPaths("*", "tags").Filter(newCatalog()), fieldmask.ErrWildcardNotAlone)
}

// TestParsePathsRejectsMalformedRawPaths pins that malformed raw paths are
// rejected before normalization, naming the path as written, and that
// FromPaths never widens or drops them.
func TestParsePathsRejectsMalformedRawPaths(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"",
		"shipping_address.",
		".shipping_address",
		"shipping_address..street",
		"shelves_by_name.`plain",
		"shelves_by_name.pl`ain",
		"shelves_by_name.`pl`ain",
		"*.display_name",
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			_, err := fieldmask.ParsePaths("display_name", path)
			requireInvalidPath(t, err, path)

			_, err = fieldmask.FromProtoFieldMask(&fieldmaskpb.FieldMask{Paths: []string{path}})
			requireInvalidPath(t, err, path)

			if path == "" {
				return
			}

			mask := fieldmask.FromPaths(path)
			require.False(t, mask.Contains("shipping_address"), "a malformed path must not widen to its parent")
			require.Error(t, mask.Validate(&pb.Catalog{}))

			msg := newCatalog()
			require.Error(t, update.Apply(mask, msg))
			require.True(t, proto.Equal(newCatalog(), msg))
		})
	}

	got, err := fieldmask.ParsePaths("shelves_by_name.`x``y.z`.street", "display_name")
	require.NoError(t, err)
	require.Equal(t, []string{"display_name", "shelves_by_name.`x``y.z`.street"}, got.ToPaths())
}

// TestWildcardAlone pins that "*" combined with anything, another "*"
// included, is rejected by every raw-path entry point, while a FieldMask (a
// set) holds a repeated "*" once.
func TestWildcardAlone(t *testing.T) {
	t.Parallel()

	spec, err := update.NewSpec(&pb.Catalog{})
	require.NoError(t, err)

	for _, paths := range [][]string{{"*", "*"}, {"*", "display_name"}, {"display_name", "*"}} {
		_, err := fieldmask.ParsePaths(paths...)
		require.ErrorIs(t, err, fieldmask.ErrWildcardNotAlone, paths)
		requireInvalidPath(t, err, "*")

		_, err = spec.Plan(&pb.Catalog{}, &fieldmaskpb.FieldMask{Paths: paths})
		require.ErrorIs(t, err, fieldmask.ErrWildcardNotAlone, paths)
	}

	_, err = fieldmask.FromPaths("*", "display_name").SelectsAll()
	require.ErrorIs(t, err, fieldmask.ErrWildcardNotAlone)
	requireInvalidPath(t, err, "*")

	all, err := fieldmask.FromPaths("*", "*").SelectsAll()
	require.NoError(t, err)
	require.True(t, all)

	mask, err := fieldmask.ParsePaths("*")
	require.NoError(t, err)

	all, err = mask.SelectsAll()
	require.NoError(t, err)
	require.True(t, all)
}

// TestMapKeyEscapingRoundTrip pins that a map key holding a dot or a backtick
// is written quoted with the backtick doubled and parses back unchanged.
func TestMapKeyEscapingRoundTrip(t *testing.T) {
	t.Parallel()

	paths := fieldmask.FromSetFields[[]string](&pb.Catalog{ShelvesByName: map[string]*pb.Shelf{"x`y.z": {}, "": {}, "a`b": {}}})
	require.ElementsMatch(t, []string{"shelves_by_name", "shelves_by_name.`x``y.z`", "shelves_by_name.``", "shelves_by_name.`a``b`"}, paths)

	keys := slices.DeleteFunc(slices.Clone(paths), func(p string) bool { return p == "shelves_by_name" })
	mask, err := fieldmask.ParsePaths(keys...)
	require.NoError(t, err)
	require.ElementsMatch(t, keys, mask.ToPaths())
	require.True(t, mask.Contains("shelves_by_name.`x``y.z`"))

	msg := &pb.Catalog{ShelvesByName: map[string]*pb.Shelf{"x`y.z": {DisplayName: "keep"}, "xy.z": {DisplayName: "drop"}}}
	require.NoError(t, fieldmask.FromPaths("shelves_by_name.`x``y.z`").Filter(msg))
	require.Len(t, msg.GetShelvesByName(), 1)
	require.Equal(t, "keep", msg.GetShelvesByName()["x`y.z"].GetDisplayName())
}

// TestApplyUpdateMaskPathValidationModes pins that update masks are validated
// by default (Enforce) and that Warn and Disabled are explicit opt-outs.
func TestApplyUpdateMaskPathValidationModes(t *testing.T) {
	t.Parallel()

	msg := newCatalog()
	requireInvalidPath(t, update.Apply(fieldmask.FromPaths("display_nam"), msg), "display_nam")
	require.True(t, proto.Equal(newCatalog(), msg))

	var reported []string

	require.NoError(t, update.Apply(fieldmask.FromPaths("display_name", "display_nam"), newCatalog(),
		update.WithPathValidation(update.PathValidationWarn),
		update.WithPathValidationReporter(func(ve *fieldmask.ValidationError) { reported = append(reported, ve.Path) })))
	require.Equal(t, []string{"display_nam"}, reported)

	require.NoError(t, update.Apply(fieldmask.FromPaths("display_nam"), newCatalog(),
		update.WithPathValidation(update.PathValidationDisabled)))

	// An index is rejected even with validation disabled.
	requireInvalidPath(t, update.Apply(fieldmask.FromPaths("shelves.0"), newCatalog(),
		update.WithPathValidation(update.PathValidationDisabled)), "shelves.0")
}
