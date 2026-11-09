// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadbtest

import (
	"github.com/altessa-s/go-aip/backend/mariadb"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/metrics"
)

// knownCollation pins a genuine backend limit (filtertest.Outcome.Known): the outcome follows the column collation rather than
// the evaluator's exact comparison.
const knownCollation = "comparisons follow the column collation, case-insensitive by default, so contains(\"dave\") matches \"Dave\" " +
	"(documented; a _bin column collation gives exact semantics)"

// FilterConformanceBackend returns the scalar filter translator under the filter conformance suite with the expected
// outcome of every case. The in-package conformance test runs it as is; the live-database run (module integration/docker)
// adds a Reference and a Match executing its SQL.
//
//nolint:lll // One golden rendering per line keeps the table reviewable.
func FilterConformanceBackend() filtertest.Backend {
	type O = filtertest.Outcome

	return filtertest.Backend{
		New: func(policy *filter.Policy, opts ...filter.TranslatorOption) (filtertest.TranslateFunc, error) {
			tr, err := mariadb.NewFilterTranslator(policy, mariadb.WithFilterOptions(opts...), mariadb.WithFilterCollector(metrics.Noop()))
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
			"bare bool":                       {Want: "`active` = TRUE"},
			"negated bare bool":               {Want: "NOT (`active` = TRUE)"},
			"enum value":                      {Want: "`status` = ? | [1]"},
			"nested field":                    {Want: "`address`.`city` = ? | [\"Oslo\"]"},
			"nested field mapping":            {Want: "`location`.`city` = ? | [\"Oslo\"]"},
			"field mapping":                   {Want: "`nickname` = ? | [\"al\"]"},
			"has nested":                      {Want: "`address`.`city` IS NOT NULL"},
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
			"contains":                        {Want: "LOCATE(?, `name`) > 0 | [\"ar\"]"},
			"starts with":                     {Want: "LOCATE(?, `name`) = 1 | [\"ca\"]"},
			"ends with":                       {Want: "(CHAR_LENGTH(`name`) >= CHAR_LENGTH(?) AND RIGHT(`name`, CHAR_LENGTH(?)) = ?) | [\"ob\", \"ob\", \"ob\"]"},
			"contains wildcard characters":    {Want: "LOCATE(?, `name`) > 0 | [\"%_*\"]"},
			"starts with wildcard characters": {Want: "LOCATE(?, `name`) = 1 | [\"a%\"]"},
			"contains empty string":           {Want: "LOCATE(?, `name`) > 0 | [\"\"]"},
			"contains field":                  {Want: "LOCATE(`nickname`, `name`) > 0"},
			"starts with field":               {Want: "LOCATE(`nickname`, `name`) = 1"},
			"ends with field":                 {Want: "(CHAR_LENGTH(`name`) >= CHAR_LENGTH(`address`.`city`) AND RIGHT(`name`, CHAR_LENGTH(`address`.`city`)) = `address`.`city`)"},
			"case-sensitive contains":         {Want: "LOCATE(?, `name`) > 0 | [\"dave\"]", Known: knownCollation},
			"text search":                     {Want: "(`name` REGEXP ?) OR (`nickname` REGEXP ?) | [\"(?i)ob\", \"(?i)ob\"]"},
			"text search case-insensitive":    {Want: "`name` REGEXP ? | [\"(?i)DAV\"]"},
			"matches":                         {Want: "`name` REGEXP ? | [\"^[ab]\"]"},
			"matches end anchor":              {Want: "`name` REGEXP ? | [\"ol\\\\z\"]"},
			"size of string":                  {Want: "CHAR_LENGTH(`name`) > ? | [4]"},
			"size function":                   {Want: "CHAR_LENGTH(`name`) = ? | [3]"},
			"size of array":                   {Want: "COALESCE(JSON_LENGTH(`tags`), 0) = ? | [1]"},
			"size of empty array":             {Want: "COALESCE(JSON_LENGTH(`tags`), 0) = ? | [0]"},
			"substring":                       {Want: "(CHAR_LENGTH(`name`) >= 2 AND SUBSTRING(`name`, 1, 2) = ?) | [\"ca\"]"},
			"substring from":                  {Want: "(CHAR_LENGTH(`name`) >= 1 AND SUBSTRING(`name`, 2) = ?) | [\"ob\"]"},
			"substring ordered":               {Want: "(CHAR_LENGTH(`name`) >= 1 AND SUBSTRING(`name`, 1, 1) < ?) | [\"c\"]"},
			"substring reversed range":        {Err: filter.ErrInvalidExpression},
			"timestamp greater":               {Want: "`created` > ? | [timestamp(\"2024-01-01T00:00:00Z\")]"},
			"timestamp equal":                 {Want: "`created` = ? | [timestamp(\"2024-03-01T00:00:00Z\")]"},
			"timestamp fraction":              {Want: "`created` < ? | [timestamp(\"2022-12-31T23:59:59.5Z\")]"},
			"duration literal":                {Err: filter.ErrFieldTypeMismatch},
			"bytes equal":                     {Want: "`data` = ? | [b\"ab\"]"},
			"bytes ordered":                   {Want: "`data` < ? | [b\"b\"]"},
			"decimal greater":                 {Want: "`price` > CAST(? AS DECIMAL(65,30)) | [\"5.25\"]"},
			"decimal equal":                   {Want: "`price` = CAST(? AS DECIMAL(65,30)) | [\"0.1\"]"},
			"decimal trailing zeros":          {Want: "`price` = CAST(? AS DECIMAL(65,30)) | [\"10.5\"]"},
			"decimal string literal":          {Want: "`price` >= CAST(? AS DECIMAL(65,30)) | [\"10.5\"]"},
			"decimal in":                      {Want: "`price` IN (CAST(? AS DECIMAL(65,30)), CAST(? AS DECIMAL(65,30))) | [\"3.1\", \"7\"]"},
			"decimal not a number":            {Err: filter.ErrFieldTypeMismatch},
			"array equal":                     {Want: "JSON_CONTAINS(`tags`, JSON_ARRAY(?)) | [\"red\"]"},
			"array not equal":                 {Want: "(`tags` IS NULL OR NOT (JSON_CONTAINS(`tags`, JSON_ARRAY(?)))) | [\"red\"]"},
			"element in array":                {Want: "JSON_CONTAINS(`tags`, JSON_ARRAY(?)) | [\"red\"]"},
			"array in list":                   {Want: "((JSON_CONTAINS(`tags`, JSON_ARRAY(?))) OR (JSON_CONTAINS(`tags`, JSON_ARRAY(?)))) | [\"green\", \"blue\"]"},
			"array starts with":               {Want: "EXISTS (SELECT 1 FROM JSON_TABLE(`tags`, '$[*]' COLUMNS (v LONGTEXT PATH '$')) AS _aip_e WHERE LOCATE(?, _aip_e.v) = 1) | [\"gr\"]"},
			"array ordered":                   {Want: "EXISTS (SELECT 1 FROM JSON_TABLE(`tags`, '$[*]' COLUMNS (v LONGTEXT PATH '$')) AS _aip_e WHERE _aip_e.v < ?) | [\"c\"]"},
			"array equal null":                {Want: "`tags` IS NULL"},
			"and":                             {Want: "(`name` = ?) AND (`age` > ?) | [\"alice\", 20]"},
			"or":                              {Want: "(`age` < ?) OR (`score` > ?) | [20, 2.0]"},
			"and binds tighter than or":       {Want: "(`name` = ?) OR ((`name` = ?) AND (`age` > ?)) | [\"bob\", \"alice\", 26]"},
			"parentheses":                     {Want: "((`name` = ?) OR (`name` = ?)) AND (`age` > ?) | [\"bob\", \"alice\", 26]"},
			"not":                             {Want: "(`name` != ? OR `name` IS NULL) | [\"alice\"]"},
			"not and":                         {Want: "(NOT (`active` = TRUE)) OR (NOT (`age` > ?)) | [26]"},
			"not or":                          {Want: "((`name` != ? OR `name` IS NULL)) AND (NOT (`age` < ?)) | [\"alice\", 20]"},
			"double negation":                 {Want: "`active` = TRUE"},
			"bare literal":                    {Err: filter.ErrInvalidExpression},
			"not substring out of range":      {Want: "(CHAR_LENGTH(`name`) >= 4 AND SUBSTRING(`name`, 1, 4) != ?) | [\"x\"]"},
			"unknown or true":                 {Want: "(`note` < ?) OR (`age` > ?) | [\"m\", 0]"},
			"true or unknown":                 {Want: "(`age` > ?) OR (`note` < ?) | [0, \"m\"]"},
			"unknown or false":                {Want: "(`note` < ?) OR (`age` < ?) | [\"m\", 0]"},
			"unknown and false":               {Want: "(`note` < ?) AND (`age` < ?) | [\"m\", 0]"},
			"false and unknown":               {Want: "(`age` < ?) AND (`note` < ?) | [0, \"m\"]"},
			"not unknown":                     {Want: "NOT (`note` < ?) | [\"m\"]"},
			"not unknown or false":            {Want: "(NOT (`note` < ?)) AND ((`name` != ? OR `name` IS NULL)) | [\"m\", \"zzz\"]"},
			"not unknown and false":           {Want: "(NOT (`note` < ?)) OR (NOT (`age` < ?)) | [\"m\", 0]"},
			"not size of null":                {Want: "CHAR_LENGTH(`note`) != ? | [0]"},
			"not contains null argument":      {Want: "(NOT (LOCATE(`note`, `name`) > 0) OR `name` IS NULL)"},
			"nil filter":                      {Want: "1 = 1"},
			"field equal field":               {Want: "`name` <=> `address`.`city`"},
			"field less field":                {Want: "`name` < `nickname`"},
			"field in list":                   {Want: "((`name` <=> `address`.`city`) OR (`name` = ?)) | [\"bob\"]"},
			"field kind mismatch":             {Err: filter.ErrFieldTypeMismatch},
		},
	}
}
