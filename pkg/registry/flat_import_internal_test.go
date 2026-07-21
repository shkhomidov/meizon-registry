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
	"go.meizon.cloud/registry/pkg/fwschema"
)

// nested builds a v2 document with one category holding the given requirements.
func nested(reqs ...fwschema.Requirement) *fwschema.Framework {
	return &fwschema.Framework{
		SchemaVersion: fwschema.SchemaVersion2,
		ID:            "acme", Name: "Acme", Version: "1.0", Region: "GLOBAL",
		Categories: []fwschema.Category{{Code: "C1", Name: "Cat", Requirements: reqs}},
	}
}

func requirements(doc *draftDocument) []draftRequirement {
	var out []draftRequirement
	for _, c := range doc.Categories {
		out = append(out, c.Requirements...)
	}
	return out
}

// TestFromNestedKeepsItemCodes is the invariant that protects cross-mappings:
// a mapping — this document's or another framework's stub pointing back at it —
// addresses the leaf by code. If flattening renamed the leaf to its parent
// requirement's code, every such mapping would silently stop resolving.
func TestFromNestedKeepsItemCodes(t *testing.T) {
	doc, err := fromNested(nested(fwschema.Requirement{
		Code: "Requirement 7", Number: "7", Title: "Restrict Access",
		Sections: []fwschema.Section{{Code: "7.2", Title: "Access defined", Items: []fwschema.Item{
			{Code: "7.2.1", Title: "Model is defined", Description: "obligation text",
				Guidance: "how to", Mappings: []fwschema.ItemMapping{
					{Relation: fwschema.RelationPartial, Framework: "iso-27001", Item: "A.5.15"},
				}},
		}}},
	}))
	if err != nil {
		t.Fatalf("fromNested: %v", err)
	}

	reqs := requirements(doc)
	if len(reqs) != 1 {
		t.Fatalf("got %d requirements, want 1", len(reqs))
	}
	r := reqs[0]
	if r.Code != "7.2.1" {
		t.Errorf("code = %q, want the item's own code %q", r.Code, "7.2.1")
	}
	if r.Description != "obligation text" || r.Guidance != "how to" {
		t.Errorf("item fields not lifted: description=%q guidance=%q", r.Description, r.Guidance)
	}
	if r.Number != "7" {
		t.Errorf("number = %q, want the parent requirement's %q", r.Number, "7")
	}
	if len(r.Mappings) != 1 || r.Mappings[0].Item != "A.5.15" {
		t.Errorf("mappings not carried onto the requirement: %+v", r.Mappings)
	}
}

// TestFromNestedSplitsMultipleItems: several items under one requirement were
// always several distinct obligations, so each becomes its own requirement
// rather than being merged down to one.
func TestFromNestedSplitsMultipleItems(t *testing.T) {
	doc, err := fromNested(nested(fwschema.Requirement{
		Code: "Requirement 7", Title: "Restrict Access",
		Sections: []fwschema.Section{{Code: "7.2", Title: "Access", Items: []fwschema.Item{
			{Code: "7.2.1", Title: "First"},
			{Code: "7.2.4", Title: "Second"},
		}}},
	}))
	if err != nil {
		t.Fatalf("fromNested: %v", err)
	}

	reqs := requirements(doc)
	if len(reqs) != 2 {
		t.Fatalf("got %d requirements, want 2 (one per item)", len(reqs))
	}
	for i, want := range []string{"7.2.1", "7.2.4"} {
		if reqs[i].Code != want {
			t.Errorf("requirement %d code = %q, want %q", i, reqs[i].Code, want)
		}
	}
}

// TestFromNestedRequirementWithoutItems: a requirement carrying no items is
// itself the assessable unit and keeps its own code.
func TestFromNestedRequirementWithoutItems(t *testing.T) {
	doc, err := fromNested(nested(fwschema.Requirement{
		Code: "R-1", Title: "Standalone", Description: "text",
	}))
	if err != nil {
		t.Fatalf("fromNested: %v", err)
	}

	reqs := requirements(doc)
	if len(reqs) != 1 || reqs[0].Code != "R-1" || reqs[0].Description != "text" {
		t.Fatalf("got %+v, want a single R-1 requirement carrying its own text", reqs)
	}
}

// TestFromFlatGroupsUncategorized: a requirement with no category still needs a
// parent, so it lands in the GENERAL bucket rather than being dropped.
func TestFromFlatGroupsUncategorized(t *testing.T) {
	doc, err := fromFlat(&fwflat.Framework{
		ID: "acme", Name: "Acme",
		Categories:   []fwflat.Category{{Ref: "C1", Name: "Cat"}},
		Requirements: []fwflat.Requirement{{Ref: "A", Category: "C1", Name: "In category"}, {Ref: "B", Name: "Orphan"}},
	})
	if err != nil {
		t.Fatalf("fromFlat: %v", err)
	}

	if len(doc.Categories) != 2 {
		t.Fatalf("got %d categories, want 2 (declared + GENERAL)", len(doc.Categories))
	}
	general := doc.Categories[1]
	if general.Code != uncategorizedRef {
		t.Errorf("second category = %q, want %q", general.Code, uncategorizedRef)
	}
	if len(general.Requirements) != 1 || general.Requirements[0].Code != "B" {
		t.Errorf("orphan requirement not bucketed: %+v", general.Requirements)
	}
	if doc.Version != "1.0" {
		t.Errorf("version = %q, want the 1.0 default", doc.Version)
	}
}

// TestFromFlatRejectsEmpty: an empty document is a generation failure, not an
// empty framework to persist.
func TestFromFlatRejectsEmpty(t *testing.T) {
	if _, err := fromFlat(&fwflat.Framework{ID: "acme", Name: "Acme"}); err == nil {
		t.Fatal("expected an error for a document with no requirements")
	}
}
