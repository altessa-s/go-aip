// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package resourcename

import (
	"fmt"
	"slices"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// DefaultNameField is the field holding a resource's name (AIP-122) unless
// its descriptor sets name_field.
const DefaultNameField = "name"

// Resource is a resource type of AIP-123 with its name patterns, such as
// "library.googleapis.com/Book" with "publishers/{publisher}/books/{book}".
// A resource may have several patterns (a resource with several parents):
// a name is matched against them in declaration order and the first match
// wins.
//
// A Resource is immutable and safe for concurrent use.
type Resource struct {
	typ       string
	service   string
	kind      string
	nameField string
	singular  string
	plural    string
	patterns  []*Pattern
	// parents are the distinct parent patterns, in pattern order.
	parents []*Pattern
	// rootParent is set when a pattern is top-level: the root is a parent.
	rootParent bool
	// parentLabel names the parent in a mismatch reason.
	parentLabel string
}

// NewResource returns the resource type typ ("{service}/{Kind}") with the
// given patterns, in matching order. The service must be a DNS name and Kind
// an UpperCamelCase identifier; at least one pattern is needed and no two
// patterns may match the same names (AIP-123 requires them to be mutually
// unique). Errors match [ErrInvalidResource] or [ErrInvalidPattern].
func NewResource(typ string, patterns ...string) (*Resource, error) {
	return newResource(&annotations.ResourceDescriptor{Type: typ, Pattern: patterns})
}

// ResourceFromDescriptor returns the resource a google.api.resource or
// google.api.resource_definition annotation declares: its type, patterns,
// name_field, singular and plural.
func ResourceFromDescriptor(rd *annotations.ResourceDescriptor) (*Resource, error) {
	if rd == nil {
		return nil, fmt.Errorf("%w: nil descriptor", ErrInvalidResource)
	}

	return newResource(rd)
}

// ResourceOf returns the resource the google.api.resource annotation of md
// declares. A message without the annotation is [ErrNoResource]; a
// name_field that is not a singular string field of md is
// [ErrInvalidResource].
func ResourceOf(md protoreflect.MessageDescriptor) (*Resource, error) {
	rd := resourceDescriptor(md)
	if rd == nil {
		return nil, fmt.Errorf("%w: %s", ErrNoResource, md.FullName())
	}

	r, err := newResource(rd)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", md.FullName(), err)
	}

	fd := md.Fields().ByName(protoreflect.Name(r.nameField))
	if fd == nil || fd.Kind() != protoreflect.StringKind || fd.Cardinality() == protoreflect.Repeated {
		return nil, fmt.Errorf("%w: %s: name field %q is not a singular string field", ErrInvalidResource, md.FullName(), r.nameField)
	}

	return r, nil
}

// resourceDescriptor returns the google.api.resource annotation of md, or
// nil.
func resourceDescriptor(md protoreflect.MessageDescriptor) *annotations.ResourceDescriptor {
	opts := md.Options()
	if opts == nil || !proto.HasExtension(opts, annotations.E_Resource) {
		return nil
	}

	rd, _ := proto.GetExtension(opts, annotations.E_Resource).(*annotations.ResourceDescriptor)

	return rd
}

func newResource(rd *annotations.ResourceDescriptor) (*Resource, error) {
	service, kind, ok := strings.Cut(rd.GetType(), "/")
	if !ok || !validServiceName(service) || !validKind(kind) {
		return nil, fmt.Errorf("%w: type %q is not {service}/{UpperCamelCase kind}", ErrInvalidResource, rd.GetType())
	}

	if len(rd.GetPattern()) == 0 {
		return nil, fmt.Errorf("%w: type %q has no pattern", ErrInvalidResource, rd.GetType())
	}

	r := &Resource{
		typ: rd.GetType(), service: service, kind: kind,
		nameField: rd.GetNameField(), singular: rd.GetSingular(), plural: rd.GetPlural(),
		patterns:    make([]*Pattern, 0, len(rd.GetPattern())),
		parentLabel: "the parent of " + rd.GetType(),
	}
	if r.nameField == "" {
		r.nameField = DefaultNameField
	}

	shapes := make(map[string]string, len(rd.GetPattern()))
	parents := make(map[string]struct{}, len(rd.GetPattern()))

	for _, raw := range rd.GetPattern() {
		p, err := ParsePattern(raw)
		if err != nil {
			return nil, fmt.Errorf("type %q: %w", rd.GetType(), err)
		}

		if other, dup := shapes[p.shape()]; dup {
			return nil, fmt.Errorf("%w: type %q: patterns %q and %q match the same names", ErrInvalidResource, rd.GetType(), other, raw)
		}

		shapes[p.shape()] = raw
		r.patterns = append(r.patterns, p)

		pp, ok := p.Parent()
		if !ok {
			r.rootParent = true

			continue
		}

		if _, seen := parents[pp.shape()]; !seen {
			parents[pp.shape()] = struct{}{}
			r.parents = append(r.parents, pp)
		}
	}

	return r, nil
}

