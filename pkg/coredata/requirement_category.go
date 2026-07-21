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
	"fmt"
	"maps"
	"time"

	"github.com/jackc/pgx/v5"
	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/gid"
)

type (
	// RequirementCategory is the top level of a version's hierarchy (goal /
	// theme / chapter).
	RequirementCategory struct {
		ID                 gid.GID   `db:"id"`
		FrameworkVersionID gid.GID   `db:"framework_version_id"`
		Code               string    `db:"code"`
		Name               string    `db:"name"`
		Description        string    `db:"description"`
		IsOptional         bool      `db:"is_optional"`
		ApplicabilityNote  string    `db:"applicability_note"`
		Position           int       `db:"position"`
		Origin             string    `db:"origin"`
		CreatedAt          time.Time `db:"created_at"`
	}

	RequirementCategories []*RequirementCategory
)

const requirementCategoryColumns = `id, framework_version_id, code, name, description, is_optional, applicability_note, position, origin, created_at`

func (c RequirementCategory) Insert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `
INSERT INTO requirement_categories (tenant_id, id, framework_version_id, code, name, description, is_optional, applicability_note, position, origin, created_at)
VALUES (@tenant_id, @id, @framework_version_id, @code, @name, @description, @is_optional, @applicability_note, @position, @origin, @created_at);`

	args := pgx.StrictNamedArgs{
		"tenant_id":            scope.GetTenantID(),
		"id":                   c.ID,
		"framework_version_id": c.FrameworkVersionID,
		"code":                 c.Code,
		"name":                 c.Name,
		"description":          c.Description,
		"is_optional":          c.IsOptional,
		"applicability_note":   c.ApplicabilityNote,
		"position":             c.Position,
		"origin":               orManual(c.Origin),
		"created_at":           c.CreatedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	if err != nil {
		if isUniqueViolation(err, "categories_version_code_unique") {
			return ErrResourceAlreadyExists
		}
		return err
	}
	return nil
}

func (c RequirementCategory) Delete(ctx context.Context, conn pg.Tx, scope Scoper, id gid.GID) error {
	q := fmt.Sprintf(`DELETE FROM requirement_categories WHERE %s AND id = @id;`, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"id": id}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}

// LoadAllByVersion returns a version's categories in authored order.
func (cs *RequirementCategories) LoadAllByVersion(ctx context.Context, conn pg.Querier, scope Scoper, versionID gid.GID) error {
	q := fmt.Sprintf(`SELECT %s FROM requirement_categories WHERE %s AND framework_version_id = @version_id ORDER BY position ASC, code ASC;`,
		requirementCategoryColumns, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"version_id": versionID}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query categories: %w", err)
	}

	categories, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[RequirementCategory])
	if err != nil {
		return fmt.Errorf("cannot collect categories: %w", err)
	}

	*cs = categories
	return nil
}
