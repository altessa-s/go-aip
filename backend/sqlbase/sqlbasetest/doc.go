// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package sqlbasetest is the test kit of backend/sqlbase and of SQL dialects built on it, in this repository or out of
// it: [NewFakeDB] opens a *sql.DB over a scripted database/sql driver ([FakeDB]) that records every statement, its
// arguments and whether it ran in a transaction, and answers from a script. SQL-building code — a [sqlbase.ListEngine]
// over a custom [sqlbase.ListDialect], index management, schema checks — is then testable for statement shape, argument
// order and result mapping without a server or a third-party driver.
//
// # Usage
//
//	db, fake := sqlbasetest.NewFakeDB(t, func(query string, args []any) sqlbasetest.Reply {
//		return sqlbasetest.Reply{Columns: []string{"id", "name"}, Rows: [][]driver.Value{{"1", "a"}}}
//	})
//	engine, err := sqlbase.NewListEngine[Widget](db, nil, schema, "widgets", columns, dest, myDialect{})
//	// … list, then inspect fake.Calls()
//
// See README.md.
package sqlbasetest
