// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldmask

// Union combines two masks into one containing all fields from both.
// Fields in both masks are recursively combined.
//
// Example:
//
//	combined := mask1.Union(mask2)
func (msk FieldMask) Union(other FieldMask) FieldMask {
	if len(msk) == 0 {
		return other.Clone()
	} else if len(other) == 0 {
		return msk.Clone()
	}

	// Pre-allocate result map with capacity for union of both masks
	capacity := len(msk) + len(other)
	result := make(FieldMask, capacity)

	// Copy all fields from the first mask
	for field, nested := range msk {
		if nested == nil {
			result[field] = nil
		} else {
			result[field] = nested.Clone()
		}
	}

	// Add or merge fields from the second mask
	for field, otherNested := range other {
		resultNested, exists := result[field]
		switch {
		case !exists:
			// Field only exists in the second mask
			if otherNested == nil {
				result[field] = nil
				continue
			}

			result[field] = otherNested.Clone()
		case resultNested == nil || otherNested == nil:
			// If either mask has a nil value for this field, it's a leaf field
			result[field] = nil
		default:
			result[field] = resultNested.Union(otherNested)
		}
	}

	return result
}

// Intersection creates a mask containing only fields that exist in both masks.
// Fields are recursively intersected.
//
// Example:
//
//	common := mask1.Intersection(mask2)
func (msk FieldMask) Intersection(other FieldMask) FieldMask {
	if len(msk) == 0 || len(other) == 0 {
		return make(FieldMask)
	}

	// Create a new mask for the result
	result := make(FieldMask)

	for field, nested := range msk {
		otherNested, exists := other[field]
		if !exists {
			// The field doesn't exist in the other mask
			continue
		}

		switch {
		case nested == nil && otherNested == nil:
			// Both are leaves (or represent full subtrees implicitly)
			result[field] = nil
		case nested == nil:
			result[field] = otherNested.Clone()
		case otherNested == nil:
			result[field] = nested.Clone()
		default:
			// Both masks specify nested fields, find their intersection recursively.
			intersection := nested.Intersection(otherNested)
			// Only add the field to the result if the sub-intersection is non-empty.
			if len(intersection) > 0 {
				result[field] = intersection
			}
			// If intersection is empty, do nothing (correct for intersection).
		}
	}

	return result
}

// Difference returns paths in the receiver but NOT in the other mask.
// Does not support "all except specific parts" for subtrees.
//
// Example:
//
//	diff := mask1.Difference(mask2)
func (msk FieldMask) Difference(other FieldMask) FieldMask {
	if len(msk) == 0 {
		return make(FieldMask) // Difference from empty set is empty
	} else if len(other) == 0 {
		return msk.Clone() // Difference from empty set is the original set
	}

	result := make(FieldMask)

	for field, nested1 := range msk {
		nested2, existsInOther := other[field]

		if !existsInOther && nested1 == nil {
			result[field] = nil
			continue
		} else if !existsInOther {
			result[field] = nested1.Clone() // Clone needed for nested maps
			continue
		}

		if nested1 != nil && nested2 != nil {
			// Both are non-nil, recurse
			subDifference := nested1.Difference(nested2)
			if len(subDifference) > 0 {
				result[field] = subDifference
			}

			continue
		}
	}

	return result
}
