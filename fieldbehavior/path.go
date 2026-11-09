// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldbehavior

import (
	"strconv"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/altessa-s/go-aip/internal/behavior"
)

// segmentKind tells how a path segment renders.
type segmentKind uint8

const (
	segmentField segmentKind = iota // a field name
	segmentIndex                    // a repeated element index
	segmentKey                      // a map key, quoted by the AIP-161 grammar
)

// segment is one unrendered step of a field path.
type segment struct {
	kind  segmentKind
	name  string
	index int
	key   protoreflect.MapKey
}

// pathStack holds the path of the field a walk is visiting. Segments are
// pushed and popped around each step and rendered only when a violation needs
// the path, so a walk that finds nothing builds no strings.
type pathStack []segment

func (p *pathStack) pushField(fd protoreflect.FieldDescriptor) {
	*p = append(*p, segment{kind: segmentField, name: string(fd.Name())})
}

func (p *pathStack) pushIndex(i int) {
	*p = append(*p, segment{kind: segmentIndex, index: i})
}

func (p *pathStack) pushKey(k protoreflect.MapKey) {
	*p = append(*p, segment{kind: segmentKey, key: k})
}

func (p *pathStack) pop() {
	*p = (*p)[:len(*p)-1]
}

// String renders the path as dot-separated segments, the same text
// [behavior.JoinPath] builds step by step.
func (p pathStack) String() string {
	var b strings.Builder

	for i, seg := range p {
		if i > 0 {
			b.WriteByte('.')
		}

		switch seg.kind {
		case segmentField:
			b.WriteString(seg.name)
		case segmentIndex:
			b.WriteString(strconv.Itoa(seg.index))
		case segmentKey:
			b.WriteString(behavior.QuoteSegment(seg.key.String()))
		}
	}

	return b.String()
}
