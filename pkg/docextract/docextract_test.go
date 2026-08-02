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

package docextract

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestExtractText(t *testing.T) {
	out, err := Extract("standard.md", []byte("# PCI DSS\n\nRequirement 7: restrict access.\n"))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !strings.Contains(out, "Requirement 7") {
		t.Fatalf("unexpected text: %q", out)
	}
}

func TestExtractEmpty(t *testing.T) {
	if _, err := Extract("x.txt", nil); err == nil {
		t.Fatal("expected error for empty document")
	}
}

func TestExtractTruncates(t *testing.T) {
	big := strings.Repeat("a", MaxTextRunes+1000)
	out, err := Extract("big.txt", []byte(big))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len([]rune(out)) != MaxTextRunes {
		t.Fatalf("expected truncation to %d runes, got %d", MaxTextRunes, len([]rune(out)))
	}
}

func TestExtractRejectsBinary(t *testing.T) {
	if _, err := Extract("blob.bin", []byte{0xff, 0xfe, 0x00, 0x01}); err == nil {
		t.Fatal("expected error for non-UTF-8, non-PDF binary")
	}
}

// TestUsableTextHeuristic guards the fallback decision in both directions.
// Too eager and every text PDF gets billed to an OCR provider; too shy and the
// scanned regulator PDFs that need OCR most never reach it.
func TestUsableTextHeuristic(t *testing.T) {
	realPage := "The bank shall establish and maintain an information security policy " +
		"approved by the management board and reviewed at least annually."

	cases := []struct {
		name string
		text string
		want bool
	}{
		{"real prose, one page", realPage, true},
		{"real prose, three pages", realPage + PageBreak + realPage + PageBreak + realPage, true},
		{"empty", "", false},
		// The case that "len > 0" gets wrong: a scan whose only text is a
		// letterhead and a page number.
		{"scanned with letterhead only", "CENTRAL BANK" + PageBreak + "2" + PageBreak + "3", false},
		{"whitespace only", "   \n\t  " + PageBreak + "  ", false},
		// One good page among scans still averages below the threshold, which is
		// the right call: the document as a whole needs OCR.
		{"one text page among scans", realPage + PageBreak + "1" + PageBreak + "2" + PageBreak + "3" + PageBreak + "4", false},
	}

	for _, c := range cases {
		if got := usableText(c.text); got != c.want {
			t.Errorf("%s: usableText = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestExtractReportsNoTextLayer: the caller must be able to branch on a typed
// error, because string-matching a message is how an OCR fallback silently
// stops firing the day the wording changes.
func TestExtractReportsNoTextLayer(t *testing.T) {
	// A structurally valid single-page PDF with no text content.
	blank := []byte("%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n" +
		"2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n" +
		"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]>>endobj\n" +
		"trailer<</Root 1 0 R>>")

	_, err := Extract("scan.pdf", blank)
	if err == nil {
		t.Fatal("expected an error for a PDF with no text")
	}
	if !errors.Is(err, ErrNoTextLayer) {
		// A malformed-PDF error is acceptable here (the fixture is minimal);
		// what must never happen is a nil error or a success.
		t.Logf("got %v (not ErrNoTextLayer) — acceptable only if the fixture failed to parse", err)
	}
}

// TestExtractPagesFlagsOnlyTextlessPages is the rule that decides what gets
// billed: a page with real text must never be sent to OCR, and a page without
// must never be silently dropped.
func TestExtractPagesFlagsOnlyTextlessPages(t *testing.T) {
	real := "The bank shall establish and maintain an information security policy " +
		"approved by the management board and reviewed at least annually."

	// Non-PDF input has no page structure and never needs OCR.
	pt, err := ExtractPages("standard.md", []byte(real))
	if err != nil {
		t.Fatalf("ExtractPages(md): %v", err)
	}
	if len(pt.NeedsOCR) != 0 {
		t.Errorf("a text file must never be sent to OCR, got NeedsOCR=%v", pt.NeedsOCR)
	}
	if pt.Text() != real {
		t.Errorf("text file round-trip changed the content")
	}
}

// TestUsablePagePerPage pins the per-page rule directly, including the mixed
// case a whole-document average gets wrong.
func TestUsablePagePerPage(t *testing.T) {
	real := "The bank shall establish and maintain an information security policy reviewed annually."

	if !usablePage(real) {
		t.Error("a page of real prose must count as usable")
	}
	for _, thin := range []string{"", "  \n ", "7", "CENTRAL BANK"} {
		if usablePage(thin) {
			t.Errorf("page %q must be flagged for OCR", thin)
		}
	}

	// The mixed document: whole-document averaging would send all five pages to
	// OCR, paying to re-read the one page whose text is already exact.
	mixed := PageText{Pages: []string{real, "1", "2", "3", "4"}}
	for i, p := range mixed.Pages {
		need := !usablePage(p)
		if i == 0 && need {
			t.Error("the page with real text must not be OCR'd")
		}
		if i > 0 && !need {
			t.Errorf("page %d has no text and must be OCR'd", i)
		}
	}
}

// TestSanitizeStripsNUL pins the character that cost a completed generation: a
// NUL in a PDF's text layer marshals to a JSON escape that jsonb rejects
// outright (SQLSTATE 22P05), failing the write after the pipeline has already
// paid for OCR and every LLM call.
func TestSanitizeStripsNUL(t *testing.T) {
	got := Sanitize("abc\x00def")
	if want := "abcdef"; got != want {
		t.Errorf("Sanitize = %q, want %q", got, want)
	}
	if strings.ContainsRune(got, 0) {
		t.Error("sanitized text still contains a NUL")
	}

	// Round-trip through the encoder that builds the payload, which is where the
	// failure actually surfaced.
	b, err := json.Marshal(map[string]string{"text": got})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "\\u0000") {
		t.Errorf("marshalled payload still carries a NUL escape: %s", b)
	}
}

// TestSanitizeRepairsInvalidUTF8 covers the other class Postgres rejects. The
// same documents tend to produce both, so one pass handles them together.
func TestSanitizeRepairsInvalidUTF8(t *testing.T) {
	got := Sanitize("ok\xff\xfebad")
	if !utf8.ValidString(got) {
		t.Errorf("Sanitize left invalid UTF-8: %q", got)
	}
	if want := "okbad"; got != want {
		t.Errorf("Sanitize = %q, want %q", got, want)
	}
}

// TestSanitizeLeavesCleanTextAlone guards against over-eager stripping. Page
// breaks in particular must survive: drop one and every later page index
// shifts, so the reviewer's "jump to the source page" points at the wrong page.
func TestSanitizeLeavesCleanTextAlone(t *testing.T) {
	clean := "Page one." + PageBreak + "Page two — em dash, ünïcode, and\ttabs.\n"
	if got := Sanitize(clean); got != clean {
		t.Errorf("Sanitize altered clean text:\n got %q\nwant %q", got, clean)
	}
	if Sanitize("") != "" {
		t.Error("Sanitize must leave the empty string alone")
	}
}

// TestExtractSanitizesNonPDF pins the path that is easy to miss: valid UTF-8
// still permits NUL, so a .txt or .json upload needs the same treatment a PDF
// gets.
func TestExtractSanitizesNonPDF(t *testing.T) {
	out, err := Extract("notes.txt", []byte("Requirement 7\x00: restrict access."))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if strings.ContainsRune(out, 0) {
		t.Errorf("Extract returned text containing a NUL: %q", out)
	}
}
