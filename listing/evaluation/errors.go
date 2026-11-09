// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluation

import (
	"errors"
	"fmt"
)

// ErrNonFiniteFloat reports a stored NaN or infinity in a float field: the listing contract requires finite floats, so the record
// cannot be evaluated. The value comes from storage, not from the request.
var ErrNonFiniteFloat = errors.New("evaluation: stored float is not finite")

// nonFiniteFloatError names the field whose stored value is not finite; it wraps [ErrNonFiniteFloat].
type nonFiniteFloatError struct{ field string }

func (e *nonFiniteFloatError) Error() string {
	return fmt.Sprintf("field %q contains a non-finite float", e.field)
}

func (e *nonFiniteFloatError) Unwrap() error { return ErrNonFiniteFloat }
