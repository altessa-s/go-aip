// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package maskpath

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/internal/behavior"
)

// The field mask path grammar (AIP-161), shared by every entry point that
// reads a path — read masks and update masks alike:
//
//	path    = segment { "." segment }
//	segment = name | "`" { char | "``" } "`" | "*"
//
// A segment is a field name, a map key or the wildcard "*". A segment that is
// not a plain name (a map key holding a dot, a backtick or nothing at all) is
// written between backticks, with a literal backtick doubled: the key x`y.z
// is written `x``y.z`. A backtick may only open and close a whole segment.
//
// Against a message schema, a path is valid when:
//   - every field segment names a field of the current message by its
//     declared name ("display_name"), its JSON name ("displayName") or the
//     snake_case form protojson decodes a JSON mask path to;
//   - the segment after a repeated field is "*" (every element): an index
//     ("authors.0") and a field of the element without "*"
//     ("authors.given_name") are both rejected;
//   - the segment after a map field is "*" (every value) or a key that is a
//     valid value of the map's key type in its canonical form (an int32 key
//     is "7", not "07" or "abc");
//   - nothing follows a scalar field, nor the "*" or key of a collection of
//     scalars;
//   - a google.protobuf.Struct is addressed as a map<string, Value>: the
//     segment after it is "*" (every key) or a key, any string quoted as a
//     map key is; a quoted "*" is a literal JSON key;
//   - below that, the dynamic JSON of a Struct entry, a google.protobuf.Value
//     or a ListValue is walked by its data, an extension beyond AIP-161: a
//     key addresses an entry of a Struct value at any depth
//     ("extra_data.work_period.from", "value_data.key"), "*" every entry of
//     a Struct value or every element of a list value, and the segment after
//     a ListValue field is "*" (an index such as "list_data.0" and a key
//     without "*" are rejected, as for a repeated field). Where the data has
//     another shape, the path addresses nothing;
//   - with strict dynamic paths (fieldmask.WithStrictDynamicPaths), the AIP-161
//     rule alone: nothing follows the key or "*" of a Struct, and nothing
//     follows a google.protobuf.Value or ListValue;
//   - the full-replacement path "*" stands alone (see fieldmask.FieldMask.SelectsAll).

// Segment is one component of a parsed field mask path.
type Segment struct {
	Text   string // the unescaped content
	Quoted bool   // written between backticks
}

// Parse splits raw into its segments by the path grammar. A malformed
// path — empty, with an empty segment ("a.", ".a", "a..b"), an unclosed
// backtick, a backtick inside an unquoted segment, or "*" followed by more
// segments at the root — is a *[Error] naming raw.
func Parse(raw string) ([]Segment, error) {
	if !utf8.ValidString(raw) {
		return nil, &Error{Path: raw, Reason: "path is not valid UTF-8"}
	}

	if raw == "" {
		return nil, &Error{Path: raw, Reason: "path is empty"}
	}

	var segs []Segment

	for i := 0; ; {
		seg, next, reason := scanSegment(raw, i)
		if reason != "" {
			return nil, &Error{Path: raw, Reason: reason}
		}

		segs = append(segs, seg)

		if next == len(raw) {
			break
		}

		i = next + 1 // skip the separator
		if i == len(raw) {
			return nil, &Error{Path: raw, Reason: "path ends with a separator"}
		}
	}

	if len(segs) > 1 && IsWildcard(segs[0]) {
		return nil, &Error{Path: raw, Reason: `the full-replacement path "*" cannot be followed by more segments`}
	}

	return segs, nil
}

// scanSegment reads the segment of raw that starts at i and returns it with
// the index of the separator or end of input that follows it, or the reason
// it is malformed.
func scanSegment(raw string, i int) (seg Segment, next int, reason string) {
	if raw[i] != '`' {
		j := i
		for j < len(raw) && raw[j] != '.' {
			if raw[j] == '`' {
				return Segment{}, 0, "a backtick may only quote a whole segment"
			}

			j++
		}

		if j == i {
			return Segment{}, 0, "path has an empty segment"
		}

		return Segment{Text: raw[i:j]}, j, ""
	}

	var b strings.Builder

	for j := i + 1; j < len(raw); j++ {
		if raw[j] != '`' {
			b.WriteByte(raw[j])
			continue
		}

		if j+1 < len(raw) && raw[j+1] == '`' { // a doubled backtick
			b.WriteByte('`')

			j++

			continue
		}

		if j+1 < len(raw) && raw[j+1] != '.' {
			return Segment{}, 0, "a quoted segment must be followed by a separator or the end of the path"
		}

		return Segment{Text: b.String(), Quoted: true}, j + 1, ""
	}

	return Segment{}, 0, "path has an unclosed backtick"
}

