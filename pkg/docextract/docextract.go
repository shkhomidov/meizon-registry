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

// Package docextract turns an uploaded source document into plain text for the
// AI framework generator. Text-based formats pass through; PDFs are extracted
// with a pure-Go reader. The extracted text is what the LLM reads to produce the
// structured framework.
package docextract

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// MaxTextRunes bounds the extracted text. It is a safety valve against a
// pathological upload, not a context limit: the ingestion pipeline chunks the
// text before sending it, so a full multi-hundred-page standard is fine.
const MaxTextRunes = 2_000_000

// PageBreak separates pages in extracted PDF text so the review UI can render
// the document page by page. It is a form feed (U+000C) — harmless to the model
// and unlikely to occur in source text.
const PageBreak = "\f"

// ErrNoTextLayer means a PDF carries no usable text layer — almost always a
// scan. Callers branch on this to fall back to OCR, so it is a typed error and
// not a message: string-matching an error is how a fallback silently stops
// firing the day the wording changes.
var ErrNoTextLayer = errors.New("the PDF has no text layer (it looks scanned)")

// Sanitize strips characters that extracted text may legally contain but
// PostgreSQL cannot store, and must be applied to every string leaving this
// package.
//
// NUL is the one that bites: PDF text layers carry it routinely, json.Marshal
// encodes it as a JSON NUL escape, and jsonb then rejects the whole document
// with SQLSTATE 22P05 — which is how a fully generated framework was lost
// after the pipeline had already paid for OCR and every LLM call. Postgres
// text columns reject NUL too, so this is not specific to the jsonb payload.
//
// Invalid UTF-8 is handled in the same pass because the documents that produce
// one generally produce the other, and Postgres requires valid UTF-8 as well.
// Both substitutions are lossy by design: neither is meaningful content, and
// silently dropping them beats failing the write minutes later.
func Sanitize(s string) string {
	if s == "" {
		return s
	}
	s = strings.ReplaceAll(s, "\x00", "")
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	return s
}

// minCharsPerPage is the usable-text threshold. A scanned page is rarely empty
// — a letterhead, a page number or a stamp often yields a few stray glyphs — so
// "length > 0" would keep OCR from ever running on exactly the documents that
// need it. Averaged per page, real prose clears this by an order of magnitude.
const minCharsPerPage = 50

// usablePage reports whether one page carries enough text to skip OCR.
func usablePage(page string) bool {
	return len(strings.Join(strings.Fields(page), "")) >= minCharsPerPage
}

// usableText reports whether extracted PDF text is substantive enough to skip
// OCR entirely. Deliberately conservative in the other direction too: OCR is
// slow and billable, so a document with real text must never be sent.
func usableText(text string) bool {
	pages := strings.Split(text, PageBreak)
	var chars int
	for _, p := range pages {
		chars += len(strings.Join(strings.Fields(p), ""))
	}
	if len(pages) == 0 {
		return chars > 0
	}
	return chars/len(pages) >= minCharsPerPage
}

// PageText is a PDF split into pages, with the pages that carry no usable text
// called out by index (0-based).
//
// Per-page rather than whole-document, because most real scans are mixed: a
// born-digital standard with a scanned annex, or a scan with a text cover
// sheet. OCR'ing the whole file in that case pays for pages whose text is
// already exact — and replaces exact text with a transcription that can be
// subtly wrong.
type PageText struct {
	Pages    []string
	NeedsOCR []int
}

// Text joins the pages back into the pipeline's page-break-delimited form.
func (p PageText) Text() string { return strings.Join(p.Pages, PageBreak) }

// ExtractPages reads a PDF page by page and reports which pages need OCR.
// Non-PDF input has no page structure, so it comes back as a single page.
func ExtractPages(filename string, data []byte) (PageText, error) {
	if len(data) == 0 {
		return PageText{}, fmt.Errorf("the document is empty")
	}
	if !isPDF(filename, data) {
		text, err := Extract(filename, data)
		if err != nil {
			return PageText{}, err
		}
		return PageText{Pages: []string{text}}, nil
	}

	raw, err := extractPDF(data)
	if err != nil {
		return PageText{}, fmt.Errorf("cannot read PDF (try pasting the text instead): %w", err)
	}
	pages := strings.Split(raw, PageBreak)

	out := PageText{Pages: pages}
	for i, page := range pages {
		if !usablePage(page) {
			out.NeedsOCR = append(out.NeedsOCR, i)
		}
	}
	return out, nil
}

// Extract returns the plain text of a document. filename is used only to detect
// PDFs; everything else is treated as UTF-8 text (txt, md, json, csv…). The
// result is truncated to MaxTextRunes.
func Extract(filename string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("the document is empty")
	}

	var text string
	switch {
	case isPDF(filename, data):
		extracted, err := extractPDF(data)
		if err != nil {
			return "", fmt.Errorf("cannot read PDF (try pasting the text instead): %w", err)
		}
		if !usableText(extracted) {
			return "", ErrNoTextLayer
		}
		text = extracted
	case isSpreadsheet(filename):
		// Excel/CSV are binary or non-UTF-8, so they cannot go through the text
		// branch below — they are flattened to labelled text first, then
		// sanitized like everything else.
		flat, err := extractSpreadsheet(filename, data)
		if err != nil {
			return "", err
		}
		text = Sanitize(flat)
	default:
		if !utf8.Valid(data) {
			return "", fmt.Errorf("the document is not valid UTF-8 text; upload a PDF, Excel, CSV, or paste the text")
		}
		// Valid UTF-8 still permits NUL, so a .txt/.json upload needs the same
		// treatment as a PDF. (extractPDF sanitizes its own output.)
		text = Sanitize(string(data))
	}

	// Trim surrounding blank space but NOT page breaks: a leading/trailing empty
	// page must keep its separator or every later page index shifts, and the
	// reviewer's "jump to the page this came from" would point at the wrong page.
	text = strings.Trim(text, " \t\r\n")
	if strings.Trim(text, " \t\r\n"+PageBreak) == "" {
		return "", fmt.Errorf("no text could be extracted from the document")
	}

	if runes := []rune(text); len(runes) > MaxTextRunes {
		text = string(runes[:MaxTextRunes])
	}
	return text, nil
}

func isPDF(filename string, data []byte) bool {
	if strings.HasSuffix(strings.ToLower(filename), ".pdf") {
		return true
	}
	return bytes.HasPrefix(data, []byte("%PDF-"))
}

func extractPDF(data []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	var pages []string
	for i := 1; i <= r.NumPage(); i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			pages = append(pages, "")
			continue
		}
		content, err := page.GetPlainText(nil)
		if err != nil {
			pages = append(pages, "") // keep page numbering even if unreadable
			continue
		}
		pages = append(pages, content)
	}
	// Join with a form-feed so the reviewer UI can show one page at a time while
	// the model still receives the whole text. Sanitized here rather than at each
	// caller: this is the only place PDF bytes become strings, so it is the one
	// place a NUL can enter.
	return Sanitize(strings.Join(pages, PageBreak)), nil
}
