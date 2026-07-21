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
	"time"

	"github.com/jackc/pgx/v5"
	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/gid"
)

type (
	// Identity is a registered user of the console.
	Identity struct {
		ID             gid.GID        `db:"id"`
		Email          string         `db:"email"`
		FullName       string         `db:"full_name"`
		HashedPassword []byte         `db:"hashed_password"`
		Status         IdentityStatus `db:"status"`
		CreatedAt      time.Time      `db:"created_at"`
		UpdatedAt      time.Time      `db:"updated_at"`
	}

	Identities []*Identity
)

func (i Identity) Insert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `
INSERT INTO identities (
    tenant_id, id, email, full_name, hashed_password, status, created_at, updated_at
) VALUES (
    @tenant_id, @id, @email, @full_name, @hashed_password, @status, @created_at, @updated_at
);`

	args := pgx.StrictNamedArgs{
		"tenant_id":       scope.GetTenantID(),
		"id":              i.ID,
		"email":           i.Email,
		"full_name":       i.FullName,
		"hashed_password": i.HashedPassword,
		"status":          i.Status,
		"created_at":      i.CreatedAt,
		"updated_at":      i.UpdatedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	if err != nil {
		if isUniqueViolation(err, "identities_email_unique") {
			return ErrResourceAlreadyExists
		}
		return err
	}

	return nil
}

func (i *Identity) LoadByID(ctx context.Context, conn pg.Querier, scope Scoper, id gid.GID) error {
	return i.load(ctx, conn, scope, "id = @id", pgx.StrictNamedArgs{"id": id})
}

func (i *Identity) LoadByEmail(ctx context.Context, conn pg.Querier, scope Scoper, email string) error {
	return i.load(ctx, conn, scope, "email = @email", pgx.StrictNamedArgs{"email": email})
}

func (i *Identity) load(ctx context.Context, conn pg.Querier, scope Scoper, pred string, extra pgx.StrictNamedArgs) error {
	q := fmt.Sprintf(`
SELECT id, email, full_name, hashed_password, status, created_at, updated_at
FROM identities
WHERE %s AND %s
LIMIT 1;`, scope.SQLFragment(), pred)

	args := pgx.StrictNamedArgs{}
	maps.Copy(args, extra)
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query identity: %w", err)
	}

	identity, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Identity])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrResourceNotFound
		}
		return fmt.Errorf("cannot collect identity: %w", err)
	}

	*i = identity

	return nil
}

// LoadAll returns every identity in the scope, ordered by email.
func (is *Identities) LoadAll(ctx context.Context, conn pg.Querier, scope Scoper) error {
	q := fmt.Sprintf(`
SELECT id, email, full_name, hashed_password, status, created_at, updated_at
FROM identities
WHERE %s
ORDER BY email ASC;`, scope.SQLFragment())

	rows, err := conn.Query(ctx, q, scope.SQLArguments())
	if err != nil {
		return fmt.Errorf("cannot query identities: %w", err)
	}

	identities, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[Identity])
	if err != nil {
		return fmt.Errorf("cannot collect identities: %w", err)
	}

	*is = identities

	return nil
}

func (i *Identity) Update(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := fmt.Sprintf(`
UPDATE identities
SET email = @email, full_name = @full_name, hashed_password = @hashed_password,
    status = @status, updated_at = @updated_at
WHERE %s AND id = @id;`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{
		"id":              i.ID,
		"email":           i.Email,
		"full_name":       i.FullName,
		"hashed_password": i.HashedPassword,
		"status":          i.Status,
		"updated_at":      i.UpdatedAt,
	}
	maps.Copy(args, scope.SQLArguments())

	_, err := conn.Exec(ctx, q, args)
	return err
}
