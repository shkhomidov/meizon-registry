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
	// Control is a single requirement belonging to a framework version. refs and
	// mappings are stored as JSONB. position preserves the authored order so a
	// flattened seed is byte-stable.
	Control struct {
		ID                 gid.GID   `db:"id"`
		FrameworkVersionID gid.GID   `db:"framework_version_id"`
		RefID              string    `db:"ref_id"`
		Name               string    `db:"name"`
		Description        string    `db:"description"`
		Section            string    `db:"section"`
		ParentRefID        *string   `db:"parent_ref_id"`
		Guidance           string    `db:"guidance"`
		Refs               []string  `db:"refs"`
		Mappings           []Mapping `db:"mappings"`
		Position           int       `db:"position"`
		CreatedAt          time.Time `db:"created_at"`
		UpdatedAt          time.Time `db:"updated_at"`
	}

	// Mapping is a cross-framework control relationship.
	Mapping struct {
		Framework string `json:"framework"`
		Control   string `json:"control"`
	}

	Controls []*Control
)

const controlColumns = `id, framework_version_id, ref_id, name, description, section, parent_ref_id, guidance, refs, mappings, position, created_at, updated_at`

func (c Control) Insert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `
INSERT INTO controls (
    tenant_id, id, framework_version_id, ref_id, name, description, section, parent_ref_id, guidance, refs, mappings, position, created_at, updated_at
) VALUES (
    @tenant_id, @id, @framework_version_id, @ref_id, @name, @description, @section, @parent_ref_id, @guidance, @refs, @mappings, @position, @created_at, @updated_at
);`

	refs := c.Refs
	if refs == nil {
		refs = []string{}
	}
	mappings := c.Mappings
	if mappings == nil {
		mappings = []Mapping{}
	}

	args := pgx.StrictNamedArgs{
		"tenant_id":            scope.GetTenantID(),
		"id":                   c.ID,
		"framework_version_id": c.FrameworkVersionID,
		"ref_id":               c.RefID,
		"name":                 c.Name,
		"description":          c.Description,
		"section":              c.Section,
		"parent_ref_id":        c.ParentRefID,
		"guidance":             c.Guidance,
		"refs":                 refs,
		"mappings":             mappings,
		"position":             c.Position,
		"created_at":           c.CreatedAt,
		"updated_at":           c.UpdatedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	if err != nil {
		if isUniqueViolation(err, "controls_version_ref_unique") {
			return ErrResourceAlreadyExists
		}
		return err
	}

	return nil
}

func (c Control) Delete(ctx context.Context, conn pg.Tx, scope Scoper, id gid.GID) error {
	q := fmt.Sprintf(`DELETE FROM controls WHERE %s AND id = @id;`, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"id": id}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}

// LoadAllByVersion returns a version's controls in authored order.
func (cs *Controls) LoadAllByVersion(ctx context.Context, conn pg.Querier, scope Scoper, versionID gid.GID) error {
	q := fmt.Sprintf(`SELECT %s FROM controls WHERE %s AND framework_version_id = @version_id ORDER BY position ASC, ref_id ASC;`,
		controlColumns, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"version_id": versionID}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query controls: %w", err)
	}

	controls, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[Control])
	if err != nil {
		return fmt.Errorf("cannot collect controls: %w", err)
	}

	*cs = controls

	return nil
}

// CountByVersion returns the number of controls in a version.
func (cs *Controls) CountByVersion(ctx context.Context, conn pg.Querier, scope Scoper, versionID gid.GID) (int, error) {
	q := fmt.Sprintf(`SELECT COUNT(id) FROM controls WHERE %s AND framework_version_id = @version_id;`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"version_id": versionID}
	maps.Copy(args, scope.SQLArguments())

	var count int
	if err := conn.QueryRow(ctx, q, args).Scan(&count); err != nil {
		return 0, fmt.Errorf("cannot count controls: %w", err)
	}

	return count, nil
}
