// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package lua_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/altessa-s/go-aip/backend/lua"
	"github.com/altessa-s/go-aip/filter"
	"github.com/altessa-s/go-aip/filter/evaluator"
	"github.com/altessa-s/go-aip/filter/filtertest"
	"github.com/altessa-s/go-aip/metrics"

	glua "github.com/yuin/gopher-lua"
)

// storedLua converts a value of filtertest.ConformanceRecords to the form a Lua script reads: a timestamp as Unix epoch seconds
// (the translator's literal form), bytes as a Lua string, a decimal as a number, a map or a list as a table.
func storedLua(l *glua.LState, v any) glua.LValue {
	switch x := v.(type) {
	case time.Time:
		return glua.LNumber(float64(x.UnixNano()) / float64(time.Second))
	case []byte:
		return glua.LString(x)
	case decimal.Decimal:
		return glua.LNumber(x.InexactFloat64())
	case []any:
		t := l.NewTable()
		for _, el := range x {
			t.Append(storedLua(l, el))
		}

		return t
	case map[string]any:
		t := l.NewTable()
		for k, el := range x {
			t.RawSetString(k, storedLua(l, el))
		}

		return t
	case nil:
		return glua.LNil
	case bool:
		return glua.LBool(x)
	case int64:
		return glua.LNumber(x)
	case float64:
		return glua.LNumber(x)
	case string:
		return glua.LString(x)
	default:
		panic(fmt.Sprintf("unsupported record value %T", v))
	}
}

// luaMatch translates a filter and runs it with gopher-lua against one record bound to d; a Lua runtime error is an error of
// that record.
func luaMatch(policy *filter.Policy, opts ...filter.TranslatorOption) (filtertest.MatchFunc, error) {
	tr, err := lua.NewFilterTranslator(policy, lua.WithFilterOptions(opts...))
	if err != nil {
		return nil, err
	}

	return func(node filter.Node, record map[string]any) (bool, error) {
		expr, err := tr.Translate(node)
		if err != nil {
			return false, err
		}

		l := glua.NewState()
		defer l.Close()

		l.SetGlobal(lua.DefaultTableVar, storedLua(l, record))

		if err := l.DoString("result = " + expr); err != nil {
			return false, fmt.Errorf("%s on record %v: %w", expr, record["id"], err)
		}

		return l.GetGlobal("result") == glua.LTrue, nil
	}, nil
}

// evaluatorMatch is the reference semantics: the default evaluator on the same policy and options.
func evaluatorMatch(policy *filter.Policy, opts ...filter.TranslatorOption) (filtertest.MatchFunc, error) {
	e, err := evaluator.New(policy, evaluator.WithFilterOptions(opts...))
	if err != nil {
		return nil, err
	}

	return e.Evaluate, nil
}

