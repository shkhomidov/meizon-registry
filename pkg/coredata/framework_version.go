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
	// FrameworkVersion is an immutable snapshot of a framework at a semver. Once
	// PUBLISHED its content, signature and content hash never change; a new
	// revision forks a fresh DRAFT.
	FrameworkVersion struct {
		ID          gid.GID                `db:"id"`
		FrameworkID gid.GID                `db:"framework_id"`
		Version     string                 `db:"version"`
		Status      FrameworkVersionStatus `db:"status"`
		ContentHash string                 `db:"content_hash"`
		Signature   []byte                 `db:"signature"`
		KeyID       string                 `db:"key_id"`
		Changelog   string                 `db:"changelog"`
		Quorum      int                    `db:"quorum"`
		AuthorID    gid.GID                `db:"author_id"`
		PublishedAt *time.Time             `db:"published_at"`
		// Release provenance. Stored as strings rather than gid.GID because a
		// version published before these columns existed has no recoverable
		// publisher — empty means "not known", which a zero GID cannot express.
		PublishedBy string     `db:"published_by"`
		ApprovedBy  string     `db:"approved_by"`
		ApprovedAt  *time.Time `db:"approved_at"`
		CreatedAt   time.Time  `db:"created_at"`
		UpdatedAt   time.Time  `db:"updated_at"`
	}

	FrameworkVersions []*FrameworkVersion
)

const frameworkVersionColumns = `id, framework_id, version, status, content_hash, signature, key_id, changelog, quorum, author_id, published_at, published_by, approved_by, approved_at, created_at, updated_at`

func (v FrameworkVersion) Insert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `
INSERT INTO framework_versions (
    tenant_id, id, framework_id, version, status, content_hash, signature, key_id, changelog, quorum, author_id, published_at, created_at, updated_at
) VALUES (
    @tenant_id, @id, @framework_id, @version, @status, @content_hash, @signature, @key_id, @changelog, @quorum, @author_id, @published_at, @created_at, @updated_at
);`

	args := pgx.StrictNamedArgs{
		"tenant_id":    scope.GetTenantID(),
		"id":           v.ID,
		"framework_id": v.FrameworkID,
		"version":      v.Version,
		"status":       v.Status,
		"content_hash": v.ContentHash,
		"signature":    v.Signature,
		"key_id":       v.KeyID,
		"changelog":    v.Changelog,
		"quorum":       v.Quorum,
		"author_id":    v.AuthorID,
		"published_at": v.PublishedAt,
		"created_at":   v.CreatedAt,
		"updated_at":   v.UpdatedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	if err != nil {
		if isUniqueViolation(err, "framework_versions_fw_version_unique") {
			return ErrResourceAlreadyExists
		}
		return err
	}

	return nil
}

func (v *FrameworkVersion) LoadByID(ctx context.Context, conn pg.Querier, scope Scoper, id gid.GID) error {
	return v.loadOne(ctx, conn, scope, "id = @id", pgx.StrictNamedArgs{"id": id})
}

func (v *FrameworkVersion) LoadByFrameworkAndVersion(ctx context.Context, conn pg.Querier, scope Scoper, frameworkID gid.GID, version string) error {
	return v.loadOne(ctx, conn, scope, "framework_id = @framework_id AND version = @version",
		pgx.StrictNamedArgs{"framework_id": frameworkID, "version": version})
}

func (v *FrameworkVersion) loadOne(ctx context.Context, conn pg.Querier, scope Scoper, pred string, extra pgx.StrictNamedArgs) error {
	q := fmt.Sprintf(`SELECT %s FROM framework_versions WHERE %s AND %s LIMIT 1;`, frameworkVersionColumns, scope.SQLFragment(), pred)

	args := pgx.StrictNamedArgs{}
	maps.Copy(args, extra)
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query framework version: %w", err)
	}

	version, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[FrameworkVersion])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrResourceNotFound
		}
		return fmt.Errorf("cannot collect framework version: %w", err)
	}

	*v = version

	return nil
}

// Update rewrites the mutable fields of a version. Callers must enforce that a
// PUBLISHED version is never mutated.
func (v *FrameworkVersion) Update(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := fmt.Sprintf(`
UPDATE framework_versions
SET status = @status, content_hash = @content_hash, signature = @signature, key_id = @key_id,
    changelog = @changelog, quorum = @quorum, published_at = @published_at,
    published_by = @published_by, approved_by = @approved_by, approved_at = @approved_at,
    updated_at = @updated_at
WHERE %s AND id = @id;`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{
		"id":           v.ID,
		"status":       v.Status,
		"content_hash": v.ContentHash,
		"signature":    v.Signature,
		"key_id":       v.KeyID,
		"changelog":    v.Changelog,
		"quorum":       v.Quorum,
		"published_at": v.PublishedAt,
		"published_by": v.PublishedBy,
		"approved_by":  v.ApprovedBy,
		"approved_at":  v.ApprovedAt,
		"updated_at":   v.UpdatedAt,
	}
	maps.Copy(args, scope.SQLArguments())

	_, err := conn.Exec(ctx, q, args)
	return err
}

// LoadAllByFramework returns every version of a framework, newest first.
func (vs *FrameworkVersions) LoadAllByFramework(ctx context.Context, conn pg.Querier, scope Scoper, frameworkID gid.GID) error {
	q := fmt.Sprintf(`SELECT %s FROM framework_versions WHERE %s AND framework_id = @framework_id ORDER BY created_at DESC;`,
		frameworkVersionColumns, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"framework_id": frameworkID}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query framework versions: %w", err)
	}

	versions, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[FrameworkVersion])
	if err != nil {
		return fmt.Errorf("cannot collect framework versions: %w", err)
	}

	*vs = versions

	return nil
}

// LoadLatestPublished loads the most recently published version of a framework,
// or ErrResourceNotFound when none is published. It reads unscoped so the
// distribution layer can serve public frameworks across tenants.
func (v *FrameworkVersion) LoadLatestPublished(ctx context.Context, conn pg.Querier, frameworkID gid.GID) error {
	q := fmt.Sprintf(`
SELECT %s FROM framework_versions
WHERE framework_id = @framework_id AND status = @status
ORDER BY published_at DESC
LIMIT 1;`, frameworkVersionColumns)

	args := pgx.StrictNamedArgs{
		"framework_id": frameworkID,
		"status":       FrameworkVersionStatusPublished,
	}

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query latest published version: %w", err)
	}

	version, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[FrameworkVersion])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrResourceNotFound
		}
		return fmt.Errorf("cannot collect latest published version: %w", err)
	}

	*v = version

	return nil
}
