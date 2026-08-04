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
	"errors"
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

// AddItemMappingRequest describes a new cross-mapping on a draft requirement.
// The console still speaks of "items" on the wire; a requirement is the leaf
// those codes now address.
type AddItemMappingRequest struct {
	VersionID       gid.GID
	ItemCode        string
	Relation        string
	TargetFramework string
	TargetVersion   string
	TargetItem      string
	Notes           string
}

// AddItemMapping records a relation-typed cross-mapping (stub by code) on a
// draft requirement, then opportunistically resolves it if the target is
// already published.
func (s *Service) AddItemMapping(ctx context.Context, actorID gid.GID, req AddItemMappingRequest) (gid.GID, error) {
	v := validator.New()
	v.Check(req.ItemCode, "item", validator.Required())
	v.Check(req.TargetFramework, "framework", validator.Required(), validator.MaxLen(128), validator.NoNewLine())
	v.Check(req.TargetItem, "targetItem", validator.Required(), validator.MaxLen(128), validator.NoNewLine())
	if err := v.Error(); err != nil {
		return gid.Nil, err
	}
	if !fwschema.MappingRelation(req.Relation).IsValid() {
		return gid.Nil, fmt.Errorf("unknown relation %q (equivalent|partial|superset|subset)", req.Relation)
	}

	var id gid.GID
	err := s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		version, _, err := s.requireMappable(ctx, tx, actorID, req.VersionID, iam.ActionControlEdit)
		if err != nil {
			return err
		}
		scope := s.platformScope()

		var requirement coredata.Requirement
		if err := requirement.LoadByVersionAndCode(ctx, tx, scope, version.ID, req.ItemCode); err != nil {
			return fmt.Errorf("requirement %q: %w", req.ItemCode, err)
		}

		mapping := coredata.RequirementCrossMapping{
			ID:                       gid.New(s.cfg.PlatformTenant, coredata.RequirementCrossMappingEntityType),
			SourceRequirementID:      requirement.ID,
			SourceFrameworkVersionID: version.ID,
			Relation:                 req.Relation,
			TargetFrameworkCode:      strings.TrimSpace(req.TargetFramework),
			TargetFrameworkVersion:   strings.TrimSpace(req.TargetVersion),
			TargetRequirementCode:    strings.TrimSpace(req.TargetItem),
			Notes:                    req.Notes,
			CreatedAt:                time.Now(),
		}
		if err := mapping.Insert(ctx, tx, scope); err != nil {
			return err
		}
		id = mapping.ID

		// Resolve immediately when the target is already published.
		if _, err := (coredata.RequirementCrossMappings{}).ResolveOwnStubs(ctx, tx, scope, version.ID); err != nil {
			return err
		}
		return nil
	})
	return id, err
}

// RemoveItemMapping deletes a mapping from a draft version.
func (s *Service) RemoveItemMapping(ctx context.Context, actorID, versionID, mappingID gid.GID) error {
	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		if _, _, err := s.requireMappable(ctx, tx, actorID, versionID, iam.ActionControlEdit); err != nil {
			return err
		}
		return coredata.RequirementCrossMapping{}.Delete(ctx, tx, s.platformScope(), mappingID)
	})
}

// resolveAfterPublish runs both resolution directions for a just-published
// version, inside the publish transaction: the version's own stubs, and every
// other framework's stubs pointing at this framework's code.
func (s *Service) resolveAfterPublish(ctx context.Context, tx pg.Tx, framework coredata.Framework, version coredata.FrameworkVersion) (int, error) {
	scope := s.platformScope()

	own, err := (coredata.RequirementCrossMappings{}).ResolveOwnStubs(ctx, tx, scope, version.ID)
	if err != nil {
		return 0, err
	}
	targeting, err := (coredata.RequirementCrossMappings{}).ResolveStubsTargeting(ctx, tx, scope, framework.ReferenceID, version.Version, version.ID)
	if err != nil {
		return own, err
	}
	if _, err := (coredata.ControlCrossMappings{}).ResolveOwnStubs(ctx, tx, scope, version.ID); err != nil {
		return own + targeting, err
	}

	// Publishing a new version can silently invalidate mappings that point at
	// this framework: the refs still match, so they stay "resolved", but the
	// obligation behind a ref may have changed or vanished. Flag them.
	if err := s.flagMappingsAfterPublish(ctx, tx, framework, version); err != nil {
		return own + targeting, err
	}
	return own + targeting, nil
}

// flagMappingsAfterPublish compares the newly published version against the
// previous one and marks every mapping pointing at a changed or removed
// requirement for re-review.
//
// This is the cheap half of "sync updated parts": unchanged requirements carry
// their mappings forward with NO LLM call at all, so a version bump costs
// nothing for the (usually large) majority that did not move. Only what
// actually changed is surfaced for attention.
//
// Nothing is auto-deleted. A mapping whose target disappeared becomes
// `orphaned` for an auditor to judge — deleting it would quietly shrink a
// coverage claim that someone may already have relied on.
func (s *Service) flagMappingsAfterPublish(ctx context.Context, tx pg.Tx, framework coredata.Framework, version coredata.FrameworkVersion) error {
	scope := s.platformScope()

	var versions coredata.FrameworkVersions
	if err := versions.LoadAllByFramework(ctx, tx, scope, framework.ID); err != nil {
		return err
	}
	// versions[0] is the newest; the baseline is the one published before it.
	var previousID gid.GID
	for _, v := range versions {
		if v.ID != version.ID && v.Status == coredata.FrameworkVersionStatusPublished {
			previousID = v.ID
			break
		}
	}
	if previousID == gid.Nil {
		return nil // first publication — nothing to compare against
	}

	current, err := s.requirementTextByCode(ctx, tx, version.ID)
	if err != nil {
		return err
	}
	previous, err := s.requirementTextByCode(ctx, tx, previousID)
	if err != nil {
		return err
	}

	var changed, removed []string
	for code, before := range previous {
		now, still := current[code]
		switch {
		case !still:
			removed = append(removed, code)
		case now != before:
			changed = append(changed, code)
		}
	}

	ms := coredata.RequirementCrossMappings{}
	if err := ms.MarkReviewState(ctx, tx, scope, framework.ReferenceID, changed, coredata.MappingReviewNeedsReview); err != nil {
		return err
	}
	return ms.MarkReviewState(ctx, tx, scope, framework.ReferenceID, removed, coredata.MappingReviewOrphaned)
}

