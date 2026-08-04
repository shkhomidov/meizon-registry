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

package console_v1

import (
	"bytes"
	"strings"
	"testing"
)

// TestReadUpToDetectsOverLimit is the guard against the silent-truncation bug: a
// source larger than the cap must be REPORTED, not quietly cut. Before this, a
// 397-page PDF over the byte cap was read as only its first ~39 pages and
// processed as if complete.
func TestReadUpTo(t *testing.T) {
	tests := []struct {
		name    string
		size    int
		limit   int64
		wantBig bool
		wantLen int
	}{
		{"under limit", 10, 100, false, 10},
		{"exactly at limit", 100, 100, false, 100},
		{"one over limit", 101, 100, true, 0},
		{"far over limit", 5000, 100, true, 0},
		{"empty", 0, 100, false, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := bytes.NewReader(make([]byte, tc.size))
			data, tooBig, err := readUpTo(src, tc.limit)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tooBig != tc.wantBig {
				t.Fatalf("tooBig = %v, want %v", tooBig, tc.wantBig)
			}
			if !tooBig && len(data) != tc.wantLen {
				t.Fatalf("len = %d, want %d", len(data), tc.wantLen)
			}
			// The critical property: when over-limit, no truncated data is
			// returned that a caller could mistake for the whole document.
			if tooBig && data != nil {
				t.Fatalf("over-limit read must return no data, got %d bytes", len(data))
			}
		})
	}
}

// TestReadUpToPreservesContent: an under-limit read returns the exact bytes.
func TestReadUpToPreservesContent(t *testing.T) {
	want := "%PDF-1.7 … a small document"
	data, tooBig, err := readUpTo(strings.NewReader(want), 1024)
	if err != nil || tooBig {
		t.Fatalf("small read: tooBig=%v err=%v", tooBig, err)
	}
	if string(data) != want {
		t.Fatalf("content mangled: %q", string(data))
	}
}
