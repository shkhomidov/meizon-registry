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

// isoControlList mirrors the shape of a public ISO 27001 control-list export:
// an id/name header, a logo we ignore, and a flat controls array of {id, name}
// where the name is "Category - Title".
const isoControlList = `{
  "id": "ISO/IEC 27001:2022",
  "name": "ISO 27001 (2022)",
  "logo": { "light": "<svg/>", "dark": "<svg/>" },
  "controls": [
    { "id": "4.1", "name": "Context of the organization - Understanding the organization and its context" },
    { "id": "5.1", "name": "Leadership - Leadership and commitment" },
    { "id": "9.2.1", "name": "Performance evaluation - Internal Audit - General" },
    { "id": "A.5.1", "name": "Organizational - Policies for information security" },
    { "id": "A.8.24", "name": "Technological - Use of cryptography" },
    { "id": "X1", "name": "A control with no category separator" }
  ]
}`

// TestDetectControlListJSON: a plain control-list JSON is recognized and converted
// 1:1 to a valid flat framework — every control becomes a requirement (ref = id),
// with categories derived from the name prefix. This is the ISO27001 upload that
// was being (needlessly) run through the model.
func TestDetectControlListJSON(t *testing.T) {
	doc, ok := DetectControlListJSON("ISO27001-2022.json", []byte(isoControlList))
	if !ok {
		t.Fatal("a control-list JSON must be detected")
	}
	if err := doc.Validate(fwflat.ValidateOptions{}); err != nil {
		t.Fatalf("converted framework must validate: %v", err)
	}
	if doc.ID != "iso-iec-27001-2022" {
		t.Fatalf("id = %q, want iso-iec-27001-2022", doc.ID)
	}
	if len(doc.Requirements) != 6 {
		t.Fatalf("every control must map to a requirement, got %d", len(doc.Requirements))
	}

	byRef := map[string]fwflat.Requirement{}
	for _, r := range doc.Requirements {
		byRef[r.Ref] = r
	}
	catName := map[string]string{}
	for _, c := range doc.Categories {
		catName[c.Ref] = c.Name
	}

	// Category comes from the prefix; the title is the remainder (split on the
	// FIRST " - ", so "Internal Audit - General" is kept intact as the title).
	if r := byRef["9.2.1"]; catName[r.Category] != "Performance evaluation" || r.Name != "Internal Audit - General" {
		t.Fatalf("9.2.1 mis-split: category=%q name=%q", catName[r.Category], r.Name)
	}
	if r := byRef["A.5.1"]; catName[r.Category] != "Organizational" {
		t.Fatalf("A.5.1 category = %q, want Organizational", catName[r.Category])
	}
	// A name with no separator falls under General.
	if r := byRef["X1"]; catName[r.Category] != "General" || r.Name != "A control with no category separator" {
		t.Fatalf("X1 mis-handled: category=%q name=%q", catName[r.Category], r.Name)
	}
}

// TestDetectControlListJSONRejects: a framework export (has the schema marker),
// a non-JSON name, and a controls-less JSON must all be left for their own paths.
func TestDetectControlListJSONRejects(t *testing.T) {
	// A meizon-framework/v2 export is DetectFrameworkJSON's job, not this one.
	export := []byte(`{"$schema":"` + fwflat.SchemaMarker + `","id":"x","name":"X","controls":[{"id":"1","name":"a"}]}`)
	if _, ok := DetectControlListJSON("x.json", export); ok {
		t.Fatal("a framework export must not be taken as a control list")
	}
	// Not a .json upload.
	if _, ok := DetectControlListJSON("x.pdf", []byte(isoControlList)); ok {
		t.Fatal("a non-json filename must not be detected")
	}
	// No controls array.
	if _, ok := DetectControlListJSON("x.json", []byte(`{"id":"x","name":"X"}`)); ok {
		t.Fatal("a JSON without controls must not be detected")
	}
	// A control missing its name is not a clean list.
	if _, ok := DetectControlListJSON("x.json", []byte(`{"controls":[{"id":"1"}]}`)); ok {
		t.Fatal("a control with no name must not be detected")
	}
}
