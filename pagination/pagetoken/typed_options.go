// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package pagetoken

import "encoding/json"

//go:generate optgen generate --type=typedOptions --output=typed_options_gen.go --option-type=TypedOption

// PayloadCodec encodes cursor values. Implementations must support concurrent use.
// Applications must version token bindings when changing the encoding.
type PayloadCodec interface {
	Marshal(any) ([]byte, error)
	Unmarshal([]byte, any) error
}

// JSONPayloadCodec is the default cursor representation.
type JSONPayloadCodec struct{}

// Marshal encodes v as JSON.
func (JSONPayloadCodec) Marshal(v any) ([]byte, error) { return json.Marshal(v) }

// Unmarshal decodes the JSON in b into v.
func (JSONPayloadCodec) Unmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

type typedOptions struct {
	payloadCodec PayloadCodec `optgen:"default=JSONPayloadCodec{},notnil"`
}
