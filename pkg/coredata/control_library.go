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

// EvidenceType classifies evidence guidance entries.
type EvidenceType string

const (
	EvidenceAutomatedTest EvidenceType = "automated_test"
	EvidenceDocument      EvidenceType = "document"
	EvidencePolicy        EvidenceType = "policy"
	EvidenceInterview     EvidenceType = "interview"
	EvidenceObservation   EvidenceType = "observation"
)

// IsValid reports whether t is a known evidence type.
func (t EvidenceType) IsValid() bool {
	switch t {
	case EvidenceAutomatedTest, EvidenceDocument, EvidencePolicy, EvidenceInterview, EvidenceObservation:
		return true
	default:
		return false
	}
}

type (
	// ControlLibraryEntry is an implementable control shipped with a framework
	// (or shared when FrameworkID is nil), satisfiable across requirement items.
	ControlLibraryEntry struct {
		ID          gid.GID   `db:"id"`
		FrameworkID *gid.GID  `db:"framework_id"`
		Code        string    `db:"code"`
		Name        string    `db:"name"`
		Description string    `db:"description"`
		Domain      string    `db:"domain"`
		Tags        []string  `db:"tags"`
		Origin      string    `db:"origin"`
		CreatedAt   time.Time `db:"created_at"`
	}

	ControlLibraryEntries []*ControlLibraryEntry

	// ControlRequirementLink relates a control to a requirement (M:N).
	ControlRequirementLink struct {
		ControlID     gid.GID `db:"control_id"`
		RequirementID gid.GID `db:"requirement_id"`
	}

	ControlRequirementLinks []*ControlRequirementLink
)

const controlLibraryColumns = `id, framework_id, code, name, description, domain, tags, origin, created_at`

func (c ControlLibraryEntry) Insert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `
INSERT INTO control_library (tenant_id, id, framework_id, code, name, description, domain, tags, origin, created_at)
VALUES (@tenant_id, @id, @framework_id, @code, @name, @description, @domain, @tags, @origin, @created_at);`

	args := pgx.StrictNamedArgs{
		"tenant_id":    scope.GetTenantID(),
		"id":           c.ID,
		"framework_id": c.FrameworkID,
		"code":         c.Code,
		"name":         c.Name,
		"description":  c.Description,
		"domain":       c.Domain,
		"tags":         orEmpty(c.Tags),
		"origin":       orManual(c.Origin),
		"created_at":   c.CreatedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	if err != nil {
		if isUniqueViolation(err, "control_library_code_unique") {
			return ErrResourceAlreadyExists
		}
		return err
	}
	return nil
}

func (c ControlLibraryEntry) Delete(ctx context.Context, conn pg.Tx, scope Scoper, id gid.GID) error {
	q := fmt.Sprintf(`DELETE FROM control_library WHERE %s AND id = @id;`, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"id": id}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}

// LoadAllByFramework returns a framework's control entries (shared/global rows
// excluded), ordered by code.
func (cs *ControlLibraryEntries) LoadAllByFramework(ctx context.Context, conn pg.Querier, scope Scoper, frameworkID gid.GID) error {
	q := fmt.Sprintf(`SELECT %s FROM control_library WHERE %s AND framework_id = @framework_id ORDER BY code ASC;`,
		controlLibraryColumns, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"framework_id": frameworkID}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query control library: %w", err)
	}

	entries, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[ControlLibraryEntry])
	if err != nil {
		return fmt.Errorf("cannot collect control library: %w", err)
	}

	*cs = entries
	return nil
}

func (l ControlRequirementLink) Insert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `INSERT INTO control_requirements (tenant_id, control_id, requirement_id) VALUES (@tenant_id, @control_id, @requirement_id) ON CONFLICT DO NOTHING;`
	args := pgx.StrictNamedArgs{"tenant_id": scope.GetTenantID(), "control_id": l.ControlID, "requirement_id": l.RequirementID}
	_, err := conn.Exec(ctx, q, args)
	return err
}

// DeleteByControl removes every requirement link of a control (used on replace).
func (l ControlRequirementLink) DeleteByControl(ctx context.Context, conn pg.Tx, scope Scoper, controlID gid.GID) error {
	q := fmt.Sprintf(`DELETE FROM control_requirements WHERE %s AND control_id = @control_id;`, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"control_id": controlID}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}

// LoadAllByControls returns the requirement links of the given controls.
func (ls *ControlRequirementLinks) LoadAllByControls(ctx context.Context, conn pg.Querier, scope Scoper, controlIDs []gid.GID) error {
	q := fmt.Sprintf(`SELECT control_id, requirement_id FROM control_requirements WHERE %s AND control_id = ANY(@ids::text[]);`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"ids": controlIDs}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query control requirement links: %w", err)
	}

	links, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[ControlRequirementLink])
	if err != nil {
		return fmt.Errorf("cannot collect control requirement links: %w", err)
	}

	*ls = links
	return nil
}
