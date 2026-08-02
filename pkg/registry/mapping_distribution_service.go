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
	"time"

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
)

// MappingCatalogEntry is one row of the mapping-set catalog: a published set a
// consumer may fetch, with enough to decide whether it wants it.
type MappingCatalogEntry struct {
	Source          string    `json:"source"`
	SourceVersion   string    `json:"sourceVersion"`
	Target          string    `json:"target"`
	TargetVersion   string    `json:"targetVersion"`
	RequirementMaps int       `json:"requirementMappings"`
	ControlMaps     int       `json:"controlMappings"`
	ContentHash     string    `json:"contentHash"`
	PublishedAt     time.Time `json:"publishedAt"`
}

// MappingCatalog lists the published mapping sets a token may see. Visibility
// follows the SOURCE framework — the framework the set is authored on — so a set
// is offered on exactly the same terms as that framework: gated by the token's
// regions and the copyright rule. This is what a consumer walks on a cold start,
// the mapping equivalent of the framework catalog.
func (s *Service) MappingCatalog(ctx context.Context, tc TokenContext) ([]MappingCatalogEntry, error) {
	out := []MappingCatalogEntry{}
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		// Reuse the framework catalog's visibility filter to get the set of
		// source frameworks this token may see, then list their published sets.
		var frameworks coredata.Frameworks
		if err := frameworks.LoadCatalog(ctx, conn, tc.OwnerTenant, tc.Regions); err != nil {
			return err
		}

		for _, f := range frameworks {
			var latest coredata.FrameworkVersion
			if err := latest.LoadLatestPublished(ctx, conn, f.ID); err != nil {
				// A framework with no published version can still carry mapping
				// sets on its latest (draft) version — sets publish independently.
				// Load the full version row (not just the id) so SourceVersion is
				// populated on this path too.
				vid, verr := s.latestVersionIDConn(ctx, conn, f.ID)
				if verr != nil {
					continue
				}
				if verr := latest.LoadByID(ctx, conn, coredata.NewScopeFromObjectID(f.ID), vid); verr != nil {
					continue
				}
			}

			var sets coredata.MappingSets
			if err := sets.LoadPublishedBySource(ctx, conn, coredata.NewScopeFromObjectID(f.ID), latest.ID); err != nil {
				return err
			}
			for _, ms := range sets {
				out = append(out, MappingCatalogEntry{
					Source: f.ReferenceID, SourceVersion: latest.Version,
					Target: ms.TargetFrameworkCode, TargetVersion: ms.TargetFrameworkVersion,
					RequirementMaps: ms.RequirementMappingCount, ControlMaps: ms.ControlMappingCount,
					ContentHash: ms.ContentHash, PublishedAt: derefTime(ms.PublishedAt),
				})
			}
		}
		return nil
	})
	return out, err
}

// MappingSetDocument returns the exact signed JSON of a published mapping set,
// gated by the source framework's visibility. The bytes are served verbatim from
// storage — never re-derived — so what the consumer verifies is exactly what was
// signed.
func (s *Service) MappingSetDocument(ctx context.Context, tc TokenContext, source, sourceVersion, target, targetVersion string) ([]byte, error) {
	var doc []byte
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		var framework coredata.Framework
		if err := framework.LoadByReferenceID(ctx, conn, coredata.NewNoScope(), source); err != nil {
			return err
		}
		// Same gate as a framework bundle: proprietary sets only for the owning
		// tenant, and the token's region must include the source framework's.
		if !framework.Public && framework.ID.TenantID() != tc.OwnerTenant {
			return ErrNotDistributable
		}
		if len(tc.Regions) > 0 && !contains(tc.Regions, framework.Region) {
			return ErrNotDistributable
		}

		scope := coredata.NewScopeFromObjectID(framework.ID)
		var version coredata.FrameworkVersion
		if sourceVersion == "" || sourceVersion == "latest" {
			vid, err := s.latestVersionIDConn(ctx, conn, framework.ID)
			if err != nil {
				return err
			}
			if err := version.LoadByID(ctx, conn, scope, vid); err != nil {
				return err
			}
		} else {
			if err := version.LoadByFrameworkAndVersion(ctx, conn, scope, framework.ID, sourceVersion); err != nil {
				return err
			}
		}

		var set coredata.MappingSet
		if err := set.LoadByPair(ctx, conn, scope, version.ID, target, targetVersion); err != nil {
			return err
		}
		if set.Status != coredata.MappingSetStatusPublished {
			return ErrNotDistributable
		}
		doc = set.Document
		return nil
	})
	return doc, err
}
