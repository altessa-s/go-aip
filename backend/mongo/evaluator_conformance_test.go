// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongo_test

import (
	"testing"

	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/metrics"
)

// Known gaps the suite pins (filtertest.Outcome.Known): the outcome is what the translator does today, not what it should do.
// knownMongoNegation is a deliberate one: the MongoDB profile mirrors the MongoDB translator, so it is not changed.
const (
	knownMongoNegation = "MongoDB semantics: $nor matches a document whose field is null or missing, where SQL and the default " +
		"evaluator keep an ordering comparison, size(), substring() or a null string-function argument unknown under negation (documented backend limit)"
)

// evaluatorMatch builds an evaluator with extra options and returns its per-record match.
func evaluatorMatch(extra ...evaluator.Option) func(*filter.Policy, ...filter.TranslatorOption) (filtertest.MatchFunc, error) {
	return func(policy *filter.Policy, opts ...filter.TranslatorOption) (filtertest.MatchFunc, error) {
		e, err := evaluator.New(policy, append([]evaluator.Option{evaluator.WithFilterOptions(opts...)}, extra...)...)
		if err != nil {
			return nil, err
		}

		return func(node filter.Node, record map[string]any) (bool, error) {
			p, err := e.Prepare(node)
			if err != nil {
				return false, err
			}

			return p.Evaluate(record)
		}, nil
	}
}

// evaluatorBackend renders an evaluator as the records of filtertest.ConformanceRecords it selects; an expression the evaluator
// rejects (Prepare) is the error of the case.
func evaluatorBackend(expect map[string]filtertest.Outcome, extra ...evaluator.Option) filtertest.Backend {
	return filtertest.Backend{
		New: func(policy *filter.Policy, opts ...filter.TranslatorOption) (filtertest.TranslateFunc, error) {
			e, err := evaluator.New(policy, append([]evaluator.Option{
				evaluator.WithFilterOptions(opts...), evaluator.WithCollector(metrics.Noop()),
			}, extra...)...)
			if err != nil {
				return nil, err
			}

			return func(node filter.Node) (string, error) {
				p, err := e.Prepare(node)
				if err != nil {
					return "", err
				}

				return filtertest.FormatMatches(func(_ filter.Node, record map[string]any) (bool, error) {
					return p.Evaluate(record)
				}, node), nil
			}, nil
		},
		Expect: expect,
	}
}

