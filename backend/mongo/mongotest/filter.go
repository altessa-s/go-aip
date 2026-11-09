// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mongotest

import (
	"fmt"
	"maps"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/altessa-s/go-aip/backend/mongo"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/metrics"
)

// FilterConformanceBackend returns the MongoDB filter translator under the filtertest conformance suite: New renders the ordered
// query (TranslateD) as relaxed Extended JSON and fails when Translate and TranslateD disagree on an error; Expect holds the
// outcome of every case. The live suite of integration/docker adds a Match executing the query on a server and a Reference.
func FilterConformanceBackend() filtertest.Backend {
	return filtertest.Backend{
		New: func(policy *filter.Policy, opts ...filter.TranslatorOption) (filtertest.TranslateFunc, error) {
			tr, err := mongo.NewFilterTranslator(policy, mongo.WithFilterOptions(opts...), mongo.WithFilterCollector(metrics.Noop()))
			if err != nil {
				return nil, err
			}

			return func(node filter.Node) (string, error) {
				d, err := tr.TranslateD(node)
				if _, errM := tr.Translate(node); (errM == nil) != (err == nil) {
					return "", fmt.Errorf("translate: Translate() error %v, TranslateD() error %v", errM, err)
				}

				if err != nil {
					return "", err
				}

				if d == nil {
					d = bson.D{}
				}

				out, err := bson.MarshalExtJSON(d, false, false)

				return string(out), err
			}, nil
		},
		Expect: maps.Clone(conformanceExpect),
	}
}

