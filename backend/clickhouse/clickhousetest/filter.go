// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package clickhousetest

import (
	"github.com/altessa-s/go-aip/backend/clickhouse"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/metrics"
)

// FilterConformanceBackend returns the scalar filter translator under the filter conformance suite with the expected
// outcome of every case. The in-package conformance test runs it as is; the live-database run (module integration/docker)
// adds a Reference and a Match executing its SQL.
//
//nolint:lll // One golden rendering per line keeps the table reviewable.
func FilterConformanceBackend() filtertest.Backend {
	type O = filtertest.Outcome

	return filtertest.Backend{
		New: func(policy *filter.Policy, opts ...filter.TranslatorOption) (filtertest.TranslateFunc, error) {
			tr, err := clickhouse.NewFilterTranslator(policy, clickhouse.WithFilterOptions(opts...), clickhouse.WithFilterCollector(metrics.Noop()))
			if err != nil {
				return nil, err
			}

			return func(node filter.Node) (string, error) {
				query, args, err := tr.Translate(node)

				return filtertest.FormatSQL(query, args), err
			}, nil
		},
		Expect: map[string]O{
			"equal string":                    {Want: "`name` = ? | [\"alice\"]"},
			"not equal string":                {Want: "(`name` != ? OR `name` IS NULL) | [\"alice\"]"},
			"less int":                        {Want: "`age` < ? | [30]"},
			"less or equal int":               {Want: "`age` <= ? | [30]"},
			"greater float":                   {Want: "`score` > ? | [1.5]"},
			"greater or equal float":          {Want: "`score` >= ? | [1.5]"},
			"float literal on int field":      {Err: filter.ErrFieldTypeMismatch},
			"int literal on float field":      {Want: "`score` = ? | [3]"},
			"negative literal":                {Want: "`age` > ? | [-1]"},
			"ordered string":                  {Want: "`name` < ? | [\"c\"]"},
			"equal bool":                      {Want: "`active` = ? | [true]"},
			"bare bool":                       {Want: "`active` = true"},
			"negated bare bool":               {Want: "NOT (`active` = true)"},
			"enum value":                      {Want: "`status` = ? | [1]"},
			"nested field":                    {Want: "`address.city` = ? | [\"Oslo\"]"},
			"nested field mapping":            {Want: "`location.city` = ? | [\"Oslo\"]"},
			"field mapping":                   {Want: "`nickname` = ? | [\"al\"]"},
			"has nested":                      {Want: "`address.city` IS NOT NULL"},
			"equal null":                      {Want: "`note` IS NULL"},
			"not equal null":                  {Want: "`note` IS NOT NULL"},
			"ordered null":                    {Err: filter.ErrUnsupportedOperation},
			"ordered nullable field":          {Want: "`note` < ? | [\"m\"]"},
			"equal empty string":              {Want: "`note` = ? | [\"\"]"},
			"in ints":                         {Want: "`age` IN (?, ?) | [25, 30]"},
			"in strings":                      {Want: "`name` IN (?, ?) | [\"alice\", \"carol\"]"},
			"in empty list":                   {Want: "1 = 0"},
			"in with null":                    {Want: "(`note` IN (?) OR `note` IS NULL) | [\"x\"]"},
			"not in":                          {Want: "(`age` NOT IN (?, ?) OR `age` IS NULL) | [25, 30]"},
			"in non-array field":              {Err: filter.ErrInvalidExpression},
			"contains":                        {Want: "position(`name`, ?) > 0 | [\"ar\"]"},
			"starts with":                     {Want: "startsWith(`name`, ?) | [\"ca\"]"},
			"ends with":                       {Want: "endsWith(`name`, ?) | [\"ob\"]"},
			"contains wildcard characters":    {Want: "position(`name`, ?) > 0 | [\"%_*\"]"},
			"starts with wildcard characters": {Want: "startsWith(`name`, ?) | [\"a%\"]"},
			"contains empty string":           {Want: "position(`name`, ?) > 0 | [\"\"]"},
			"contains field":                  {Want: "position(`name`, `nickname`) > 0"},
			"starts with field":               {Want: "startsWith(`name`, `nickname`)"},
			"ends with field":                 {Want: "endsWith(`name`, `address.city`)"},
			"case-sensitive contains":         {Want: "position(`name`, ?) > 0 | [\"dave\"]"},
			"text search":                     {Want: "(match(`name`, ?)) OR (match(`nickname`, ?)) | [\"(?i)ob\", \"(?i)ob\"]"},
			"text search case-insensitive":    {Want: "match(`name`, ?) | [\"(?i)DAV\"]"},
			"matches":                         {Want: "match(`name`, ?) | [\"^[ab]\"]"},
			"matches end anchor":              {Want: "match(`name`, ?) | [\"ol$\"]"},
			"size of string":                  {Want: "if(startsWith(toTypeName(`name`), 'Array('), length(`name`), lengthUTF8(toString(`name`))) > ? | [4]"},
			"size function":                   {Want: "if(startsWith(toTypeName(`name`), 'Array('), length(`name`), lengthUTF8(toString(`name`))) = ? | [3]"},
			"size of array":                   {Want: "length(`tags`) = ? | [1]"},
			"size of empty array":             {Want: "length(`tags`) = ? | [0]"},
			"substring":                       {Want: "(if(startsWith(toTypeName(`name`), 'Array('), length(`name`), lengthUTF8(toString(`name`))) >= 2 AND substringUTF8(`name`, 1, 2) = ?) | [\"ca\"]"},
			"substring from":                  {Want: "(if(startsWith(toTypeName(`name`), 'Array('), length(`name`), lengthUTF8(toString(`name`))) >= 1 AND substringUTF8(`name`, 2) = ?) | [\"ob\"]"},
			"substring ordered":               {Want: "(if(startsWith(toTypeName(`name`), 'Array('), length(`name`), lengthUTF8(toString(`name`))) >= 1 AND substringUTF8(`name`, 1, 1) < ?) | [\"c\"]"},
			"substring reversed range":        {Err: filter.ErrInvalidExpression},
			"timestamp greater":               {Want: "`created` > toDateTime64(?, 0, 'UTC') | [\"2024-01-01 00:00:00\"]"},
			"timestamp equal":                 {Want: "`created` = toDateTime64(?, 0, 'UTC') | [\"2024-03-01 00:00:00\"]"},
			"timestamp fraction":              {Want: "`created` < toDateTime64(?, 1, 'UTC') | [\"2022-12-31 23:59:59.5\"]"},
			"duration literal":                {Err: filter.ErrFieldTypeMismatch},
			"bytes equal":                     {Want: "`data` = ? | [b\"ab\"]"},
			"bytes ordered":                   {Want: "`data` < ? | [b\"b\"]"},
			"decimal greater":                 {Want: "`price` > toDecimal128(?, 2) | [\"5.25\"]"},
			"decimal equal":                   {Want: "`price` = toDecimal128(?, 1) | [\"0.1\"]"},
			"decimal trailing zeros":          {Want: "`price` = toDecimal128(?, 1) | [\"10.5\"]"},
			"decimal string literal":          {Want: "`price` >= toDecimal128(?, 1) | [\"10.5\"]"},
			"decimal in":                      {Want: "`price` IN (toDecimal128(?, 1), toDecimal128(?, 0)) | [\"3.1\", \"7\"]"},
			"decimal not a number":            {Err: filter.ErrFieldTypeMismatch},
			"array equal":                     {Want: "has(`tags`, ?) | [\"red\"]"},
			"array not equal":                 {Want: "NOT (has(`tags`, ?)) | [\"red\"]"},
			"element in array":                {Want: "has(`tags`, ?) | [\"red\"]"},
			"array in list":                   {Want: "((has(`tags`, ?)) OR (has(`tags`, ?))) | [\"green\", \"blue\"]"},
			"array starts with":               {Want: "arrayExists(_aip_e -> startsWith(_aip_e, ?), `tags`) | [\"gr\"]"},
			"array ordered":                   {Want: "arrayExists(_aip_e -> _aip_e < ?, `tags`) | [\"c\"]"},
			"array equal null":                {Want: "empty(`tags`)"},
			"and":                             {Want: "(`name` = ?) AND (`age` > ?) | [\"alice\", 20]"},
			"or":                              {Want: "(`age` < ?) OR (`score` > ?) | [20, 2.0]"},
			"and binds tighter than or":       {Want: "(`name` = ?) OR ((`name` = ?) AND (`age` > ?)) | [\"bob\", \"alice\", 26]"},
			"parentheses":                     {Want: "((`name` = ?) OR (`name` = ?)) AND (`age` > ?) | [\"bob\", \"alice\", 26]"},
			"not":                             {Want: "(`name` != ? OR `name` IS NULL) | [\"alice\"]"},
			"not and":                         {Want: "(NOT (`active` = true)) OR (NOT (`age` > ?)) | [26]"},
			"not or":                          {Want: "((`name` != ? OR `name` IS NULL)) AND (NOT (`age` < ?)) | [\"alice\", 20]"},
			"double negation":                 {Want: "`active` = true"},
			"bare literal":                    {Err: filter.ErrInvalidExpression},
			"not substring out of range":      {Want: "(if(startsWith(toTypeName(`name`), 'Array('), length(`name`), lengthUTF8(toString(`name`))) >= 4 AND substringUTF8(`name`, 1, 4) != ?) | [\"x\"]"},
			"unknown or true":                 {Want: "(`note` < ?) OR (`age` > ?) | [\"m\", 0]"},
			"true or unknown":                 {Want: "(`age` > ?) OR (`note` < ?) | [0, \"m\"]"},
			"unknown or false":                {Want: "(`note` < ?) OR (`age` < ?) | [\"m\", 0]"},
			"unknown and false":               {Want: "(`note` < ?) AND (`age` < ?) | [\"m\", 0]"},
			"false and unknown":               {Want: "(`age` < ?) AND (`note` < ?) | [0, \"m\"]"},
			"not unknown":                     {Want: "NOT (`note` < ?) | [\"m\"]"},
			"not unknown or false":            {Want: "(NOT (`note` < ?)) AND ((`name` != ? OR `name` IS NULL)) | [\"m\", \"zzz\"]"},
			"not unknown and false":           {Want: "(NOT (`note` < ?)) OR (NOT (`age` < ?)) | [\"m\", 0]"},
			"not size of null":                {Want: "if(startsWith(toTypeName(`note`), 'Array('), length(`note`), lengthUTF8(toString(`note`))) != ? | [0]"},
			"not contains null argument":      {Want: "(NOT (position(`name`, `note`) > 0) OR `name` IS NULL)"},
			"nil filter":                      {Want: "1 = 1"},
			"field equal field":               {Want: "COALESCE(`name` = `address.city`, `name` IS NULL AND `address.city` IS NULL)"},
			"field less field":                {Want: "`name` < `nickname`"},
			"field in list":                   {Want: "((COALESCE(`name` = `address.city`, `name` IS NULL AND `address.city` IS NULL)) OR (`name` = ?)) | [\"bob\"]"},
			"field kind mismatch":             {Err: filter.ErrFieldTypeMismatch},
		},
	}
}
