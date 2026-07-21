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

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/fwschema"
	"go.meizon.cloud/registry/pkg/gid"
)

// ListFrameworks returns every framework in the platform tenant, for the console
// and CLI.
func (s *Service) ListFrameworks(ctx context.Context) (coredata.Frameworks, error) {
	var frameworks coredata.Frameworks
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		return frameworks.LoadAll(ctx, conn, s.platformScope())
	})
	return frameworks, err
}

// FrameworkByReference resolves a framework by its reference id.
func (s *Service) FrameworkByReference(ctx context.Context, referenceID string) (coredata.Framework, error) {
	var framework coredata.Framework
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		return framework.LoadByReferenceID(ctx, conn, s.platformScope(), referenceID)
	})
	return framework, err
}

// VersionsOf returns all versions of a framework, newest first.
func (s *Service) VersionsOf(ctx context.Context, frameworkID gid.GID) (coredata.FrameworkVersions, error) {
	var versions coredata.FrameworkVersions
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		return versions.LoadAllByFramework(ctx, conn, s.platformScope(), frameworkID)
	})
	return versions, err
}

// LatestVersionID returns the id of a framework's newest version (for CLI
// convenience when authoring).
func (s *Service) LatestVersionID(ctx context.Context, frameworkID gid.GID) (gid.GID, error) {
	versions, err := s.VersionsOf(ctx, frameworkID)
	if err != nil {
		return gid.Nil, err
	}
	if len(versions) == 0 {
		return gid.Nil, coredata.ErrResourceNotFound
	}
	return versions[0].ID, nil
}

// ExportBundle builds the signed rich-schema bundle for a version, for CLI
// "bundle export". A published version carries its stored signature.
func (s *Service) ExportBundle(ctx context.Context, versionID gid.GID) (*fwschema.Framework, error) {
	var bundle *fwschema.Framework
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		scope := s.platformScope()
		version, framework, err := s.loadVersionAndFramework(ctx, conn, scope, versionID)
		if err != nil {
			return err
		}

		b, err := s.assembleBundle(ctx, conn, scope, framework, version)
		if err != nil {
			return err
		}

		if len(version.Signature) > 0 {
			var sig fwschema.Signature
			if err := jsonUnmarshal(version.Signature, &sig); err != nil {
				return fmt.Errorf("cannot decode stored signature: %w", err)
			}
			b.Signature = &sig
		}

		bundle = b
		return nil
	})
	return bundle, err
}

// ExportSeed builds the flat GRC seed for a version, for CLI "seed export".
func (s *Service) ExportSeed(ctx context.Context, versionID gid.GID) (fwschema.GRCSeed, error) {
	bundle, err := s.ExportBundle(ctx, versionID)
	if err != nil {
		return fwschema.GRCSeed{}, err
	}
	return bundle.Flatten(), nil
}

// ViewerInfo is the console's view of the signed-in identity.
type ViewerInfo struct {
	ID       gid.GID  `json:"id"`
	Email    string   `json:"email"`
	FullName string   `json:"fullName"`
	Role     string   `json:"role"`
	Regions  []string `json:"regions"`
}

// Viewer returns the identity and role/region scope for a signed-in user. An
// identity with no membership yet has an empty role (no access).
func (s *Service) Viewer(ctx context.Context, identityID gid.GID) (ViewerInfo, error) {
	var out ViewerInfo
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		var identity coredata.Identity
		if err := identity.LoadByID(ctx, conn, s.platformScope(), identityID); err != nil {
			return err
		}

		out = ViewerInfo{ID: identity.ID, Email: identity.Email, FullName: identity.FullName}

		var membership coredata.Membership
		if err := membership.LoadByIdentityID(ctx, conn, s.platformScope(), identityID); err == nil {
			out.Role = membership.Role
			out.Regions = membership.RegionList()
		}
		return nil
	})
	return out, err
}

// ControlsOf returns the controls of a version in authored order (console/CLI).
func (s *Service) ControlsOf(ctx context.Context, versionID gid.GID) (coredata.Controls, error) {
	var controls coredata.Controls
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		return controls.LoadAllByVersion(ctx, conn, s.platformScope(), versionID)
	})
	return controls, err
}

// RecentAudit returns recent audit-log entries for the platform tenant.
func (s *Service) RecentAudit(ctx context.Context, limit int) (coredata.AuditLogEntries, error) {
	var entries coredata.AuditLogEntries
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		return entries.LoadRecent(ctx, conn, s.platformScope(), limit)
	})
	return entries, err
}