// IsWildcard reports whether seg is the unquoted wildcard "*".
func IsWildcard(seg Segment) bool { return !seg.Quoted && seg.Text == Wildcard }

// CheckSyntax reports whether raw is a well-formed path by the grammar
// alone, without a schema: a *[Error] naming raw when it is not.
func CheckSyntax(raw string) error {
	_, err := Parse(raw)
	return err
}

// ResolvePath reports whether raw is a valid path of md by the full path
// grammar, accepting every spelling of a field (fieldmask.FieldMask.Resolve), and
// returns it spelled canonically: every field segment by its declared name
// and every segment quoted only as the grammar requires, so the result
// round-trips through fieldmask.FromPaths. A rejected path is a *[Error]
// naming raw. The lone "*" is valid (the full-replacement path); whether it
// stands alone in its mask is the caller's check.
// With strict, a path into dynamic JSON follows the AIP-161 rule alone (see
// fieldmask.WithStrictDynamicPaths).
func ResolvePath(md protoreflect.MessageDescriptor, raw string, strict bool) (string, error) {
	segs, err := Check{Aliases: true, Strict: strict}.Resolve(md, raw)
	if err != nil {
		return "", err
	}

	return Join(segs), nil
}

// Join writes unescaped segments as one path, quoting each as
// [behavior.QuoteSegment] does.
func Join(segs []string) string {
	var b strings.Builder

	for i, s := range segs {
		if i > 0 {
			b.WriteString(Separator)
		}

		b.WriteString(PathSegment(s))
	}

	return b.String()
}

// PathSegment renders a mask-tree segment, preserving the distinction between
// the wildcard and a literal JSON "*" key. Other keys use AIP-161 quoting.
func PathSegment(s string) string {
	if s == LiteralStar {
		return "`*`"
	}

	return behavior.QuoteSegment(s)
}

// JSONKey decodes a mask-tree segment to its literal JSON object key.
func JSONKey(s string) string {
	if s == LiteralStar {
		return Wildcard
	}

	return s
}

// QuoteJSONKey renders a literal JSON key as a path segment, including "*".
// Use PathSegment instead when s is already a mask-tree key (possibly wildcard).
func QuoteJSONKey(s string) string {
	if s == Wildcard {
		return PathSegment(LiteralStar)
	}

	return behavior.QuoteSegment(s)
}

// Check walks a path against a message schema by the path grammar.
type Check struct {
	// aliases also resolves a field segment by its JSON name or by the
	// snake_case form protojson decodes a JSON mask path to, not only by its
	// declared name.
	Aliases bool

	// structuralOnly reports only the structural problems no mask may have
	// whatever its schema validation: an index after a repeated field or a
	// ListValue, and with strict a sub-path into the dynamic value of a
	// google.protobuf.Struct entry, a Value or a ListValue. Every other
	// problem ends the walk without an error.
	StructuralOnly bool

	// strict limits the paths into dynamic JSON to the AIP-161 rule (see
	// fieldmask.WithStrictDynamicPaths).
	Strict bool
}

// fail returns the *Error of raw for reason, or nil when c reports
// only structural problems and this one is not (structural false).
func (c Check) fail(raw string, structural bool, reason string) error {
	if c.StructuralOnly && !structural {
		return nil
	}

	return &Error{Path: raw, Reason: reason}
}

