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

// AIGeneration is the provenance record of one LLM proposal exchange: the
// prompt, the raw model output and what the auditor decided. Content only enters
// a draft through explicit human acceptance; this table proves it.
type AIGeneration struct {
	ID                 gid.GID   `db:"id"`
	FrameworkVersionID gid.GID   `db:"framework_version_id"`
	Step               string    `db:"step"`
	Provider           string    `db:"provider"`
	Model              string    `db:"model"`
	Prompt             string    `db:"prompt"`
	RawOutput          string    `db:"raw_output"`
	Status             string    `db:"status"` // proposed | accepted | partially_accepted | rejected
	AcceptedCount      int       `db:"accepted_count"`
	CreatedBy          gid.GID   `db:"created_by"`
	CreatedAt          time.Time `db:"created_at"`
}

func (g AIGeneration) Insert(ctx context.Context, conn pg.Querier, scope Scoper) error {
	q := `
INSERT INTO ai_generations (tenant_id, id, framework_version_id, step, provider, model, prompt, raw_output, status, accepted_count, created_by, created_at)
VALUES (@tenant_id, @id, @framework_version_id, @step, @provider, @model, @prompt, @raw_output, @status, @accepted_count, @created_by, @created_at);`

	args := pgx.StrictNamedArgs{
		"tenant_id":            scope.GetTenantID(),
		"id":                   g.ID,
		"framework_version_id": g.FrameworkVersionID,
		"step":                 g.Step,
		"provider":             g.Provider,
		"model":                g.Model,
		"prompt":               g.Prompt,
		"raw_output":           g.RawOutput,
		"status":               g.Status,
		"accepted_count":       g.AcceptedCount,
		"created_by":           g.CreatedBy,
		"created_at":           g.CreatedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	return err
}

// UpdateDecision records the auditor's decision on a proposal.
func (g *AIGeneration) UpdateDecision(ctx context.Context, conn pg.Querier, scope Scoper, status string, acceptedCount int) error {
	q := fmt.Sprintf(`UPDATE ai_generations SET status = @status, accepted_count = @accepted_count WHERE %s AND id = @id;`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"id": g.ID, "status": status, "accepted_count": acceptedCount}
	maps.Copy(args, scope.SQLArguments())

	_, err := conn.Exec(ctx, q, args)
	return err
}
