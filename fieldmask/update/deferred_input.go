// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"maps"
	"slices"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/altessa-s/go-aip/fieldmask"
	"github.com/altessa-s/go-aip/internal/behavior"
	"github.com/altessa-s/go-aip/internal/maskpath"
	"github.com/altessa-s/go-aip/internal/maskwalk"
)

// boundaryInput is the single resource source (R0) for bound Spec and Patch
// consumers, including creation where the mask must be ignored. Only standalone
// Complete replays the raw snapshot to preserve its ApplyMask refusal contract.
func (p *DeferredPlan) boundaryInput() (proto.Message, error) {
	input, err := cloneMessage(p.original)
	if err != nil {
		return nil, err
	}

	policy := *p.policy
	policy.current = nil

	if err := clearOutputOnly(input, &policy); err != nil {
		return nil, err
	}

	return input, nil
}

// specInput derives K0 from the boundary-sanitized resource returned by boundaryInput.
// Keep raw order, spellings and covered paths so the Spec checks each surviving
// client path itself, including paths allowed by a permissive boundary. Each
// surviving or expanded path retains its original client index and spelling.
func (p *DeferredPlan) specInput(input proto.Message) (proto.Message, *fieldmaskpb.FieldMask, maskProvenance, error) {
	if p.raw == nil || slices.Contains(p.raw.GetPaths(), maskpath.Wildcard) {
		return input, proto.CloneOf(p.raw), originalMaskProvenance(p.raw), nil
	}

	md := input.ProtoReflect().Descriptor()
	named := fieldmask.FieldMask{}
	selections := make([]fieldmask.FieldMask, len(p.raw.GetPaths()))

	for i, raw := range p.raw.GetPaths() {
		segs, reason := resolveUpdateSegments(md, raw, p.policy)
		if reason != "" {
			continue // the Spec must still see and validate this raw path
		}

		maskwalk.Insert(named, segs)

		selections[i] = fieldmask.FieldMask{}
		maskwalk.Insert(selections[i], segs)
	}

	dropOutputPaths(named, md, p.policy)

	for _, selection := range selections {
		dropOutputPaths(selection, md, p.policy)
	}

	// Pinning ignored keyed exclusions uses only request keys, never stored
	// state. Reuse the writer's representation and its existing limits.
	u := &updateCheck{policy: p.policy, verify: true}
	pairs := []msgPair{{req: input.ProtoReflect()}}
	originalNames := named.Clone()

	for {
		raw, origins, dropped := u.outputPaths(p.raw.GetPaths(), selections, named, md, pairs)
		if u.refusal != nil {
			return nil, nil, nil, u.refusal
		}

		if !dropped {
			return input, raw, origins, nil
		}

		// An inner wildcard can address no entries after its ignored key is
		// excluded. Removing that raw path may empty an outer key's selection.
		// Retain that key as an exclusion and pin the outer wildcard as well.
		// Each repeat removes at least one raw path; none can be reintroduced.
		named = fieldmask.FieldMask{}

		for _, selection := range selections {
			for _, path := range selection.ToPaths() {
				maskwalk.Insert(named, maskpath.Split(path))
			}
		}

		retainOutputKeys(named, originalNames, md)
	}
}

// outputPaths retains each surviving raw path, pinning only wildcards whose
// keyed exclusions would otherwise be lost. It reports paths that became empty
// so the caller can propagate their exclusions to an enclosing collection.
func (u *updateCheck) outputPaths(paths []string, selections []fieldmask.FieldMask, named fieldmask.FieldMask,
	md protoreflect.MessageDescriptor, pairs []msgPair,
) (*fieldmaskpb.FieldMask, maskProvenance, bool) {
	raw := &fieldmaskpb.FieldMask{}

	var origins maskProvenance

	dropped := false

	for i, selection := range selections {
		if selection == nil {
			raw.Paths = append(raw.Paths, paths[i])
			origins = append(origins, maskOrigin{index: i, path: paths[i]})

			continue // preserve paths the boundary allowed without resolving
		}

		if len(selection) == 0 {
			continue
		}

		work := selection.Clone()
		u.pinOutputExclusions(work, named, md, pairs, "")

		if u.refusal != nil {
			return nil, nil, false
		}

		after := work.ToPaths()
		switch {
		case len(after) == 0:
			selections[i] = fieldmask.FieldMask{}
			dropped = true
		case slices.Equal(selection.ToPaths(), after):
			raw.Paths = append(raw.Paths, paths[i])
			origins = append(origins, maskOrigin{index: i, path: paths[i]})
		default:
			raw.Paths = append(raw.Paths, after...)
			for range after {
				origins = append(origins, maskOrigin{index: i, path: paths[i]})
			}
		}
	}

	return raw, origins, dropped
}

