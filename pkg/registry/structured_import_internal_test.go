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

import "testing"

// TestDetectFrameworkJSON is the gate that decides model vs no-model. It must
// accept a real flat export and reject everything else, because a false accept
// silently imports an arbitrary file as a framework, and a false reject wastes
// an LLM run re-deriving a framework we already have verbatim.
func TestDetectFrameworkJSON(t *testing.T) {
	valid := `{
	  "$schema": "meizon-framework/v2",
	  "id": "iso-27001",
	  "name": "ISO/IEC 27001",
	  "requirements": [
	    {"ref": "A.5.1", "name": "Policies", "description": "Define policies."}
	  ]
	}`

	tests := []struct {
		name     string
		filename string
		data     string
		want     bool
	}{
		{"flat export", "iso.json", valid, true},
		{"not json extension", "iso.txt", valid, false},
		{"json without the schema marker", "other.json", `{"id":"x","name":"y"}`, false},
		{"arbitrary json document", "notes.json", `{"title":"meeting notes","body":"..."}`, false},
		{"marker present but not decodable as a framework", "broken.json",
			`{"$schema":"meizon-framework/v2","requirements":"should-be-an-array"}`, false},
		{"empty", "empty.json", ``, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, ok := DetectFrameworkJSON(tc.filename, []byte(tc.data))
			if ok != tc.want {
				t.Fatalf("DetectFrameworkJSON = %v, want %v", ok, tc.want)
			}
			if ok && doc == nil {
				t.Fatal("ok=true but no document returned")
			}
			if ok && doc.ID != "iso-27001" {
				t.Fatalf("decoded wrong framework: id=%q", doc.ID)
			}
		})
	}
}
