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
	// EvidenceGuidance suggests how to evidence a control (not an upload — a
	// recommendation shipped with the framework catalog).
	EvidenceGuidance struct {
		ID                   gid.GID   `db:"id"`
		ControlID            gid.GID   `db:"control_id"`
		Type                 string    `db:"type"`
		Hint                 string    `db:"hint"`
		RenewalCadenceMonths *int      `db:"renewal_cadence_months"`
		PolicyTemplateID     *gid.GID  `db:"policy_template_id"`
		Origin               string    `db:"origin"`
		CreatedAt            time.Time `db:"created_at"`
	}

	EvidenceGuidances []*EvidenceGuidance
)

const evidenceGuidanceColumns = `id, control_id, type, hint, renewal_cadence_months, policy_template_id, origin, created_at`

func (e EvidenceGuidance) Insert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `
INSERT INTO evidence_guidance (tenant_id, id, control_id, type, hint, renewal_cadence_months, policy_template_id, origin, created_at)
VALUES (@tenant_id, @id, @control_id, @type, @hint, @renewal_cadence_months, @policy_template_id, @origin, @created_at);`

	args := pgx.StrictNamedArgs{
		"tenant_id":              scope.GetTenantID(),
		"id":                     e.ID,
		"control_id":             e.ControlID,
		"type":                   e.Type,
		"hint":                   e.Hint,
		"renewal_cadence_months": e.RenewalCadenceMonths,
		"policy_template_id":     e.PolicyTemplateID,
		"origin":                 orManual(e.Origin),
		"created_at":             e.CreatedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	return err
}

func (e EvidenceGuidance) Delete(ctx context.Context, conn pg.Tx, scope Scoper, id gid.GID) error {
	q := fmt.Sprintf(`DELETE FROM evidence_guidance WHERE %s AND id = @id;`, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"id": id}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}

// LoadAllByControls returns evidence rows for the given controls.
func (es *EvidenceGuidances) LoadAllByControls(ctx context.Context, conn pg.Querier, scope Scoper, controlIDs []gid.GID) error {
	q := fmt.Sprintf(`SELECT %s FROM evidence_guidance WHERE %s AND control_id = ANY(@ids::text[]) ORDER BY created_at ASC;`,
		evidenceGuidanceColumns, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"ids": controlIDs}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query evidence guidance: %w", err)
	}

	entries, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[EvidenceGuidance])
	if err != nil {
		return fmt.Errorf("cannot collect evidence guidance: %w", err)
	}

	*es = entries
	return nil
}
