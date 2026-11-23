// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package listing

import (
	"encoding/hex"
	"slices"
	"strings"

	"github.com/altessa-s/go-atlas-core/encoding/keys"

	"github.com/altessa-s/go-aip/filter"
)

// derivedContractPrefix reserves the namespace of library-derived page-token contracts.
const derivedContractPrefix = "go-aip:contains:"

func (s *Schema) compileContainsFields() error {
	s.contractID = s.opts.contractID
	if strings.HasPrefix(s.contractID, derivedContractPrefix) {
		return invalid("contract ID prefix %q is reserved", derivedContractPrefix)
	}

	if len(s.opts.caseInsensitiveContainsFields) == 0 {
		return nil
	}

	names := slices.Clone(s.opts.caseInsensitiveContainsFields)
	slices.Sort(names)
	names = slices.Compact(names)

	parts := [][]byte{[]byte("go-aip:contains:simple-lower-15.1"), []byte(s.contractID)}
	for _, name := range names {
		f, ok := s.fields.Get(name)
		if !ok || f.Kind != filter.FieldKindString || f.enum != nil {
			return invalid("case-insensitive contains requires a declared plain string field or string array: %q", name)
		}

		parts = append(parts, []byte(name))
	}

	s.caseInsensitiveContainsFields = names
	s.contractID = derivedContractPrefix + hex.EncodeToString(keys.Digest(parts...))

	return nil
}

// CaseInsensitiveContainsFields returns a detached sorted slice of the public fields using Unicode 15.1 simple lowercase in contains.
func (s *Schema) CaseInsensitiveContainsFields() []string {
	return slices.Clone(s.caseInsensitiveContainsFields)
}
