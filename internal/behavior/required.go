// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package behavior

import (
	"slices"
	"strconv"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// ReasonRequired explains a REQUIRED field that is absent or set to an empty
// value (AIP-203: a required field must be present and set to a non-empty
// value).
const ReasonRequired = "required field must be set to a non-empty value"

// Resolve returns the field behaviors of a field.
type Resolve func(protoreflect.FieldDescriptor) []annotations.FieldBehavior

// IsEmpty reports whether field fd of m holds no value or a falsy one, the
// values AIP-203 does not accept for a REQUIRED field: an unset field (proto
// presence where the field tracks it), the zero value of a scalar or enum
// (empty string or bytes, 0, false, the zero enum value) whether or not the
// field tracks presence, an empty repeated or map field and an unset message.
// A set message counts as non-empty even when none of its own fields is set.
// A nil m holds nothing.
func IsEmpty(m protoreflect.Message, fd protoreflect.FieldDescriptor) bool {
	if m == nil || !m.IsValid() || !m.Has(fd) {
		return true
	}

	v := m.Get(fd)

	switch {
	case fd.IsList():
		return v.List().Len() == 0
	case fd.IsMap():
		return v.Map().Len() == 0
	case fd.Message() != nil:
		return false
	}

	switch fd.Kind() { //nolint:exhaustive // message and group kinds are handled above.
	case protoreflect.StringKind:
		return v.String() == ""
	case protoreflect.BytesKind:
		return len(v.Bytes()) == 0
	case protoreflect.BoolKind:
		return !v.Bool()
	case protoreflect.EnumKind:
		return v.Enum() == 0
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return v.Int() == 0
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind, protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return v.Uint() == 0
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return v.Float() == 0
	default:
		return false
	}
}

// RequiredViolations reports every REQUIRED field of m, under prefix, that
// [IsEmpty] finds absent or falsy, and descends into every populated message:
// singular sub-messages, repeated message elements (indexed "aliases.2") and
// map message values (keyed `labels.k`). An absent sub-message is not
// descended: the annotations of a nested message are independent of its
// parent's (AIP-203), so its REQUIRED fields apply only when it is present.
//
// Fields that also carry a behavior of skip are not checked nor descended:
// create skips OUTPUT_ONLY and IDENTIFIER, which are not client input; an
// update also skips IMMUTABLE, whose stored value an absent one keeps.
// A REQUIRED member of a real oneof is satisfied by any member of its oneof:
// it is reported when the oneof holds no member, or holds this member with an
// empty value. Map entries are visited in key order, so the violations are
// deterministic.
//
// The walk follows the populated value, so recursive schemas terminate. It
// stops descending below maxDepth nested messages and then reports exceeded.
func RequiredViolations(
	m protoreflect.Message,
	prefix string,
	resolve Resolve,
	skip []annotations.FieldBehavior,
	maxDepth int,
) ([]Violation, bool) {
	return CheckedRequiredViolations(m, prefix, resolve, skip, maxDepth, nil)
}

// ReplacedRequiredViolations is [RequiredViolations] for a value an update
// writes whole: skip governs m and its singular sub-messages, whose IMMUTABLE
// fields keep their stored value when absent, and elementSkip the elements of
// every repeated and map field below, which are new values checked as on
// create (and everything below them).
func ReplacedRequiredViolations(
	m protoreflect.Message,
	prefix string,
	resolve Resolve,
	skip, elementSkip []annotations.FieldBehavior,
	maxDepth int,
) []Violation {
	w := requiredWalk{resolve: resolve, maxDepth: maxDepth, elementSkip: elementSkip}
	w.walk(m, prefix, 0, skip)

	return w.violations
}

// FieldCheck returns the violations of the populated field fd of m, whose
// path is path, beyond REQUIRED: fieldbehavior checks the AIP-202 format of a
// value with it in the same walk.
type FieldCheck func(m protoreflect.Message, fd protoreflect.FieldDescriptor, path string) []Violation

// CheckedRequiredViolations is [RequiredViolations] that also calls check,
// when non-nil, for every populated field it visits (every field not skipped
// that is not reported as REQUIRED) and records its violations in walk order.
func CheckedRequiredViolations(
	m protoreflect.Message,
	prefix string,
	resolve Resolve,
	skip []annotations.FieldBehavior,
	maxDepth int,
	check FieldCheck,
) ([]Violation, bool) {
	w := requiredWalk{resolve: resolve, maxDepth: maxDepth, check: check}
	w.walk(m, prefix, 0, skip)

	return w.violations, w.exceeded
}

type requiredWalk struct {
	resolve Resolve
	// elementSkip, when set, replaces the skip of the walk in the elements of
	// repeated and map fields.
	elementSkip []annotations.FieldBehavior
	maxDepth    int
	check       FieldCheck
	violations  []Violation
	exceeded    bool
}

func (w *requiredWalk) walk(m protoreflect.Message, prefix string, depth int, skip []annotations.FieldBehavior) {
	if m == nil || !m.IsValid() {
		return
	}

	if depth > w.maxDepth {
		w.exceeded = true
		return
	}

	fields := m.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		path := JoinPath(prefix, string(fd.Name()))

		behaviors := w.resolve(fd)
		if slices.ContainsFunc(behaviors, func(b annotations.FieldBehavior) bool { return slices.Contains(skip, b) }) {
			continue
		}

		if slices.Contains(behaviors, annotations.FieldBehavior_REQUIRED) && RequiredUnmet(m, fd) {
			w.violations = append(w.violations, Violation{Path: path, Behavior: annotations.FieldBehavior_REQUIRED, Reason: ReasonRequired})
			continue
		}

		if !m.Has(fd) {
			continue
		}

		if w.check != nil {
			w.violations = append(w.violations, w.check(m, fd, path)...)
		}

		w.descend(m, fd, path, depth, skip)
	}
}

