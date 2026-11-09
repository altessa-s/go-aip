// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package mariadb

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/altessa-s/go-aip/backend/sqlbase"
	"github.com/altessa-s/go-aip/backend/sqlbase/sqltype"
	"github.com/altessa-s/go-aip/filter"
)

// Spend implements [sqltype.Budget].
func (c *contractValidator) Spend(n int) error {
	if n < 0 || n > c.remaining {
		return filter.ErrDataLimitExceeded
	}

	c.remaining -= n

	return nil
}

// TypeError implements [sqltype.Budget].
func (*contractValidator) TypeError() error { return typeError() }

func (c *contractValidator) validateLiteral(value any, kind sqlbase.Kind) error {
	// Match the SQL contract's literal budget, including pointer unwrapping.
	value, err := sqltype.Unwrap(value, c.options.maxValueDepth, c, nil)
	if err != nil || value == nil {
		return err
	}

	switch kind {
	case sqlbase.Numeric:
		_, err := sqltype.ParseNumber(value, c)
		return err
	case sqlbase.Integer:
		_, err := sqltype.Integer(value, c)
		return err
	case sqlbase.Float:
		_, err := sqltype.Float(value, c)
		return err
	case sqlbase.Text:
		s, ok := value.(string)
		if !ok {
			return typeError()
		}

		if err := c.Spend(len(s)); err != nil {
			return err
		}

		if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
			return typeError()
		}
	case sqlbase.Bytes:
		b, ok := value.([]byte)
		if !ok {
			return typeError()
		}

		return c.Spend(len(b))
	case sqlbase.Boolean:
		if _, ok := value.(bool); !ok {
			return typeError()
		}
	case sqlbase.Timestamp:
		if _, ok := value.(time.Time); !ok {
			return typeError()
		}
	default:
		return typeError()
	}

	return nil
}
