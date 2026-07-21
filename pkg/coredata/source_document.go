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
	// SourceDocument is the file a framework was generated from, kept so a
	// requirement can be checked against its origin.
	SourceDocument struct {
		ID          gid.GID   `db:"id"`
		FrameworkID *gid.GID  `db:"framework_id"`
		JobID       string    `db:"job_id"`
		Filename    string    `db:"filename"`
		ContentType string    `db:"content_type"`
		ByteSize    int64     `db:"byte_size"`
		SHA256      string    `db:"sha256"`
		PageCount   int       `db:"page_count"`
		OCRPages    int       `db:"ocr_pages"`
		UploadedBy  string    `db:"uploaded_by"`
		UploadedAt  time.Time `db:"uploaded_at"`
	}

	SourceDocuments []*SourceDocument
)

// Metadata columns only — `content` is deliberately excluded so listing
// documents never drags megabytes of PDF through memory.
const sourceDocumentColumns = `id, framework_id, job_id, filename, content_type, byte_size, sha256, page_count, ocr_pages, uploaded_by, uploaded_at`

// Insert stores the file and its metadata.
func (d SourceDocument) Insert(ctx context.Context, conn pg.Tx, scope Scoper, content []byte) error {
	q := `
INSERT INTO source_documents (
    tenant_id, id, framework_id, job_id, filename, content_type, byte_size,
    sha256, page_count, ocr_pages, content, uploaded_by, uploaded_at
) VALUES (
    @tenant_id, @id, @framework_id, @job_id, @filename, @content_type, @byte_size,
    @sha256, @page_count, @ocr_pages, @content, @uploaded_by, @uploaded_at
);`

	args := pgx.StrictNamedArgs{
		"tenant_id":    scope.GetTenantID(),
		"id":           d.ID,
		"framework_id": d.FrameworkID,
		"job_id":       d.JobID,
		"filename":     d.Filename,
		"content_type": d.ContentType,
		"byte_size":    d.ByteSize,
		"sha256":       d.SHA256,
		"page_count":   d.PageCount,
		"ocr_pages":    d.OCRPages,
		"content":      content,
		"uploaded_by":  d.UploadedBy,
		"uploaded_at":  d.UploadedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	return err
}

// LinkToFramework attaches a document staged under a job id to the framework
// the auditor accepted. Only unlinked rows are claimed, so re-accepting cannot
// steal another framework's document.
func (d SourceDocument) LinkToFramework(ctx context.Context, conn pg.Tx, scope Scoper, jobID string, frameworkID gid.GID) error {
	q := fmt.Sprintf(`UPDATE source_documents SET framework_id = @framework_id
WHERE %s AND job_id = @job_id AND framework_id IS NULL;`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"job_id": jobID, "framework_id": frameworkID}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}

// LoadByFramework reads a framework's newest source document metadata.
func (d *SourceDocument) LoadByFramework(ctx context.Context, conn pg.Querier, scope Scoper, frameworkID gid.GID) error {
	q := fmt.Sprintf(`SELECT %s FROM source_documents WHERE %s AND framework_id = @framework_id
ORDER BY uploaded_at DESC LIMIT 1;`, sourceDocumentColumns, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"framework_id": frameworkID}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query source document: %w", err)
	}
	out, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[SourceDocument])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrResourceNotFound
		}
		return fmt.Errorf("cannot collect source document: %w", err)
	}
	*d = out
	return nil
}

// LoadAllLinked returns metadata for every framework-linked document.
func (ds *SourceDocuments) LoadAllLinked(ctx context.Context, conn pg.Querier, scope Scoper) error {
	q := fmt.Sprintf(`SELECT %s FROM source_documents WHERE %s AND framework_id IS NOT NULL
ORDER BY uploaded_at DESC;`, sourceDocumentColumns, scope.SQLFragment())

	rows, err := conn.Query(ctx, q, scope.SQLArguments())
	if err != nil {
		return fmt.Errorf("cannot query source documents: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[SourceDocument])
	if err != nil {
		return fmt.Errorf("cannot collect source documents: %w", err)
	}
	*ds = out
	return nil
}

// Content reads one document's bytes. Separate from the metadata load so the
// megabytes are only fetched when someone actually downloads the file.
func (d SourceDocument) Content(ctx context.Context, conn pg.Querier, scope Scoper, id gid.GID) ([]byte, error) {
	q := fmt.Sprintf(`SELECT content FROM source_documents WHERE %s AND id = @id LIMIT 1;`, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"id": id}
	maps.Copy(args, scope.SQLArguments())

	var content []byte
	if err := conn.QueryRow(ctx, q, args).Scan(&content); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrResourceNotFound
		}
		return nil, fmt.Errorf("cannot read source document: %w", err)
	}
	return content, nil
}

// DeleteStale removes documents staged for generation runs that were never
// accepted. Without this an abandoned upload keeps its megabytes forever.
func (d SourceDocument) DeleteStale(ctx context.Context, conn pg.Tx, scope Scoper, before time.Time) (int, error) {
	q := fmt.Sprintf(`DELETE FROM source_documents WHERE %s AND framework_id IS NULL AND uploaded_at < @before;`,
		scope.SQLFragment())
	args := pgx.StrictNamedArgs{"before": before}
	maps.Copy(args, scope.SQLArguments())

	tag, err := conn.Exec(ctx, q, args)
	if err != nil {
		return 0, fmt.Errorf("cannot sweep stale source documents: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