// descend walks the populated messages held by field fd of m.
func (w *requiredWalk) descend(m protoreflect.Message, fd protoreflect.FieldDescriptor, path string, depth int, skip []annotations.FieldBehavior) {
	elementSkip := skip
	if w.elementSkip != nil {
		elementSkip = w.elementSkip
	}

	switch {
	case fd.IsList():
		if fd.Kind() != protoreflect.MessageKind {
			return
		}

		list := m.Get(fd).List()
		for i := range list.Len() {
			w.walk(list.Get(i).Message(), JoinPath(path, strconv.Itoa(i)), depth+1, elementSkip)
		}
	case fd.IsMap():
		if fd.MapValue().Kind() != protoreflect.MessageKind {
			return
		}

		mm := m.Get(fd).Map()
		for _, k := range SortedMapKeys(mm) {
			w.walk(mm.Get(k).Message(), JoinPath(path, QuoteSegment(k.String())), depth+1, elementSkip)
		}
	case fd.Message() != nil:
		w.walk(m.Get(fd).Message(), path, depth+1, skip)
	}
}

// RequiredUnmet reports whether the REQUIRED field fd of m is not satisfied:
// [IsEmpty], except that a member of a real oneof whose oneof holds another
// member is satisfied.
func RequiredUnmet(m protoreflect.Message, fd protoreflect.FieldDescriptor) bool {
	if od := fd.ContainingOneof(); od != nil && !od.IsSynthetic() && m != nil && m.IsValid() {
		if set := m.WhichOneof(od); set != nil && set.Number() != fd.Number() {
			return false
		}
	}

	return IsEmpty(m, fd)
}

// SortedMapKeys returns the keys of mm ordered by their string form.
func SortedMapKeys(mm protoreflect.Map) []protoreflect.MapKey {
	keys := make([]protoreflect.MapKey, 0, mm.Len())
	mm.Range(func(k protoreflect.MapKey, _ protoreflect.Value) bool {
		keys = append(keys, k)
		return true
	})

	if len(keys) > 1 {
		slices.SortFunc(keys, func(a, b protoreflect.MapKey) int { return strings.Compare(a.String(), b.String()) })
	}

	return keys
}

// Child returns the message the singular message field fd of m holds, or nil
// when m is nil or invalid, does not populate fd, or fd is not a singular
// message field.
func Child(m protoreflect.Message, fd protoreflect.FieldDescriptor) protoreflect.Message {
	if fd.Message() == nil || fd.IsList() || fd.IsMap() || m == nil || !m.IsValid() || !m.Has(fd) {
		return nil
	}

	return m.Get(fd).Message()
}

// SameValue reports whether field fd holds the same value in r and c: both
// leave it unset, or both set it to equal values. A nil or invalid message
// holds nothing, so it matches a message that leaves fd unset.
func SameValue(r, c protoreflect.Message, fd protoreflect.FieldDescriptor) bool {
	rh := r != nil && r.IsValid() && r.Has(fd)
	ch := c != nil && c.IsValid() && c.Has(fd)

	if rh != ch {
		return false
	}

	return !rh || r.Get(fd).Equal(c.Get(fd))
}