func TestConformance(t *testing.T) {
	t.Parallel()

	type O = filtertest.Outcome

	// A Lua runtime error aborts the whole EVAL script, so every translated filter must run on every record without one; the
	// suite counts a failed record as no match, so the errors are collected here and reported once the cases have run.
	var (
		mu            sync.Mutex
		runtimeErrors []error
	)

	t.Cleanup(func() {
		for _, err := range runtimeErrors {
			t.Errorf("Lua runtime error: %v", err)
		}
	})

	match := func(policy *filter.Policy, opts ...filter.TranslatorOption) (filtertest.MatchFunc, error) {
		run, err := luaMatch(policy, opts...)
		if err != nil {
			return nil, err
		}

		return func(node filter.Node, record map[string]any) (bool, error) {
			ok, err := run(node, record)
			if err != nil {
				mu.Lock()

				runtimeErrors = append(runtimeErrors, err)

				mu.Unlock()
			}

			return ok, err
		}, nil
	}

	filtertest.RunConformance(t, filtertest.Backend{
		New: func(policy *filter.Policy, opts ...filter.TranslatorOption) (filtertest.TranslateFunc, error) {
			tr, err := lua.NewFilterTranslator(policy, lua.WithFilterOptions(opts...), lua.WithFilterCollector(metrics.Noop()))
			if err != nil {
				return nil, err
			}

			return tr.Translate, nil
		},
		Match:     match,
		Reference: evaluatorMatch,
		Expect: map[string]O{
			"equal string":                    {Want: `(d["name"] == "alice")`},
			"not equal string":                {Want: `(d["name"] == nil or (type(d["name"]) == "string" and d["name"] ~= "alice"))`},
			"less int":                        {Want: `(type(d["age"]) == "number" and d["age"] < 30)`},
			"less or equal int":               {Want: `(type(d["age"]) == "number" and d["age"] <= 30)`},
			"greater float":                   {Want: `(type(d["score"]) == "number" and d["score"] > 1.5)`},
			"greater or equal float":          {Want: `(type(d["score"]) == "number" and d["score"] >= 1.5)`},
			"float literal on int field":      {Err: filter.ErrFieldTypeMismatch},
			"int literal on float field":      {Want: `(d["score"] == 3)`},
			"negative literal":                {Want: `(type(d["age"]) == "number" and d["age"] > -1)`},
			"ordered string":                  {Want: `(type(d["name"]) == "string" and d["name"] < "c")`},
			"equal bool":                      {Want: `(d["active"] == true)`},
			"bare bool":                       {Want: `(d["active"] == true)`},
			"negated bare bool":               {Want: "(d[\"active\"] == false)"},
			"enum value":                      {Want: `(d["status"] == 1)`},
			"nested field":                    {Want: `((function(v, ...) for _, k in ipairs({...}) do if type(v) ~= "table" then return nil end v = v[k] end return v end)(d, "address", "city") == "Oslo")`},
			"nested field mapping":            {Want: `((function(v, ...) for _, k in ipairs({...}) do if type(v) ~= "table" then return nil end v = v[k] end return v end)(d, "location", "city") == "Oslo")`},
			"field mapping":                   {Want: `(d["nickname"] == "al")`},
			"has nested":                      {Want: `((function(v, ...) for _, k in ipairs({...}) do if type(v) ~= "table" then return nil end v = v[k] end return v end)(d, "address", "city") ~= nil)`},
			"equal null":                      {Want: `(d["note"] == nil)`},
			"not equal null":                  {Want: `(d["note"] ~= nil)`},
			"ordered null":                    {Err: filter.ErrUnsupportedOperation},
			"ordered nullable field":          {Want: `(type(d["note"]) == "string" and d["note"] < "m")`},
			"equal empty string":              {Want: `(d["note"] == "")`},
			"in ints":                         {Want: `(d["age"] == 25 or d["age"] == 30)`},
			"in strings":                      {Want: `(d["name"] == "alice" or d["name"] == "carol")`},
			"in empty list":                   {Want: "false"},
			"in with null":                    {Want: `(d["note"] == "x" or d["note"] == nil)`},
			"not in":                          {Want: `(not (d["age"] == 25 or d["age"] == 30))`},
			"in non-array field":              {Err: filter.ErrInvalidExpression},
			"contains":                        {Want: `(type(d["name"]) == "string" and string.find(d["name"], "ar", 1, true) ~= nil)`},
			"starts with":                     {Want: `(type(d["name"]) == "string" and string.sub(d["name"], 1, 2) == "ca")`},
			"ends with":                       {Want: `(type(d["name"]) == "string" and string.sub(d["name"], -2) == "ob")`},
			"contains wildcard characters":    {Want: `(type(d["name"]) == "string" and string.find(d["name"], "%_*", 1, true) ~= nil)`},
			"starts with wildcard characters": {Want: `(type(d["name"]) == "string" and string.sub(d["name"], 1, 2) == "a%")`},
			"contains empty string":           {Want: `(type(d["name"]) == "string" and string.find(d["name"], "", 1, true) ~= nil)`},
			"contains field":                  {Want: `(type(d["name"]) == "string" and type(d["nickname"]) == "string" and string.find(d["name"], d["nickname"], 1, true) ~= nil)`},
			"starts with field":               {Want: `(type(d["name"]) == "string" and type(d["nickname"]) == "string" and string.sub(d["name"], 1, #d["nickname"]) == d["nickname"])`},
			"ends with field":                 {Want: `(type(d["name"]) == "string" and type((function(v, ...) for _, k in ipairs({...}) do if type(v) ~= "table" then return nil end v = v[k] end return v end)(d, "address", "city")) == "string" and (#(function(v, ...) for _, k in ipairs({...}) do if type(v) ~= "table" then return nil end v = v[k] end return v end)(d, "address", "city") == 0 or string.sub(d["name"], -#(function(v, ...) for _, k in ipairs({...}) do if type(v) ~= "table" then return nil end v = v[k] end return v end)(d, "address", "city")) == (function(v, ...) for _, k in ipairs({...}) do if type(v) ~= "table" then return nil end v = v[k] end return v end)(d, "address", "city")))`},
			"case-sensitive contains":         {Want: `(type(d["name"]) == "string" and string.find(d["name"], "dave", 1, true) ~= nil)`},
			"text search":                     {Err: filter.ErrUnsupportedOperation},
			"text search case-insensitive":    {Err: filter.ErrUnsupportedOperation},
			"matches":                         {Err: filter.ErrUnsupportedOperation},
			"matches end anchor":              {Err: filter.ErrUnsupportedOperation},
			"size of string":                  {Want: "((type(d[\"name\"]) == \"string\" or type(d[\"name\"]) == \"table\") and (type(d[\"name\"]) == \"string\" and select(2, string.gsub(d[\"name\"], \"[%z\\1-\\127\\194-\\244]\", \"\")) or #d[\"name\"]) > 4)"},
			"size function":                   {Want: "((type(d[\"name\"]) == \"string\" or type(d[\"name\"]) == \"table\") and (type(d[\"name\"]) == \"string\" and select(2, string.gsub(d[\"name\"], \"[%z\\1-\\127\\194-\\244]\", \"\")) or #d[\"name\"]) == 3)"},
			"size of array":                   {Want: `((type(d["tags"]) == "string" and select(2, string.gsub(d["tags"], "[%z\1-\127\194-\244]", "")) or type(d["tags"]) == "table" and #d["tags"] or 0) == 1)`},
			"size of empty array":             {Want: `((type(d["tags"]) == "string" and select(2, string.gsub(d["tags"], "[%z\1-\127\194-\244]", "")) or type(d["tags"]) == "table" and #d["tags"] or 0) == 0)`},
			"substring":                       {Want: `(function(a, b) if a == nil then return false end return a == b end)((function(s, i, j) if type(s) ~= "string" or type(i) ~= "number" or math.floor(i) ~= i or (j ~= nil and (type(j) ~= "number" or math.floor(j) ~= j)) then return nil end local n, o = 0, {} for c in string.gmatch(s, "[%z\1-\127\194-\244][\128-\191]*") do if n >= i and (j == nil or n < j) then o[#o + 1] = c end n = n + 1 end if i < 0 or i > n or (j ~= nil and (j < i or j > n)) then return nil end return table.concat(o) end)(d["name"], 0, 2), "ca")`},
			"substring from":                  {Want: `(function(a, b) if a == nil then return false end return a == b end)((function(s, i, j) if type(s) ~= "string" or type(i) ~= "number" or math.floor(i) ~= i or (j ~= nil and (type(j) ~= "number" or math.floor(j) ~= j)) then return nil end local n, o = 0, {} for c in string.gmatch(s, "[%z\1-\127\194-\244][\128-\191]*") do if n >= i and (j == nil or n < j) then o[#o + 1] = c end n = n + 1 end if i < 0 or i > n or (j ~= nil and (j < i or j > n)) then return nil end return table.concat(o) end)(d["name"], 1, nil), "ob")`},
			"substring ordered":               {Want: `(function(a, b) if a == nil then return false end return type(a) == type(b) and a < b end)((function(s, i, j) if type(s) ~= "string" or type(i) ~= "number" or math.floor(i) ~= i or (j ~= nil and (type(j) ~= "number" or math.floor(j) ~= j)) then return nil end local n, o = 0, {} for c in string.gmatch(s, "[%z\1-\127\194-\244][\128-\191]*") do if n >= i and (j == nil or n < j) then o[#o + 1] = c end n = n + 1 end if i < 0 or i > n or (j ~= nil and (j < i or j > n)) then return nil end return table.concat(o) end)(d["name"], 0, 1), "c")`},
			"substring reversed range":        {Err: filter.ErrInvalidExpression},
			"timestamp greater":               {Want: `(type(d["created"]) == "number" and d["created"] > 1704067200)`},
			"timestamp equal":                 {Want: `(d["created"] == 1709251200)`},
			"timestamp fraction":              {Want: `(type(d["created"]) == "number" and d["created"] < 1672531199.5)`},
			"duration literal":                {Err: filter.ErrFieldTypeMismatch},
			"bytes equal":                     {Want: `(d["data"] == "ab")`},
			"bytes ordered":                   {Want: `(type(d["data"]) == "string" and d["data"] < "b")`},
			"decimal greater":                 {Err: filter.ErrUnsupportedType},
			"decimal equal":                   {Err: filter.ErrUnsupportedType},
			"decimal trailing zeros":          {Err: filter.ErrUnsupportedType},
			"decimal string literal":          {Err: filter.ErrUnsupportedType},
			"decimal in":                      {Err: filter.ErrUnsupportedType},
			"decimal not a number":            {Err: filter.ErrUnsupportedType},
			"array equal":                     {Want: `(function(v, x) if type(v) ~= "table" then return (v == x) end for _, e in ipairs(v) do if (e == x) then return true end end return false end)(d["tags"], "red")`},
			"array not equal":                 {Want: `(not (function(v, x) if type(v) ~= "table" then return (v == x) end for _, e in ipairs(v) do if (e == x) then return true end end return false end)(d["tags"], "red"))`},
			"element in array":                {Want: `(function(v, x) if type(v) ~= "table" then return (v == x) end for _, e in ipairs(v) do if (e == x) then return true end end return false end)(d["tags"], "red")`},
			"array in list":                   {Want: `(function(v) if type(v) ~= "table" then return (v == "green" or v == "blue") end for _, e in ipairs(v) do if (e == "green" or e == "blue") then return true end end return false end)(d["tags"])`},
			"array starts with":               {Want: `(function(v, x) if type(v) ~= "table" then return (type(v) == "string" and string.sub(v, 1, 2) == x) end for _, e in ipairs(v) do if (type(e) == "string" and string.sub(e, 1, 2) == x) then return true end end return false end)(d["tags"], "gr")`},
			"array ordered":                   {Want: `(function(v, x) if type(v) ~= "table" then return (type(v) == "string" and v < x) end for _, e in ipairs(v) do if (type(e) == "string" and e < x) then return true end end return false end)(d["tags"], "c")`},
			"array equal null":                {Want: `(function(v, x) if type(v) ~= "table" then return (v == x) end for _, e in ipairs(v) do if (e == x) then return true end end return false end)(d["tags"], nil)`},
			"and":                             {Want: `((d["name"] == "alice") and (type(d["age"]) == "number" and d["age"] > 20))`},
			"or":                              {Want: `((type(d["age"]) == "number" and d["age"] < 20) or (type(d["score"]) == "number" and d["score"] > 2))`},
			"and binds tighter than or":       {Want: `((d["name"] == "bob") or ((d["name"] == "alice") and (type(d["age"]) == "number" and d["age"] > 26)))`},
			"parentheses":                     {Want: `(((d["name"] == "bob") or (d["name"] == "alice")) and (type(d["age"]) == "number" and d["age"] > 26))`},
			"not":                             {Want: "(d[\"name\"] == nil or (type(d[\"name\"]) == \"string\" and d[\"name\"] ~= \"alice\"))"},
			"not and":                         {Want: "((d[\"active\"] == false) or (type(d[\"age\"]) == \"number\" and not (d[\"age\"] > 26)))"},
			"not or":                          {Want: "((d[\"name\"] == nil or (type(d[\"name\"]) == \"string\" and d[\"name\"] ~= \"alice\")) and (type(d[\"age\"]) == \"number\" and not (d[\"age\"] < 20)))"},
			"double negation":                 {Want: `(d["active"] == true)`},
			"bare literal":                    {Err: filter.ErrInvalidExpression},
			"not substring out of range":      {Want: "(function(a, b) if a == nil then return false end return not (a == b) end)((function(s, i, j) if type(s) ~= \"string\" or type(i) ~= \"number\" or math.floor(i) ~= i or (j ~= nil and (type(j) ~= \"number\" or math.floor(j) ~= j)) then return nil end local n, o = 0, {} for c in string.gmatch(s, \"[%z\\1-\\127\\194-\\244][\\128-\\191]*\") do if n >= i and (j == nil or n < j) then o[#o + 1] = c end n = n + 1 end if i < 0 or i > n or (j ~= nil and (j < i or j > n)) then return nil end return table.concat(o) end)(d[\"name\"], 0, 4), \"x\")"},
			"unknown or true":                 {Want: "((type(d[\"note\"]) == \"string\" and d[\"note\"] < \"m\") or (type(d[\"age\"]) == \"number\" and d[\"age\"] > 0))"},
			"true or unknown":                 {Want: "((type(d[\"age\"]) == \"number\" and d[\"age\"] > 0) or (type(d[\"note\"]) == \"string\" and d[\"note\"] < \"m\"))"},
			"unknown or false":                {Want: "((type(d[\"note\"]) == \"string\" and d[\"note\"] < \"m\") or (type(d[\"age\"]) == \"number\" and d[\"age\"] < 0))"},
			"unknown and false":               {Want: "((type(d[\"note\"]) == \"string\" and d[\"note\"] < \"m\") and (type(d[\"age\"]) == \"number\" and d[\"age\"] < 0))"},
			"false and unknown":               {Want: "((type(d[\"age\"]) == \"number\" and d[\"age\"] < 0) and (type(d[\"note\"]) == \"string\" and d[\"note\"] < \"m\"))"},
			"not unknown":                     {Want: "(type(d[\"note\"]) == \"string\" and not (d[\"note\"] < \"m\"))"},
			"not unknown or false":            {Want: "((type(d[\"note\"]) == \"string\" and not (d[\"note\"] < \"m\")) and (d[\"name\"] == nil or (type(d[\"name\"]) == \"string\" and d[\"name\"] ~= \"zzz\")))"},
			"not unknown and false":           {Want: "((type(d[\"note\"]) == \"string\" and not (d[\"note\"] < \"m\")) or (type(d[\"age\"]) == \"number\" and not (d[\"age\"] < 0)))"},
			"not size of null":                {Want: "((type(d[\"note\"]) == \"string\" or type(d[\"note\"]) == \"table\") and not ((type(d[\"note\"]) == \"string\" and select(2, string.gsub(d[\"note\"], \"[%z\\1-\\127\\194-\\244]\", \"\")) or #d[\"note\"]) == 0))"},
			"not contains null argument":      {Want: "(type(d[\"name\"]) ~= \"string\" or (type(d[\"note\"]) == \"string\" and not (type(d[\"name\"]) == \"string\" and type(d[\"note\"]) == \"string\" and string.find(d[\"name\"], d[\"note\"], 1, true) ~= nil)))"},
			"nil filter":                      {Want: "true"},
			"field equal field":               {Want: `(d["name"] == (function(v, ...) for _, k in ipairs({...}) do if type(v) ~= "table" then return nil end v = v[k] end return v end)(d, "address", "city"))`},
			"field less field":                {Want: `(type(d["name"]) == type(d["nickname"]) and (type(d["name"]) == "number" or type(d["name"]) == "string") and d["name"] < d["nickname"])`},
			"field in list":                   {Want: `(d["name"] == (function(v, ...) for _, k in ipairs({...}) do if type(v) ~= "table" then return nil end v = v[k] end return v end)(d, "address", "city") or d["name"] == "bob")`},
			"field kind mismatch":             {Err: filter.ErrFieldTypeMismatch},
		},
	})
}
