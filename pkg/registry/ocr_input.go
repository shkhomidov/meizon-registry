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
	"errors"
	"fmt"
	"strings"

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/docextract"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/ocr"
)

// DocInput is a document on its way into the pipeline: either already-extracted
// text (the paste path, or a PDF with a usable text layer) or the raw bytes of a
// scan that still needs OCR.
//
// Raw is carried into the job rather than OCR'd in the upload request on
// purpose: a 49-page scan takes minutes, and a request that hangs that long
// looks like a hung app. Inside the job it is a visible step with a page
// counter.
type DocInput struct {
	Text string
	// Raw is the uploaded file. It is carried for two reasons, and both matter:
	// OCR needs the bytes to read a scan, and the upload is archived as the
	// framework's source document. It is therefore populated for EVERY file
	// upload, not only the ones needing OCR — an earlier version only set it on
	// the OCR path, which silently archived nothing for ordinary text PDFs.
	Raw      []byte
	Filename string

	// Pages is the text already extracted per page, and OCRPages the 0-based
	// indices that carry no text. OCR reads only those; every other page keeps
	// its exact, free, already-extracted text.
	Pages    []string
	OCRPages []int
}

// NeedsOCR reports whether this input still has to be read by OCR.
func (d DocInput) NeedsOCR() bool { return len(d.OCRPages) > 0 && len(d.Raw) > 0 }

// PageCount is the document's total page count, known before OCR runs.
func (d DocInput) PageCount() int { return len(d.Pages) }

// PrepareDocument runs text-layer extraction and reports whether OCR is needed.
//
// This is the whole fallback decision, and it lives in the upload path so the
// caller learns immediately — before any job starts — that a scan cannot be
// processed without an OCR credential. Returning ErrOCRNotConfigured here means
// the user gets a straight answer instead of a job that dies a minute later.
func (s *Service) PrepareDocument(ctx context.Context, filename string, data []byte) (DocInput, error) {
	pt, err := docextract.ExtractPages(filename, data)
	if err != nil {
		return DocInput{}, err
	}

	// Every page already has text — the free, exact path. No OCR, no cost.
	if len(pt.NeedsOCR) == 0 {
		return DocInput{Text: pt.Text(), Pages: pt.Pages, Raw: data, Filename: filename}, nil
	}

	configured := false
	if cerr := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		if _, err := s.ocrClient(ctx, conn); err != nil {
			if errors.Is(err, ErrOCRNotConfigured) {
				return nil
			}
			return err
		}
		configured = true
		return nil
	}); cerr != nil {
		return DocInput{}, cerr
	}

	if !configured {
		// Nothing readable at all: the document simply cannot be ingested.
		if len(pt.NeedsOCR) == len(pt.Pages) {
			return DocInput{}, fmt.Errorf(
				"this PDF has no text layer — it looks scanned. Configure OCR in Settings to read documents like this: %w",
				ErrOCRNotConfigured)
		}
		// Partly readable. Proceeding loses those pages, but blank covers and
		// separator pages are common enough that hard-failing would block
		// ordinary documents. Continue with what was extracted — and say what
		// was skipped, so a missing requirement later has an explanation.
		s.logger.WarnCtx(ctx, fmt.Sprintf(
			"%q: %d of %d page(s) carry no text and OCR is not configured — those pages are being skipped",
			filename, len(pt.NeedsOCR), len(pt.Pages)))
		return DocInput{Text: pt.Text(), Pages: pt.Pages, Raw: data, Filename: filename}, nil
	}

	return DocInput{
		Raw: data, Filename: filename,
		Pages: pt.Pages, OCRPages: pt.NeedsOCR,
	}, nil
}

// resolveText returns the document's text, running OCR when the input is a scan.
// Reported through the job as an `ocr` step so a slow scanned upload visibly
// says why it is slow.
func (s *Service) resolveText(ctx context.Context, job *ingestJob, actorID gid.GID, in DocInput) (string, error) {
	if !in.NeedsOCR() {
		return in.Text, nil
	}

	// The total is known before the first call, so the checklist reads
	// "3 / 12" from the start rather than counting up from an unknown.
	job.set("ocr", 0, len(in.OCRPages))

	var client ocr.Client
	if err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		c, err := s.ocrClient(ctx, conn)
		if err != nil {
			return err
		}
		client = c
		return nil
	}); err != nil {
		return "", err
	}

	// Progress advances per batch. Without it a 100-page scan shows 0/N for
	// minutes and reads as a hung job — which is exactly how it was reported.
	res, err := client.ExtractPDF(ctx, in.Filename, in.Raw, in.OCRPages, func(done, total int) {
		job.set("ocr", done, total)
	})
	if err != nil {
		return "", fmt.Errorf("ocr: %s", ocr.RedactSecrets(err.Error()))
	}
	job.set("ocr", len(in.OCRPages), len(in.OCRPages))

	// Splice: OCR text fills only the pages that had none. Every other page
	// keeps its exact extracted text — cheaper, and not re-transcribed into
	// something subtly different.
	merged := make([]string, len(in.Pages))
	copy(merged, in.Pages)
	filled := 0
	for _, idx := range in.OCRPages {
		if idx < 0 || idx >= len(merged) {
			continue
		}
		if text, ok := res.ByIndex[idx]; ok && text != "" {
			merged[idx] = text
			filled++
		}
	}
	text := strings.Join(merged, docextract.PageBreak)

	// Which pages were paid for is a cost signal, not a detail: OCR'ing pages
	// that already had text is a billing bug, so the split is always recorded.
	summary := fmt.Sprintf("pages=%d/%d filled=%d model=%s",
		len(in.OCRPages), len(in.Pages), filled, res.Model)
	s.logger.InfoCtx(ctx, fmt.Sprintf("ocr read %s for %q", summary, in.Filename))
	_ = s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		return s.recordAudit(ctx, conn, s.platformScope(), actorID, "document.ocr", in.Filename, summary)
	})

	if strings.TrimSpace(strings.ReplaceAll(text, docextract.PageBreak, "")) == "" {
		return "", fmt.Errorf("ocr returned no text for this document")
	}
	return text, nil
}
