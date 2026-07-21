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

package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/gid"
)

// staleSourceAfter is how long an uploaded document waits to be accepted before
// it is swept. Generous: an auditor may review a 142-requirement draft over a
// coffee break, and losing the source mid-review would be worse than the disk.
const staleSourceAfter = 24 * time.Hour

// SourceDocumentView is the console shape. The bytes are never inlined — the
// file is fetched from its own endpoint only when someone opens or downloads it.
type SourceDocumentView struct {
	ID          gid.GID   `json:"id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"contentType"`
	ByteSize    int64     `json:"byteSize"`
	SHA256      string    `json:"sha256"`
	PageCount   int       `json:"pageCount,omitempty"`
	OCRPages    int       `json:"ocrPages,omitempty"`
	UploadedBy  string    `json:"uploadedBy,omitempty"`
	UploadedAt  time.Time `json:"uploadedAt"`
}

// sourceView projects a stored row into the console shape.
func sourceView(d *coredata.SourceDocument) SourceDocumentView {
	return SourceDocumentView{
		ID: d.ID, Filename: d.Filename, ContentType: d.ContentType,
		ByteSize: d.ByteSize, SHA256: d.SHA256,
		PageCount: d.PageCount, OCRPages: d.OCRPages,
		UploadedBy: d.UploadedBy, UploadedAt: d.UploadedAt,
	}
}

// StageSourceDocument stores an uploaded file against a generation job, before
// the auditor has decided whether to keep the resulting draft.
//
// Best-effort by design: failing an upload because the archive copy could not
// be written would block the actual work over a nice-to-have.
func (s *Service) StageSourceDocument(ctx context.Context, actorID gid.GID, jobID string, in DocInput, filename string, data []byte) {
	if len(data) == 0 || jobID == "" {
		return
	}

	sum := sha256.Sum256(data)
	doc := coredata.SourceDocument{
		ID:          gid.New(s.cfg.PlatformTenant, coredata.SourceDocumentEntityType),
		JobID:       jobID,
		Filename:    filename,
		ContentType: "application/pdf",
		ByteSize:    int64(len(data)),
		SHA256:      hex.EncodeToString(sum[:]),
		PageCount:   in.PageCount(),
		OCRPages:    len(in.OCRPages),
		UploadedBy:  actorID.String(),
		UploadedAt:  time.Now(),
	}

	if err := s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		scope := s.platformScope()
		if _, err := (coredata.SourceDocument{}).DeleteStale(ctx, tx, scope, time.Now().Add(-staleSourceAfter)); err != nil {
			return err
		}
		return doc.Insert(ctx, tx, scope, data)
	}); err != nil {
		s.logger.WarnCtx(ctx, fmt.Sprintf("cannot keep source document %q: %v", filename, err))
	}
}

// linkSourceDocumentTx attaches a staged document to the framework that was
// accepted from it. Runs inside the accept transaction so a framework and its
// source are linked atomically or not at all.
func (s *Service) linkSourceDocumentTx(ctx context.Context, tx pg.Tx, jobID string, frameworkID gid.GID) error {
	if jobID == "" {
		return nil
	}
	return (coredata.SourceDocument{}).LinkToFramework(ctx, tx, s.platformScope(), jobID, frameworkID)
}

// sourcesByFramework indexes stored source metadata for the list page.
func (s *Service) sourcesByFramework(ctx context.Context, conn pg.Querier) (map[gid.GID]*coredata.SourceDocument, error) {
	var docs coredata.SourceDocuments
	if err := docs.LoadAllLinked(ctx, conn, s.platformScope()); err != nil {
		return nil, err
	}
	// Newest first from the query, so the first one seen per framework wins.
	out := map[gid.GID]*coredata.SourceDocument{}
	for _, d := range docs {
		if d.FrameworkID == nil {
			continue
		}
		if _, seen := out[*d.FrameworkID]; !seen {
			out[*d.FrameworkID] = d
		}
	}
	return out, nil
}

// SourceDocumentFor returns a framework's stored source file with its bytes,
// for download or preview.
func (s *Service) SourceDocumentFor(ctx context.Context, ref string) (SourceDocumentView, []byte, error) {
	var view SourceDocumentView
	var content []byte

	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		scope := s.platformScope()

		var framework coredata.Framework
		if err := framework.LoadByReferenceID(ctx, conn, scope, ref); err != nil {
			return err
		}
		var doc coredata.SourceDocument
		if err := doc.LoadByFramework(ctx, conn, scope, framework.ID); err != nil {
			return err
		}
		data, err := doc.Content(ctx, conn, scope, doc.ID)
		if err != nil {
			return err
		}
		view, content = sourceView(&doc), data
		return nil
	})
	return view, content, err
}
