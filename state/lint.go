// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package state

import (
	"fmt"
	"strings"
	"unicode"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/internal/behavior"
)

// Rule identifies the AIP-216 rule a [Violation] breaks.
type Rule string

// Resource rules checked by [Lint].
const (
	// RuleOutputOnly: the state field should be OUTPUT_ONLY.
	RuleOutputOnly Rule = "state-output-only"
	// RuleEnumName: the state enum should be called State or end in State.
	RuleEnumName Rule = "state-enum-name"
	// RuleEnumNested: the state enum should be nested in the resource.
	RuleEnumNested Rule = "state-enum-nested"
	// RuleNotStatus: State, not Status, which is reserved for HTTP and gRPC
	// statuses.
	RuleNotStatus Rule = "state-not-status"
	// RuleZeroValue: the zero value should be STATE_UNSPECIFIED (the enum name
	// in UPPER_SNAKE_CASE followed by _UNSPECIFIED).
	RuleZeroValue Rule = "state-zero-value"
	// RuleValuePrefix: the other values should not carry the enum name prefix.
	RuleValuePrefix Rule = "state-value-prefix"
	// RuleValueTerm: a value uses a term AIP-216 replaces (READY and AVAILABLE
	// with ACTIVE, SUCCESSFUL with SUCCEEDED, FAILURE with FAILED).
	RuleValueTerm Rule = "state-value-term"
)

// Method rules checked by [LintTransitionMethod].
const (
	// RuleMethodName: the name should be a verb followed by the resource name.
	RuleMethodName Rule = "transition-method-name"
	// RuleRequestName: the request must be named after the method with a
	// Request suffix.
	RuleRequestName Rule = "transition-request-name"
	// RuleResponse: the response should be the resource or a
	// google.longrunning.Operation.
	RuleResponse Rule = "transition-response"
	// RuleNameField: the request must have the resource name in a string
	// field, which should be called name.
	RuleNameField Rule = "transition-name-field"
	// RuleHTTPMethod: the HTTP verb must be POST.
	RuleHTTPMethod Rule = "transition-http-method"
	// RuleHTTPBody: the HTTP body must be "*".
	RuleHTTPBody Rule = "transition-http-body"
	// RuleHTTPVerb: the URI must end with ":" and the method's verb in
	// camelCase.
	RuleHTTPVerb Rule = "transition-http-verb"
)

// operationName is the long-running operation a transition method may return.
const operationName protoreflect.FullName = "google.longrunning.Operation"

// discouragedTerms maps value names AIP-216 replaces to its terms.
var discouragedTerms = map[protoreflect.Name]string{
	"READY":      "ACTIVE",
	"AVAILABLE":  "ACTIVE",
	"SUCCESS":    "SUCCEEDED",
	"SUCCESSFUL": "SUCCEEDED",
	"FAILURE":    "FAILED",
}

// Violation is one AIP-216 rule a descriptor breaks.
type Violation struct {
	// Rule is the broken rule.
	Rule Rule
	// Element is the full name of the offending field, enum, enum value or
	// method.
	Element protoreflect.FullName
	// Message explains the violation.
	Message string
}

// CheckOutputOnly returns nil when fd is annotated OUTPUT_ONLY, the AIP-216
// behavior of a state field, and an error matching [ErrNotOutputOnly]
// otherwise. With the annotation, fieldbehavior and fieldmask clear the state
// a Create or Update request sends, so only state-changing methods change it.
func CheckOutputOnly(fd protoreflect.FieldDescriptor) error {
	if behavior.Has(fd, annotations.FieldBehavior_OUTPUT_ONLY) {
		return nil
	}

	return fmt.Errorf("%w: %s", ErrNotOutputOnly, fd.FullName())
}

