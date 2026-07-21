// Copyright (c) 2026 Meizon Inc.
//
// Permission to use, copy, modify, and/or distribute this software for any
// purpose with or without fee is hereby granted, provided that the above
// copyright notice and this permission notice appear in all copies.
//
// THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES WITH
// REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY
// AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY SPECIAL, DIRECT,
// INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES WHATSOEVER RESULTING FROM
// LOSS OF USE, DATA OR PROFITS, WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR
// OTHER TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR
// PERFORMANCE OF THIS SOFTWARE.

package coredata

import (
	"fmt"

	"go.meizon.cloud/registry/pkg/page"
)

// FrameworkOrderField enumerates the sortable columns for framework pagination.
type FrameworkOrderField string

const (
	FrameworkOrderFieldCreatedAt FrameworkOrderField = "CREATED_AT"
	FrameworkOrderFieldName      FrameworkOrderField = "NAME"
)

var (
	_ page.OrderField = FrameworkOrderField("")
	_ fmt.Stringer    = FrameworkOrderField("")
)

// IsValid reports whether the order field is known.
func (f FrameworkOrderField) IsValid() bool {
	switch f {
	case FrameworkOrderFieldCreatedAt, FrameworkOrderFieldName:
		return true
	default:
		return false
	}
}

func (f FrameworkOrderField) String() string { return string(f) }

// Column maps the order field to its SQL column.
func (f FrameworkOrderField) Column() string {
	switch f {
	case FrameworkOrderFieldCreatedAt:
		return "created_at"
	case FrameworkOrderFieldName:
		return "name"
	default:
		panic(fmt.Sprintf("unsupported order field: %s", f))
	}
}