// TestEvaluatorConformance runs the shared filter conformance suite on the evaluator with the MongoDB profile, against the
// default evaluator as the reference.
func TestEvaluatorConformance(t *testing.T) {
	t.Parallel()

	type O = filtertest.Outcome

	schema := mustMongoSchema(t)

	b := evaluatorBackend(map[string]O{
		"equal string":                    {Want: "1"},
		"not equal string":                {Want: "2 3 4 5"},
		"less int":                        {Want: "2 4"},
		"less or equal int":               {Want: "1 2 4 5"},
		"greater float":                   {Want: "2 4"},
		"greater or equal float":          {Want: "1 2 4 5"},
		"float literal on int field":      {Err: filter.ErrFieldTypeMismatch},
		"int literal on float field":      {Want: "4"},
		"negative literal":                {Want: "1 2 3 4 5"},
		"ordered string":                  {Want: "1 2 4"},
		"equal bool":                      {Want: "1 3 5"},
		"bare bool":                       {Want: "1 3 5"},
		"negated bare bool":               {Want: "2 4"},
		"enum value":                      {Want: "1 4"},
		"nested field":                    {Want: "1"},
		"nested field mapping":            {Want: "none"},
		"field mapping":                   {Want: "1"},
		"has nested":                      {Want: "1 2 3 4 5"},
		"equal null":                      {Want: "2 3"},
		"not equal null":                  {Want: "1 4 5"},
		"ordered null":                    {Err: filter.ErrUnsupportedOperation},
		"ordered nullable field":          {Want: "4"},
		"equal empty string":              {Want: "4"},
		"in ints":                         {Want: "1 2 5"},
		"in strings":                      {Want: "1 3"},
		"in empty list":                   {Want: "none"},
		"in with null":                    {Want: "1 2 3"},
		"not in":                          {Want: "3 4"},
		"in non-array field":              {Err: filter.ErrInvalidExpression},
		"contains":                        {Want: "3"},
		"starts with":                     {Want: "3"},
		"ends with":                       {Want: "2"},
		"contains wildcard characters":    {Want: "none"},
		"starts with wildcard characters": {Want: "none"},
		"contains empty string":           {Want: "1 2 3 4 5"},
		"contains field":                  {Want: "1 3 4 5"},
		"starts with field":               {Want: "1 3 4 5"},
		"ends with field":                 {Want: "3"},
		"case-sensitive contains":         {Want: "none"},
		"text search":                     {Want: "2"},
		"text search case-insensitive":    {Want: "4"},
		"matches":                         {Want: "1 2"},
		"matches end anchor":              {Want: "3"},
		"size of string":                  {Want: "1 3"},
		"size function":                   {Want: "2"},
		"size of array":                   {Want: "2 4"},
		"size of empty array":             {Want: "3 5"},
		"substring":                       {Want: "3"},
		"substring from":                  {Want: "2"},
		"substring ordered":               {Want: "1 2 4"},
		"substring reversed range":        {Err: filter.ErrInvalidExpression},
		"timestamp greater":               {Want: "1 3 5"},
		"timestamp equal":                 {Want: "1 5"},
		"timestamp fraction":              {Want: "4"},
		"duration literal":                {Err: filter.ErrFieldTypeMismatch},
		"bytes equal":                     {Want: "1 5"},
		"bytes ordered":                   {Err: filter.ErrUnsupportedOperation},
		"decimal greater":                 {Want: "1 3 5"},
		"decimal equal":                   {Want: "4"},
		"decimal trailing zeros":          {Want: "1 5"},
		"decimal string literal":          {Want: "1 5"},
		"decimal in":                      {Want: "2 3"},
		"decimal not a number":            {Err: filter.ErrFieldTypeMismatch},
		"array equal":                     {Want: "1 4"},
		"array not equal":                 {Want: "2 3 5"},
		"element in array":                {Want: "1 4"},
		"array in list":                   {Want: "1 2"},
		"array starts with":               {Want: "2"},
		"array ordered":                   {Want: "1"},
		"array equal null":                {Want: "5"},
		"and":                             {Want: "1"},
		"or":                              {Want: "2 4"},
		"and binds tighter than or":       {Want: "1 2"},
		"parentheses":                     {Want: "1"},
		"not":                             {Want: "2 3 4 5"},
		"not and":                         {Want: "2 4"},
		"not or":                          {Want: "2 3 5"},
		"double negation":                 {Want: "1 3 5"},
		"bare literal":                    {Err: filter.ErrInvalidExpression},
		"unknown or true":                 {Want: "1 2 3 4 5"},
		"true or unknown":                 {Want: "1 2 3 4 5"},
		"unknown or false":                {Want: "4"},
		"unknown and false":               {Want: "none"},
		"false and unknown":               {Want: "none"},
		"not unknown":                     {Want: "1 2 3 5", Known: knownMongoNegation},
		"not unknown or false":            {Want: "1 2 3 5", Known: knownMongoNegation},
		"not unknown and false":           {Want: "1 2 3 4 5"},
		"not size of null":                {Want: "1 2 3 5", Known: knownMongoNegation},
		"not contains null argument":      {Want: "1 2 3 5", Known: knownMongoNegation},
		"not substring out of range":      {Want: "1 2 3 4 5", Known: knownMongoNegation},
		"nil filter":                      {Want: "1 2 3 4 5"},
		"field equal field":               {Want: "3"},
		"field less field":                {Want: "2"},
		"field in list":                   {Want: "2 3"},
		"field kind mismatch":             {Err: filter.ErrFieldTypeMismatch},
	}, evaluator.WithProfile(schema))
	b.Match, b.Reference = evaluatorMatch(evaluator.WithProfile(schema)), evaluatorMatch()
	filtertest.RunConformance(t, b)
}
