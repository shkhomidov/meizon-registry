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
	// AuditLogEntry is an append-only record of a governance action: role
	// changes, approvals, publishes, token issuance and downloads.
	AuditLogEntry struct {
		ID        gid.GID   `db:"id"`
		ActorID   string    `db:"actor_id"`
		Action    string    `db:"action"`
		TargetID  string    `db:"target_id"`
		Detail    string    `db:"detail"`
		CreatedAt time.Time `db:"created_at"`
	}

	AuditLogEntries []*AuditLogEntry
)

const auditLogColumns = `id, actor_id, action, target_id, detail, created_at`

func (e AuditLogEntry) Insert(ctx context.Context, conn pg.Querier, scope Scoper) error {
	q := `
INSERT INTO audit_log (tenant_id, id, actor_id, action, target_id, detail, created_at)
VALUES (@tenant_id, @id, @actor_id, @action, @target_id, @detail, @created_at);`

	args := pgx.StrictNamedArgs{
		"tenant_id":  scope.GetTenantID(),
		"id":         e.ID,
		"actor_id":   e.ActorID,
		"action":     e.Action,
		"target_id":  e.TargetID,
		"detail":     e.Detail,
		"created_at": e.CreatedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	return err
}

// LoadRecent returns the most recent audit entries in the scope, newest first.
func (es *AuditLogEntries) LoadRecent(ctx context.Context, conn pg.Querier, scope Scoper, limit int) error {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	q := fmt.Sprintf(`SELECT %s FROM audit_log WHERE %s ORDER BY created_at DESC LIMIT @limit;`, auditLogColumns, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"limit": limit}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query audit log: %w", err)
	}

	entries, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[AuditLogEntry])
	if err != nil {
		return fmt.Errorf("cannot collect audit log: %w", err)
	}

	*es = entries

	return nil
}
