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
	"bytes"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// TestExtractCSVLabelsColumns is the behaviour the LLM path depends on: cells
// arrive tagged with their column header, not as a bare grid.
func TestExtractCSVLabelsColumns(t *testing.T) {
	csv := "Ref,Requirement,Category\n" +
		"3.1.1,Limit system access to authorized users,Access Control\n" +
		"3.1.2,Limit transactions to permitted functions,Access Control\n"

	text, err := Extract("controls.csv", []byte(csv))
	if err != nil {
		t.Fatalf("extract csv: %v", err)
	}

	for _, want := range []string{
		"Ref: 3.1.1",
		"Requirement: Limit system access to authorized users",
		"Category: Access Control",
		"Ref: 3.1.2",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected label %q in output, got:\n%s", want, text)
		}
	}

	// The header itself must not be emitted as a data row.
	if strings.Contains(text, "Ref: Ref") {
		t.Fatalf("header row was emitted as data:\n%s", text)
	}
}

// TestExtractXLSXFlattensSheets checks the real binary path end to end: build an
// xlsx in memory, extract it, and confirm the labelled rows come back.
func TestExtractXLSXFlattensSheets(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	sheet := f.GetSheetName(0)
	rows := [][]string{
		{"Code", "Title", "Description"},
		{"A.5.1", "Policies for information security", "A set of policies shall be defined."},
		{"A.5.2", "Information security roles", "Responsibilities shall be allocated."},
	}
	for r, row := range rows {
		for c, cell := range row {
			cellRef, _ := excelize.CoordinatesToCellName(c+1, r+1)
			_ = f.SetCellValue(sheet, cellRef, cell)
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write xlsx: %v", err)
	}

	text, err := Extract("iso.xlsx", buf.Bytes())
	if err != nil {
		t.Fatalf("extract xlsx: %v", err)
	}

	for _, want := range []string{
		"Code: A.5.1",
		"Title: Policies for information security",
		"Description: A set of policies shall be defined.",
		"Code: A.5.2",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in output, got:\n%s", want, text)
		}
	}
}

// TestExtractXLSXIsRoutedByExtractPages confirms the pipeline entry point, not
// just Extract, handles a spreadsheet — ExtractPages is what the upload path
// calls, and it must not treat xlsx bytes as invalid UTF-8.
func TestExtractXLSXIsRoutedByExtractPages(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	sheet := f.GetSheetName(0)
	_ = f.SetCellValue(sheet, "A1", "Ref")
	_ = f.SetCellValue(sheet, "B1", "Requirement")
	_ = f.SetCellValue(sheet, "A2", "1.1")
	_ = f.SetCellValue(sheet, "B2", "Do the thing")
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write xlsx: %v", err)
	}

	pt, err := ExtractPages("f.xlsx", buf.Bytes())
	if err != nil {
		t.Fatalf("ExtractPages xlsx: %v", err)
	}
	if len(pt.Pages) != 1 {
		t.Fatalf("expected a spreadsheet to be one page, got %d", len(pt.Pages))
	}
	if len(pt.NeedsOCR) != 0 {
		t.Fatalf("a spreadsheet must never request OCR, got %v", pt.NeedsOCR)
	}
	if !strings.Contains(pt.Text(), "Requirement: Do the thing") {
		t.Fatalf("labelled row missing:\n%s", pt.Text())
	}
}

// TestSpreadsheetNULSanitized guards the production wedged-job class: a cell
// carrying a NUL byte must not survive into the extracted text, or the jsonb
// write of the generation result fails exactly as it did for the PCI-DSS PDF.
func TestSpreadsheetNULSanitized(t *testing.T) {
	csv := "Ref,Requirement\n1.1,bad\x00value\n"
	text, err := Extract("x.csv", []byte(csv))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if strings.ContainsRune(text, 0) {
		t.Fatalf("NUL byte survived extraction: %q", text)
	}
	if !strings.Contains(text, "badvalue") {
		t.Fatalf("expected the NUL stripped but the text kept, got: %q", text)
	}
}

// TestSpreadsheetRaggedRows: a data row wider than the header (a stray trailing
// cell) must not panic and must still label what it can.
func TestSpreadsheetRaggedRows(t *testing.T) {
	csv := "A,B\n1,2,3\n"
	text, err := Extract("r.csv", []byte(csv))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !strings.Contains(text, "A: 1") || !strings.Contains(text, "B: 2") {
		t.Fatalf("labelled cells missing: %q", text)
	}
	if !strings.Contains(text, "Column 3: 3") {
		t.Fatalf("the extra cell should fall back to a positional label: %q", text)
	}
}
