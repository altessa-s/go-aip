// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package update

import "google.golang.org/protobuf/reflect/protoreflect"

// SynthesisSkipFields exposes synthesisSkipFields to the black-box tests.
func SynthesisSkipFields(msg protoreflect.Message) []string {
	return synthesisSkipFields(msg, newOptions())
}

// SelectsAll exposes selectsAll, the full-replacement check of raw update
// mask paths, to the black-box tests.
func SelectsAll(paths []string) (bool, error) {
	return selectsAll(paths)
}