// conformanceExpect is the outcome of every filtertest conformance case for the MongoDB translator, the query rendered as relaxed
// Extended JSON. The queries stay on one line each so a diff shows the case that changed.
//
//nolint:lll,goconst // one expected query per line
var conformanceExpect = map[string]filtertest.Outcome{
	"equal string":                    {Want: `{"name":"alice"}`},
	"not equal string":                {Want: `{"name":{"$ne":"alice"}}`},
	"less int":                        {Want: `{"age":{"$lt":30}}`},
	"less or equal int":               {Want: `{"age":{"$lte":30}}`},
	"greater float":                   {Want: `{"score":{"$gt":1.5}}`},
	"greater or equal float":          {Want: `{"score":{"$gte":1.5}}`},
	"float literal on int field":      {Err: filter.ErrFieldTypeMismatch},
	"int literal on float field":      {Want: `{"score":3}`},
	"negative literal":                {Want: `{"age":{"$gt":-1}}`},
	"ordered string":                  {Want: `{"name":{"$lt":"c"}}`},
	"equal bool":                      {Want: `{"active":true}`},
	"bare bool":                       {Want: `{"active":true}`},
	"negated bare bool":               {Want: `{"active":{"$ne":true}}`},
	"enum value":                      {Want: `{"status":1}`},
	"nested field":                    {Want: `{"address.city":"Oslo"}`},
	"nested field mapping":            {Want: `{"location.city":"Oslo"}`},
	"field mapping":                   {Want: `{"nickname":"al"}`},
	"has nested":                      {Want: `{"address.city":{"$exists":true}}`},
	"equal null":                      {Want: `{"note":null}`},
	"not equal null":                  {Want: `{"note":{"$ne":null}}`},
	"ordered null":                    {Err: filter.ErrUnsupportedOperation},
	"ordered nullable field":          {Want: `{"note":{"$lt":"m"}}`},
	"equal empty string":              {Want: `{"note":""}`},
	"in ints":                         {Want: `{"age":{"$in":[25,30]}}`},
	"in strings":                      {Want: `{"name":{"$in":["alice","carol"]}}`},
	"in empty list":                   {Want: `{"age":{"$in":[]}}`},
	"in with null":                    {Want: `{"note":{"$in":["x",null]}}`},
	"not in":                          {Want: `{"$nor":[{"age":{"$in":[25,30]}}]}`},
	"in non-array field":              {Err: filter.ErrInvalidExpression},
	"contains":                        {Want: `{"name":{"$regex":"ar"}}`},
	"starts with":                     {Want: `{"name":{"$regex":"^ca"}}`},
	"ends with":                       {Want: `{"name":{"$regex":"ob\\z"}}`},
	"contains wildcard characters":    {Want: `{"name":{"$regex":"%_\\*"}}`},
	"starts with wildcard characters": {Want: `{"name":{"$regex":"^a%"}}`},
	"contains empty string":           {Want: `{"name":{"$regex":""}}`},
	"contains field":                  {Want: `{"$expr":{"$cond":[{"$and":[{"$eq":[{"$type":{"$ifNull":["$name",null]}},"string"]},{"$eq":[{"$type":{"$ifNull":["$nickname",null]}},"string"]}]},{"$ne":[{"$indexOfCP":[{"$ifNull":["$name",null]},{"$ifNull":["$nickname",null]}]},-1]},false]}}`},
	"starts with field":               {Want: `{"$expr":{"$cond":[{"$and":[{"$eq":[{"$type":{"$ifNull":["$name",null]}},"string"]},{"$eq":[{"$type":{"$ifNull":["$nickname",null]}},"string"]}]},{"$eq":[{"$indexOfCP":[{"$ifNull":["$name",null]},{"$ifNull":["$nickname",null]}]},0]},false]}}`},
	"ends with field":                 {Want: `{"$expr":{"$cond":[{"$and":[{"$eq":[{"$type":{"$ifNull":["$name",null]}},"string"]},{"$eq":[{"$type":{"$ifNull":["$address.city",null]}},"string"]}]},{"$cond":[{"$gte":[{"$strLenCP":{"$ifNull":["$name",null]}},{"$strLenCP":{"$ifNull":["$address.city",null]}}]},{"$eq":[{"$substrCP":[{"$ifNull":["$name",null]},{"$subtract":[{"$strLenCP":{"$ifNull":["$name",null]}},{"$strLenCP":{"$ifNull":["$address.city",null]}}]},{"$strLenCP":{"$ifNull":["$address.city",null]}}]},{"$ifNull":["$address.city",null]}]},false]},false]}}`},
	"case-sensitive contains":         {Want: `{"name":{"$regex":"dave"}}`},
	"text search":                     {Want: `{"$or":[{"name":{"$regex":"(?i)ob"}},{"nickname":{"$regex":"(?i)ob"}}]}`},
	"text search case-insensitive":    {Want: `{"name":{"$regex":"(?i)DAV"}}`},
	"matches":                         {Want: `{"name":{"$regex":"^[ab]"}}`},
	"matches end anchor":              {Want: `{"name":{"$regex":"ol\\z"}}`},
	"size of string":                  {Want: `{"$expr":{"$and":[{"$ne":[{"$cond":[{"$isArray":{"$ifNull":["$name",[]]}},{"$size":{"$ifNull":["$name",[]]}},{"$cond":[{"$eq":[{"$type":"$name"},"string"]},{"$strLenCP":"$name"},null]}]},null]},{"$gt":[{"$cond":[{"$isArray":{"$ifNull":["$name",[]]}},{"$size":{"$ifNull":["$name",[]]}},{"$cond":[{"$eq":[{"$type":"$name"},"string"]},{"$strLenCP":"$name"},null]}]},4]}]}}`},
	"size function":                   {Want: `{"$or":[{"name":{"$size":3}},{"$expr":{"$eq":[{"$cond":[{"$eq":[{"$type":"$name"},"string"]},{"$strLenCP":"$name"},null]},3]}}]}`},
	"size of array":                   {Want: `{"$expr":{"$eq":[{"$cond":[{"$isArray":{"$ifNull":["$tags",[]]}},{"$size":{"$ifNull":["$tags",[]]}},{"$cond":[{"$eq":[{"$type":"$tags"},"string"]},{"$strLenCP":"$tags"},null]}]},1]}}`},
	"size of empty array":             {Want: `{"$expr":{"$eq":[{"$cond":[{"$isArray":{"$ifNull":["$tags",[]]}},{"$size":{"$ifNull":["$tags",[]]}},{"$cond":[{"$eq":[{"$type":"$tags"},"string"]},{"$strLenCP":"$tags"},null]}]},0]}}`},
	"substring":                       {Want: `{"$expr":{"$and":[{"$ne":[{"$cond":[{"$eq":[{"$type":{"$ifNull":["$name",null]}},"string"]},{"$cond":[{"$gte":[{"$strLenCP":{"$ifNull":["$name",null]}},2]},{"$substrCP":[{"$ifNull":["$name",null]},0,2]},null]},null]},null]},{"$eq":[{"$cond":[{"$eq":[{"$type":{"$ifNull":["$name",null]}},"string"]},{"$cond":[{"$gte":[{"$strLenCP":{"$ifNull":["$name",null]}},2]},{"$substrCP":[{"$ifNull":["$name",null]},0,2]},null]},null]},{"$literal":"ca"}]}]}}`},
	"substring from":                  {Want: `{"$expr":{"$and":[{"$ne":[{"$cond":[{"$eq":[{"$type":{"$ifNull":["$name",null]}},"string"]},{"$cond":[{"$gte":[{"$strLenCP":{"$ifNull":["$name",null]}},1]},{"$substrCP":[{"$ifNull":["$name",null]},1,{"$subtract":[{"$strLenCP":{"$ifNull":["$name",null]}},1]}]},null]},null]},null]},{"$eq":[{"$cond":[{"$eq":[{"$type":{"$ifNull":["$name",null]}},"string"]},{"$cond":[{"$gte":[{"$strLenCP":{"$ifNull":["$name",null]}},1]},{"$substrCP":[{"$ifNull":["$name",null]},1,{"$subtract":[{"$strLenCP":{"$ifNull":["$name",null]}},1]}]},null]},null]},{"$literal":"ob"}]}]}}`},
	"substring ordered":               {Want: `{"$expr":{"$and":[{"$ne":[{"$cond":[{"$eq":[{"$type":{"$ifNull":["$name",null]}},"string"]},{"$cond":[{"$gte":[{"$strLenCP":{"$ifNull":["$name",null]}},1]},{"$substrCP":[{"$ifNull":["$name",null]},0,1]},null]},null]},null]},{"$and":[{"$ne":[{"$cond":[{"$eq":[{"$type":{"$ifNull":["$name",null]}},"string"]},{"$cond":[{"$gte":[{"$strLenCP":{"$ifNull":["$name",null]}},1]},{"$substrCP":[{"$ifNull":["$name",null]},0,1]},null]},null]},null]},{"$ne":[{"$literal":"c"},null]},{"$or":[{"$and":[{"$isNumber":{"$cond":[{"$eq":[{"$type":{"$ifNull":["$name",null]}},"string"]},{"$cond":[{"$gte":[{"$strLenCP":{"$ifNull":["$name",null]}},1]},{"$substrCP":[{"$ifNull":["$name",null]},0,1]},null]},null]}},{"$isNumber":{"$literal":"c"}}]},{"$eq":[{"$type":{"$cond":[{"$eq":[{"$type":{"$ifNull":["$name",null]}},"string"]},{"$cond":[{"$gte":[{"$strLenCP":{"$ifNull":["$name",null]}},1]},{"$substrCP":[{"$ifNull":["$name",null]},0,1]},null]},null]}},{"$type":{"$literal":"c"}}]}]},{"$lt":[{"$cond":[{"$eq":[{"$type":{"$ifNull":["$name",null]}},"string"]},{"$cond":[{"$gte":[{"$strLenCP":{"$ifNull":["$name",null]}},1]},{"$substrCP":[{"$ifNull":["$name",null]},0,1]},null]},null]},{"$literal":"c"}]}]}]}}`},
	"substring reversed range":        {Err: filter.ErrInvalidExpression},
	"timestamp greater":               {Want: `{"created":{"$gt":{"$date":"2024-01-01T00:00:00Z"}}}`},
	"timestamp equal":                 {Want: `{"created":{"$date":"2024-03-01T00:00:00Z"}}`},
	"timestamp fraction":              {Want: `{"created":{"$lt":{"$date":"2022-12-31T23:59:59.5Z"}}}`},
	"duration literal":                {Err: filter.ErrFieldTypeMismatch},
	"bytes equal":                     {Want: `{"data":{"$binary":{"base64":"YWI=","subType":"00"}}}`},
	"bytes ordered":                   {Err: filter.ErrUnsupportedOperation},
	"decimal greater":                 {Want: `{"price":{"$gt":{"$numberDecimal":"5.25"}}}`},
	"decimal equal":                   {Want: `{"price":{"$numberDecimal":"0.1"}}`},
	"decimal trailing zeros":          {Want: `{"price":{"$numberDecimal":"10.5"}}`},
	"decimal string literal":          {Want: `{"price":{"$gte":{"$numberDecimal":"10.5"}}}`},
	"decimal in":                      {Want: `{"price":{"$in":[{"$numberDecimal":"3.1"},{"$numberDecimal":"7"}]}}`},
	"decimal not a number":            {Err: filter.ErrFieldTypeMismatch},
	"array equal":                     {Want: `{"tags":"red"}`},
	"array not equal":                 {Want: `{"tags":{"$ne":"red"}}`},
	"element in array":                {Want: `{"tags":"red"}`},
	"array in list":                   {Want: `{"tags":{"$in":["green","blue"]}}`},
	"array starts with":               {Want: `{"tags":{"$regex":"^gr"}}`},
	"array ordered":                   {Want: `{"tags":{"$lt":"c"}}`},
	"array equal null":                {Want: `{"tags":null}`},
	"and":                             {Want: `{"$and":[{"name":"alice"},{"age":{"$gt":20}}]}`},
	"or":                              {Want: `{"$or":[{"age":{"$lt":20}},{"score":{"$gt":2.0}}]}`},
	"and binds tighter than or":       {Want: `{"$or":[{"name":"bob"},{"$and":[{"name":"alice"},{"age":{"$gt":26}}]}]}`},
	"parentheses":                     {Want: `{"$and":[{"$or":[{"name":"bob"},{"name":"alice"}]},{"age":{"$gt":26}}]}`},
	"not":                             {Want: `{"$nor":[{"name":"alice"}]}`},
	"not and":                         {Want: `{"$nor":[{"$and":[{"active":true},{"age":{"$gt":26}}]}]}`},
	"not or":                          {Want: `{"$nor":[{"$or":[{"name":"alice"},{"age":{"$lt":20}}]}]}`},
	"double negation":                 {Want: `{"active":true}`},
	"bare literal":                    {Err: filter.ErrInvalidExpression},
	"not substring out of range":      {Want: "{\"$nor\":[{\"$expr\":{\"$and\":[{\"$ne\":[{\"$cond\":[{\"$eq\":[{\"$type\":{\"$ifNull\":[\"$name\",null]}},\"string\"]},{\"$cond\":[{\"$gte\":[{\"$strLenCP\":{\"$ifNull\":[\"$name\",null]}},4]},{\"$substrCP\":[{\"$ifNull\":[\"$name\",null]},0,4]},null]},null]},null]},{\"$eq\":[{\"$cond\":[{\"$eq\":[{\"$type\":{\"$ifNull\":[\"$name\",null]}},\"string\"]},{\"$cond\":[{\"$gte\":[{\"$strLenCP\":{\"$ifNull\":[\"$name\",null]}},4]},{\"$substrCP\":[{\"$ifNull\":[\"$name\",null]},0,4]},null]},null]},{\"$literal\":\"x\"}]}]}}]}"},
	"unknown or true":                 {Want: "{\"$or\":[{\"note\":{\"$lt\":\"m\"}},{\"age\":{\"$gt\":0}}]}"},
	"true or unknown":                 {Want: "{\"$or\":[{\"age\":{\"$gt\":0}},{\"note\":{\"$lt\":\"m\"}}]}"},
	"unknown or false":                {Want: "{\"$or\":[{\"note\":{\"$lt\":\"m\"}},{\"age\":{\"$lt\":0}}]}"},
	"unknown and false":               {Want: "{\"$and\":[{\"note\":{\"$lt\":\"m\"}},{\"age\":{\"$lt\":0}}]}"},
	"false and unknown":               {Want: "{\"$and\":[{\"age\":{\"$lt\":0}},{\"note\":{\"$lt\":\"m\"}}]}"},
	"not unknown":                     {Want: "{\"$nor\":[{\"note\":{\"$lt\":\"m\"}}]}"},
	"not unknown or false":            {Want: "{\"$nor\":[{\"$or\":[{\"note\":{\"$lt\":\"m\"}},{\"name\":\"zzz\"}]}]}"},
	"not unknown and false":           {Want: "{\"$nor\":[{\"$and\":[{\"note\":{\"$lt\":\"m\"}},{\"age\":{\"$lt\":0}}]}]}"},
	"not size of null":                {Want: "{\"$nor\":[{\"$or\":[{\"note\":{\"$size\":0}},{\"$expr\":{\"$eq\":[{\"$cond\":[{\"$eq\":[{\"$type\":\"$note\"},\"string\"]},{\"$strLenCP\":\"$note\"},null]},0]}}]}]}"},
	"not contains null argument":      {Want: "{\"$nor\":[{\"$expr\":{\"$cond\":[{\"$and\":[{\"$eq\":[{\"$type\":{\"$ifNull\":[\"$name\",null]}},\"string\"]},{\"$eq\":[{\"$type\":{\"$ifNull\":[\"$note\",null]}},\"string\"]}]},{\"$ne\":[{\"$indexOfCP\":[{\"$ifNull\":[\"$name\",null]},{\"$ifNull\":[\"$note\",null]}]},-1]},false]}}]}"},
	"nil filter":                      {Want: "{}"},
	"field equal field":               {Want: `{"$expr":{"$eq":[{"$ifNull":["$name",null]},{"$ifNull":["$address.city",null]}]}}`},
	"field less field":                {Want: `{"$expr":{"$and":[{"$ne":[{"$ifNull":["$name",null]},null]},{"$ne":[{"$ifNull":["$nickname",null]},null]},{"$or":[{"$and":[{"$isNumber":{"$ifNull":["$name",null]}},{"$isNumber":{"$ifNull":["$nickname",null]}}]},{"$eq":[{"$type":{"$ifNull":["$name",null]}},{"$type":{"$ifNull":["$nickname",null]}}]}]},{"$lt":[{"$ifNull":["$name",null]},{"$ifNull":["$nickname",null]}]}]}}`},
	"field in list":                   {Want: `{"$or":[{"name":{"$in":["bob"]}},{"$expr":{"$eq":[{"$ifNull":["$name",null]},{"$ifNull":["$address.city",null]}]}}]}`},
	"field kind mismatch":             {Err: filter.ErrFieldTypeMismatch},
}
