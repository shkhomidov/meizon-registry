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
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/iam"
)

// ErrOrgNotApproved is returned when a sync is attempted for an organization
// that is not approved (pending, suspended, or unknown). It is deliberately the
// same answer for all three, so the sync surface never reveals whether an org
// exists — only whether the caller may sync.
var ErrOrgNotApproved = errors.New("organization is not approved for sync")

// OrgView is the console-facing shape of an organization.
type OrgView struct {
	TenantID   string     `json:"tenantId"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	ApprovedBy string     `json:"approvedBy,omitempty"`
	ApprovedAt *time.Time `json:"approvedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
}

// RegisterOrganization creates a pending organization with a fresh tenant. It is
// the entry point for a consuming org to request access; approval is a separate,
// superadmin-gated step. Returns the org's tenant id, which becomes its sync
// scope once approved.
func (s *Service) RegisterOrganization(ctx context.Context, name, requestedBy string) (gid.TenantID, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return gid.NilTenant, fmt.Errorf("%w: organization name is required", ErrInvalidInput)
	}

	tenantID := gid.NewTenantID()
	now := time.Now()
	err := s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		return coredata.Organization{
			TenantID:    tenantID,
			Name:        name,
			Status:      coredata.OrganizationStatusPending,
			RequestedBy: requestedBy,
			CreatedAt:   now,
			UpdatedAt:   now,
		}.Insert(ctx, tx)
	})
	if err != nil {
		return gid.NilTenant, err
	}
	return tenantID, nil
}

// ApproveOrganization moves an org to approved (superadmin only). Its effect is
// immediate: the org's next sync request re-reads this status and passes.
func (s *Service) ApproveOrganization(ctx context.Context, actorID gid.GID, tenantID gid.TenantID) error {
	return s.setOrgStatus(ctx, actorID, tenantID, coredata.OrganizationStatusApproved, "organization.approve")
}

// SuspendOrganization revokes an approved org's sync access without deleting it
// (superadmin only). Immediate, since approval is checked per request.
func (s *Service) SuspendOrganization(ctx context.Context, actorID gid.GID, tenantID gid.TenantID) error {
	return s.setOrgStatus(ctx, actorID, tenantID, coredata.OrganizationStatusSuspended, "organization.suspend")
}

func (s *Service) setOrgStatus(ctx context.Context, actorID gid.GID, tenantID gid.TenantID, status, auditAction string) error {
	return s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		// Org governance is a superadmin-only action, the same authority that
		// manages users and keys.
		if err := s.authorize(ctx, tx, actorID, iam.ActionUserManage, "", gid.Nil); err != nil {
			return err
		}

		var org coredata.Organization
		if err := org.LoadByTenant(ctx, tx, tenantID); err != nil {
			return err
		}

		now := time.Now()
		approvedBy, approvedAt := org.ApprovedBy, org.ApprovedAt
		if status == coredata.OrganizationStatusApproved {
			approvedBy = actorID.String()
			approvedAt = &now
		}
		if err := org.UpdateStatus(ctx, tx, tenantID, status, approvedBy, approvedAt, now); err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, s.platformScope(), actorID, auditAction, tenantID.String(), org.Name)
	})
}

// ListOrganizations returns the review queue (superadmin only). An empty status
// returns all.
func (s *Service) ListOrganizations(ctx context.Context, actorID gid.GID, status string) ([]OrgView, error) {
	out := []OrgView{}
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		if err := s.authorize(ctx, conn, actorID, iam.ActionUserManage, "", gid.Nil); err != nil {
			return err
		}
		var orgs coredata.Organizations
		if err := orgs.LoadByStatus(ctx, conn, status); err != nil {
			return err
		}
		for _, o := range orgs {
			out = append(out, OrgView{
				TenantID: o.TenantID.String(), Name: o.Name, Status: o.Status,
				ApprovedBy: o.ApprovedBy, ApprovedAt: o.ApprovedAt, CreatedAt: o.CreatedAt,
			})
		}
		return nil
	})
	return out, err
}

// SyncContextForOrg is the keyless-sync security boundary: it maps an
// organization's tenant to a distribution TokenContext, but ONLY if the org is
// approved. It fails closed — a pending, suspended or unknown org gets
// ErrOrgNotApproved, never a context.
//
// The scope is the whole point: OwnerTenant is the org's own tenant, which owns
// none of the registry's frameworks, so the existing copyright gate
// (public = TRUE OR tenant_id = owner) admits exactly the published PUBLIC
// frameworks. Regions is nil (all regions). No new distribution logic is
// needed — this is the only thing keyless sync adds.
func (s *Service) SyncContextForOrg(ctx context.Context, tenantID gid.TenantID) (TokenContext, error) {
	var tc TokenContext
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		var org coredata.Organization
		if err := org.LoadByTenant(ctx, conn, tenantID); err != nil {
			if errors.Is(err, coredata.ErrResourceNotFound) {
				return ErrOrgNotApproved
			}
			return err
		}
		if org.Status != coredata.OrganizationStatusApproved {
			return ErrOrgNotApproved
		}
		tc = TokenContext{OwnerTenant: tenantID, Regions: nil}
		return nil
	})
	return tc, err
}
