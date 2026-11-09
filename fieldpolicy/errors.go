// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldpolicy

import "errors"

// ErrMalformedPattern indicates an [AllowList] entry that is empty, has an empty dotted segment or puts a star inside a
// segment; [AllowList.Validate] returns it wrapped with the entry.
var ErrMalformedPattern = errors.New("malformed field pattern")
