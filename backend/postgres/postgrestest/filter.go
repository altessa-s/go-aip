// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package postgrestest

import (
	"github.com/altessa-s/go-aip/backend/postgres"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/metrics"
)

// FilterConformanceBackend returns the scalar filter translator under the filter conformance suite with the expected
// outcome of every case. The in-package conformance test runs it as is; the live-database run (module integration/docker)
// adds a Reference and a Match executing its SQL.
func FilterConformanceBackend() filtertest.Backend {
	type O = filtertest.Outcome

	return filtertest.Backend{
		New: func(policy *filter.Policy, opts ...filter.TranslatorOption) (filtertest.TranslateFunc, error) {
			tr, err := postgres.NewFilterTranslator(policy, postgres.WithFilterOptions(opts...), postgres.WithFilterCollector(metrics.Noop()))
			if err != nil {
				return nil, err
			}

			return func(node filter.Node) (string, error) {
				query, args, err := tr.Translate(node)

				return filtertest.FormatSQL(query, args), err
			}, nil
		},
		Expect: map[string]O{
			"equal string":                    {Want: `"name" = $1 | ["alice"]`},
			"not equal string":                {Want: `("name" != $1 OR "name" IS NULL) | ["alice"]`},
			"less int":                        {Want: `"age" < $1 | [30]`},
			"less or equal int":               {Want: `"age" <= $1 | [30]`},
			"greater float":                   {Want: `"score" > $1 | [1.5]`},
			"greater or equal float":          {Want: `"score" >= $1 | [1.5]`},
			"float literal on int field":      {Err: filter.ErrFieldTypeMismatch},
			"int literal on float field":      {Want: `"score" = $1 | [3]`},
			"negative literal":                {Want: `"age" > $1 | [-1]`},
			"ordered string":                  {Want: `"name" < $1 | ["c"]`},
			"equal bool":                      {Want: `"active" = $1 | [true]`},
			"bare bool":                       {Want: `"active" = TRUE`},
			"negated bare bool":               {Want: `NOT ("active" = TRUE)`},
			"enum value":                      {Want: `"status" = $1 | [1]`},
			"nested field":                    {Want: `"address"."city" = $1 | ["Oslo"]`},
			"nested field mapping":            {Want: `"location"."city" = $1 | ["Oslo"]`},
			"field mapping":                   {Want: `"nickname" = $1 | ["al"]`},
			"has nested":                      {Want: `"address"."city" IS NOT NULL`},
			"equal null":                      {Want: `"note" IS NULL`},
			"not equal null":                  {Want: `"note" IS NOT NULL`},
			"ordered null":                    {Err: filter.ErrUnsupportedOperation},
			"ordered nullable field":          {Want: `"note" < $1 | ["m"]`},
			"equal empty string":              {Want: `"note" = $1 | [""]`},
			"in ints":                         {Want: `"age" IN ($1, $2) | [25, 30]`},
			"in strings":                      {Want: `"name" IN ($1, $2) | ["alice", "carol"]`},
			"in empty list":                   {Want: "1 = 0"},
			"in with null":                    {Want: `("note" IN ($1) OR "note" IS NULL) | ["x"]`},
			"not in":                          {Want: `("age" NOT IN ($1, $2) OR "age" IS NULL) | [25, 30]`},
			"in non-array field":              {Err: filter.ErrInvalidExpression},
			"contains":                        {Want: `strpos("name", $1) > 0 | ["ar"]`},
			"starts with":                     {Want: `starts_with("name", $1) | ["ca"]`},
			"ends with":                       {Want: `right("name", length($1)) = $2 | ["ob", "ob"]`},
			"contains wildcard characters":    {Want: `strpos("name", $1) > 0 | ["%_*"]`},
			"starts with wildcard characters": {Want: `starts_with("name", $1) | ["a%"]`},
			"contains empty string":           {Want: `strpos("name", $1) > 0 | [""]`},
			"contains field":                  {Want: `strpos("name", "nickname") > 0`},
			"starts with field":               {Want: `starts_with("name", "nickname")`},
			"ends with field":                 {Want: `right("name", length("address"."city")) = "address"."city"`},
			"case-sensitive contains":         {Want: `strpos("name", $1) > 0 | ["dave"]`},
			"text search":                     {Want: `("name" ~ $1) OR ("nickname" ~ $2) | ["(?i)ob", "(?i)ob"]`},
			"text search case-insensitive":    {Want: `"name" ~ $1 | ["(?i)DAV"]`},
			"matches":                         {Want: `"name" ~ $1 | ["^[ab]"]`},
			"matches end anchor":              {Want: `"name" ~ $1 | ["ol$"]`},
			"size of string":                  {Want: `length("name") > $1 | [4]`},
			"size function":                   {Want: `length("name") = $1 | [3]`},
			"size of array":                   {Want: `COALESCE(cardinality("tags"), 0) = $1 | [1]`},
			"size of empty array":             {Want: `COALESCE(cardinality("tags"), 0) = $1 | [0]`},
			"substring":                       {Want: `(length("name") >= 2 AND substr("name", 1, 2) = $1) | ["ca"]`},
			"substring from":                  {Want: `(length("name") >= 1 AND substr("name", 2) = $1) | ["ob"]`},
			"substring ordered":               {Want: `(length("name") >= 1 AND substr("name", 1, 1) < $1) | ["c"]`},
			"substring reversed range":        {Err: filter.ErrInvalidExpression},
			"timestamp greater":               {Want: `"created" > $1 | [timestamp("2024-01-01T00:00:00Z")]`},
			"timestamp equal":                 {Want: `"created" = $1 | [timestamp("2024-03-01T00:00:00Z")]`},
			"timestamp fraction":              {Want: `"created" < $1 | [timestamp("2022-12-31T23:59:59.5Z")]`},
			"duration literal":                {Err: filter.ErrFieldTypeMismatch},
			"bytes equal":                     {Want: `"data" = $1 | [b"ab"]`},
			"bytes ordered":                   {Want: `"data" < $1 | [b"b"]`},
			"decimal greater":                 {Want: `"price" > CAST($1 AS NUMERIC) | ["5.25"]`},
			"decimal equal":                   {Want: `"price" = CAST($1 AS NUMERIC) | ["0.1"]`},
			"decimal trailing zeros":          {Want: `"price" = CAST($1 AS NUMERIC) | ["10.5"]`},
			"decimal string literal":          {Want: `"price" >= CAST($1 AS NUMERIC) | ["10.5"]`},
			"decimal in":                      {Want: `"price" IN (CAST($1 AS NUMERIC), CAST($2 AS NUMERIC)) | ["3.1", "7"]`},
			"decimal not a number":            {Err: filter.ErrFieldTypeMismatch},
			"array equal":                     {Want: `$1 = ANY("tags") | ["red"]`},
			"array not equal":                 {Want: `("tags" IS NULL OR NOT ($1 = ANY("tags"))) | ["red"]`},
			"element in array":                {Want: `$1 = ANY("tags") | ["red"]`},
			"array in list":                   {Want: `(($1 = ANY("tags")) OR ($2 = ANY("tags"))) | ["green", "blue"]`},
			"array starts with":               {Want: `EXISTS (SELECT 1 FROM unnest("tags") AS _aip_e(v) WHERE starts_with(_aip_e.v, $1)) | ["gr"]`},
			"array ordered":                   {Want: `$1 > ANY("tags") | ["c"]`},
			"array equal null":                {Want: `"tags" IS NULL`},
			"and":                             {Want: `("name" = $1) AND ("age" > $2) | ["alice", 20]`},
			"or":                              {Want: `("age" < $1) OR ("score" > $2) | [20, 2.0]`},
			"and binds tighter than or":       {Want: `("name" = $1) OR (("name" = $2) AND ("age" > $3)) | ["bob", "alice", 26]`},
			"parentheses":                     {Want: `(("name" = $1) OR ("name" = $2)) AND ("age" > $3) | ["bob", "alice", 26]`},
			"not":                             {Want: `("name" != $1 OR "name" IS NULL) | ["alice"]`},
			"not and":                         {Want: `(NOT ("active" = TRUE)) OR (NOT ("age" > $1)) | [26]`},
			"not or":                          {Want: `(("name" != $1 OR "name" IS NULL)) AND (NOT ("age" < $2)) | ["alice", 20]`},
			"double negation":                 {Want: `"active" = TRUE`},
			"bare literal":                    {Err: filter.ErrInvalidExpression},
			"not substring out of range":      {Want: "(length(\"name\") >= 4 AND substr(\"name\", 1, 4) != $1) | [\"x\"]"},
			"unknown or true":                 {Want: "(\"note\" < $1) OR (\"age\" > $2) | [\"m\", 0]"},
			"true or unknown":                 {Want: "(\"age\" > $1) OR (\"note\" < $2) | [0, \"m\"]"},
			"unknown or false":                {Want: "(\"note\" < $1) OR (\"age\" < $2) | [\"m\", 0]"},
			"unknown and false":               {Want: "(\"note\" < $1) AND (\"age\" < $2) | [\"m\", 0]"},
			"false and unknown":               {Want: "(\"age\" < $1) AND (\"note\" < $2) | [0, \"m\"]"},
			"not unknown":                     {Want: "NOT (\"note\" < $1) | [\"m\"]"},
			"not unknown or false":            {Want: "(NOT (\"note\" < $1)) AND ((\"name\" != $2 OR \"name\" IS NULL)) | [\"m\", \"zzz\"]"},
			"not unknown and false":           {Want: "(NOT (\"note\" < $1)) OR (NOT (\"age\" < $2)) | [\"m\", 0]"},
			"not size of null":                {Want: "length(\"note\") != $1 | [0]"},
			"not contains null argument":      {Want: "(NOT (strpos(\"name\", \"note\") > 0) OR \"name\" IS NULL)"},
			"nil filter":                      {Want: "1 = 1"},
			"field equal field":               {Want: `"name" IS NOT DISTINCT FROM "address"."city"`},
			"field less field":                {Want: `"name" < "nickname"`},
			"field in list":                   {Want: `(("name" IS NOT DISTINCT FROM "address"."city") OR ("name" = $1)) | ["bob"]`},
			"field kind mismatch":             {Err: filter.ErrFieldTypeMismatch},
		},
	}
}
