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

package fwschema

// SchemaVersion2 is the rich hierarchical exchange schema:
// categories → requirements → sections → items, with relation-typed
// cross-mappings on items. v1 bundles (flat controls) remain valid.
const SchemaVersion2 = "2.0"

// MappingRelation classifies how a source item relates to a target item.
type MappingRelation string

const (
	RelationEquivalent MappingRelation = "equivalent"
	RelationPartial    MappingRelation = "partial"
	RelationSuperset   MappingRelation = "superset"
	RelationSubset     MappingRelation = "subset"
)

// IsValid reports whether r is a known relation.
func (r MappingRelation) IsValid() bool {
	switch r {
	case RelationEquivalent, RelationPartial, RelationSuperset, RelationSubset:
		return true
	default:
		return false
	}
}

// ItemType classifies a requirement item.
type ItemType string

const (
	ItemTypeControlRequirement       ItemType = "control_requirement"
	ItemTypeLegalObligation          ItemType = "legal_obligation"
	ItemTypeManagementClause         ItemType = "management_clause"
	ItemTypeDocumentationRequirement ItemType = "documentation_requirement"
	ItemTypeReportingObligation      ItemType = "reporting_obligation"
)

// IsValid reports whether t is a known item type. Empty defaults to
// control_requirement at the service layer.
func (t ItemType) IsValid() bool {
	switch t {
	case ItemTypeControlRequirement, ItemTypeLegalObligation, ItemTypeManagementClause,
		ItemTypeDocumentationRequirement, ItemTypeReportingObligation:
		return true
	default:
		return false
	}
}

type (
	// Category is the top hierarchy level (goal / theme / chapter).
	Category struct {
		Code              string        `json:"code"`
		Name              string        `json:"name"`
		Description       string        `json:"description,omitempty"`
		IsOptional        bool          `json:"isOptional,omitempty"`
		ApplicabilityNote string        `json:"applicabilityNote,omitempty"`
		Requirements      []Requirement `json:"requirements"`
	}

	// Requirement is a numbered requirement inside a category, and the
	// assessable unit: the obligation text, guidance and cross-mappings live
	// here. Structure is Category → Requirement → Control.
	//
	// Sections is legacy. Documents authored against the earlier four-level
	// shape still carry it and are accepted on import (each item becomes a
	// requirement); it is never emitted, so bundles produced now are strictly
	// two-level.
	Requirement struct {
		Code                   string        `json:"code"`
		Number                 string        `json:"number,omitempty"`
		Title                  string        `json:"title"`
		Description            string        `json:"description,omitempty"`
		ItemType               ItemType      `json:"itemType,omitempty"`
		LegalCitation          string        `json:"legalCitation,omitempty"`
		ValidationApproaches   []string      `json:"validationApproaches,omitempty"`
		EffectiveFrom          string        `json:"effectiveFrom,omitempty"`
		RetiredAt              string        `json:"retiredAt,omitempty"`
		Guidance               string        `json:"guidance,omitempty"`
		Tags                   []string      `json:"tags,omitempty"`
		ApplicabilityRoles     []string      `json:"applicabilityRoles,omitempty"`
		ApplicabilityCondition string        `json:"applicabilityCondition,omitempty"`
		Mappings               []ItemMapping `json:"mappings,omitempty"`
		Sections               []Section     `json:"sections,omitempty"`
	}

	// Section is a legacy objective statement grouping items. Read-only: see
	// Requirement.Sections.
	Section struct {
		Code        string `json:"code"`
		Title       string `json:"title"`
		Description string `json:"description,omitempty"`
		Items       []Item `json:"items"`
	}

	// Item is the assessable unit (control requirement, legal obligation, …).
	Item struct {
		Code                   string        `json:"code"`
		Title                  string        `json:"title"`
		Description            string        `json:"description,omitempty"`
		ItemType               ItemType      `json:"itemType,omitempty"`
		LegalCitation          string        `json:"legalCitation,omitempty"`
		ValidationApproaches   []string      `json:"validationApproaches,omitempty"`
		EffectiveFrom          string        `json:"effectiveFrom,omitempty"`
		RetiredAt              string        `json:"retiredAt,omitempty"`
		Guidance               string        `json:"guidance,omitempty"`
		Tags                   []string      `json:"tags,omitempty"`
		ApplicabilityRoles     []string      `json:"applicabilityRoles,omitempty"`
		ApplicabilityCondition string        `json:"applicabilityCondition,omitempty"`
		Mappings               []ItemMapping `json:"mappings,omitempty"`
	}

	// ItemMapping is a relation-typed cross-mapping to another framework's item,
	// addressed by code (stable under signing; resolution to concrete rows is
	// derived state outside the signed content).
	ItemMapping struct {
		Relation  MappingRelation `json:"relation"`
		Framework string          `json:"framework"`
		Version   string          `json:"version,omitempty"`
		Item      string          `json:"item"`
		Notes     string          `json:"notes,omitempty"`
	}
)

// IsV2 reports whether the framework uses the hierarchical schema.
func (f *Framework) IsV2() bool {
	return f.SchemaVersion == SchemaVersion2
}

// WalkAssessable visits every assessable unit in authored order: each
// requirement, plus — for legacy documents that still nest them — each item
// beneath it. Callers that need "one row per obligation" want this, not
// WalkItems, which only sees the legacy level.
func (f *Framework) WalkAssessable(fn func(cat *Category, req *Requirement, item *Item)) {
	for ci := range f.Categories {
		cat := &f.Categories[ci]
		for ri := range cat.Requirements {
			req := &cat.Requirements[ri]
			nested := false
			for si := range req.Sections {
				sec := &req.Sections[si]
				for ii := range sec.Items {
					nested = true
					fn(cat, req, &sec.Items[ii])
				}
			}
			if !nested {
				fn(cat, req, nil)
			}
		}
	}
}

// WalkItems visits every item in hierarchy order.
func (f *Framework) WalkItems(fn func(cat *Category, req *Requirement, sec *Section, item *Item)) {
	for ci := range f.Categories {
		cat := &f.Categories[ci]
		for ri := range cat.Requirements {
			req := &cat.Requirements[ri]
			for si := range req.Sections {
				sec := &req.Sections[si]
				for ii := range sec.Items {
					fn(cat, req, sec, &sec.Items[ii])
				}
			}
		}
	}
}
