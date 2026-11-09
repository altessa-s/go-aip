// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package resourcename

import (
	"fmt"
	"strings"
)

// fullNamePrefix starts every full resource name.
const fullNamePrefix = "//"

// IsFullName reports whether name is written as a full resource name
// ("//service/relative").
func IsFullName(name string) bool { return strings.HasPrefix(name, fullNamePrefix) }

// ParseFullName splits a full resource name of AIP-122, such as
// "//library.googleapis.com/publishers/123/books/les-miserables", into its
// service name ("library.googleapis.com") and relative resource name
// ("publishers/123/books/les-miserables"). The service must be a DNS name
// and the relative name non-empty without empty segments; it is not matched
// against any pattern. Errors are a *[NameError] matching
// [ErrInvalidFullName].
func ParseFullName(full string) (service, name string, err error) {
	rest, ok := strings.CutPrefix(full, fullNamePrefix)
	if !ok {
		return "", "", nameError(ErrInvalidFullName, -1, `a full resource name starts with "//"`)
	}

	service, name, ok = strings.Cut(rest, "/")
	if !ok || name == "" {
		return "", "", nameError(ErrInvalidFullName, -1, "a full resource name holds a service and a relative name")
	}

	if !validServiceName(service) {
		return "", "", nameError(ErrInvalidFullName, -1, "the service of a full resource name is not a DNS name")
	}

	if err := checkSegments(name); err != nil {
		return "", "", err
	}

	return service, name, nil
}

// FullName joins a service name and a relative resource name into a full
// resource name: FullName("library.googleapis.com", "publishers/123") is
// "//library.googleapis.com/publishers/123". Errors match
// [ErrInvalidFullName].
func FullName(service, name string) (string, error) {
	if !validServiceName(service) {
		return "", fmt.Errorf("%w: service %q is not a DNS name", ErrInvalidFullName, service)
	}

	if name == "" || IsFullName(name) {
		return "", fmt.Errorf("%w: %q is not a relative resource name", ErrInvalidFullName, name)
	}

	if err := checkSegments(name); err != nil {
		return "", err
	}

	return fullNamePrefix + service + "/" + name, nil
}

// checkSegments refuses a relative name with an empty segment.
func checkSegments(name string) error {
	i := 0

	for seg := range strings.SplitSeq(name, "/") {
		if seg == "" {
			return nameError(ErrInvalidFullName, i, fmt.Sprintf("segment %d of the relative name is empty", i))
		}

		i++
	}

	return nil
}

// validServiceName reports whether s is a DNS name (RFC 1123): dot-separated
// labels of 1 to 63 letters, digits and hyphens, neither starting nor ending
// with a hyphen, at most 253 characters in total.
func validServiceName(s string) bool {
	const (
		maxName  = 253
		maxLabel = 63
	)

	if s == "" || len(s) > maxName {
		return false
	}

	for label := range strings.SplitSeq(s, ".") {
		if label == "" || len(label) > maxLabel || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}

		for i := range len(label) {
			if c := label[i]; !isLower(c) && !isUpper(c) && !isDigit(c) && c != '-' {
				return false
			}
		}
	}

	return true
}
