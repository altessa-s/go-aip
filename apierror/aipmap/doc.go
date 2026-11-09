// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package aipmap maps the sentinel errors of the go-aip packages (fieldmask,
// fieldbehavior, fieldinfo, etag, validateonly, filter, orderby, projection,
// listing, pagetoken, batch, softdelete, requestid, unreachable, state,
// resourcename) to AIP-193 codes and ErrorInfo reasons for an
// [apierror.Mapper]. An error that lists several fields (a fieldinfo or
// fieldbehavior violation list, a fieldmask update check) becomes one
// BadRequest field violation per field.
//
// It lives apart from apierror so that apierror depends on
// go-atlas-core/errors only: a service that does not use these packages does
// not link them (filter brings CEL), and the packages themselves never import
// apierror, so no cycle can form.
//
// # Usage
//
//	mapper, merr := apierror.NewMapper(
//		apierror.WithRules(myRules...),       // service rules first: they win
//		apierror.WithRules(aipmap.Rules()...),
//		apierror.WithDefaultDomain("library.example.com"))
//	if merr != nil {
//		return merr
//	}
//
//	// err is the error a handler failed with.
//	return status.FromProto(mapper.Map(err).Proto()).Err()
package aipmap
