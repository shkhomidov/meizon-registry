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
	"time"

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/fwschema"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/iam"
)

// ImportFrameworkDoc creates a framework and a full DRAFT version from a rich
// v2 exchange document (hierarchy + mapping stubs) — the JSON upload path. The
// draft then goes through the normal review → approve → publish lifecycle.
func (s *Service) ImportFrameworkDoc(ctx context.Context, actorID gid.GID, doc *fwschema.Framework) (CreateFrameworkResult, error) {
	if err := doc.Validate(); err != nil {
		return CreateFrameworkResult{}, err
	}
	if !doc.IsV2() {
		return CreateFrameworkResult{}, fmt.Errorf("import requires a %s document with categories", fwschema.SchemaVersion2)
	}
	flattened, err := fromNested(doc)
	if err != nil {
		return CreateFrameworkResult{}, err
	}
	out, err := s.importFrameworkDoc(ctx, actorID, flattened, "import", "framework.import")
	if err != nil {
		return out, err
	}
	// Author the audit template for the imported draft too, so an imported
	// framework behaves like a generated one. Best-effort and only when an LLM is
	// configured; ExportFlat resolves the just-created version.
	if flat, ferr := s.ExportFlat(ctx, doc.ID); ferr == nil {
		s.AutoStartQATemplate(ctx, actorID, doc.ID, out.VersionID, flat)
	}
	return out, nil
}

// importFrameworkDoc is the shared create-from-document path; origin marks how
// the content arrived (import | ai) and auditAction labels the audit entry.
func (s *Service) importFrameworkDoc(ctx context.Context, actorID gid.GID, doc *draftDocument, origin, auditAction string) (CreateFrameworkResult, error) {
	var out CreateFrameworkResult
	err := s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		if err := s.authorize(ctx, tx, actorID, iam.ActionFrameworkCreate, doc.Region, gid.Nil); err != nil {
			return err
		}

		now := time.Now()
		scope := s.platformScope()

		framework := coredata.Framework{
			ID:             gid.New(s.cfg.PlatformTenant, coredata.FrameworkEntityType),
			ReferenceID:    doc.ID,
			Name:           doc.Name,
			ShortName:      doc.ShortName,
			Region:         doc.Region,
			Authority:      doc.Authority,
			License:        doc.License,
			Description:    doc.Description,
			SourceLanguage: doc.SourceLanguage,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := framework.Insert(ctx, tx, scope); err != nil {
			return fmt.Errorf("cannot create framework: %w", err)
		}

		versionID, err := s.createVersionTx(ctx, tx, framework.ID, actorID, doc, doc.Version, origin, now)
		if err != nil {
			return err
		}

		out = CreateFrameworkResult{FrameworkID: framework.ID, VersionID: versionID, Version: doc.Version}
		return s.recordAudit(ctx, tx, scope, actorID, auditAction, framework.ID.String(),
			fmt.Sprintf("%s@%s origin=%s", doc.ID, doc.Version, origin))
	})
	return out, err
}

// createVersionTx inserts a DRAFT framework_versions row (with its full v2
// structure) under an existing framework and resolves any stubs whose targets
// are already published. Shared by first-import and next-version creation.
func (s *Service) createVersionTx(ctx context.Context, tx pg.Tx, frameworkID, actorID gid.GID, doc *draftDocument, versionStr, origin string, now time.Time) (gid.GID, error) {
	scope := s.platformScope()

	version := coredata.FrameworkVersion{
		ID:          gid.New(s.cfg.PlatformTenant, coredata.FrameworkVersionEntityType),
		FrameworkID: frameworkID,
		Version:     versionStr,
		Status:      coredata.FrameworkVersionStatusDraft,
		Changelog:   doc.RevisionNotes,
		Quorum:      1,
		AuthorID:    actorID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := version.Insert(ctx, tx, scope); err != nil {
		return gid.Nil, fmt.Errorf("cannot create version: %w", err)
	}

	if err := s.insertStructureTx(ctx, tx, version.ID, doc, now, origin); err != nil {
		return gid.Nil, err
	}

	// Resolve any stubs whose targets are already published.
	if _, err := (coredata.RequirementCrossMappings{}).ResolveOwnStubs(ctx, tx, scope, version.ID); err != nil {
		return gid.Nil, err
	}

	return version.ID, nil
}

// insertStructureTx persists a document's structure and mapping stubs with the
// given origin marker on each row: categories, then requirements, then the
// cross-mapping stubs each requirement declares.
func (s *Service) insertStructureTx(ctx context.Context, tx pg.Tx, versionID gid.GID, doc *draftDocument, now time.Time, origin string) error {
	scope := s.platformScope()
	reqPos := 0

	for ci, cat := range doc.Categories {
		category := coredata.RequirementCategory{
			ID:                 gid.New(s.cfg.PlatformTenant, coredata.RequirementCategoryEntityType),
			FrameworkVersionID: versionID,
			Code:               cat.Code,
			Name:               cat.Name,
			Description:        cat.Description,
			IsOptional:         cat.IsOptional,
			ApplicabilityNote:  cat.ApplicabilityNote,
			Position:           ci,
			Origin:             origin,
			CreatedAt:          now,
		}
		if err := category.Insert(ctx, tx, scope); err != nil {
			return fmt.Errorf("category %q: %w", cat.Code, err)
		}

		for _, req := range cat.Requirements {
			requirement := coredata.Requirement{
				ID:                     gid.New(s.cfg.PlatformTenant, coredata.RequirementEntityType),
				FrameworkVersionID:     versionID,
				CategoryID:             category.ID,
				Code:                   req.Code,
				Number:                 req.Number,
				Title:                  req.Title,
				Description:            req.Description,
				ItemType:               req.ItemType,
				LegalCitation:          req.LegalCitation,
				ValidationApproaches:   req.ValidationApproaches,
				EffectiveFrom:          req.EffectiveFrom,
				RetiredAt:              req.RetiredAt,
				Guidance:               req.Guidance,
				Tags:                   req.Tags,
				ApplicabilityRoles:     req.ApplicabilityRoles,
				ApplicabilityCondition: req.ApplicabilityCondition,
				// Position is global across the version so document order
				// survives a category being edited later.
				Position:  reqPos,
				Origin:    origin,
				CreatedAt: now,
			}
			reqPos++
			if err := requirement.Insert(ctx, tx, scope); err != nil {
				return fmt.Errorf("requirement %q: %w", req.Code, err)
			}

			for _, m := range req.Mappings {
				mapping := coredata.RequirementCrossMapping{
					ID:                       gid.New(s.cfg.PlatformTenant, coredata.RequirementCrossMappingEntityType),
					SourceRequirementID:      requirement.ID,
					SourceFrameworkVersionID: versionID,
					Relation:                 string(m.Relation),
					TargetFrameworkCode:      m.Framework,
					TargetFrameworkVersion:   m.Version,
					TargetRequirementCode:    m.Item,
					Notes:                    m.Notes,
					CreatedAt:                now,
				}
				if err := mapping.Insert(ctx, tx, scope); err != nil {
					return fmt.Errorf("mapping %s→%s/%s: %w", req.Code, m.Framework, m.Item, err)
				}
			}
		}
	}
	return nil
}
