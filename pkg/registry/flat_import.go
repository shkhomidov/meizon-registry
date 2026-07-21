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
	"context"
	"fmt"
	"strings"

	"go.gearno.de/kit/pg"

	"go.meizon.cloud/registry/pkg/fwflat"
	"go.meizon.cloud/registry/pkg/fwschema"
	"go.meizon.cloud/registry/pkg/gid"
)

// The registry stores Category → Requirement → Control. Two document formats
// arrive from outside: the flat meizon-framework/v2 shape the generator emits
// (pkg/fwflat) and the richer nested exchange schema accepted by the JSON
// import (pkg/fwschema, which still nests sections and items).
//
// Rather than let either format reach the database directly, both are converted
// to draftDocument — the one shape the write path persists. That keeps a single
// insert path and makes the flattening rule explicit and testable instead of
// hidden inside a transaction.
type (
	draftDocument struct {
		ID, Name, ShortName, Version string
		Region, Authority, License   string
		Description, RevisionNotes   string
		SourceLanguage               string
		Categories                   []draftCategory
	}

	draftCategory struct {
		Code, Name, Description string
		IsOptional              bool
		ApplicabilityNote       string
		Requirements            []draftRequirement
	}

	draftRequirement struct {
		Code, Number, Title, Description string
		ItemType, LegalCitation          string
		ValidationApproaches             []string
		EffectiveFrom, RetiredAt         string
		Guidance                         string
		Tags, ApplicabilityRoles         []string
		ApplicabilityCondition           string
		Mappings                         []fwschema.ItemMapping
	}
)

// Requirements without a category are grouped under a single "GENERAL" bucket
// so every requirement has a parent.
const uncategorizedRef = "GENERAL"

// fromFlat converts a validated flat framework into the persisted shape. The
// mapping is direct — this is the format the storage schema was reshaped to
// match. Suggested controls are not folded into guidance here: they become real
// control-library rows in seedControlLibrary.
func fromFlat(flat *fwflat.Framework) (*draftDocument, error) {
	if flat == nil {
		return nil, fmt.Errorf("no document")
	}
	if len(flat.Requirements) == 0 {
		return nil, fmt.Errorf("the document has no requirements")
	}

	// Preserve declared category order, then append GENERAL if needed.
	order := make([]string, 0, len(flat.Categories)+1)
	names := map[string]string{}
	for _, c := range flat.Categories {
		if _, seen := names[c.Ref]; seen {
			continue
		}
		names[c.Ref] = c.Name
		order = append(order, c.Ref)
	}
	byCategory := map[string][]fwflat.Requirement{}
	for _, r := range flat.Requirements {
		ref := strings.TrimSpace(r.Category)
		if ref == "" {
			ref = uncategorizedRef
		}
		if _, known := names[ref]; !known {
			names[ref] = "General"
			order = append(order, ref)
		}
		byCategory[ref] = append(byCategory[ref], r)
	}

	doc := &draftDocument{
		ID:             flat.ID,
		Name:           flat.Name,
		ShortName:      flat.Name,
		Version:        defaultVersion(flat.Version),
		Region:         flat.PrimaryRegion(),
		License:        string(fwschema.LicensePublicDomain),
		Description:    flat.Category,
		SourceLanguage: flat.Language,
	}
	for _, ref := range order {
		reqs := byCategory[ref]
		if len(reqs) == 0 {
			continue // a declared category nothing landed in
		}
		cat := draftCategory{Code: ref, Name: names[ref]}
		for _, r := range reqs {
			cat.Requirements = append(cat.Requirements, draftRequirement{
				Code:        r.Ref,
				Title:       r.Name,
				Description: r.Description,
				ItemType:    string(fwschema.ItemTypeControlRequirement),
			})
		}
		doc.Categories = append(doc.Categories, cat)
	}
	return doc, nil
}

// fromNested flattens a nested v2 exchange document onto requirements.
//
// The nested schema allows several sections per requirement and several items
// per section; the registry stores one obligation per requirement. Where a
// requirement holds exactly one item — which is what every document the flat
// pipeline produced looks like — the conversion is lossless. Where it holds
// more, each item becomes a requirement of its own, keyed by the item code, so
// no obligation is silently dropped. That is the honest reading: a distinct
// item was always a distinct assessable unit.
func fromNested(doc *fwschema.Framework) (*draftDocument, error) {
	if doc == nil {
		return nil, fmt.Errorf("no document")
	}

	out := &draftDocument{
		ID: doc.ID, Name: doc.Name, ShortName: doc.ShortName,
		Version: doc.Version, Region: doc.Region, Authority: doc.Authority,
		License: string(doc.License), Description: doc.Description,
		RevisionNotes: doc.RevisionNotes,
	}

	for _, cat := range doc.Categories {
		dc := draftCategory{
			Code: cat.Code, Name: cat.Name, Description: cat.Description,
			IsOptional: cat.IsOptional, ApplicabilityNote: cat.ApplicabilityNote,
		}
		for _, req := range cat.Requirements {
			items := flattenItems(req)
			// An item keeps its OWN code, never the parent requirement's:
			// cross-mappings — this document's and other frameworks' stubs
			// pointing back at it — address items by code, so renaming the leaf
			// would silently break every mapping that targets it. Only a
			// requirement with no items at all is keyed by its own code.
			if len(items) == 0 {
				dc.Requirements = append(dc.Requirements, draftRequirement{
					Code: req.Code, Number: req.Number, Title: req.Title,
					Description: req.Description,
					ItemType:    string(fwschema.ItemTypeControlRequirement),
				})
				continue
			}
			for _, it := range items {
				dc.Requirements = append(dc.Requirements, mergeItem(req, it, it.Code))
			}
		}
		out.Categories = append(out.Categories, dc)
	}
	return out, nil
}

