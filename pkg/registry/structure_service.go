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
	"time"

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/fwschema"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/iam"
	"go.meizon.cloud/registry/pkg/validator"
)

// Structure view DTOs (console API shape). Mapping rows carry their id and
// resolution state so the UI can show resolved ✓ vs stub ◌ and delete rows.
type (
	StructureMapping struct {
		ID        gid.GID `json:"id"`
		Relation  string  `json:"relation"`
		Framework string  `json:"framework"`
		Version   string  `json:"version,omitempty"`
		Item      string  `json:"item"`
		Notes     string  `json:"notes,omitempty"`
		Resolved  bool    `json:"resolved"`
	}

	// StructureRequirement is the assessable leaf: the obligation text, the
	// controls that satisfy it and its cross-mappings all hang here.
	StructureRequirement struct {
		Code        string             `json:"code"`
		Number      string             `json:"number,omitempty"`
		Title       string             `json:"title"`
		Description string             `json:"description,omitempty"`
		ItemType    string             `json:"itemType,omitempty"`
		Guidance    string             `json:"guidance,omitempty"`
		Origin      string             `json:"origin,omitempty"`
		Mappings    []StructureMapping `json:"mappings"`
	}

	StructureCategory struct {
		Code         string                 `json:"code"`
		Name         string                 `json:"name"`
		Description  string                 `json:"description,omitempty"`
		IsOptional   bool                   `json:"isOptional,omitempty"`
		Requirements []StructureRequirement `json:"requirements"`
	}
)

// StructureOf assembles the full hierarchy (with mapping state) of a version.
func (s *Service) StructureOf(ctx context.Context, versionID gid.GID) ([]StructureCategory, error) {
	var out []StructureCategory
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		tree, err := s.structureOfTx(ctx, conn, versionID)
		out = tree
		return err
	})
	return out, err
}

func (s *Service) structureOfTx(ctx context.Context, conn pg.Querier, versionID gid.GID) ([]StructureCategory, error) {
	scope := s.platformScope()

	var categories coredata.RequirementCategories
	var requirements coredata.Requirements
	var mappings coredata.RequirementCrossMappings

	if err := categories.LoadAllByVersion(ctx, conn, scope, versionID); err != nil {
		return nil, err
	}
	if err := requirements.LoadAllByVersion(ctx, conn, scope, versionID); err != nil {
		return nil, err
	}
	if err := mappings.LoadAllByVersion(ctx, conn, scope, versionID); err != nil {
		return nil, err
	}

	mapsByRequirement := map[gid.GID][]StructureMapping{}
	for _, m := range mappings {
		mapsByRequirement[m.SourceRequirementID] = append(mapsByRequirement[m.SourceRequirementID], StructureMapping{
			ID: m.ID, Relation: m.Relation, Framework: m.TargetFrameworkCode,
			Version: m.TargetFrameworkVersion, Item: m.TargetRequirementCode,
			Notes: m.Notes, Resolved: m.IsResolved,
		})
	}

	reqsByCategory := map[gid.GID][]StructureRequirement{}
	for _, r := range requirements {
		sm := mapsByRequirement[r.ID]
		if sm == nil {
			sm = []StructureMapping{}
		}
		reqsByCategory[r.CategoryID] = append(reqsByCategory[r.CategoryID], StructureRequirement{
			Code: r.Code, Number: r.Number, Title: r.Title, Description: r.Description,
			ItemType: r.ItemType, Guidance: r.Guidance, Origin: r.Origin, Mappings: sm,
		})
	}

	out := make([]StructureCategory, 0, len(categories))
	for _, c := range categories {
		cr := reqsByCategory[c.ID]
		if cr == nil {
			cr = []StructureRequirement{}
		}
		out = append(out, StructureCategory{
			Code: c.Code, Name: c.Name, Description: c.Description,
			IsOptional: c.IsOptional, Requirements: cr,
		})
	}
	return out, nil
}

// requireDraft loads version+framework and authorizes a structure edit.
func (s *Service) requireDraft(ctx context.Context, conn pg.Querier, actorID, versionID gid.GID, action string) (coredata.FrameworkVersion, coredata.Framework, error) {
	version, framework, err := s.loadVersionAndFramework(ctx, conn, s.platformScope(), versionID)
	if err != nil {
		return version, framework, err
	}
	if err := s.authorize(ctx, conn, actorID, action, framework.Region, framework.ID); err != nil {
		return version, framework, err
	}
	if version.Status != coredata.FrameworkVersionStatusDraft {
		return version, framework, fmt.Errorf("%w: structure can only change in a DRAFT (current: %s)", ErrInvalidTransition, version.Status)
	}
	return version, framework, nil
}

