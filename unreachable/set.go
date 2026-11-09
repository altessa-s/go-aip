// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package unreachable

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
)

// Set collects the names of the unreachable resources of one response. Names
// are deduplicated; [Set.Names] returns them sorted and bounded by
// [WithMaxNames]. A Set is safe for concurrent use; the zero value is not
// usable, create one with [NewSet].
type Set struct {
	mu    sync.Mutex
	names map[string]struct{}
	o     *options
}

// NewSet returns an empty Set. It reads [WithMaxNames] and [WithUpscope];
// the other options only configure [Run].
func NewSet(opts ...Option) *Set {
	return &Set{names: make(map[string]struct{}), o: newOptions(opts...)}
}

// Add records names. Every name must be a service-relative resource name
// (see [ValidateName]); when one is not, Add records none of them and returns
// an error matching [ErrInvalidName].
func (s *Set) Add(names ...string) error {
	for _, n := range names {
		if err := ValidateName(n); err != nil {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, n := range names {
		s.names[n] = struct{}{}
	}

	return nil
}

// Len returns the number of distinct names recorded, before any limit.
func (s *Set) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.names)
}

// Names returns the names for the unreachable response field: sorted, so a
// response is deterministic (AIP-217 gives the order no meaning), and at most
// [WithMaxNames] of them. Over the limit the names are up-scoped with
// [WithUpscope] first, then truncated to the lexicographically first ones;
// truncated reports the truncation. An empty Set returns nil.
func (s *Set) Names() (names []string, truncated bool) {
	s.mu.Lock()
	all := slices.Sorted(maps.Keys(s.names))
	s.mu.Unlock()

	return bound(all, s.o)
}

// Response returns the names for the unreachable response field, or, when
// partialSuccess (the request's return_partial_success) is false and a name
// was recorded, an *[Error] listing them, which fails the request as
// UNAVAILABLE. An API without a return_partial_success field passes true.
func (s *Set) Response(partialSuccess bool) ([]string, error) {
	names, truncated := s.Names()
	if len(names) == 0 || partialSuccess {
		return names, nil
	}

	return nil, &Error{Names: names, Truncated: truncated}
}

// bound applies the name limit of o to the sorted names.
func bound(names []string, o *options) ([]string, bool) {
	if o.maxNames <= 0 || len(names) <= o.maxNames {
		return names, false
	}

	if o.upscope != nil {
		scoped := make(map[string]struct{}, len(names))

		for _, n := range names {
			if up := o.upscope(n); ValidateName(up) == nil {
				n = up
			}

			scoped[n] = struct{}{}
		}

		names = slices.Sorted(maps.Keys(scoped))
		if len(names) <= o.maxNames {
			return names, false
		}
	}

	return slices.Clip(names[:o.maxNames]), true
}

// ValidateName reports whether name is a service-relative resource name as
// AIP-217 requires for the unreachable field: a "/"-separated path of at
// least two non-empty segments, not a full resource name ("//service/..."),
// not a URI ("https://...") and not a bare resource ID. The error matches
// [ErrInvalidName].
func ValidateName(name string) error {
	reason := ""

	switch {
	case name == "":
		reason = "is empty"
	case strings.HasPrefix(name, "//"):
		reason = "is a full resource name, not a service-relative one"
	case strings.Contains(name, "://"):
		reason = "is a URI, not a service-relative resource name"
	case !strings.Contains(name, "/"):
		reason = "is a resource ID, not a service-relative resource name"
	case strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.Contains(name, "//"):
		reason = "has an empty segment"
	case strings.ContainsFunc(name, func(r rune) bool { return r <= ' ' || r == 0x7f }):
		reason = "holds whitespace or a control character"
	default:
		return nil
	}

	return fmt.Errorf("%w: %q %s", ErrInvalidName, name, reason)
}
