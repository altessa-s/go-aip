// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package maskpath

import (
	"errors"
	"fmt"
	"math"
)

// ErrInvalidPath reports a path that does not address a valid field. The
// fieldmask package exports it; every path error of the field mask packages
// matches it.
var ErrInvalidPath = errors.New("fieldmask: invalid path")

// Limit errors, exported by the fieldmask package. Each matches
// [ErrLimitExceeded] and [ErrInvalidPath]; the entry points wrap them in their
// structured errors.
var (
	// ErrLimitExceeded matches every limit error below.
	ErrLimitExceeded = errors.New("fieldmask: mask limit exceeded")
	// ErrMaxPathDepthExceeded reports a path with more segments than
	// [Limits.MaxPathDepth] allows.
	ErrMaxPathDepthExceeded error = &limitError{msg: "fieldmask: path depth exceeds maximum allowed"}
	// ErrMaxPathsExceeded reports a mask with more paths than
	// [Limits.MaxPaths] allows.
	ErrMaxPathsExceeded error = &limitError{msg: "fieldmask: path count exceeds maximum allowed"}
	// ErrMaxPathLengthExceeded reports a path longer than
	// [Limits.MaxPathLength] allows.
	ErrMaxPathLengthExceeded error = &limitError{msg: "fieldmask: path length exceeds maximum allowed"}
)

// limitError is a limit sentinel: it matches [ErrLimitExceeded] and
// [ErrInvalidPath].
type limitError struct{ msg string }

func (e *limitError) Error() string { return e.msg }

func (e *limitError) Unwrap() []error { return []error{ErrLimitExceeded, ErrInvalidPath} }

// Limits bounds the work a client mask can cause. A limit of zero is
// unlimited.
type Limits struct {
	MaxPaths      int // the paths of a mask
	MaxPathLength int // the bytes of a raw path
	MaxPathDepth  int // the segments of a path, "*" and quoted keys included
}

// Violation is a mask that breaks a limit: the offending path at Index (-1
// for the path count), the limit sentinel and the reason.
type Violation struct {
	Index  int
	Path   string
	Cause  error
	Reason string
}

// CheckMask reports the first limit paths break, cheapest check first and
// before any parsing: the path count, then the length and the depth of each
// path. It returns nil when every limit holds.
func (l Limits) CheckMask(paths []string) *Violation {
	if l.MaxPaths > 0 && len(paths) > l.MaxPaths {
		return &Violation{Index: -1, Cause: ErrMaxPathsExceeded,
			Reason: fmt.Sprintf("the mask has %d paths, more than the limit of %d", len(paths), l.MaxPaths)}
	}

	for i, p := range paths {
		if v := l.CheckPath(p); v != nil {
			v.Index = i
			return v
		}
	}

	return nil
}

// CheckPath reports the limit the raw path breaks, its length before its
// depth, or nil.
func (l Limits) CheckPath(raw string) *Violation {
	if l.MaxPathLength > 0 && len(raw) > l.MaxPathLength {
		return &Violation{Path: raw, Cause: ErrMaxPathLengthExceeded,
			Reason: fmt.Sprintf("the path is %d bytes long, more than the limit of %d", len(raw), l.MaxPathLength)}
	}

	if l.MaxPathDepth > 0 {
		if n := Depth(raw); n > l.MaxPathDepth {
			return &Violation{Path: raw, Cause: ErrMaxPathDepthExceeded,
				Reason: fmt.Sprintf("the path has %d segments, more than the limit of %d", n, l.MaxPathDepth)}
		}
	}

	return nil
}

// Depth counts the segments of raw by its separators outside backticks,
// without parsing it: a malformed path is counted as written.
func Depth(raw string) int {
	n, quoted := 1, false

	for i := range len(raw) {
		switch raw[i] {
		case '`':
			quoted = !quoted
		case '.':
			if !quoted {
				n++
			}
		}
	}

	return n
}

// DepthRoom returns the number of segments a path built from a schema may
// still hold under the depth limit maxDepth (zero is unlimited).
func DepthRoom(maxDepth int) int {
	if maxDepth <= 0 {
		return math.MaxInt
	}

	return maxDepth
}
