// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package apierror_test

import (
	"errors"
	"fmt"

	"google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/altessa-s/go-aip/apierror"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

func ExampleNew() {
	e, err := apierror.New(code.Code_INVALID_ARGUMENT, "the shelf name is invalid",
		apierror.WithReason("INVALID_SHELF"),
		apierror.WithDomain("library.example.com"),
		apierror.WithMetadata(map[string]string{"shelf": "shelves/7"}),
		apierror.WithFieldViolations(&errdetails.BadRequest_FieldViolation{
			Field: "shelf.name", Description: "must not be empty", Reason: "EMPTY_NAME",
		}),
		apierror.WithLocalizedMessage("en-US", "Enter a shelf name."))
	if err != nil {
		panic(err)
	}

	fmt.Println(e)
	fmt.Println(e.HTTPStatus(), len(e.Proto().GetDetails()))
	// Output:
	// INVALID_ARGUMENT: the shelf name is invalid
	// 400 3
}

func ExampleFromError() {
	err := fmt.Errorf("update book: %w", coreerrs.NewFieldError("updateMask.paths[0]", "unknown field"))

	e := apierror.FromError(err)
	fmt.Println(e.Code(), e.Reason(), e.Message())
	fmt.Println(e.FieldViolations()[0].GetField())

	fmt.Println(apierror.FromError(errors.New("dial tcp 10.0.0.1:5432: refused")).Message())
	// Output:
	// INVALID_ARGUMENT INVALID_ARGUMENT updateMask.paths[0]: unknown field
	// updateMask.paths[0]
	// internal error
}

func ExampleError_JSON() {
	e := apierror.MustNew(code.Code_NOT_FOUND, "shelf not found",
		apierror.WithReason("SHELF_NOT_FOUND"), apierror.WithDomain("library.example.com"))

	body, err := e.JSON()
	if err != nil {
		panic(err)
	}

	fmt.Println(string(body))
	// Output:
	// {"error":{"code":404,"message":"shelf not found","status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"SHELF_NOT_FOUND","domain":"library.example.com"}]}}
}

func ExampleNewMapper() {
	errNoStock := errors.New("out of stock")

	m, err := apierror.NewMapper(
		apierror.WithRules(apierror.Rule{Target: errNoStock, Code: code.Code_FAILED_PRECONDITION, Reason: "NO_STOCK"}),
		apierror.WithDefaultDomain("shop.example.com"))
	if err != nil {
		panic(err)
	}

	e := m.Map(fmt.Errorf("order 42: %w", errNoStock))
	fmt.Println(e.Code(), e.Reason(), e.Domain(), e.Message())
	// Output:
	// FAILED_PRECONDITION NO_STOCK shop.example.com failed precondition
}