// Resolve walks raw against md and returns its unescaped segments with every
// field segment spelled by its declared name. With structuralOnly a problem
// that is not structural returns the segments resolved so far and no error.
func (c Check) Resolve(md protoreflect.MessageDescriptor, raw string) ([]string, error) {
	segs, err := Parse(raw)
	if err != nil {
		if c.StructuralOnly {
			return nil, nil
		}

		return nil, err
	}

	out := make([]string, 0, len(segs))

	if len(segs) == 1 && IsWildcard(segs[0]) {
		return append(out, Wildcard), nil
	}

	cur := md

	var container protoreflect.FieldDescriptor // the list or map the next segment addresses

	for i, seg := range segs {
		last := i == len(segs)-1

		if IsDynamicMessage(cur) {
			tail, reason, structural := DynamicTail(cur, Join(out), segs[i:], c.Strict)
			if reason != "" {
				return out, c.fail(raw, structural, reason)
			}

			return append(out, tail...), nil
		}

		if container != nil {
			if ok, err := c.element(raw, container, seg, last); !ok {
				return out, err
			}

			out = append(out, seg.Text)
			cur, container = ValueMessage(container), nil

			continue
		}

		if IsWildcard(seg) {
			return out, c.fail(raw, false, `wildcard "*" may only follow a repeated or map field`)
		}

		fd := c.Lookup(cur, seg.Text)
		if fd == nil {
			return out, c.fail(raw, false, fmt.Sprintf("field '%s' does not exist in message %s", seg.Text, cur.FullName()))
		}

		out = append(out, string(fd.Name()))

		if last {
			break
		}

		switch {
		case fd.IsList() || fd.IsMap():
			container = fd
		case fd.Kind() == protoreflect.MessageKind:
			cur = fd.Message()
		default:
			return out, c.fail(raw, false, fmt.Sprintf("cannot access field '%s' in scalar field: %s", segs[i+1].Text, fd.Name()))
		}
	}

	return out, nil
}

// element checks seg, the segment after the repeated or map field container:
// "*", or a key of the map's key type. Nothing may follow it in a collection
// of scalars (last false). It reports whether the walk goes on, with the
// error of a rejected segment (nil when c does not report it).
func (c Check) element(raw string, container protoreflect.FieldDescriptor, seg Segment, last bool) (bool, error) {
	if container.IsMap() {
		if seg.Quoted && seg.Text == Wildcard {
			return false, c.fail(raw, false, fmt.Sprintf("map field %s cannot address the literal key '*'", container.Name()))
		}

		if !IsWildcard(seg) {
			if _, ok := ParseMapKey(container.MapKey().Kind(), seg.Text); !ok {
				return false, c.fail(raw, false, fmt.Sprintf("'%s' is not a valid %s key of map field %s", seg.Text, container.MapKey().Kind(), container.Name()))
			}
		}
	} else if !IsWildcard(seg) {
		if IsNumericSegment(seg.Text) {
			return false, c.fail(raw, true, fmt.Sprintf(
				"indexed access to repeated field '%s' is not permitted; use '*' to address every element", container.Name()))
		}

		return false, c.fail(raw, false, fmt.Sprintf(
			"repeated field '%s' must be followed by '*' to address a field of its elements", container.Name()))
	}

	if last || ValueMessage(container) != nil {
		return true, nil
	}

	if container.IsMap() {
		return false, c.fail(raw, false, fmt.Sprintf("cannot access nested field in map with scalar values at field: %s", container.Name()))
	}

	return false, c.fail(raw, false, fmt.Sprintf("cannot access field beyond wildcard on list of scalar type at field: %s", container.Name()))
}

// Positions in dynamic JSON (see [DynamicTail]).
const (
	dynamicStruct = iota // a google.protobuf.Struct: a key or "*" follows
	dynamicValue         // a google.protobuf.Value: a key or "*" follows
	dynamicList          // a google.protobuf.ListValue field: "*" follows
)

// DynamicTail checks segs, the segments after the google.protobuf.Struct,
// Value or ListValue md reached at the path at, and returns their texts, or
// the reason they are rejected and whether that rejection is structural
// (reported whatever the schema validation, see [Check]). By default a
// key or "*" may follow a Struct or a Value at any depth and "*" a ListValue
// field; with strict only a key or "*" right after a Struct is accepted.
// A quoted "*" names a literal JSON key and retains that identity in the tree.
func DynamicTail(md protoreflect.MessageDescriptor, at string, segs []Segment, strict bool) ([]string, string, bool) {
	pos := dynamicValue

	switch md.FullName() {
	case StructFullName:
		pos = dynamicStruct
	case ListValueFullName:
		pos = dynamicList
	}

	texts := make([]string, 0, len(segs))

	for _, seg := range segs {
		where := dynamicLocation(md, at, texts)

		switch {
		case strict && (pos != dynamicStruct || len(texts) > 0):
			kind := ValueFullName
			if len(texts) == 0 {
				kind = md.FullName()
			}

			return nil, fmt.Sprintf("'%s' is a %s, which has no sub-paths with strict dynamic paths", where, kind), true
		case pos == dynamicList && IsNumericSegment(seg.Text) && !seg.Quoted:
			return nil, fmt.Sprintf("indexed access to ListValue '%s' is not permitted; use '*' to address every element", where), true
		case pos == dynamicList && !IsWildcard(seg):
			return nil, fmt.Sprintf("ListValue '%s' must be followed by '*' to address its elements", where), false
		}

		text := seg.Text
		if seg.Quoted && text == Wildcard {
			text = LiteralStar
		}

		texts = append(texts, text)
		pos = dynamicValue
	}

	return texts, "", false
}

