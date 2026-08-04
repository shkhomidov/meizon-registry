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

package registry

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSanitizeJSONStrings is the rung that keeps an expensive result: a NUL that
// jsonb would reject (SQLSTATE 22P05) is removed from string VALUES, and the
// payload stays valid JSON with the same structure and non-string values.
func TestSanitizeJSONStrings(t *testing.T) {
	// The real payload comes from json.Marshal, which ENCODES a NUL as an escape
	// — so build it the same way, from a structure whose strings carry NULs.
	nul := "\x00"
	payload, _ := json.Marshal(map[string]any{
		"name":  "PCI DSS",
		"note":  "bad" + nul + "value",
		"items": []any{"a" + nul + "b", map[string]any{"k": "c" + nul}},
		"n":     7,
	})

	clean, ok := sanitizeJSONStrings(payload)
	if !ok {
		t.Fatal("valid JSON should sanitize")
	}
	if strings.ContainsRune(string(clean), 0) {
		t.Fatalf("NUL survived sanitization: %q", clean)
	}

	var got map[string]any
	if err := json.Unmarshal(clean, &got); err != nil {
		t.Fatalf("sanitized payload is not valid JSON: %v", err)
	}
	if got["name"] != "PCI DSS" {
		t.Fatalf("clean string field altered: %v", got["name"])
	}
	if got["note"] != "badvalue" {
		t.Fatalf("note not sanitized as expected: %q", got["note"])
	}
	if got["n"].(float64) != 7 {
		t.Fatalf("numeric field lost: %v", got["n"])
	}
	items := got["items"].([]any)
	if items[0] != "ab" {
		t.Fatalf("array string not sanitized: %q", items[0])
	}
	if items[1].(map[string]any)["k"] != "c" {
		t.Fatalf("nested string not sanitized: %v", items[1])
	}
}

// TestSanitizeJSONStringsPreservesEscapeText: a NUL byte marshals to a backslash
// escape; because we sanitize DECODED runes and re-marshal (not the raw bytes),
// text that legitimately contains a backslash is never corrupted. Round-tripping
// a normal document must be byte-stable.
func TestSanitizeJSONStringsStable(t *testing.T) {
	doc := map[string]any{
		"path":  "C:\\Users\\audit",
		"regex": "^A\\.5\\..*$",
		"n":     42.0,
		"list":  []any{"one", "two"},
	}
	raw, _ := json.Marshal(doc)
	clean, ok := sanitizeJSONStrings(raw)
	if !ok {
		t.Fatal("should sanitize")
	}
	if string(clean) != string(raw) {
		t.Fatalf("a NUL-free document was altered:\n in=%s\nout=%s", raw, clean)
	}
}
