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

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/fwflat"
	"go.meizon.cloud/registry/pkg/gid"
)

// ExportFlat renders a stored framework as a flat meizon-framework/v2 document —
// the format the generator produces and the one meant for sharing and re-import.
//
// Storage is Category → Requirement → Control, which is the same shape, so this
// is a direct read: requirements come from the structure tree and controls from
// the framework's control library, linked by requirement code.
func (s *Service) ExportFlat(ctx context.Context, ref string) (*fwflat.Framework, error) {
	var out *fwflat.Framework

	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		scope := s.platformScope()

		var framework coredata.Framework
		if err := framework.LoadByReferenceID(ctx, conn, scope, ref); err != nil {
			return err
		}

		versionID, err := s.latestVersionIDConn(ctx, conn, framework.ID)
		if err != nil {
			return err
		}
		version, err := s.versionString(ctx, conn, versionID)
		if err != nil {
			return err
		}

		tree, err := s.structureOfTx(ctx, conn, versionID)
		if err != nil {
			return err
		}

		doc := &fwflat.Framework{
			Schema:   fwflat.SchemaMarker,
			ID:       framework.ReferenceID,
			Name:     framework.Name,
			Version:  version,
			Category: framework.Description, // the flat "category" is stored here on import
			Regions:  []string{framework.Region},
		}

		// Requirement refs, in document order, with their category.
		for _, c := range tree {
			doc.Categories = append(doc.Categories, fwflat.Category{Ref: c.Code, Name: c.Name})
			for _, r := range c.Requirements {
				doc.Requirements = append(doc.Requirements, fwflat.Requirement{
					Ref:         r.Code,
					Category:    c.Code,
					Name:        r.Title,
					Description: r.Description,
				})
			}
		}

		// Controls, and the requirements each is linked to.
		controls, err := s.controlsForExport(ctx, conn, framework)
		if err != nil {
			return err
		}
		byReq := map[string][]string{}
		for _, c := range controls {
			doc.Controls = append(doc.Controls, fwflat.Control{
				Ref: c.Code, Name: c.Name, Description: c.Description, Category: c.Domain,
			})
			for _, reqCode := range c.Items {
				byReq[reqCode] = append(byReq[reqCode], c.Code)
			}
		}
		for i := range doc.Requirements {
			doc.Requirements[i].Controls = byReq[doc.Requirements[i].Ref]
		}

		doc.Normalize()
		out = doc
		return nil
	})
	return out, err
}

// controlsForExport loads the framework's control library with its requirement links.
func (s *Service) controlsForExport(ctx context.Context, conn pg.Querier, framework coredata.Framework) ([]ControlEntryView, error) {
	_, codeByID, err := s.latestRequirementsByCode(ctx, conn, framework.ID)
	if err != nil {
		return nil, err
	}

	var entries coredata.ControlLibraryEntries
	if err := entries.LoadAllByFramework(ctx, conn, s.platformScope(), framework.ID); err != nil {
		return nil, err
	}

	if len(entries) == 0 {
		return nil, nil
	}

	ids := make([]gid.GID, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	var links coredata.ControlRequirementLinks
	if err := links.LoadAllByControls(ctx, conn, s.platformScope(), ids); err != nil {
		return nil, err
	}
	itemsByControl := map[gid.GID][]string{}
	for _, l := range links {
		if code, ok := codeByID[l.RequirementID]; ok {
			itemsByControl[l.ControlID] = append(itemsByControl[l.ControlID], code)
		}
	}

	out := make([]ControlEntryView, 0, len(entries))
	for _, e := range entries {
		out = append(out, ControlEntryView{
			ID: e.ID, Code: e.Code, Name: e.Name,
			Description: e.Description, Domain: e.Domain, Origin: e.Origin,
			Items: itemsByControl[e.ID],
		})
	}
	return out, nil
}

// latestVersionIDConn is LatestVersionID on an existing connection.
func (s *Service) latestVersionIDConn(ctx context.Context, conn pg.Querier, frameworkID gid.GID) (gid.GID, error) {
	var versions coredata.FrameworkVersions
	if err := versions.LoadAllByFramework(ctx, conn, s.platformScope(), frameworkID); err != nil {
		return gid.Nil, err
	}
	if len(versions) == 0 {
		return gid.Nil, coredata.ErrResourceNotFound
	}
	return versions[0].ID, nil
}

func (s *Service) versionString(ctx context.Context, conn pg.Querier, versionID gid.GID) (string, error) {
	var v coredata.FrameworkVersion
	if err := v.LoadByID(ctx, conn, s.platformScope(), versionID); err != nil {
		return "", err
	}
	return v.Version, nil
}