// Lint reports the AIP-216 rules the state fields of the resource md break.
// A state field is an enum field named state or ending in _state, or one
// whose enum is called State or ends in State; an enum field named status or
// whose enum ends in Status breaks [RuleNotStatus]. Only the fields of md
// itself are inspected, each enum once. Every rule but the uniqueness of
// top-level enum values, which protoc enforces, is checked; the rules are
// SHOULDs, so a consumer test may skip some by [Rule]:
//
//	for _, v := range state.Lint((&pb.Book{}).ProtoReflect().Descriptor()) { t.Errorf("%s: %s", v.Element, v.Message) }
func Lint(md protoreflect.MessageDescriptor) []Violation {
	var out []Violation

	seen := make(map[protoreflect.FullName]bool)
	fields := md.Fields()

	for i := range fields.Len() {
		fd := fields.Get(i)
		if fd.Kind() != protoreflect.EnumKind {
			continue
		}

		ed := fd.Enum()

		if isStatus(fd, ed) {
			out = append(out, Violation{Rule: RuleNotStatus, Element: fd.FullName(),
				Message: "use a State enum: Status is reserved for HTTP and gRPC statuses"})

			continue
		}

		if !isState(fd, ed) {
			continue
		}

		if CheckOutputOnly(fd) != nil {
			out = append(out, Violation{Rule: RuleOutputOnly, Element: fd.FullName(),
				Message: "the state field should be annotated with (google.api.field_behavior) = OUTPUT_ONLY"})
		}

		if !seen[ed.FullName()] {
			seen[ed.FullName()] = true
			out = append(out, lintEnum(ed)...)
		}
	}

	return out
}

// isState reports whether fd is a state field.
func isState(fd protoreflect.FieldDescriptor, ed protoreflect.EnumDescriptor) bool {
	name := string(fd.Name())

	return name == "state" || strings.HasSuffix(name, "_state") || strings.HasSuffix(string(ed.Name()), "State")
}

// isStatus reports whether fd is a status field.
func isStatus(fd protoreflect.FieldDescriptor, ed protoreflect.EnumDescriptor) bool {
	return fd.Name() == "status" || strings.HasSuffix(string(ed.Name()), "Status")
}

// lintEnum checks the naming of a state enum and its values.
func lintEnum(ed protoreflect.EnumDescriptor) []Violation {
	var out []Violation

	if !strings.HasSuffix(string(ed.Name()), "State") {
		out = append(out, Violation{Rule: RuleEnumName, Element: ed.FullName(),
			Message: "the state enum should be called State or end in State"})
	}

	if _, ok := ed.Parent().(protoreflect.FileDescriptor); ok {
		out = append(out, Violation{Rule: RuleEnumNested, Element: ed.FullName(),
			Message: "the state enum should be nested in the resource it describes"})
	}

	prefix := upperSnake(string(ed.Name())) + "_"
	zero := protoreflect.Name(prefix + "UNSPECIFIED")
	values := ed.Values()

	if v := values.ByNumber(0); v == nil || v.Name() != zero {
		out = append(out, Violation{Rule: RuleZeroValue, Element: ed.FullName(),
			Message: fmt.Sprintf("the zero value should be %s", zero)})
	}

	for i := range values.Len() {
		v := values.Get(i)
		if v.Number() == 0 {
			continue
		}

		if strings.HasPrefix(string(v.Name()), prefix) {
			out = append(out, Violation{Rule: RuleValuePrefix, Element: v.FullName(),
				Message: fmt.Sprintf("the value should not be prefixed with %s", prefix)})
		}

		if term, ok := discouragedTerms[v.Name()]; ok {
			out = append(out, Violation{Rule: RuleValueTerm, Element: v.FullName(),
				Message: fmt.Sprintf("use %s instead of %s", term, v.Name())})
		}
	}

	return out
}

