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
	"maps"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/gid"
)

type (
	// Membership binds an identity to a role and a region scope. The regions
	// column is stored comma-joined; empty means no regions (superadmin ignores
	// it entirely).
	Membership struct {
		ID         gid.GID   `db:"id"`
		IdentityID gid.GID   `db:"identity_id"`
		Role       string    `db:"role"`
		Regions    string    `db:"regions"`
		CreatedAt  time.Time `db:"created_at"`
		UpdatedAt  time.Time `db:"updated_at"`
	}
)

// RegionList splits the stored comma-joined regions into a slice.
func (m *Membership) RegionList() []string {
	if strings.TrimSpace(m.Regions) == "" {
		return nil
	}
	parts := strings.Split(m.Regions, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func (m Membership) Insert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `
INSERT INTO memberships (tenant_id, id, identity_id, role, regions, created_at, updated_at)
VALUES (@tenant_id, @id, @identity_id, @role, @regions, @created_at, @updated_at);`

	args := pgx.StrictNamedArgs{
		"tenant_id":   scope.GetTenantID(),
		"id":          m.ID,
		"identity_id": m.IdentityID,
		"role":        m.Role,
		"regions":     m.Regions,
		"created_at":  m.CreatedAt,
		"updated_at":  m.UpdatedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	if err != nil {
		if isUniqueViolation(err, "memberships_identity_unique") {
			return ErrResourceAlreadyExists
		}
		return err
	}

	return nil
}

func (m *Membership) LoadByIdentityID(ctx context.Context, conn pg.Querier, scope Scoper, identityID gid.GID) error {
	q := fmt.Sprintf(`
SELECT id, identity_id, role, regions, created_at, updated_at
FROM memberships
WHERE %s AND identity_id = @identity_id
LIMIT 1;`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"identity_id": identityID}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query membership: %w", err)
	}

	membership, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Membership])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrResourceNotFound
		}
		return fmt.Errorf("cannot collect membership: %w", err)
	}

	*m = membership

	return nil
}

// Memberships is a slice of memberships.
type Memberships []*Membership

// LoadAll returns every membership in the scope.
func (ms *Memberships) LoadAll(ctx context.Context, conn pg.Querier, scope Scoper) error {
	q := fmt.Sprintf(`
SELECT id, identity_id, role, regions, created_at, updated_at
FROM memberships
WHERE %s;`, scope.SQLFragment())

	rows, err := conn.Query(ctx, q, scope.SQLArguments())
	if err != nil {
		return fmt.Errorf("cannot query memberships: %w", err)
	}

	memberships, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[Membership])
	if err != nil {
		return fmt.Errorf("cannot collect memberships: %w", err)
	}

	*ms = memberships

	return nil
}

func (m *Membership) Update(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := fmt.Sprintf(`
UPDATE memberships
SET role = @role, regions = @regions, updated_at = @updated_at
WHERE %s AND id = @id;`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{
		"id":         m.ID,
		"role":       m.Role,
		"regions":    m.Regions,
		"updated_at": m.UpdatedAt,
	}
	maps.Copy(args, scope.SQLArguments())

	_, err := conn.Exec(ctx, q, args)
	return err
}
