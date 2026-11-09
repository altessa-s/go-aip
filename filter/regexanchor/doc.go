// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package regexanchor rewrites the end-of-text anchors of an RE2 pattern so that PCRE and ICU engines read them the way RE2 does.
//
// Outside multi-line mode RE2's $ matches only at the very end of the text, while PCRE's (MongoDB, MariaDB) and ICU's (MySQL 8) also
// match before a final newline. [EndOfText] rewrites each $ RE2 reads as end of text to \z, the absolute end in all three engines, and
// fails with [ErrInvalidRegex] for a pattern it cannot anchor. The filter translators for those backends use it for matches(); a
// translator kept in another module (the MongoDB one is a nested module) imports it from here. It holds no state and is safe for
// concurrent use.
//
// # Usage
//
//	pattern, err := regexanchor.EndOfText(`^A.*e$`) // `^A.*e\z`
//
// See README.md for the complete guide, limits, errors, and extension contracts.
package regexanchor
