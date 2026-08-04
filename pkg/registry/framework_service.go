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

// CreateFrameworkRequest describes a new framework and its initial draft.
type CreateFrameworkRequest struct {
	ReferenceID string
	Name        string
	ShortName   string
	Region      string
	Authority   string
	License     string
	Description string
}

// Validate checks the request.
func (r CreateFrameworkRequest) Validate() error {
	v := validator.New()
	v.Check(r.ReferenceID, "reference_id", validator.Required(), validator.MaxLen(128), validator.NoNewLine())
	v.Check(r.Name, "name", validator.Required(), validator.MaxLen(512), validator.NoNewLine())
	v.Check(r.Region, "region", validator.Required(), validator.MaxLen(32), validator.NoNewLine())
	v.Check(r.License, "license", validator.Required(), validator.OneOf(
		string(fwschema.LicensePublicDomain), string(fwschema.LicenseStatutory), string(fwschema.LicenseProprietary)))
	return v.Error()
}

// CreateFrameworkResult identifies a newly created framework and its draft.
type CreateFrameworkResult struct {
	FrameworkID gid.GID
	VersionID   gid.GID
	Version     string
}

const initialVersion = "1.0.0"

// CreateFramework registers a framework and opens its first DRAFT version.
func (s *Service) CreateFramework(ctx context.Context, actorID gid.GID, req CreateFrameworkRequest) (CreateFrameworkResult, error) {
	if err := req.Validate(); err != nil {
		return CreateFrameworkResult{}, err
	}

	var out CreateFrameworkResult
	err := s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		if err := s.authorize(ctx, tx, actorID, iam.ActionFrameworkCreate, req.Region, gid.Nil); err != nil {
			return err
		}

		now := time.Now()
		scope := s.platformScope()

		framework := coredata.Framework{
			ID:          gid.New(s.cfg.PlatformTenant, coredata.FrameworkEntityType),
			ReferenceID: strings.TrimSpace(req.ReferenceID),
			Name:        strings.TrimSpace(req.Name),
			ShortName:   strings.TrimSpace(req.ShortName),
			Region:      strings.TrimSpace(req.Region),
			Authority:   strings.TrimSpace(req.Authority),
			License:     req.License,
			Description: req.Description,
			Public:      false,
			CreatedBy:   actorID,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := framework.Insert(ctx, tx, scope); err != nil {
			return fmt.Errorf("cannot create framework: %w", err)
		}

		version := coredata.FrameworkVersion{
			ID:          gid.New(s.cfg.PlatformTenant, coredata.FrameworkVersionEntityType),
			FrameworkID: framework.ID,
			Version:     initialVersion,
			Status:      coredata.FrameworkVersionStatusDraft,
			Quorum:      1,
			AuthorID:    actorID,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := version.Insert(ctx, tx, scope); err != nil {
			return fmt.Errorf("cannot create framework version: %w", err)
		}

		out = CreateFrameworkResult{FrameworkID: framework.ID, VersionID: version.ID, Version: version.Version}
		return s.recordAudit(ctx, tx, scope, actorID, "framework.create", framework.ID.String(), framework.ReferenceID)
	})
	return out, err
}

// DeleteFramework permanently removes a framework and, by cascade, every version
// under it and their structure, controls, cross-mappings and audit templates. It
// is gated by ActionFrameworkDelete — held only by superadmins. This is
// destructive and irreversible; retiring a published version for consumers is
// what deprecation is for. Deleting a published framework also removes it from
// the distribution catalog.
func (s *Service) DeleteFramework(ctx context.Context, actorID gid.GID, ref string) error {
	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		scope := s.platformScope()

		var framework coredata.Framework
		if err := framework.LoadByReferenceID(ctx, tx, scope, ref); err != nil {
			return err
		}
		if err := s.authorize(ctx, tx, actorID, iam.ActionFrameworkDelete, framework.Region, framework.ID); err != nil {
			return err
		}
		if err := s.recordAudit(ctx, tx, scope, actorID, "framework.delete", framework.ID.String(),
			fmt.Sprintf("%s deleted", framework.ReferenceID)); err != nil {
			return err
		}
		return coredata.Framework{}.Delete(ctx, tx, scope, framework.ID)
	})
}

// AddControlRequest describes a control to add to a draft version.
type AddControlRequest struct {
	VersionID   gid.GID
	RefID       string
	Name        string
	Description string
	Section     string
	ParentRefID string
	Guidance    string
	References  []string
	Mappings    []coredata.Mapping
}

// AddControl appends a control to a DRAFT version.
func (s *Service) AddControl(ctx context.Context, actorID gid.GID, req AddControlRequest) (gid.GID, error) {
	v := validator.New()
	v.Check(req.RefID, "ref_id", validator.Required(), validator.MaxLen(128), validator.NoNewLine())
	v.Check(req.Name, "name", validator.Required(), validator.MaxLen(512), validator.NoNewLine())
	if err := v.Error(); err != nil {
		return gid.Nil, err
	}

	var id gid.GID
	err := s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		scope := s.platformScope()

		version, framework, err := s.loadVersionAndFramework(ctx, tx, scope, req.VersionID)
		if err != nil {
			return err
		}

		if err := s.authorize(ctx, tx, actorID, iam.ActionControlCreate, framework.Region, framework.ID); err != nil {
			return err
		}

		if version.Status != coredata.FrameworkVersionStatusDraft {
			return fmt.Errorf("%w: controls can only be added to a DRAFT version (current: %s)", ErrInvalidTransition, version.Status)
		}

		count, err := (&coredata.Controls{}).CountByVersion(ctx, tx, scope, version.ID)
		if err != nil {
			return err
		}

		var parent *string
		if strings.TrimSpace(req.ParentRefID) != "" {
			p := strings.TrimSpace(req.ParentRefID)
			parent = &p
		}

		now := time.Now()
		control := coredata.Control{
			ID:                 gid.New(s.cfg.PlatformTenant, coredata.ControlEntityType),
			FrameworkVersionID: version.ID,
			RefID:              strings.TrimSpace(req.RefID),
			Name:               strings.TrimSpace(req.Name),
			Description:        req.Description,
			Section:            req.Section,
			ParentRefID:        parent,
			Guidance:           req.Guidance,
			Refs:               req.References,
			Mappings:           req.Mappings,
			Position:           count,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		if err := control.Insert(ctx, tx, scope); err != nil {
			return fmt.Errorf("cannot add control: %w", err)
		}

		id = control.ID
		return nil
	})
	return id, err
}

func (s *Service) loadVersionAndFramework(ctx context.Context, conn pg.Querier, scope coredata.Scoper, versionID gid.GID) (coredata.FrameworkVersion, coredata.Framework, error) {
	var version coredata.FrameworkVersion
	if err := version.LoadByID(ctx, conn, scope, versionID); err != nil {
		return version, coredata.Framework{}, fmt.Errorf("cannot load version: %w", err)
	}

	var framework coredata.Framework
	if err := framework.LoadByID(ctx, conn, scope, version.FrameworkID); err != nil {
		return version, framework, fmt.Errorf("cannot load framework: %w", err)
	}

	return version, framework, nil
}