// dynamicLocation names the dynamic value at the path at followed by texts,
// or md itself at the root.
func dynamicLocation(md protoreflect.MessageDescriptor, at string, texts []string) string {
	where := Join(texts)

	switch {
	case at == "" && where == "":
		return string(md.FullName())
	case at == "":
		return where
	case where == "":
		return at
	default:
		return at + Separator + where
	}
}

// ValueMessage returns the message of fd's values: the field's message, the
// element message of a repeated field, the value message of a map; nil for
// scalar values.
func ValueMessage(fd protoreflect.FieldDescriptor) protoreflect.MessageDescriptor {
	if fd.IsMap() {
		return fd.MapValue().Message()
	}

	return fd.Message()
}

// Lookup returns the field of md that name names: by declared name, and with
// aliases also by JSON name or by [snakeCase] of the declared name.
func (c Check) Lookup(md protoreflect.MessageDescriptor, name string) protoreflect.FieldDescriptor {
	fields := md.Fields()
	if fd := fields.ByName(protoreflect.Name(name)); fd != nil || !c.Aliases {
		return fd
	}

	for i := range fields.Len() {
		fd := fields.Get(i)
		if name == fd.JSONName() || name == snakeCase(string(fd.Name())) {
			return fd
		}
	}

	return nil
}

// snakeCase spells a lowerCamelCase name the way protojson decodes a JSON
// FieldMask path: every ASCII upper-case letter becomes an underscore
// followed by its lower-case form ("monthlyBudgetLimit" becomes
// "monthly_budget_limit", "noisePercent7d" becomes "noise_percent7d"). Every
// other character is kept, so a snake_case name comes back unchanged.
func snakeCase(name string) string {
	n := 0

	for i := range len(name) {
		if isASCIIUpper(name[i]) {
			n++
		}
	}

	if n == 0 {
		return name
	}

	var b strings.Builder
	b.Grow(len(name) + n)

	for i := range len(name) {
		c := name[i]
		if isASCIIUpper(c) {
			b.WriteByte('_')

			c += 'a' - 'A'
		}

		b.WriteByte(c)
	}

	return b.String()
}

// isASCIIUpper reports whether c is an ASCII upper-case letter.
func isASCIIUpper(c byte) bool { return c >= 'A' && c <= 'Z' }

// ParseMapKey returns the map key of kind that a mask key names, and whether
// it names one: key must parse as kind and format back to itself, since the
// filter matches an entry by its key string ([protoreflect.MapKey.String]).
func ParseMapKey(kind protoreflect.Kind, key string) (protoreflect.MapKey, bool) {
	var v protoreflect.Value

	switch kind { //nolint:exhaustive // the remaining kinds cannot be map keys.
	case protoreflect.StringKind:
		return protoreflect.ValueOfString(key).MapKey(), true
	case protoreflect.BoolKind:
		b, err := strconv.ParseBool(key)
		if err != nil {
			return protoreflect.MapKey{}, false
		}

		v = protoreflect.ValueOfBool(b)
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		n, err := strconv.ParseInt(key, 10, 32)
		if err != nil {
			return protoreflect.MapKey{}, false
		}

		v = protoreflect.ValueOfInt32(int32(n))
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		n, err := strconv.ParseInt(key, 10, 64)
		if err != nil {
			return protoreflect.MapKey{}, false
		}

		v = protoreflect.ValueOfInt64(n)
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		n, err := strconv.ParseUint(key, 10, 32)
		if err != nil {
			return protoreflect.MapKey{}, false
		}

		v = protoreflect.ValueOfUint32(uint32(n))
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		n, err := strconv.ParseUint(key, 10, 64)
		if err != nil {
			return protoreflect.MapKey{}, false
		}

		v = protoreflect.ValueOfUint64(n)
	default:
		return protoreflect.MapKey{}, false
	}

	k := v.MapKey()

	return k, k.String() == key
}

// IsNumericSegment reports whether s is a non-empty base-10 integer literal,
// the textual form of a list index in a dot-separated field path.
func IsNumericSegment(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}
