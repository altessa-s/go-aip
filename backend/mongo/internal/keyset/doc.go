// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package keyset reads one keyset page of a MongoDB collection with a plain find: the page token carries the sort values of the
// last document, signed with a pagetoken.Signer and bound to the marshaled filter, the sort and the subject. It is the pager of
// the MongoDB list engine (mongo.ListEngine) and is ported from go-atlas store/mongo/keyset.
//
// Before a filter reaches the driver, [ValidateFilter] admits only the BSON containers, raw BSON and scalar leaves a filter is
// built from ([ErrValueType] for any other Go type), refuses the server-side JavaScript operators ([ErrDangerousOperator]) and
// bounds its nesting, values and bytes; the filter is then encoded through a bounded writer with its maps in key order, so the
// token binding is stable.
//
// # Usage
//
//	res, err := keyset.FindPage[widget](ctx, coll, signer, keyset.Query{
//		Filter: filter, Sort: bson.D{{Key: "name", Value: 1}}, Limit: 50, After: token, Subject: subject,
//	})
package keyset
