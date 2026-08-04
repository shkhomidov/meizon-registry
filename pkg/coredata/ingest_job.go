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

// Job kinds and statuses.
const (
	JobKindGenerate    = "generate"
	JobKindNextVersion = "next_version"
	JobKindTranslate   = "translate"
	JobKindAutoMap     = "automap"
	JobKindQATemplate  = "qa_template"

	JobStatusRunning = "running"
	JobStatusDone    = "done"
	JobStatusError   = "error"
)

type (
	// IngestJob is a long-running pipeline run, persisted so it outlives the
	// browser tab that started it and the process that runs it.
	IngestJob struct {
		ID           gid.GID   `db:"id"`
		Kind         string    `db:"kind"`
		Status       string    `db:"status"`
		Step         string    `db:"step"`
		Current      int       `db:"current"`
		Total        int       `db:"total"`
		Label        string    `db:"label"`
		FrameworkRef string    `db:"framework_ref"`
		Error        string    `db:"error"`
		ActorID      string    `db:"actor_id"`
		CreatedAt    time.Time `db:"created_at"`
		UpdatedAt    time.Time `db:"updated_at"`
	}

	IngestJobs []*IngestJob
)

// Metadata only — `result` is excluded so listing jobs never pulls whole
// generated frameworks into memory.
const ingestJobColumns = `id, kind, status, step, current, total, label, framework_ref, error, actor_id, created_at, updated_at`

// Insert records a newly started job.
func (j IngestJob) Insert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `
INSERT INTO ingest_jobs (
    tenant_id, id, kind, status, step, current, total, label, framework_ref, error, actor_id, created_at, updated_at
) VALUES (
    @tenant_id, @id, @kind, @status, @step, @current, @total, @label, @framework_ref, @error, @actor_id, @created_at, @updated_at
);`

	args := pgx.StrictNamedArgs{
		"tenant_id": scope.GetTenantID(), "id": j.ID, "kind": j.Kind, "status": j.Status,
		"step": j.Step, "current": j.Current, "total": j.Total,
		"label": j.Label, "framework_ref": j.FrameworkRef, "error": j.Error,
		"actor_id": j.ActorID, "created_at": j.CreatedAt, "updated_at": j.UpdatedAt,
	}
	_, err := conn.Exec(ctx, q, args)
	return err
}

// UpdateProgress writes the current step and counters.
func (j IngestJob) UpdateProgress(ctx context.Context, conn pg.Querier, scope Scoper, id gid.GID, step string, current, total int) error {
	q := fmt.Sprintf(`UPDATE ingest_jobs SET step = @step, current = @current, total = @total, updated_at = @now
WHERE %s AND id = @id AND status = 'running';`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"id": id, "step": step, "current": current, "total": total, "now": time.Now()}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}

// Finish records the terminal state, storing the result document when there is
// one so a reviewer can return to it without re-running the pipeline.
func (j IngestJob) Finish(ctx context.Context, conn pg.Querier, scope Scoper, id gid.GID, status string, result []byte, errText string) error {
	q := fmt.Sprintf(`UPDATE ingest_jobs SET status = @status, result = @result, error = @error, updated_at = @now
WHERE %s AND id = @id;`, scope.SQLFragment())

	var payload any
	if len(result) > 0 {
		payload = result
	}
	args := pgx.StrictNamedArgs{"id": id, "status": status, "result": payload, "error": errText, "now": time.Now()}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}

// LoadByID reads one job's metadata.
func (j *IngestJob) LoadByID(ctx context.Context, conn pg.Querier, scope Scoper, id gid.GID) error {
	q := fmt.Sprintf(`SELECT %s FROM ingest_jobs WHERE %s AND id = @id LIMIT 1;`, ingestJobColumns, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"id": id}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query job: %w", err)
	}
	out, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[IngestJob])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrResourceNotFound
		}
		return fmt.Errorf("cannot collect job: %w", err)
	}
	*j = out
	return nil
}

// Result reads a finished job's stored document.
func (j IngestJob) Result(ctx context.Context, conn pg.Querier, scope Scoper, id gid.GID) ([]byte, error) {
	q := fmt.Sprintf(`SELECT result FROM ingest_jobs WHERE %s AND id = @id LIMIT 1;`, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"id": id}
	maps.Copy(args, scope.SQLArguments())

	var out []byte
	if err := conn.QueryRow(ctx, q, args).Scan(&out); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrResourceNotFound
		}
		return nil, fmt.Errorf("cannot read job result: %w", err)
	}
	return out, nil
}

// LoadRecent returns the newest jobs, for the status page.
func (js *IngestJobs) LoadRecent(ctx context.Context, conn pg.Querier, scope Scoper, limit int) error {
	if limit <= 0 {
		limit = 50
	}
	q := fmt.Sprintf(`SELECT %s FROM ingest_jobs WHERE %s ORDER BY created_at DESC LIMIT @limit;`,
		ingestJobColumns, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"limit": limit}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query jobs: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[IngestJob])
	if err != nil {
		return fmt.Errorf("cannot collect jobs: %w", err)
	}
	*js = out
	return nil
}

// FailOrphaned marks jobs left "running" by a process that died. Without this
// a crashed run shows as in-progress forever, and a user waits for something
// that will never finish.
func (j IngestJob) FailOrphaned(ctx context.Context, conn pg.Tx, scope Scoper, message string) (int, error) {
	q := fmt.Sprintf(`UPDATE ingest_jobs SET status = 'error', error = @message, updated_at = @now
WHERE %s AND status = 'running';`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"message": message, "now": time.Now()}
	maps.Copy(args, scope.SQLArguments())

	tag, err := conn.Exec(ctx, q, args)
	if err != nil {
		return 0, fmt.Errorf("cannot fail orphaned jobs: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// HasRunning reports whether a job of this kind is already in flight for a
// framework. Without this check a repeated trigger starts a second identical
// run — paying twice for the same work, and racing to write the same rows.
func (j IngestJob) HasRunning(ctx context.Context, conn pg.Querier, scope Scoper, kind, frameworkRef string) (bool, error) {
	q := fmt.Sprintf(`SELECT EXISTS (
  SELECT 1 FROM ingest_jobs WHERE %s AND status = 'running' AND kind = @kind AND framework_ref = @ref
);`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"kind": kind, "ref": frameworkRef}
	maps.Copy(args, scope.SQLArguments())

	var exists bool
	if err := conn.QueryRow(ctx, q, args).Scan(&exists); err != nil {
		return false, fmt.Errorf("cannot check running jobs: %w", err)
	}
	return exists, nil
}
