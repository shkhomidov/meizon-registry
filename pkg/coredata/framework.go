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
	"go.meizon.cloud/registry/pkg/iam/policy"
	"go.meizon.cloud/registry/pkg/page"
)

type (
	// Framework is a catalog entry: a stable compliance standard with a region,
	// authority, license and public-catalog visibility flag. Its actual content
	// lives in immutable FrameworkVersion snapshots.
	Framework struct {
		ID          gid.GID `db:"id"`
		ReferenceID string  `db:"reference_id"`
		Name        string  `db:"name"`
		ShortName   string  `db:"short_name"`
		Region      string  `db:"region"`
		Authority   string  `db:"authority"`
		License     string  `db:"license"`
		Description string  `db:"description"`
		// SourceLanguage is the language the content was authored in. English is
		// the canonical language; other languages live in framework_translations.
		SourceLanguage string    `db:"source_language"`
		Public         bool      `db:"public"`
		CreatedAt      time.Time `db:"created_at"`
		UpdatedAt      time.Time `db:"updated_at"`
	}

	Frameworks []*Framework
)

const frameworkColumns = `id, reference_id, name, short_name, region, authority, license, description, source_language, public, created_at, updated_at`

// CursorKey builds the pagination cursor value for the given order field.
func (f *Framework) CursorKey(orderBy FrameworkOrderField) page.CursorKey {
	switch orderBy {
	case FrameworkOrderFieldCreatedAt:
		return page.NewCursorKey(f.ID, f.CreatedAt)
	case FrameworkOrderFieldName:
		return page.NewCursorKey(f.ID, f.Name)
	}
	panic(fmt.Sprintf("unsupported order by: %s", orderBy))
}

// AuthorizationAttributes returns the region attribute per framework id, which
// the policy engine consults for region-scoped decisions.
func (f *Framework) AuthorizationAttributes(ctx context.Context, conn pg.Querier, ids []gid.GID) (policy.AttributesByID, error) {
	q := `SELECT id, region FROM frameworks WHERE id = ANY(@ids::text[])`

	rows, err := conn.Query(ctx, q, pgx.StrictNamedArgs{"ids": ids})
	if err != nil {
		return nil, fmt.Errorf("cannot query framework authorization attributes: %w", err)
	}
	defer rows.Close()

	out := make(policy.AttributesByID)
	for rows.Next() {
		var id gid.GID
		var region string
		if err := rows.Scan(&id, &region); err != nil {
			return nil, fmt.Errorf("cannot scan framework authorization attributes: %w", err)
		}
		out[id] = policy.Attributes{"region": region}
	}

	return out, rows.Err()
}

func (f Framework) Insert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `
INSERT INTO frameworks (
    tenant_id, id, reference_id, name, short_name, region, authority, license, description, source_language, public, created_at, updated_at
) VALUES (
    @tenant_id, @id, @reference_id, @name, @short_name, @region, @authority, @license, @description, @source_language, @public, @created_at, @updated_at
);`

	args := pgx.StrictNamedArgs{
		"tenant_id":       scope.GetTenantID(),
		"id":              f.ID,
		"reference_id":    f.ReferenceID,
		"name":            f.Name,
		"short_name":      f.ShortName,
		"region":          f.Region,
		"authority":       f.Authority,
		"license":         f.License,
		"description":     f.Description,
		"source_language": f.SourceLanguage,
		"public":          f.Public,
		"created_at":      f.CreatedAt,
		"updated_at":      f.UpdatedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	if err != nil {
		if isUniqueViolation(err, "frameworks_tenant_ref_unique") {
			return ErrResourceAlreadyExists
		}
		return err
	}

	return nil
}

func (f *Framework) LoadByID(ctx context.Context, conn pg.Querier, scope Scoper, id gid.GID) error {
	return f.loadOne(ctx, conn, scope, "id = @id", pgx.StrictNamedArgs{"id": id})
}

func (f *Framework) LoadByReferenceID(ctx context.Context, conn pg.Querier, scope Scoper, referenceID string) error {
	return f.loadOne(ctx, conn, scope, "reference_id = @reference_id", pgx.StrictNamedArgs{"reference_id": referenceID})
}

func (f *Framework) loadOne(ctx context.Context, conn pg.Querier, scope Scoper, pred string, extra pgx.StrictNamedArgs) error {
	q := fmt.Sprintf(`SELECT %s FROM frameworks WHERE %s AND %s LIMIT 1;`, frameworkColumns, scope.SQLFragment(), pred)

	args := pgx.StrictNamedArgs{}
	maps.Copy(args, extra)
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query framework: %w", err)
	}

	framework, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Framework])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrResourceNotFound
		}
		return fmt.Errorf("cannot collect framework: %w", err)
	}

	*f = framework

	return nil
}

func (f *Framework) Update(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := fmt.Sprintf(`
UPDATE frameworks
SET name = @name, short_name = @short_name, authority = @authority, license = @license,
    description = @description, public = @public, updated_at = @updated_at
WHERE %s AND id = @id;`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{
		"id":          f.ID,
		"name":        f.Name,
		"short_name":  f.ShortName,
		"authority":   f.Authority,
		"license":     f.License,
		"description": f.Description,
		"public":      f.Public,
		"updated_at":  f.UpdatedAt,
	}
	maps.Copy(args, scope.SQLArguments())

	_, err := conn.Exec(ctx, q, args)
	return err
}

// LoadAll loads every framework in the scope, ordered by name, for console
// listing.
func (fs *Frameworks) LoadAll(ctx context.Context, conn pg.Querier, scope Scoper) error {
	q := fmt.Sprintf(`SELECT %s FROM frameworks WHERE %s ORDER BY name ASC;`, frameworkColumns, scope.SQLFragment())

	rows, err := conn.Query(ctx, q, scope.SQLArguments())
	if err != nil {
		return fmt.Errorf("cannot query frameworks: %w", err)
	}

	frameworks, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[Framework])
	if err != nil {
		return fmt.Errorf("cannot collect frameworks: %w", err)
	}

	*fs = frameworks

	return nil
}

// LoadCatalog loads the frameworks a distribution consumer may see: those that
// are public plus those owned by ownerTenantID, optionally restricted to a set
// of regions (empty means all regions). This is the copyright gate at the data
// layer — proprietary frameworks are never public, so they only surface for
// their owning tenant. It deliberately reads across tenants and so is unscoped.
func (fs *Frameworks) LoadCatalog(ctx context.Context, conn pg.Querier, ownerTenantID gid.TenantID, regions []string) error {
	q := `
SELECT ` + frameworkColumns + `
FROM frameworks
WHERE (public = TRUE OR tenant_id = @owner_tenant_id)
  AND (@all_regions OR region = ANY(@regions::text[]))
ORDER BY name ASC;`

	args := pgx.StrictNamedArgs{
		"owner_tenant_id": ownerTenantID,
		"all_regions":     len(regions) == 0,
		"regions":         regions,
	}

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query catalog: %w", err)
	}

	frameworks, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[Framework])
	if err != nil {
		return fmt.Errorf("cannot collect catalog: %w", err)
	}

	*fs = frameworks

	return nil
}
