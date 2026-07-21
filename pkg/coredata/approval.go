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
	// Approval records a reviewer's decision on a version. Uniqueness on
	// (version, reviewer) prevents a reviewer voting twice.
	Approval struct {
		ID                 gid.GID          `db:"id"`
		FrameworkVersionID gid.GID          `db:"framework_version_id"`
		ReviewerID         gid.GID          `db:"reviewer_id"`
		Decision           ApprovalDecision `db:"decision"`
		Comment            string           `db:"comment"`
		CreatedAt          time.Time        `db:"created_at"`
	}

	Approvals []*Approval
)

const approvalColumns = `id, framework_version_id, reviewer_id, decision, comment, created_at`

func (a Approval) Insert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `
INSERT INTO approvals (tenant_id, id, framework_version_id, reviewer_id, decision, comment, created_at)
VALUES (@tenant_id, @id, @framework_version_id, @reviewer_id, @decision, @comment, @created_at);`

	args := pgx.StrictNamedArgs{
		"tenant_id":            scope.GetTenantID(),
		"id":                   a.ID,
		"framework_version_id": a.FrameworkVersionID,
		"reviewer_id":          a.ReviewerID,
		"decision":             a.Decision,
		"comment":              a.Comment,
		"created_at":           a.CreatedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	if err != nil {
		if isUniqueViolation(err, "approvals_version_reviewer_unique") {
			return ErrResourceAlreadyExists
		}
		return err
	}

	return nil
}

// LoadAllByVersion returns every approval recorded against a version.
func (as *Approvals) LoadAllByVersion(ctx context.Context, conn pg.Querier, scope Scoper, versionID gid.GID) error {
	q := fmt.Sprintf(`SELECT %s FROM approvals WHERE %s AND framework_version_id = @version_id ORDER BY created_at ASC;`,
		approvalColumns, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"version_id": versionID}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query approvals: %w", err)
	}

	approvals, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[Approval])
	if err != nil {
		return fmt.Errorf("cannot collect approvals: %w", err)
	}

	*as = approvals

	return nil
}
