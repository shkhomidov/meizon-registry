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

import "testing"

// TestDocInputNeedsOCROnlyForTextlessPages: Raw is carried for every upload so
// the file can be archived, which means NeedsOCR must key on the page list —
// not merely on Raw being present. Getting this wrong would send every text PDF
// to a billable OCR provider.
func TestDocInputNeedsOCROnlyForTextlessPages(t *testing.T) {
	archivedTextPDF := DocInput{
		Text:     "real text",
		Pages:    []string{"real text"},
		Raw:      []byte("%PDF"),
		Filename: "doc.pdf",
	}
	if archivedTextPDF.NeedsOCR() {
		t.Error("a PDF whose pages all have text must never be sent to OCR, even though its bytes are kept")
	}
	if archivedTextPDF.PageCount() != 1 {
		t.Errorf("PageCount = %d, want 1", archivedTextPDF.PageCount())
	}

	scan := DocInput{
		Pages:    []string{"", ""},
		OCRPages: []int{0, 1},
		Raw:      []byte("%PDF"),
		Filename: "scan.pdf",
	}
	if !scan.NeedsOCR() {
		t.Error("a scan with no text must be sent to OCR")
	}

	pasted := DocInput{Text: "pasted text"}
	if pasted.NeedsOCR() {
		t.Error("pasted text has no file and must never reach OCR")
	}
}
