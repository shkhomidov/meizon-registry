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
	// Requirement is the assessable leaf of a framework version: a numbered
	// obligation within a category, satisfied by one or more controls. It
	// carries the fields that used to live on a requirement item — the
	// section/item levels were 1:1 padding and were removed in Phase 13.
	Requirement struct {
		ID                     gid.GID   `db:"id"`
		FrameworkVersionID     gid.GID   `db:"framework_version_id"`
		CategoryID             gid.GID   `db:"category_id"`
		Code                   string    `db:"code"`
		Number                 string    `db:"number"`
		Title                  string    `db:"title"`
		Description            string    `db:"description"`
		ItemType               string    `db:"item_type"`
		LegalCitation          string    `db:"legal_citation"`
		ValidationApproaches   []string  `db:"validation_approaches"`
		EffectiveFrom          string    `db:"effective_from"`
		RetiredAt              string    `db:"retired_at"`
		Guidance               string    `db:"guidance"`
		Tags                   []string  `db:"tags"`
		ApplicabilityRoles     []string  `db:"applicability_roles"`
		ApplicabilityCondition string    `db:"applicability_condition"`
		Position               int       `db:"position"`
		Origin                 string    `db:"origin"`
		CreatedAt              time.Time `db:"created_at"`
	}

	Requirements []*Requirement
)

const requirementColumns = `id, framework_version_id, category_id, code, number, title, description, item_type, legal_citation, validation_approaches, effective_from, retired_at, guidance, tags, applicability_roles, applicability_condition, position, origin, created_at`

func (r Requirement) Insert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `
INSERT INTO requirements (
    tenant_id, id, framework_version_id, category_id, code, number, title, description,
    item_type, legal_citation, validation_approaches, effective_from, retired_at, guidance,
    tags, applicability_roles, applicability_condition, position, origin, created_at
) VALUES (
    @tenant_id, @id, @framework_version_id, @category_id, @code, @number, @title, @description,
    @item_type, @legal_citation, @validation_approaches, @effective_from, @retired_at, @guidance,
    @tags, @applicability_roles, @applicability_condition, @position, @origin, @created_at
);`

	args := pgx.StrictNamedArgs{
		"tenant_id":               scope.GetTenantID(),
		"id":                      r.ID,
		"framework_version_id":    r.FrameworkVersionID,
		"category_id":             r.CategoryID,
		"code":                    r.Code,
		"number":                  r.Number,
		"title":                   r.Title,
		"description":             r.Description,
		"item_type":               orControlRequirement(r.ItemType),
		"legal_citation":          r.LegalCitation,
		"validation_approaches":   orEmpty(r.ValidationApproaches),
		"effective_from":          r.EffectiveFrom,
		"retired_at":              r.RetiredAt,
		"guidance":                r.Guidance,
		"tags":                    orEmpty(r.Tags),
		"applicability_roles":     orEmpty(r.ApplicabilityRoles),
		"applicability_condition": r.ApplicabilityCondition,
		"position":                r.Position,
		"origin":                  orManual(r.Origin),
		"created_at":              r.CreatedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	if err != nil {
		if isUniqueViolation(err, "requirements_version_code_unique") {
			return ErrResourceAlreadyExists
		}
		return err
	}
	return nil
}

func (r Requirement) Delete(ctx context.Context, conn pg.Tx, scope Scoper, id gid.GID) error {
	q := fmt.Sprintf(`DELETE FROM requirements WHERE %s AND id = @id;`, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"id": id}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}

func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// orManual defaults an unset origin marker to "manual".
func orManual(origin string) string {
	if origin == "" {
		return "manual"
	}
	return origin
}

// orControlRequirement defaults an unset item type, mirroring the column default.
func orControlRequirement(t string) string {
	if t == "" {
		return "control_requirement"
	}
	return t
}

// LoadByVersionAndCode loads a single requirement by its code within a version.
func (r *Requirement) LoadByVersionAndCode(ctx context.Context, conn pg.Querier, scope Scoper, versionID gid.GID, code string) error {
	q := fmt.Sprintf(`SELECT %s FROM requirements WHERE %s AND framework_version_id = @version_id AND code = @code LIMIT 1;`,
		requirementColumns, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"version_id": versionID, "code": code}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query requirement: %w", err)
	}

	requirement, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Requirement])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrResourceNotFound
		}
		return fmt.Errorf("cannot collect requirement: %w", err)
	}

	*r = requirement
	return nil
}

// CountByVersion returns the number of requirements in a version.
func (rs *Requirements) CountByVersion(ctx context.Context, conn pg.Querier, scope Scoper, versionID gid.GID) (int, error) {
	q := fmt.Sprintf(`SELECT COUNT(id) FROM requirements WHERE %s AND framework_version_id = @version_id;`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"version_id": versionID}
	maps.Copy(args, scope.SQLArguments())

	var count int
	if err := conn.QueryRow(ctx, q, args).Scan(&count); err != nil {
		return 0, fmt.Errorf("cannot count requirements: %w", err)
	}
	return count, nil
}

// LoadAllByVersion returns a version's requirements in authored order.
func (rs *Requirements) LoadAllByVersion(ctx context.Context, conn pg.Querier, scope Scoper, versionID gid.GID) error {
	q := fmt.Sprintf(`SELECT %s FROM requirements WHERE %s AND framework_version_id = @version_id ORDER BY position ASC, code ASC;`,
		requirementColumns, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"version_id": versionID}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query requirements: %w", err)
	}

	requirements, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[Requirement])
	if err != nil {
		return fmt.Errorf("cannot collect requirements: %w", err)
	}

	*rs = requirements
	return nil
}