// AddCategory adds a category to a draft version.
func (s *Service) AddCategory(ctx context.Context, actorID, versionID gid.GID, code, name, description, origin string) error {
	v := validator.New()
	v.Check(code, "code", validator.Required(), validator.MaxLen(128), validator.NoNewLine())
	v.Check(name, "name", validator.Required(), validator.MaxLen(512), validator.NoNewLine())
	if err := v.Error(); err != nil {
		return err
	}

	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		version, _, err := s.requireDraft(ctx, tx, actorID, versionID, iam.ActionControlCreate)
		if err != nil {
			return err
		}

		var existing coredata.RequirementCategories
		if err := existing.LoadAllByVersion(ctx, tx, s.platformScope(), version.ID); err != nil {
			return err
		}

		cat := coredata.RequirementCategory{
			ID:                 gid.New(s.cfg.PlatformTenant, coredata.RequirementCategoryEntityType),
			FrameworkVersionID: version.ID,
			Code:               strings.TrimSpace(code),
			Name:               strings.TrimSpace(name),
			Description:        description,
			Position:           len(existing),
			Origin:             origin,
			CreatedAt:          time.Now(),
		}
		return cat.Insert(ctx, tx, s.platformScope())
	})
}

// AddRequirement adds a requirement under a category (by code). The requirement
// is the assessable leaf, so it carries the item type and guidance directly.
func (s *Service) AddRequirement(ctx context.Context, actorID, versionID gid.GID, categoryCode, code, number, title, description, itemType, guidance, origin string) error {
	v := validator.New()
	v.Check(categoryCode, "category", validator.Required())
	v.Check(code, "code", validator.Required(), validator.MaxLen(128), validator.NoNewLine())
	v.Check(title, "title", validator.Required(), validator.MaxLen(512), validator.NoNewLine())
	if err := v.Error(); err != nil {
		return err
	}
	if itemType == "" {
		itemType = string(fwschema.ItemTypeControlRequirement)
	}
	if !fwschema.ItemType(itemType).IsValid() {
		return fmt.Errorf("%w: unknown item type %q", ErrInvalidInput, itemType)
	}

	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		version, _, err := s.requireDraft(ctx, tx, actorID, versionID, iam.ActionControlCreate)
		if err != nil {
			return err
		}

		var categories coredata.RequirementCategories
		if err := categories.LoadAllByVersion(ctx, tx, s.platformScope(), version.ID); err != nil {
			return err
		}
		parent := findCategory(categories, categoryCode)
		if parent == nil {
			return fmt.Errorf("category %q: %w", categoryCode, coredata.ErrResourceNotFound)
		}

		var existing coredata.Requirements
		if err := existing.LoadAllByVersion(ctx, tx, s.platformScope(), version.ID); err != nil {
			return err
		}

		req := coredata.Requirement{
			ID:                 gid.New(s.cfg.PlatformTenant, coredata.RequirementEntityType),
			FrameworkVersionID: version.ID,
			CategoryID:         parent.ID,
			Code:               strings.TrimSpace(code),
			Number:             strings.TrimSpace(number),
			Title:              strings.TrimSpace(title),
			Description:        description,
			ItemType:           itemType,
			Guidance:           guidance,
			Position:           len(existing),
			Origin:             origin,
			CreatedAt:          time.Now(),
		}
		return req.Insert(ctx, tx, s.platformScope())
	})
}

// DeleteStructureNode removes a category or requirement by code from a draft
// (requirements, control links and mappings cascade via FKs).
func (s *Service) DeleteStructureNode(ctx context.Context, actorID, versionID gid.GID, level, code string) error {
	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		version, _, err := s.requireDraft(ctx, tx, actorID, versionID, iam.ActionControlDelete)
		if err != nil {
			return err
		}
		scope := s.platformScope()

		switch level {
		case "category":
			var all coredata.RequirementCategories
			if err := all.LoadAllByVersion(ctx, tx, scope, version.ID); err != nil {
				return err
			}
			if c := findCategory(all, code); c != nil {
				return coredata.RequirementCategory{}.Delete(ctx, tx, scope, c.ID)
			}
		// "item" is accepted as an alias for "requirement": the two levels
		// collapsed into one, and an older console build still says "item".
		case "requirement", "item":
			var all coredata.Requirements
			if err := all.LoadAllByVersion(ctx, tx, scope, version.ID); err != nil {
				return err
			}
			if r := findRequirement(all, code); r != nil {
				return coredata.Requirement{}.Delete(ctx, tx, scope, r.ID)
			}
		default:
			return fmt.Errorf("%w: unknown structure level %q", ErrInvalidInput, level)
		}
		return coredata.ErrResourceNotFound
	})
}

func findCategory(cs coredata.RequirementCategories, code string) *coredata.RequirementCategory {
	for _, c := range cs {
		if c.Code == code {
			return c
		}
	}
	return nil
}

func findRequirement(rs coredata.Requirements, code string) *coredata.Requirement {
	for _, r := range rs {
		if r.Code == code {
			return r
		}
	}
	return nil
}
