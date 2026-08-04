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
	"testing"

	"go.meizon.cloud/registry/pkg/fwflat"
)

// isoControlList mirrors a public ISO 27001 control-list export: an id/name
// header and a flat controls array of {id, name}. It carries no
// meizon-framework/v2 schema marker.
const isoControlList = `{
  "id": "ISO/IEC 27001:2022",
  "name": "ISO 27001 (2022)",
  "controls": [
    { "id": "4.1", "name": "Context of the organization - Understanding the organization and its context" },
    { "id": "A.8.24", "name": "Technological - Use of cryptography" }
  ]
}`

// TestDetectFrameworkJSON_ControlListFallsThroughToModel pins the routing this
// package relies on: only a full meizon-framework/v2 export round-trips directly.
// A plain control-list JSON is NOT a framework export, so it is left for the
// generation pipeline — which enriches it with generated requirements and
// controls rather than importing the bare id/name pairs 1:1.
func TestDetectFrameworkJSON_ControlListFallsThroughToModel(t *testing.T) {
	if _, ok := DetectFrameworkJSON("ISO27001-2022.json", []byte(isoControlList)); ok {
		t.Fatal("a control-list JSON must not be taken as a framework export; it must fall through to the model")
	}
}

// TestDetectFrameworkJSON_ExportRoundTrips confirms the one case that still
// skips the model: a document carrying the exact schema marker is a framework we
// produced, complete with controls, so re-uploading it reproduces it directly.
func TestDetectFrameworkJSON_ExportRoundTrips(t *testing.T) {
	export := []byte(`{"$schema":"` + fwflat.SchemaMarker + `","id":"x","name":"X","version":"1.0",` +
		`"categories":[{"ref":"c","name":"C"}],` +
		`"requirements":[{"ref":"1","category":"c","name":"a"}]}`)

	doc, ok := DetectFrameworkJSON("x.json", export)
	if !ok {
		t.Fatal("a meizon-framework/v2 export must be detected and round-tripped")
	}
	if doc.ID != "x" {
		t.Fatalf("id = %q, want x", doc.ID)
	}

	// A non-.json filename is never a framework export, regardless of content.
	if _, ok := DetectFrameworkJSON("x.pdf", export); ok {
		t.Fatal("a non-json filename must not be detected")
	}
}
