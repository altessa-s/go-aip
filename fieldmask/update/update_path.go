// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"fmt"
	"slices"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/internal/behavior"
	"github.com/altessa-s/go-aip/internal/maskmerge"
	"github.com/altessa-s/go-aip/internal/maskpath"
)

// ImpliedPaths returns the implied update mask of resource (AIP-134: an
// omitted mask updates every populated field): the paths of the populated
// LEAF fields, spelled by declared names, ordered by the declaration of their
// top-level field ([FromSetFields] reduced to leaves). A leaf that covers a
// skipped path (a skip inside a well-known type, such as
// "scheduled_at.seconds") is left out whole. Presence follows
// protoreflect: zero scalars, false, empty lists and unset messages are not
// populated.
//
// A singular message field is descended into, so a request that sets only
// filters.keywords implies "filters.keywords" and keeps the other filters; a
// message populated with no populated field implies nothing. Repeated and
// map fields and the well-known types (google.protobuf.*, google.type.*) are
// leaves, replaced as a whole. A set oneof contributes the leaves of its set
// branch. Fields listed in skip (top-level or nested declared paths) and
// server-managed fields (OUTPUT_ONLY, IDENTIFIER, IMMUTABLE) at any depth are
// never included.
//
// A dynamic JSON root uses its populated object keys; a populated list or scalar
// Value implies "*" (whole-root replacement). Empty objects/lists and an unset
// Value imply no paths. Generated and dynamicpb representations behave alike.
func ImpliedPaths(resource proto.Message, opts ...Option) []string {
	s, err := NewSpec(resource, opts...)
	if err != nil {
		return nil
	}

	return impliedPaths(resource, slices.Concat(s.skip, s.opts.impliedSkipFields), s.opts)
}

func impliedPaths(resource proto.Message, skip []string, policy *options) []string {
	if resource == nil {
		return nil
	}

	return maskmerge.ImpliedPaths(resource.ProtoReflect(), skip, policy.behaviors, policy.atomicMessage, policy.maxPathDepth, false)
}

// descends reports whether fd is a singular message field whose subfields
// are update paths of their own: not repeated, not a map and not a
// well-known type (google.protobuf.*, google.type.*), which is a leaf.
func descends(fd protoreflect.FieldDescriptor, policy *options) bool {
	if fd.Message() == nil || fd.IsList() || fd.IsMap() {
		return false
	}

	return policy.atomicMessage == nil || !policy.atomicMessage(fd.Message())
}

// ResolvePath returns the update path path of md spelled by declared
// names, or "" when path is not one. An update path names a field reached
// through singular, non-atomic messages, map keys and the "*" wildcard of a
// repeated or map field (see [Spec.Plan]): any field segment may use its
// declared, JSON or snake_case spelling, a map key must parse as the map's
// key kind and format back to itself ("443" for an integer key, "true" for a
// bool key), and a key holding a dot is quoted in backticks ("labels.`a.b`").
// A trailing "*" names the collection whole and is dropped ("aliases.*"
// resolves to "aliases"). A google.protobuf.Struct is addressed as a
// map<string, Value> ("attrs.color", "attrs.*" resolving to "attrs"), and by
// default the dynamic JSON below by key and "*" ("attrs.work.from",
// "list_data.*.name"; a trailing "*" is dropped there too). A list index
// ("aliases.0", "list_data.0"), a field of the element type spelled without
// "*" ("aliases.display_name", "list_data.name"), a path into a scalar or
// atomic value ("minPrice.units" with [DefaultAtomicMessage]), with
// [WithStrictDynamicPaths] a path below a Struct key or into a
// google.protobuf.Value or ListValue ("attrs.color.hue"), and "*" alone are
// not update paths.
// A path over a limit of the options ([WithMaxPathLength],
// [WithMaxPathDepth]) is not one either.
func ResolvePath(md protoreflect.MessageDescriptor, path string, opts ...Option) string {
	policy := newOptions(opts...)
	if policy.limits().CheckPath(path) != nil {
		return ""
	}

	segs, reason := resolveUpdateSegments(md, path, policy)
	if reason != "" {
		return ""
	}

	return maskpath.Join(segs)
}

// resolveUpdateSegments resolves path, an update path of md (see
// [ResolvePath]), into its segments: field segments spelled by their
// declared names, map keys and "*" as written, a trailing "*" dropped. It
// returns the reason path is not an update path, or "".
func resolveUpdateSegments(md protoreflect.MessageDescriptor, path string, policy *options) ([]string, string) {
	segs, err := maskpath.Parse(path)
	if err != nil {
		return nil, fmt.Sprintf(maskpath.ReasonMalformedPath, path)
	}

	out := make([]string, 0, len(segs))
	lookup := maskpath.Check{Aliases: true}

	for i := 0; i < len(segs); i++ {
		seg := segs[i]
		last := i == len(segs)-1

		if maskpath.IsDynamicMessage(md) {
			return dynamicUpdateSegments(md, out, segs[i:], path, policy.strictDynamicPaths)
		}

		if maskpath.IsWildcard(seg) {
			return nil, fmt.Sprintf(maskpath.ReasonMalformedPath, path)
		}

		fd := lookup.Lookup(md, seg.Text)
		if fd == nil {
			return nil, fmt.Sprintf("unknown path %q", path)
		}

		out = append(out, string(fd.Name()))

		if last {
			break
		}

		switch {
		case fd.IsList() || fd.IsMap():
			i++

			key, reason := elementSegment(fd, segs[i], path)
			if reason != "" {
				return nil, reason
			}

			if i == len(segs)-1 {
				if key != maskpath.Wildcard {
					out = append(out, key)
				}

				return out, ""
			}

			out = append(out, key)

			md = updateElementDescriptor(fd, policy)
			if md == nil {
				return nil, fmt.Sprintf("path %q addresses a field inside the elements of %q, which have no fields to update", path, fd.Name())
			}
		case descends(fd, policy):
			md = fd.Message()
		case fd.Message() != nil:
			return nil, fmt.Sprintf("unknown path %q: %q is replaced as a whole", path, fd.Name())
		default:
			return nil, fmt.Sprintf("unknown path %q", path)
		}
	}

	return out, ""
}