// requirementTextByCode returns each requirement's comparable text, so a diff
// detects a reworded obligation and not merely a renumbering.
func (s *Service) requirementTextByCode(ctx context.Context, tx pg.Tx, versionID gid.GID) (map[string]string, error) {
	var requirements coredata.Requirements
	if err := requirements.LoadAllByVersion(ctx, tx, s.platformScope(), versionID); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(requirements))
	for _, r := range requirements {
		out[r.Code] = r.Title + "\x00" + r.Description
	}
	return out, nil
}

// ResolveAllStubs re-runs stub resolution across every published framework.
// Superadmin remediation action (normally resolution happens at publish).
func (s *Service) ResolveAllStubs(ctx context.Context, actorID gid.GID) (int, error) {
	total := 0
	err := s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		if err := s.authorize(ctx, tx, actorID, iam.ActionMappingResolve, "", gid.Nil); err != nil {
			return err
		}
		scope := s.platformScope()

		summary, err := (coredata.RequirementCrossMappings{}).UnresolvedSummary(ctx, tx, scope)
		if err != nil {
			return err
		}

		for _, row := range summary {
			var framework coredata.Framework
			if err := framework.LoadByReferenceID(ctx, tx, scope, row.TargetFrameworkCode); err != nil {
				if errors.Is(err, coredata.ErrResourceNotFound) {
					continue // target framework not loaded yet — stays a stub
				}
				return err
			}
			var latest coredata.FrameworkVersion
			if err := latest.LoadLatestPublished(ctx, tx, framework.ID); err != nil {
				if errors.Is(err, coredata.ErrResourceNotFound) {
					continue // not published yet
				}
				return err
			}
			n, err := (coredata.RequirementCrossMappings{}).ResolveStubsTargeting(ctx, tx, scope, framework.ReferenceID, latest.Version, latest.ID)
			if err != nil {
				return err
			}
			total += n
		}

		return s.recordAudit(ctx, tx, scope, actorID, "mapping.resolve_all", "", fmt.Sprintf("resolved=%d", total))
	})
	return total, err
}

// CoverageReport summarizes how a source framework maps onto others.
// CoverageReport carries BOTH levels: requirements and controls are mapped
// separately and a report showing only one silently understates coverage —
// which is how 18 saved control mappings can look like zero.
type CoverageReport struct {
	SourceFramework string                 `json:"sourceFramework"`
	SourceVersion   string                 `json:"sourceVersion"`
	TargetFramework string                 `json:"targetFramework,omitempty"`
	TotalItems      int                    `json:"totalItems"`
	Rows            []coredata.CoverageRow `json:"rows"`
	// Controls are mapped separately from requirements; reporting only one
	// silently understates coverage.
	ControlRows   []coredata.CoverageRow `json:"controlRows"`
	TotalControls int                    `json:"totalControls"`
}

// CoverageFor reports per-relation mapping counts for the latest version of a
// source framework, optionally filtered to one target framework code.
func (s *Service) CoverageFor(ctx context.Context, sourceRef, targetCode string) (CoverageReport, error) {
	var report CoverageReport
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		scope := s.platformScope()

		var framework coredata.Framework
		if err := framework.LoadByReferenceID(ctx, conn, scope, sourceRef); err != nil {
			return err
		}
		var versions coredata.FrameworkVersions
		if err := versions.LoadAllByFramework(ctx, conn, scope, framework.ID); err != nil {
			return err
		}
		if len(versions) == 0 {
			return coredata.ErrResourceNotFound
		}
		latest := versions[0]

		rows, err := (coredata.RequirementCrossMappings{}).Coverage(ctx, conn, scope, latest.ID, targetCode)
		if err != nil {
			return err
		}
		controlRows, err := (coredata.ControlCrossMappings{}).Coverage(ctx, conn, scope, latest.ID, targetCode)
		if err != nil {
			return err
		}
		var library coredata.ControlLibraryEntries
		if err := library.LoadAllByFramework(ctx, conn, scope, framework.ID); err != nil {
			return err
		}
		total, err := (&coredata.Requirements{}).CountByVersion(ctx, conn, scope, latest.ID)
		if err != nil {
			return err
		}

		report = CoverageReport{
			SourceFramework: framework.ReferenceID,
			SourceVersion:   latest.Version,
			TargetFramework: targetCode,
			TotalItems:      total,
			Rows:            rows,
			ControlRows:     controlRows,
			TotalControls:   len(library),
		}
		if report.Rows == nil {
			report.Rows = []coredata.CoverageRow{}
		}
		return nil
	})
	return report, err
}

// UnresolvedStubSummary lists unresolved stub groups for the admin view.
func (s *Service) UnresolvedStubSummary(ctx context.Context) ([]coredata.UnresolvedSummaryRow, error) {
	var out []coredata.UnresolvedSummaryRow
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		rows, err := (coredata.RequirementCrossMappings{}).UnresolvedSummary(ctx, conn, s.platformScope())
		out = rows
		return err
	})
	if out == nil {
		out = []coredata.UnresolvedSummaryRow{}
	}
	return out, err
}
