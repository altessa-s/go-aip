// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package meili_test

import (
	"testing"

	"github.com/altessa-s/go-aip/backend/meili"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/metrics"
)

// TestConformance runs the suite against the latest Meilisearch release without the experimental containsFilter feature.
func TestFilterConformance(t *testing.T) {
	t.Parallel()

	type O = filtertest.Outcome

	filtertest.RunConformance(t, filtertest.Backend{
		New: func(policy *filter.Policy, opts ...filter.TranslatorOption) (filtertest.TranslateFunc, error) {
			tr, err := meili.NewFilterTranslator(policy, meili.WithFilterOptions(opts...), meili.WithFilterCollector(metrics.Noop()))
			if err != nil {
				return nil, err
			}

			return tr.Translate, nil
		},
		Expect: map[string]O{
			"equal string":                    {Want: `name = "alice"`},
			"not equal string":                {Want: `name != "alice"`},
			"less int":                        {Want: "age < 30"},
			"less or equal int":               {Want: "age <= 30"},
			"greater float":                   {Want: "score > 1.5"},
			"greater or equal float":          {Want: "score >= 1.5"},
			"float literal on int field":      {Err: filter.ErrFieldTypeMismatch},
			"int literal on float field":      {Want: "score = 3"},
			"negative literal":                {Want: "age > -1"},
			"ordered string":                  {Want: `name < "c"`},
			"equal bool":                      {Want: "active = true"},
			"bare bool":                       {Want: "active = true"},
			"negated bare bool":               {Want: "active = false"},
			"enum value":                      {Want: "status = 1"},
			"nested field":                    {Want: `address.city = "Oslo"`},
			"nested field mapping":            {Want: `location.city = "Oslo"`},
			"field mapping":                   {Want: `nickname = "al"`},
			"has nested":                      {Want: "address.city EXISTS"},
			"equal null":                      {Want: "(note IS NULL OR note NOT EXISTS)"},
			"not equal null":                  {Want: "(note EXISTS AND note IS NOT NULL)"},
			"ordered null":                    {Err: filter.ErrUnsupportedOperation},
			"ordered nullable field":          {Want: `note < "m"`},
			"equal empty string":              {Want: "note IS EMPTY"},
			"in ints":                         {Want: "age IN [25, 30]"},
			"in strings":                      {Want: `name IN ["alice", "carol"]`},
			"in empty list":                   {Want: "age IN []"},
			"in with null":                    {Want: `(note IN ["x"] OR (note IS NULL OR note NOT EXISTS))`},
			"not in":                          {Want: "NOT (age IN [25, 30])"},
			"in non-array field":              {Err: filter.ErrInvalidExpression},
			"contains":                        {Err: filter.ErrUnsupportedOperation},
			"starts with":                     {Want: `name STARTS WITH "ca"`},
			"ends with":                       {Err: filter.ErrUnsupportedOperation},
			"contains wildcard characters":    {Err: filter.ErrUnsupportedOperation},
			"starts with wildcard characters": {Want: `name STARTS WITH "a%"`},
			"contains empty string":           {Err: filter.ErrUnsupportedOperation},
			"contains field":                  {Err: filter.ErrUnsupportedOperation},
			"starts with field":               {Err: filter.ErrUnsupportedOperation},
			"ends with field":                 {Err: filter.ErrUnsupportedOperation},
			"case-sensitive contains":         {Err: filter.ErrUnsupportedOperation},
			"text search":                     {Err: filter.ErrUnsupportedOperation},
			"text search case-insensitive":    {Err: filter.ErrUnsupportedOperation},
			"matches":                         {Err: filter.ErrUnsupportedOperation},
			"matches end anchor":              {Err: filter.ErrUnsupportedOperation},
			"size of string":                  {Err: filter.ErrUnsupportedOperation},
			"size function":                   {Err: filter.ErrUnsupportedOperation},
			"size of array":                   {Err: filter.ErrUnsupportedOperation},
			"size of empty array":             {Want: "(tags NOT EXISTS OR tags IS NULL OR tags IS EMPTY)"},
			"substring":                       {Err: filter.ErrUnsupportedOperation},
			"substring from":                  {Err: filter.ErrUnsupportedOperation},
			"substring ordered":               {Err: filter.ErrUnsupportedOperation},
			"substring reversed range":        {Err: filter.ErrUnsupportedOperation},
			"timestamp greater":               {Want: "created > 1704067200"},
			"timestamp equal":                 {Want: "created = 1709251200"},
			"timestamp fraction":              {Want: "created < 1672531199.5"},
			"duration literal":                {Err: filter.ErrFieldTypeMismatch},
			"bytes equal":                     {Want: `data = "ab"`},
			"bytes ordered":                   {Want: `data < "b"`},
			"decimal greater":                 {Err: filter.ErrUnsupportedType},
			"decimal equal":                   {Err: filter.ErrUnsupportedType},
			"decimal trailing zeros":          {Err: filter.ErrUnsupportedType},
			"decimal string literal":          {Err: filter.ErrUnsupportedType},
			"decimal in":                      {Err: filter.ErrUnsupportedType},
			"decimal not a number":            {Err: filter.ErrUnsupportedType},
			"array equal":                     {Want: `tags = "red"`},
			"array not equal":                 {Want: `tags != "red"`},
			"element in array":                {Want: `tags = "red"`},
			"array in list":                   {Want: `tags IN ["green", "blue"]`},
			"array starts with":               {Want: `tags STARTS WITH "gr"`},
			"array ordered":                   {Want: `tags < "c"`},
			"array equal null":                {Want: "(tags IS NULL OR tags NOT EXISTS)"},
			"and":                             {Want: `(name = "alice") AND (age > 20)`},
			"or":                              {Want: "(age < 20) OR (score > 2)"},
			"and binds tighter than or":       {Want: `(name = "bob") OR ((name = "alice") AND (age > 26))`},
			"parentheses":                     {Want: `((name = "bob") OR (name = "alice")) AND (age > 26)`},
			"not":                             {Want: `name != "alice"`},
			"not and":                         {Want: "(active = false) OR (age <= 26)"},
			"not or":                          {Want: `(name != "alice") AND (age >= 20)`},
			"double negation":                 {Want: "active = true"},
			"bare literal":                    {Err: filter.ErrInvalidExpression},
			"not substring out of range":      {Err: filter.ErrUnsupportedOperation},
			"unknown or true":                 {Want: "(note < \"m\") OR (age > 0)"},
			"true or unknown":                 {Want: "(age > 0) OR (note < \"m\")"},
			"unknown or false":                {Want: "(note < \"m\") OR (age < 0)"},
			"unknown and false":               {Want: "(note < \"m\") AND (age < 0)"},
			"false and unknown":               {Want: "(age < 0) AND (note < \"m\")"},
			"not unknown":                     {Want: `note >= "m"`},
			"not unknown or false":            {Want: `(note >= "m") AND (name != "zzz")`},
			"not unknown and false":           {Want: `(note >= "m") OR (age >= 0)`},
			"not size of null":                {Want: "(note EXISTS AND note IS NOT NULL AND note IS NOT EMPTY)"},
			"not contains null argument":      {Err: filter.ErrUnsupportedOperation},
			"field equal field":               {Err: filter.ErrUnsupportedOperation},
			"field less field":                {Err: filter.ErrUnsupportedOperation},
			"field in list":                   {Err: filter.ErrUnsupportedOperation},
			"field kind mismatch":             {Err: filter.ErrUnsupportedOperation},
			"nil filter":                      {},
		},
	})
}