// retainOutputKeys carries original map-key exclusions after an ignored nested
// selection disappears. These empty branches are internal, never returned as
// paths or applied to the source resource.
func retainOutputKeys(selection, original fieldmask.FieldMask, md protoreflect.MessageDescriptor) {
	if maskpath.IsDynamicMessage(md) {
		return
	}

	for name, nested := range selection {
		fd := md.Fields().ByName(protoreflect.Name(name))

		child := maskpath.ValueMessage(fd)
		if nested == nil || child == nil {
			continue
		}

		if fd.IsMap() {
			for key := range original[name] {
				if _, exists := nested[key]; !exists && key != maskpath.Wildcard {
					nested[key] = fieldmask.FieldMask{}
				}
			}
		}

		if fd.IsMap() || fd.IsList() {
			for key, sub := range nested {
				retainOutputKeys(sub, original[name][key], child)
			}
		} else {
			retainOutputKeys(nested, original[name], child)
		}
	}
}

func dropOutputPaths(selection fieldmask.FieldMask, md protoreflect.MessageDescriptor, policy *options) {
	if maskpath.IsDynamicMessage(md) {
		return
	}

	for name, nested := range selection {
		fd := md.Fields().ByName(protoreflect.Name(name))
		if fd != nil && (policy.serverManaged(fd) == annotations.FieldBehavior_OUTPUT_ONLY || !keepInputSelection(fd, nested, policy)) {
			delete(selection, name)
		}
	}
}

// pinOutputExclusions makes empty keyed exclusions representable in K0. It does
// not intersect policies or expand protected messages; only an ignored keyed
// path beside a surviving wildcard can require concrete request keys.
func (u *updateCheck) pinOutputExclusions(selection, named fieldmask.FieldMask, md protoreflect.MessageDescriptor,
	pairs []msgPair, prefix string,
) {
	if maskpath.IsDynamicMessage(md) {
		return
	}

	for _, name := range slices.Sorted(maps.Keys(selection)) {
		nested := selection[name]
		fd := md.Fields().ByName(protoreflect.Name(name))

		child := maskpath.ValueMessage(fd)
		if nested == nil || child == nil {
			continue
		}

		path := behavior.JoinPath(prefix, name)
		if !fd.IsList() && !fd.IsMap() {
			u.pinOutputExclusions(nested, named[name], child, childPairs(pairs, fd), path)
		} else {
			for _, key := range slices.Sorted(maps.Keys(nested)) {
				u.pinOutputExclusions(nested[key], named[name][key], child,
					elementPairs(fd, key, named[name], pairs), elementPath(path, key))
			}

			if fd.IsMap() && nested[maskpath.Wildcard] != nil {
				u.pinOutputMap(fd, nested, named[name], pairs, path)
			}
		}

		if len(nested) == 0 {
			delete(selection, name)
		}
	}
}

func (u *updateCheck) pinOutputMap(fd protoreflect.FieldDescriptor, nested, named fieldmask.FieldMask, pairs []msgPair, path string) {
	var placeholders []string

	// The other surviving raw paths will emit these keys themselves. Temporary
	// placeholders keep pinWildcard from treating them as ignored exclusions.
	for key, sub := range named {
		if key == maskpath.Wildcard || (sub != nil && len(sub) == 0) {
			continue
		}

		if _, exists := nested[key]; !exists {
			nested[key] = nil
			placeholders = append(placeholders, key)
		}
	}

	u.pinWildcard(fd, nested, slices.Collect(maps.Keys(named)), pairs, path)

	for _, key := range placeholders {
		delete(nested, key)
	}
}
