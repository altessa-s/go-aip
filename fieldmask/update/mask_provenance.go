// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import (
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// maskOrigin records the client entry that produced a sanitized mask entry.
// Several entries can share an origin when an excluded key pins a wildcard.
type maskOrigin struct {
	index int
	path  string
}

type maskProvenance []maskOrigin

func originalMaskProvenance(raw *fieldmaskpb.FieldMask) maskProvenance {
	origins := make(maskProvenance, len(raw.GetPaths()))
	for i, path := range raw.GetPaths() {
		origins[i] = maskOrigin{index: i, path: path}
	}

	return origins
}

// translate is the common exit for deferred errors. Only mask coordinates
// change: resource-field wrappers, reasons and classification causes survive.
// An identity table is used for errors already expressed in client coordinates,
// including Patch callbacks and the final boundary guard.
func (p maskProvenance) translate(err error, maskField string) error {
	switch e := err.(type) {
	case *coreerrs.FieldError:
		switch e.Err.(type) {
		case *coreerrs.FieldError, *MaskPathError:
		default:
			return err
		}

		cloned := *e

		cloned.Err = p.translate(e.Err, maskField)
		if cloned.Err == e.Err {
			return err
		}

		if mp, ok := e.Err.(*MaskPathError); ok && e.Field == maskPathField(maskField, mp.Index) {
			translated, ok := cloned.Err.(*MaskPathError)
			if ok {
				cloned.Field = maskPathField(maskField, translated.Index)
			}
		}

		return &cloned
	case *MaskPathError:
		if e.Index < 0 || e.Index >= len(p) {
			return err
		}

		if e.Index == p[e.Index].index && e.Path == p[e.Index].path {
			return err
		}

		cloned := *e
		cloned.Index, cloned.Path = p[e.Index].index, p[e.Index].path

		return &cloned
	default:
		return err
	}
}

// checkLimits distinguishes a raw mask count from a count increased by
// OUTPUT_ONLY wildcard pinning. The latter belongs to an expanding client path,
// not to the count of paths the client sent. Pinning emits each origin together.
func (p maskProvenance) checkLimits(raw *fieldmaskpb.FieldMask, policy *options) error {
	v := policy.limits().CheckMask(raw.GetPaths())
	if v == nil {
		return nil
	}

	if v.Index < 0 {
		for i := 1; i < len(p); i++ {
			if p[i] == p[i-1] {
				v.Index, v.Path = i, raw.GetPaths()[i]
				v.Reason = "OUTPUT_ONLY sanitization expands the mask: " + v.Reason

				break
			}
		}
	}

	return limitFieldError(v)
}