// dynamicUpdateSegments ends the update path path with segs, the segments
// after the google.protobuf.Struct, Value or ListValue md at the resolved
// segments out (see [dynamicTail]; strict for [WithStrictDynamicPaths]).
// Trailing "*" segments name the value whole and are dropped.
func dynamicUpdateSegments(md protoreflect.MessageDescriptor, out []string, segs []maskpath.Segment, path string, strict bool) ([]string, string) {
	tail, reason, _ := maskpath.DynamicTail(md, maskpath.Join(out), segs, strict)
	if reason != "" {
		return nil, fmt.Sprintf("path %q: %s", path, reason)
	}

	out = append(out, tail...)
	for len(out) > 1 && out[len(out)-1] == maskpath.Wildcard {
		out = out[:len(out)-1]
	}

	return out, ""
}

// updateElementDescriptor returns the message a sub-path through a key or
// "*" of the repeated or map field fd walks: [wildcardElementDescriptor], or
// a google.protobuf.Struct, Value or ListValue element the policy does not
// make atomic, whose segments [dynamicTail] checks. Nil for
// scalar and atomic elements.
func updateElementDescriptor(fd protoreflect.FieldDescriptor, policy *options) protoreflect.MessageDescriptor {
	if md := wildcardElementDescriptor(fd, policy); md != nil {
		return md
	}

	if md := maskpath.ValueMessage(fd); md != nil && maskpath.IsDynamicMessage(md) && (policy.atomicMessage == nil || !policy.atomicMessage(md)) {
		return md
	}

	return nil
}

// elementSegment checks seg, the segment of path after the repeated or map
// field fd, and returns it as the mask key: "*" for either, a map key that
// parses as the key kind of fd and formats back to itself. A list index and
// a field name after a repeated field are refused with their reason.
func elementSegment(fd protoreflect.FieldDescriptor, seg maskpath.Segment, path string) (string, string) {
	if maskpath.IsWildcard(seg) {
		return maskpath.Wildcard, ""
	}

	if fd.IsMap() {
		if seg.Text == maskpath.Wildcard {
			return "", fmt.Sprintf("path %q: map %q cannot address the literal key %q", path, fd.Name(), maskpath.Wildcard)
		}

		if _, ok := maskpath.ParseMapKey(fd.MapKey().Kind(), seg.Text); !ok {
			return "", fmt.Sprintf("path %q: %q is not a %s key of map %q", path, seg.Text, fd.MapKey().Kind(), fd.Name())
		}

		return seg.Text, ""
	}

	if fd.Message() == nil {
		return "", fmt.Sprintf("unknown path %q", path)
	}

	if maskpath.IsNumericSegment(seg.Text) {
		return "", fmt.Sprintf("path %q addresses an element of repeated field %q by index; replace the list or use %q",
			path, fd.Name(), string(fd.Name())+maskpath.Separator+maskpath.Wildcard)
	}

	return "", fmt.Sprintf("path %q names a field of the elements of repeated field %q without %q; use %q",
		path, fd.Name(), maskpath.Wildcard, string(fd.Name())+maskpath.Separator+maskpath.Wildcard+maskpath.Separator+behavior.QuoteSegment(seg.Text))
}

// selectsAll reports whether paths is the full-replace mask: exactly the
// one literal path "*" ([fieldmask.FieldMask.SelectsAll]). "*" with any other path
// (another "*" included) is a [coreerrs.FieldError] wrapping
// [fieldmask.ErrWildcardNotAlone], and an empty path or a path with an empty segment
// is one wrapping [fieldmask.ErrInvalidPath] ([maskpath.ReasonMalformedPath]); either names the
// offending path, "updateMask.paths[i]", and holds a *[MaskPathError].
func selectsAll(paths []string) (bool, error) {
	if len(paths) == 1 && paths[0] == maskpath.Wildcard {
		return true, nil
	}

	if i := slices.Index(paths, maskpath.Wildcard); i >= 0 {
		return false, indexedFieldError(fieldmask.ErrWildcardNotAlone, i, maskpath.Wildcard, "", maskpath.ReasonWildcardNotAlone)
	}

	for i, p := range paths {
		if maskpath.CheckSyntax(p) != nil {
			return false, indexedFieldError(fieldmask.ErrInvalidPath, i, p, "", fmt.Sprintf(maskpath.ReasonMalformedPath, p))
		}
	}

	return false, nil
}
