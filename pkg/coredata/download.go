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
	"time"

	"github.com/jackc/pgx/v5"
	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/gid"
)

// Download records a served framework version, for the distribution ledger.
type Download struct {
	ID                 gid.GID   `db:"id"`
	FrameworkVersionID gid.GID   `db:"framework_version_id"`
	TokenID            string    `db:"token_id"`
	Format             string    `db:"format"`
	CreatedAt          time.Time `db:"created_at"`
}

// Download format values.
const (
	DownloadFormatBundle = "bundle"
	DownloadFormatSeed   = "seed"
	DownloadFormatQA     = "qa"
)

func (d Download) Insert(ctx context.Context, conn pg.Querier, scope Scoper) error {
	q := `
INSERT INTO downloads (tenant_id, id, framework_version_id, token_id, format, created_at)
VALUES (@tenant_id, @id, @framework_version_id, @token_id, @format, @created_at);`

	args := pgx.StrictNamedArgs{
		"tenant_id":            scope.GetTenantID(),
		"id":                   d.ID,
		"framework_version_id": d.FrameworkVersionID,
		"token_id":             d.TokenID,
		"format":               d.Format,
		"created_at":           d.CreatedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	return err
}
