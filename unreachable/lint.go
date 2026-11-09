// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package unreachable

import (
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/internal/behavior"
)

// AIP-217 field names.
const (
	// UnreachableFieldName is the proto name of the response field.
	UnreachableFieldName protoreflect.Name = "unreachable"
	// ReturnPartialSuccessFieldName is the proto name of the request field.
	ReturnPartialSuccessFieldName protoreflect.Name = "return_partial_success"
)

// Rule identifies the AIP-217 rule a [Violation] breaks.
type Rule string

// Rules checked by [LintMethod].
const (
	// RuleUnreachableType: unreachable must be a repeated string.
	RuleUnreachableType Rule = "unreachable-type"
	// RuleUnreachableUnordered: unreachable must be annotated with
	// UNORDERED_LIST.
	RuleUnreachableUnordered Rule = "unreachable-unordered"
	// RulePartialSuccessType: return_partial_success must be a singular bool.
	RulePartialSuccessType Rule = "partial-success-type"
	// RulePartialSuccessPair: a request with return_partial_success needs a
	// response with unreachable; both are added together.
	RulePartialSuccessPair Rule = "partial-success-pair"
)

// Violation is one AIP-217 rule a method breaks.
type Violation struct {
	// Rule is the broken rule.
	Rule Rule
	// Element is the full name of the offending field or method.
	Element protoreflect.FullName
	// Message explains the violation.
	Message string
}

// LintMethod reports the AIP-217 rules the request and response of md break:
// an unreachable response field that is not a repeated string annotated with
// UNORDERED_LIST, a return_partial_success request field that is not a
// singular bool, and a return_partial_success without an unreachable response
// field. A method with neither field has no violation. Use it in a consumer
// test over every multi-source read of an API:
//
//	for _, v := range unreachable.LintMethod(desc) { t.Errorf("%s: %s", v.Element, v.Message) }
func LintMethod(md protoreflect.MethodDescriptor) []Violation {
	var out []Violation

	resp := md.Output().Fields().ByName(UnreachableFieldName)
	if resp != nil {
		if !resp.IsList() || resp.Kind() != protoreflect.StringKind {
			out = append(out, Violation{Rule: RuleUnreachableType, Element: resp.FullName(),
				Message: "unreachable must be a repeated string"})
		}

		if !behavior.Has(resp, annotations.FieldBehavior_UNORDERED_LIST) {
			out = append(out, Violation{Rule: RuleUnreachableUnordered, Element: resp.FullName(),
				Message: "unreachable must be annotated with (google.api.field_behavior) = UNORDERED_LIST"})
		}
	}

	if req := md.Input().Fields().ByName(ReturnPartialSuccessFieldName); req != nil {
		if req.Cardinality() == protoreflect.Repeated || req.Kind() != protoreflect.BoolKind {
			out = append(out, Violation{Rule: RulePartialSuccessType, Element: req.FullName(),
				Message: "return_partial_success must be a singular bool"})
		}

		if resp == nil {
			out = append(out, Violation{Rule: RulePartialSuccessPair, Element: md.FullName(),
				Message: "a request with return_partial_success needs a response with a repeated string unreachable"})
		}
	}

	return out
}
