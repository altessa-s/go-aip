// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package filter

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/altessa-s/go-atlas-core/encoding/keys"

	"github.com/altessa-s/go-aip/internal/literalvalue"

	coreerrs "github.com/altessa-s/go-atlas-core/errors"
)

// Canonical returns a deterministic encoding of the AST rooted at n: two ASTs encode alike exactly when they have the same shape,
// operators, field names and literal values (with their Go types), whatever the source spelled — whitespace, the global or method
// form of a string function, an enum name rewritten to its stored value. A nil n encodes as "". Use it to bind state, such as a
// page token, to the meaning of a filter. It recurses once per nesting level, so bound the depth first ([Policy.CheckLimits]).
//
// The encoding is the [keys.Compose] of a tag, the operator or name, and the encodings of the children, nested: a literal is
// ("L", value, source spelling), an identifier ("I", name), a binary node ("B", op, left, right), a unary node ("U", op, operand),
// a call ("C", op, target, args...) and a list ("A", elements...); an absent child encodes as "". It accepts what [Clone] accepts
// and fails as Clone fails, lowering an [ExtensionNode] first.
func Canonical(n Node) (string, error) {
	e := encoderPool.Get().(*canonicalEncoder) //nolint:errcheck // the pool holds only encoders
	defer e.release()

	total, err := e.measure(n)
	if errors.Is(err, errLowerExtensions) {
		// An extension node encodes as its lowering, which Clone performs (once per node, as it always has).
		if n, err = Clone(n); err != nil {
			return "", err
		}

		e.reset()
		total, err = e.measure(n)
	}

	if err != nil {
		return "", err
	}

	var b strings.Builder

	b.Grow(total)

	if n != nil {
		e.write(&b, n)
	}

	return b.String(), nil
}

// encoderPool recycles the work buffers of [Canonical], which a paged list request calls on every page.
var encoderPool = sync.Pool{New: func() any { return new(canonicalEncoder) }}

// maxPooledEncoder bounds the buffers a pooled encoder keeps, so that one huge filter does not pin its buffers in the pool.
const maxPooledEncoder = 64 << 10

// reset empties e for another tree, keeping its buffers.
func (e *canonicalEncoder) reset() {
	*e = canonicalEncoder{sizes: e.sizes[:0], arena: e.arena[:0]}
}

// release resets e and returns it to the pool, unless its buffers grew beyond maxPooledEncoder.
func (e *canonicalEncoder) release() {
	if cap(e.sizes) > maxPooledEncoder/8 || cap(e.arena) > maxPooledEncoder {
		return
	}

	e.reset()
	encoderPool.Put(e)
}

// errLowerExtensions stops [canonicalEncoder.measure] at an [ExtensionNode]: [Canonical] then encodes the tree's [Clone].
var errLowerExtensions = errors.New("filter: lower extension nodes first")

// canonicalEncoder encodes a tree in two passes over the same nodes in the same order. measure validates the tree as [Clone] does
// and records the length of every node's encoding, so that write can emit each length prefix before the encoding it measures
// and fill a buffer of the exact size: one allocation, and every byte written once, however deep the tree.
type canonicalEncoder struct {
	// sizes holds, in pre-order, the encoded length of every non-nil node and, after a literal's, the length of its value's
	// canonical form.
	sizes []int
	// arena holds the canonical forms of the literal values, in pre-order.
	arena  []byte
	cursor int // the next entry of sizes write reads
	offset int // the start in arena of the next literal value write emits
}

// measure validates node and returns the length of its encoding.
//
//nolint:gocyclo // one case per node type, mirroring write
func (e *canonicalEncoder) measure(node Node) (int, error) {
	if node == nil {
		return 0, nil
	}

	slot := len(e.sizes)
	e.sizes = append(e.sizes, 0)

	var size int

	switch n := node.(type) {
	case *LiteralNode:
		if n == nil {
			return 0, nilNodeError(node)
		}

		if err := checkLiteral(n.Value); err != nil {
			return 0, err
		}

		start := len(e.arena)
		e.arena = appendCanonicalLiteral(e.arena, n.Value)
		value := len(e.arena) - start
		e.sizes = append(e.sizes, value)
		size = partSize(len("L")) + partSize(value) + partSize(len(n.Text))
	case *IdentNode:
		if n == nil {
			return 0, nilNodeError(node)
		}

		size = partSize(len("I")) + partSize(len(n.Name))
	case *BinaryOpNode:
		if n == nil {
			return 0, nilNodeError(node)
		}

		size = partSize(len("B")) + partSize(len(n.Op.String()))
		if err := e.measureChildren(&size, n.Left, n.Right); err != nil {
			return 0, err
		}
	case *UnaryOpNode:
		if n == nil {
			return 0, nilNodeError(node)
		}

		size = partSize(len("U")) + partSize(len(n.Op.String()))
		if err := e.measureChildren(&size, n.Operand); err != nil {
			return 0, err
		}
	case *CallNode:
		if n == nil {
			return 0, nilNodeError(node)
		}

		size = partSize(len("C")) + partSize(len(n.Op.String()))
		if err := e.measureChildren(&size, n.Target); err != nil {
			return 0, err
		}

		if err := e.measureChildren(&size, n.Args...); err != nil {
			return 0, err
		}
	case *ListNode:
		if n == nil {
			return 0, nilNodeError(node)
		}

		size = partSize(len("A"))
		if err := e.measureChildren(&size, n.Elements...); err != nil {
			return 0, err
		}
	default:
		if _, ok := node.(ExtensionNode); ok {
			return 0, errLowerExtensions
		}

		return 0, coreerrs.Wrapf(ErrInvalidExpression, "cannot copy custom node %T", node)
	}

	e.sizes[slot] = size

	return size, nil
}