func flattenItems(req fwschema.Requirement) []fwschema.Item {
	var items []fwschema.Item
	for _, sec := range req.Sections {
		items = append(items, sec.Items...)
	}
	return items
}

// mergeItem folds an item onto its parent requirement under the given code.
// The item wins for the obligation text and its own metadata; the requirement
// supplies number and a title fallback.
func mergeItem(req fwschema.Requirement, it fwschema.Item, code string) draftRequirement {
	title := it.Title
	if strings.TrimSpace(title) == "" {
		title = req.Title
	}
	description := it.Description
	if strings.TrimSpace(description) == "" {
		description = req.Description
	}
	itemType := string(it.ItemType)
	if itemType == "" {
		itemType = string(fwschema.ItemTypeControlRequirement)
	}
	return draftRequirement{
		Code:                   code,
		Number:                 req.Number,
		Title:                  title,
		Description:            description,
		ItemType:               itemType,
		LegalCitation:          it.LegalCitation,
		ValidationApproaches:   it.ValidationApproaches,
		EffectiveFrom:          it.EffectiveFrom,
		RetiredAt:              it.RetiredAt,
		Guidance:               it.Guidance,
		Tags:                   it.Tags,
		ApplicabilityRoles:     it.ApplicabilityRoles,
		ApplicabilityCondition: it.ApplicabilityCondition,
		Mappings:               it.Mappings,
	}
}

func defaultVersion(v string) string {
	if strings.TrimSpace(v) == "" {
		return "1.0"
	}
	return v
}

// AcceptGeneratedFlat imports a reviewed flat framework as a DRAFT (origin=ai),
// then seeds the control library from the flat controls so the suggestions are
// editable in the Controls tab.
func (s *Service) AcceptGeneratedFlat(ctx context.Context, actorID gid.GID, flat *fwflat.Framework, jobID string) (CreateFrameworkResult, error) {
	flat.Normalize()
	if err := flat.Validate(fwflat.ValidateOptions{}); err != nil {
		return CreateFrameworkResult{}, fmt.Errorf("the framework is not valid: %w", err)
	}
	doc, err := fromFlat(flat)
	if err != nil {
		return CreateFrameworkResult{}, err
	}

	out, err := s.importFrameworkDoc(ctx, actorID, doc, "ai", "framework.generate_accept")
	if err != nil {
		return out, err
	}

	// Best-effort: populate the control library with the generated controls and
	// link each to the requirements that referenced it. A failure here must not
	// undo the draft the auditor just created.
	if err := s.seedControlLibrary(ctx, actorID, flat, out); err != nil {
		s.logger.WarnCtx(ctx, fmt.Sprintf("cannot seed control library from generated framework: %v", err))
	}

	// Attach the document this was generated from, if one was kept. Also
	// best-effort and for the same reason — but note the consequence: a failure
	// here leaves the file staged, and the sweeper removes it a day later, so
	// the framework simply has no source on file.
	if jobID != "" {
		if err := s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
			return s.linkSourceDocumentTx(ctx, tx, jobID, out.FrameworkID)
		}); err != nil {
			s.logger.WarnCtx(ctx, fmt.Sprintf("cannot attach source document for job %s: %v", jobID, err))
		}
	}

	// English is what cross-mapping reads, so a non-English framework gets one
	// without being asked. Runs in the background; a framework already in
	// English is skipped rather than translated into itself.
	s.EnsureCanonicalTranslation(ctx, actorID, flat.ID)
	return out, nil
}

// seedControlLibrary adds each flat control to the framework's control library
// and links it to the requirements that referenced it.
func (s *Service) seedControlLibrary(ctx context.Context, actorID gid.GID, flat *fwflat.Framework, created CreateFrameworkResult) error {
	for _, c := range flat.Controls {
		var requirementCodes []string
		for _, r := range flat.Requirements {
			for _, ref := range r.Controls {
				if ref == c.Ref {
					requirementCodes = append(requirementCodes, r.Ref)
					break
				}
			}
		}
		if _, err := s.AddControlEntry(ctx, actorID, flat.ID, c.Ref, c.Name, c.Description, c.Category, requirementCodes); err != nil {
			return fmt.Errorf("control %q: %w", c.Ref, err)
		}
	}
	return nil
}
