// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package redis_test

import (
	"testing"

	"github.com/altessa-s/go-aip/backend/redis"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/metrics"
)

// Known gaps the suite pins (filtertest.Outcome.Known): the outcome is what the translator does today, not what it should do.
const (
	knownCaseInsensitiveTag = "TAG fields match case-insensitively unless declared CASESENSITIVE, so contains(\"dave\") matches \"Dave\" (documented)"
)

// conformanceSchema indexes the suite's numbers and timestamps as NUMERIC, nickname as TEXT and the other fields (strings, the
// bool, bytes, the tags array) as TAG; note and tags are declared INDEXMISSING, note also INDEXEMPTY.
func conformanceSchema() map[string]redis.FieldType {
	schema := map[string]redis.FieldType{"location.city": redis.FieldTypeTag}

	for field, kind := range filtertest.ConformanceFieldKinds() {
		switch kind {
		case filter.FieldKindInt, filter.FieldKindFloat, filter.FieldKindTimestamp, filter.FieldKindDecimal:
			schema[field] = redis.FieldTypeNumeric
		default:
			schema[field] = redis.FieldTypeTag
		}
	}

	schema["nickname"] = redis.FieldTypeText

	return schema
}

func TestFilterConformance(t *testing.T) {
	t.Parallel()

	type O = filtertest.Outcome

	filtertest.RunConformance(t, filtertest.Backend{
		New: func(policy *filter.Policy, opts ...filter.TranslatorOption) (filtertest.TranslateFunc, error) {
			tr, err := redis.NewFilterTranslatorWithSchema(policy, mustSchema(conformanceSchema(), redis.WithSchemaIndexMissing("note", "tags"), redis.WithSchemaIndexEmpty("note")), redis.WithFilterOptions(opts...), redis.WithFilterCollector(metrics.Noop()))
			if err != nil {
				return nil, err
			}

			return tr.Translate, nil
		},
		Expect: map[string]O{
			"equal string":                    {Want: "@name:{alice}"},
			"not equal string":                {Want: "-@name:{alice}"},
			"less int":                        {Want: "@age:[-inf (30]"},
			"less or equal int":               {Want: "@age:[-inf 30]"},
			"greater float":                   {Want: "@score:[(1.5 +inf]"},
			"greater or equal float":          {Want: "@score:[1.5 +inf]"},
			"float literal on int field":      {Err: filter.ErrFieldTypeMismatch},
			"int literal on float field":      {Want: "@score:[3 3]"},
			"negative literal":                {Want: "@age:[(-1 +inf]"},
			"ordered string":                  {Err: filter.ErrUnsupportedOperation},
			"equal bool":                      {Want: "@active:{true}"},
			"bare bool":                       {Want: "@active:{true}"},
			"negated bare bool":               {Want: "-@active:{true}"},
			"enum value":                      {Want: "@status:[1 1]"},
			"nested field":                    {Want: `@address\.city:{Oslo}`},
			"nested field mapping":            {Want: `@location\.city:{Oslo}`},
			"field mapping":                   {Want: "@nickname:(al)"},
			"has nested":                      {Err: filter.ErrUnsupportedOperation},
			"equal null":                      {Want: "ismissing(@note)"},
			"not equal null":                  {Want: "-ismissing(@note)"},
			"ordered null":                    {Err: filter.ErrUnsupportedOperation},
			"ordered nullable field":          {Err: filter.ErrUnsupportedOperation},
			"equal empty string":              {Want: `@note:{""}`},
			"in ints":                         {Want: "(@age:[25 25]|@age:[30 30])"},
			"in strings":                      {Want: "@name:{alice|carol}"},
			"in empty list":                   {Want: "(@age:[0 0] -@age:[0 0])"},
			"in with null":                    {Want: "((@note:{x})|(ismissing(@note)))"},
			"not in":                          {Want: "-((@age:[25 25]|@age:[30 30]))"},
			"in non-array field":              {Err: filter.ErrInvalidExpression},
			"contains":                        {Want: "@name:{*ar*}"},
			"starts with":                     {Want: "@name:{ca*}"},
			"ends with":                       {Want: "@name:{*ob}"},
			"contains wildcard characters":    {Want: `@name:{*\%_\**}`},
			"starts with wildcard characters": {Want: `@name:{a\%*}`},
			"contains empty string":           {Err: filter.ErrUnsupportedOperation},
			"contains field":                  {Err: filter.ErrUnsupportedOperation},
			"starts with field":               {Err: filter.ErrUnsupportedOperation},
			"ends with field":                 {Err: filter.ErrUnsupportedOperation},
			"case-sensitive contains":         {Want: "@name:{*dave*}", Known: knownCaseInsensitiveTag},
			"text search":                     {Err: filter.ErrUnsupportedOperation},
			"text search case-insensitive":    {Err: filter.ErrUnsupportedOperation},
			"matches":                         {Err: filter.ErrUnsupportedOperation},
			"matches end anchor":              {Err: filter.ErrUnsupportedOperation},
			"size of string":                  {Err: filter.ErrUnsupportedOperation},
			"size function":                   {Err: filter.ErrUnsupportedOperation},
			"size of array":                   {Err: filter.ErrUnsupportedOperation},
			"size of empty array":             {Err: filter.ErrUnsupportedOperation},
			"substring":                       {Err: filter.ErrUnsupportedOperation},
			"substring from":                  {Err: filter.ErrUnsupportedOperation},
			"substring ordered":               {Err: filter.ErrUnsupportedOperation},
			"substring reversed range":        {Err: filter.ErrUnsupportedOperation},
			"timestamp greater":               {Want: "@created:[(1704067200 +inf]"},
			"timestamp equal":                 {Want: "@created:[1709251200 1709251200]"},
			"timestamp fraction":              {Want: "@created:[-inf (1.6725311995e+09]"},
			"duration literal":                {Err: filter.ErrFieldTypeMismatch},
			"bytes equal":                     {Want: "@data:{ab}"},
			"bytes ordered":                   {Err: filter.ErrUnsupportedOperation},
			"decimal greater":                 {Err: filter.ErrUnsupportedType},
			"decimal equal":                   {Err: filter.ErrUnsupportedType},
			"decimal trailing zeros":          {Err: filter.ErrUnsupportedType},
			"decimal string literal":          {Err: filter.ErrUnsupportedType},
			"decimal in":                      {Err: filter.ErrUnsupportedType},
			"decimal not a number":            {Err: filter.ErrUnsupportedType},
			"array equal":                     {Want: "@tags:{red}"},
			"array not equal":                 {Want: "-@tags:{red}"},
			"element in array":                {Want: "@tags:{red}"},
			"array in list":                   {Want: "@tags:{green|blue}"},
			"array starts with":               {Want: "@tags:{gr*}"},
			"array ordered":                   {Err: filter.ErrUnsupportedOperation},
			"array equal null":                {Want: "ismissing(@tags)"},
			"and":                             {Want: "(@name:{alice} @age:[(20 +inf])"},
			"or":                              {Want: "((@age:[-inf (20])|(@score:[(2 +inf]))"},
			"and binds tighter than or":       {Want: "((@name:{bob})|((@name:{alice} @age:[(26 +inf])))"},
			"parentheses":                     {Want: "(((@name:{bob})|(@name:{alice})) @age:[(26 +inf])"},
			"not":                             {Want: "-(@name:{alice})"},
			"not and":                         {Want: "-((@active:{true} @age:[(26 +inf]))"},
			"not or":                          {Want: "-(((@name:{alice})|(@age:[-inf (20])))"},
			"double negation":                 {Want: "@active:{true}"},
			"bare literal":                    {Err: filter.ErrInvalidExpression},
			"not substring out of range":      {Err: filter.ErrUnsupportedOperation},
			"unknown or true":                 {Err: filter.ErrUnsupportedOperation},
			"true or unknown":                 {Err: filter.ErrUnsupportedOperation},
			"unknown or false":                {Err: filter.ErrUnsupportedOperation},
			"unknown and false":               {Err: filter.ErrUnsupportedOperation},
			"false and unknown":               {Err: filter.ErrUnsupportedOperation},
			"not unknown":                     {Err: filter.ErrUnsupportedOperation},
			"not unknown or false":            {Err: filter.ErrUnsupportedOperation},
			"not unknown and false":           {Err: filter.ErrUnsupportedOperation},
			"not size of null":                {Err: filter.ErrUnsupportedOperation},
			"not contains null argument":      {Err: filter.ErrUnsupportedOperation},
			"nil filter":                      {Want: "*"},
			"field equal field":               {Err: filter.ErrUnsupportedOperation},
			"field less field":                {Err: filter.ErrUnsupportedOperation},
			"field in list":                   {Err: filter.ErrUnsupportedOperation},
			"field kind mismatch":             {Err: filter.ErrUnsupportedOperation},
		},
	})
}