// measureChildren measures children in order and adds the length of each one's part to size.
func (e *canonicalEncoder) measureChildren(size *int, children ...Node) error {
	for _, child := range children {
		n, err := e.measure(child)
		if err != nil {
			return err
		}

		*size += partSize(n)
	}

	return nil
}

// write appends the encoding of node, measured by measure, to b.
func (e *canonicalEncoder) write(b *strings.Builder, node Node) {
	e.cursor++ // node's own length, written by the caller as its prefix

	switch n := node.(type) {
	case *LiteralNode:
		value := e.sizes[e.cursor]
		e.cursor++

		writePart(b, "L")
		writePrefix(b, value)
		b.Write(e.arena[e.offset : e.offset+value])
		e.offset += value

		writePart(b, n.Text)
	case *IdentNode:
		writePart(b, "I")
		writePart(b, n.Name)
	case *BinaryOpNode:
		writePart(b, "B")
		writePart(b, n.Op.String())
		e.writeChild(b, n.Left)
		e.writeChild(b, n.Right)
	case *UnaryOpNode:
		writePart(b, "U")
		writePart(b, n.Op.String())
		e.writeChild(b, n.Operand)
	case *CallNode:
		writePart(b, "C")
		writePart(b, n.Op.String())
		e.writeChild(b, n.Target)

		for _, arg := range n.Args {
			e.writeChild(b, arg)
		}
	case *ListNode:
		writePart(b, "A")

		for _, element := range n.Elements {
			e.writeChild(b, element)
		}
	}
}

// writeChild appends the part of a child node: its length prefix, then its encoding.
func (e *canonicalEncoder) writeChild(b *strings.Builder, child Node) {
	if child == nil {
		writePrefix(b, 0)

		return
	}

	writePrefix(b, e.sizes[e.cursor])
	e.write(b, child)
}

// writePart appends s as a [keys.Compose] part: its decimal length, a colon, then s.
func writePart(b *strings.Builder, s string) {
	writePrefix(b, len(s))
	b.WriteString(s)
}

// writePrefix appends the length prefix of a [keys.Compose] part n bytes long.
func writePrefix(b *strings.Builder, n int) {
	var digits [20]byte

	b.Write(strconv.AppendUint(digits[:0], uint64(n), 10))
	b.WriteByte(':')
}

// partSize returns the length of a [keys.Compose] part n bytes long, its prefix included.
func partSize(n int) int {
	size := n + len(":0") // a colon and the first digit
	for v := n; v >= 10; v /= 10 {
		size++
	}

	return size
}

// nilNodeError is the error of [Clone] for a typed nil node.
func nilNodeError(node Node) error {
	return coreerrs.Wrapf(ErrInvalidExpression, "nil node %T", node)
}

// The type parts of the canonical forms appendCanonicalLiteral writes itself: what [literalvalue.Canonical] composes for a
// predeclared type, its package path (empty) and name.
var (
	stringTypeKey  = keys.Compose("", "string")
	boolTypeKey    = keys.Compose("", "bool")
	intTypeKey     = keys.Compose("", "int")
	int64TypeKey   = keys.Compose("", "int64")
	uint64TypeKey  = keys.Compose("", "uint64")
	float64TypeKey = keys.Compose("", "float64")
)

// appendCanonicalLiteral appends [literalvalue.Canonical] of v to dst. It writes the forms of the values a parser produces
// itself, without the reflection and the intermediate strings of literalvalue, and defers to literalvalue for any other value.
func appendCanonicalLiteral(dst []byte, v any) []byte {
	var scratch [32]byte

	switch x := v.(type) {
	case nil:
		return append(dst, "nil"...)
	case string:
		return appendComposed(dst, stringTypeKey, x)
	case bool:
		return appendComposed(dst, boolTypeKey, strconv.AppendBool(scratch[:0], x))
	case int:
		return appendComposed(dst, intTypeKey, strconv.AppendInt(scratch[:0], int64(x), 10))
	case int64:
		return appendComposed(dst, int64TypeKey, strconv.AppendInt(scratch[:0], x, 10))
	case uint64:
		return appendComposed(dst, uint64TypeKey, strconv.AppendUint(scratch[:0], x, 10))
	case float64:
		return appendComposed(dst, float64TypeKey, strconv.AppendFloat(scratch[:0], x, 'g', -1, 64))
	case time.Time:
		return x.UTC().AppendFormat(append(dst, "time="...), time.RFC3339Nano)
	case []byte:
		return append(append(dst, "bytes="...), x...)
	default:
		return append(dst, literalvalue.Canonical(v)...)
	}
}

// appendComposed appends keys.Compose(typeKey, string(value)) to dst.
func appendComposed[S string | []byte](dst []byte, typeKey string, value S) []byte {
	dst = append(appendPrefix(dst, len(typeKey)), typeKey...)

	return append(appendPrefix(dst, len(value)), value...)
}

// appendPrefix appends the length prefix of a [keys.Compose] part n bytes long.
func appendPrefix(dst []byte, n int) []byte {
	return append(strconv.AppendUint(dst, uint64(n), 10), ':')
}
