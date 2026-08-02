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

package coredata

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/gid"
)

// Organization statuses. Only `approved` grants sync access; the check is made
// per request, so `suspended` takes effect immediately.
const (
	OrganizationStatusPending   = "pending"
	OrganizationStatusApproved  = "approved"
	OrganizationStatusSuspended = "suspended"
)

// Organization is a consuming tenant gated by superadmin approval. It carries
// only the approval state — the org's users are ordinary identities under its
// tenant. There is intentionally no bearer token: an approved org's cloud
// session is its credential.
type Organization struct {
	TenantID    gid.TenantID `db:"tenant_id"`
	Name        string       `db:"name"`
	Status      string       `db:"status"`
	RequestedBy string       `db:"requested_by"`
	ApprovedBy  string       `db:"approved_by"`
	ApprovedAt  *time.Time   `db:"approved_at"`
	CreatedAt   time.Time    `db:"created_at"`
	UpdatedAt   time.Time    `db:"updated_at"`
}

type Organizations []*Organization

const organizationColumns = `tenant_id, name, status, requested_by, approved_by, approved_at, created_at, updated_at`

func (o Organization) Insert(ctx context.Context, conn pg.Tx) error {
	q := `
INSERT INTO organizations (tenant_id, name, status, requested_by, approved_by, approved_at, created_at, updated_at)
VALUES (@tenant_id, @name, @status, @requested_by, @approved_by, @approved_at, @created_at, @updated_at);`

	args := pgx.StrictNamedArgs{
		"tenant_id":    o.TenantID,
		"name":         o.Name,
		"status":       o.Status,
		"requested_by": o.RequestedBy,
		"approved_by":  o.ApprovedBy,
		"approved_at":  o.ApprovedAt,
		"created_at":   o.CreatedAt,
		"updated_at":   o.UpdatedAt,
	}
	if _, err := conn.Exec(ctx, q, args); err != nil {
		if isUniqueViolation(err, "organizations_pkey") {
			return ErrResourceAlreadyExists
		}
		return err
	}
	return nil
}

// LoadByTenant loads the org for a tenant. This is the hot path — it runs in the
// sync bridge on every keyless request — so it is a single primary-key lookup.
func (o *Organization) LoadByTenant(ctx context.Context, conn pg.Querier, tenantID gid.TenantID) error {
	q := fmt.Sprintf(`SELECT %s FROM organizations WHERE tenant_id = @tenant_id LIMIT 1;`, organizationColumns)
	rows, err := conn.Query(ctx, q, pgx.StrictNamedArgs{"tenant_id": tenantID})
	if err != nil {
		return fmt.Errorf("cannot query organization: %w", err)
	}
	org, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Organization])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrResourceNotFound
		}
		return fmt.Errorf("cannot collect organization: %w", err)
	}
	*o = org
	return nil
}

// UpdateStatus flips an org's status and records the approver/time on approval.
func (o Organization) UpdateStatus(ctx context.Context, conn pg.Tx, tenantID gid.TenantID, status, approvedBy string, approvedAt *time.Time, updatedAt time.Time) error {
	q := `
UPDATE organizations
SET status = @status, approved_by = @approved_by, approved_at = @approved_at, updated_at = @updated_at
WHERE tenant_id = @tenant_id;`

	args := pgx.StrictNamedArgs{
		"tenant_id":   tenantID,
		"status":      status,
		"approved_by": approvedBy,
		"approved_at": approvedAt,
		"updated_at":  updatedAt,
	}
	_, err := conn.Exec(ctx, q, args)
	return err
}

// LoadByStatus returns organizations in one status (empty = all), newest first —
// the superadmin review queue.
func (os *Organizations) LoadByStatus(ctx context.Context, conn pg.Querier, status string) error {
	q := fmt.Sprintf(`SELECT %s FROM organizations WHERE (@all OR status = @status) ORDER BY created_at DESC;`, organizationColumns)
	args := pgx.StrictNamedArgs{"all": status == "", "status": status}

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query organizations: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[Organization])
	if err != nil {
		return fmt.Errorf("cannot collect organizations: %w", err)
	}
	*os = out
	return nil
}
