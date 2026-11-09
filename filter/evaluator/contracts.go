// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package evaluator

import "github.com/altessa-s/go-aip/filter"

// ValueDecoder converts driver-owned values to native SQL values.
type ValueDecoder interface{ Decode(value any) (any, error) }

// Program is a validated expression compiled by a [Compiler] profile and executed by the evaluator. It must be safe for
// concurrent use.
type Program interface {
	Evaluate(data map[string]any, maxWork int, decoder ValueDecoder) (bool, error)
}

// Profile validates a semantic contract against the policy. A profile supplies its execution by implementing [Compiler], or
// [Binder] when its programs depend on the evaluator's settings; it may implement [FieldAccessor] for [Evaluator.Lookup].
type Profile interface {
	ValidatePolicy(policy *filter.Policy) error
}

// Compiler compiles a filter into a concurrency-safe program. [Evaluator.Prepare] passes it a private copy of the filter, within the
// policy's limits, which the program may keep; [Evaluator.Evaluate] and [Evaluator.Validate] pass the caller's filter and evaluate
// the program at once, so a compiler need not copy the filter it compiles.
type Compiler interface {
	CompileEvaluator(policy *filter.Policy, node filter.Node) (Program, error)
}

// Binder is implemented by a [Profile] whose programs read data through the evaluator's [ValueEncoding] ([WithValueEncoding]): [New] calls
// BindEvaluator once, after ValidatePolicy, with the evaluator's policy (bound by [WithFilterOptions]) and its encoding (nil
// without one), and compiles every filter with the [Compiler] it returns. When that Compiler implements [FieldAccessor],
// [Evaluator.Lookup] reads through it.
type Binder interface {
	BindEvaluator(policy *filter.Policy, enc ValueEncoding) (Compiler, error)
}

// FieldAccessor reads a field without compiling a predicate.
type FieldAccessor interface {
	Lookup(policy *filter.Policy, data map[string]any, name string, maxWork int, decoder ValueDecoder) (any, bool, error)
}
