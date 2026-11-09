// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package protopath_test

import (
	"errors"
	"fmt"

	"github.com/altessa-s/go-aip/projection"
	"github.com/altessa-s/go-aip/projection/protopath"

	fieldbehaviortestv1 "github.com/altessa-s/go-aip/internal/proto/gen/fieldbehaviortest/v1"
)

func ExampleValidator_Validate() {
	v, err := protopath.New((&fieldbehaviortestv1.Account{}).ProtoReflect().Descriptor())
	if err != nil {
		panic(err)
	}

	for _, path := range []string{"owner.email", "emails", "emails.0", "attributes.region", "nickname"} {
		err := v.Validate(path)
		fmt.Printf("%-18s invalid=%t unsupported=%t\n", path,
			errors.Is(err, projection.ErrInvalidFieldPath), errors.Is(err, projection.ErrUnsupportedPath))
	}

	// Output:
	// owner.email        invalid=false unsupported=false
	// emails             invalid=false unsupported=false
	// emails.0           invalid=true unsupported=false
	// attributes.region  invalid=false unsupported=true
	// nickname           invalid=true unsupported=false
}