// validKind reports whether s is an UpperCamelCase type name: an upper-case
// ASCII letter followed by ASCII letters and digits.
func validKind(s string) bool {
	if s == "" || !isUpper(s[0]) {
		return false
	}

	for i := 1; i < len(s); i++ {
		if c := s[i]; !isLower(c) && !isUpper(c) && !isDigit(c) {
			return false
		}
	}

	return true
}

// Type returns the resource type, such as "library.googleapis.com/Book".
func (r *Resource) Type() string { return r.typ }

// Service returns the service name of the type, such as
// "library.googleapis.com".
func (r *Resource) Service() string { return r.service }

// Kind returns the type name without the service, such as "Book".
func (r *Resource) Kind() string { return r.kind }

// NameField returns the field of the resource message holding its name.
func (r *Resource) NameField() string { return r.nameField }

// Singular returns the singular of the descriptor, or "" when unset.
func (r *Resource) Singular() string { return r.singular }

// Plural returns the plural of the descriptor, or "" when unset.
func (r *Resource) Plural() string { return r.plural }

// Patterns returns the patterns in matching order.
func (r *Resource) Patterns() []*Pattern { return slices.Clone(r.patterns) }

// ParentPatterns returns the distinct patterns of the resource's parents in
// pattern order. The root, the parent of a top-level pattern, is not a
// pattern and is reported by [Resource.HasRootParent].
func (r *Resource) ParentPatterns() []*Pattern { return slices.Clone(r.parents) }

// HasRootParent reports whether a pattern of the resource is top-level, so
// the root (an empty parent) is one of its parents.
func (r *Resource) HasRootParent() bool { return r.rootParent }

// Matches reports whether name matches a pattern of the resource.
func (r *Resource) Matches(name string) bool {
	var buf [8]string

	p, _ := r.match(buf[:0], name)

	return p != nil
}

// Match returns the first pattern name matches and its variable values. An
// error is a *[NameError] matching [ErrNoMatch].
func (r *Resource) Match(name string) (*Pattern, map[string]string, error) {
	var buf [8]string

	p, vals := r.match(buf[:0], name)
	if p == nil {
		return nil, nil, noMatch(r.patterns, name, "")
	}

	m := make(map[string]string, len(vals))
	for i, v := range vals {
		m[p.vars[i]] = v
	}

	return p, m, nil
}

// Parent returns the name of the parent of a name of the resource: its
// leading segments matched by the parent pattern. The parent of a name of a
// top-level pattern is the root, "". An error is a *[NameError] matching
// [ErrNoMatch].
func (r *Resource) Parent(name string) (string, error) {
	var buf [8]string

	p, _ := r.match(buf[:0], name)
	if p == nil {
		return "", noMatch(r.patterns, name, "")
	}

	pp, ok := p.Parent()
	if !ok {
		return "", nil
	}

	return prefixSegments(name, len(pp.segs)), nil
}

// match returns the first pattern matching name and its values appended to
// dst, or nil.
func (r *Resource) match(dst []string, name string) (*Pattern, []string) {
	return firstMatch(r.patterns, dst, name)
}

// firstMatch returns the first of ps matching name, skipping by segment
// count, with its values appended to dst; or nil.
func firstMatch(ps []*Pattern, dst []string, name string) (*Pattern, []string) {
	n := segmentCount(name)

	for _, p := range ps {
		if len(p.segs) != n {
			continue
		}

		if vals, ok := p.appendValues(dst, name); ok {
			return p, vals
		}
	}

	return nil, dst
}

// noMatch is the error of a name none of ps matches; what names the
// expectation ("" for the resource itself).
func noMatch(ps []*Pattern, name, what string) *NameError {
	if len(ps) == 1 && what == "" {
		return ps[0].explain(name)
	}

	if what == "" {
		what = "the name"
	}

	if len(ps) == 1 {
		return nameError(ErrNoMatch, -1, fmt.Sprintf("%s does not match pattern %s", what, quoteList(ps)))
	}

	return nameError(ErrNoMatch, -1, fmt.Sprintf("%s matches none of the patterns %s", what, quoteList(ps)))
}

// prefixSegments returns the first n segments of name.
func prefixSegments(name string, n int) string {
	for i := range len(name) {
		if name[i] == '/' {
			n--
			if n == 0 {
				return name[:i]
			}
		}
	}

	return name
}