// LintTransitionMethod reports the AIP-216 rules the state transition method
// md of resource breaks: its name, request and response messages, the
// resource name field and, when md has a google.api.http annotation, the HTTP
// verb, body and custom verb. A method without the annotation is not checked
// for the HTTP rules.
func LintTransitionMethod(md protoreflect.MethodDescriptor, resource protoreflect.MessageDescriptor) []Violation {
	var out []Violation

	name := string(md.Name())
	verb, ok := strings.CutSuffix(name, string(resource.Name()))

	if !ok || verb == "" {
		verb = name

		out = append(out, Violation{Rule: RuleMethodName, Element: md.FullName(),
			Message: fmt.Sprintf("the method name should be a verb followed by %s", resource.Name())})
	}

	if want := protoreflect.Name(name + "Request"); md.Input().Name() != want {
		out = append(out, Violation{Rule: RuleRequestName, Element: md.FullName(),
			Message: fmt.Sprintf("the request message must be named %s", want)})
	}

	if resp := md.Output().FullName(); resp != resource.FullName() && resp != operationName {
		out = append(out, Violation{Rule: RuleResponse, Element: md.FullName(),
			Message: fmt.Sprintf("the response should be %s or %s", resource.FullName(), operationName)})
	}

	if fd := md.Input().Fields().ByName("name"); fd == nil || fd.Kind() != protoreflect.StringKind || fd.IsList() {
		out = append(out, Violation{Rule: RuleNameField, Element: md.Input().FullName(),
			Message: "the request must carry the resource name in a string field called name"})
	}

	return append(out, lintHTTP(md, verb)...)
}

// lintHTTP checks the google.api.http annotation of a transition method.
func lintHTTP(md protoreflect.MethodDescriptor, verb string) []Violation {
	opts := md.Options()
	if opts == nil || !proto.HasExtension(opts, annotations.E_Http) {
		return nil
	}

	rule, _ := proto.GetExtension(opts, annotations.E_Http).(*annotations.HttpRule)

	var out []Violation

	if rule.GetPost() == "" {
		out = append(out, Violation{Rule: RuleHTTPMethod, Element: md.FullName(), Message: "the HTTP verb must be POST"})
	}

	if rule.GetBody() != "*" {
		out = append(out, Violation{Rule: RuleHTTPBody, Element: md.FullName(), Message: `the HTTP body must be "*"`})
	}

	want := lowerCamel(verb)
	if got, ok := customVerb(httpPath(rule)); !ok || got != want {
		out = append(out, Violation{Rule: RuleHTTPVerb, Element: md.FullName(),
			Message: fmt.Sprintf("the URI must end with :%s", want)})
	}

	return out
}

// httpPath returns the URI template of rule, whatever its HTTP verb.
func httpPath(rule *annotations.HttpRule) string {
	switch p := rule.GetPattern().(type) {
	case *annotations.HttpRule_Get:
		return p.Get
	case *annotations.HttpRule_Put:
		return p.Put
	case *annotations.HttpRule_Post:
		return p.Post
	case *annotations.HttpRule_Delete:
		return p.Delete
	case *annotations.HttpRule_Patch:
		return p.Patch
	case *annotations.HttpRule_Custom:
		return p.Custom.GetPath()
	}

	return ""
}

// customVerb returns the custom verb after the last ":" outside a variable.
func customVerb(path string) (string, bool) {
	i := strings.LastIndexByte(path, ':')
	if i < 0 || i < strings.LastIndexByte(path, '}') {
		return "", false
	}

	return path[i+1:], true
}

// upperSnake turns a CamelCase name into UPPER_SNAKE_CASE, keeping an
// acronym together: JobState is JOB_STATE, HTTPRouteState HTTP_ROUTE_STATE.
func upperSnake(s string) string {
	var sb strings.Builder

	rs := []rune(s)
	for i, r := range rs {
		if i > 0 && unicode.IsUpper(r) {
			prev := rs[i-1]
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])

			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				sb.WriteByte('_')
			}
		}

		sb.WriteRune(unicode.ToUpper(r))
	}

	return sb.String()
}

// lowerCamel lowercases the first letter of a CamelCase name.
func lowerCamel(s string) string {
	if s == "" {
		return s
	}

	return strings.ToLower(s[:1]) + s[1:]
}
