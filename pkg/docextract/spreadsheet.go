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
	"encoding/csv"
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

// Spreadsheets are handed to the generation pipeline as text, like a PDF —
// never parsed straight into a framework. A compliance spreadsheet has no fixed
// column layout across regulators, so the model interprets it; but the model
// interprets far better if it is told what each column MEANS. So rather than
// dumping bare cells, each row is emitted as "Header: value" pairs, carrying the
// sheet's own headers through as labels.
//
// Example — a sheet with headers "Ref | Requirement | Guidance" becomes:
//
//	Ref: 3.1.1
//	Requirement: Limit system access to authorized users.
//	Guidance: Maintain a current access list.
//
// which reads to the model as labelled fields rather than a grid it has to
// second-guess.

func isXLSX(filename string) bool {
	return strings.HasSuffix(strings.ToLower(filename), ".xlsx")
}

func isCSV(filename string) bool {
	f := strings.ToLower(filename)
	return strings.HasSuffix(f, ".csv") || strings.HasSuffix(f, ".tsv")
}

// isSpreadsheet reports whether the upload is a spreadsheet this package can
// flatten to labelled text. Detection is by extension: the ingest handler passes
// the real filename, and an xlsx renamed without its extension is a corner case
// not worth sniffing zip magic bytes for.
func isSpreadsheet(filename string) bool {
	return isXLSX(filename) || isCSV(filename)
}

// extractSpreadsheet flattens every sheet to labelled text. The result is
// sanitized by the caller (Extract), as with every other path.
func extractSpreadsheet(filename string, data []byte) (string, error) {
	if isCSV(filename) {
		delim := ','
		if strings.HasSuffix(strings.ToLower(filename), ".tsv") {
			delim = '\t'
		}
		return flattenCSV(data, delim)
	}
	return flattenXLSX(data)
}

func flattenXLSX(data []byte) (string, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("cannot read the Excel file (try exporting it as CSV): %w", err)
	}
	defer f.Close()

	var b strings.Builder
	sheets := f.GetSheetList()
	for _, sheet := range sheets {
		rows, err := f.GetRows(sheet)
		if err != nil {
			// One unreadable sheet should not sink a multi-sheet workbook.
			continue
		}
		// Name the sheet only when there is more than one, so a single-sheet
		// export does not carry a noisy "Sheet1" heading into the model.
		if len(sheets) > 1 {
			fmt.Fprintf(&b, "# Sheet: %s\n", sheet)
		}
		writeRows(&b, rows)
		b.WriteString("\n")
	}

	out := strings.TrimSpace(b.String())
	if out == "" {
		return "", fmt.Errorf("the spreadsheet has no readable rows")
	}
	return out, nil
}

func flattenCSV(data []byte, delim rune) (string, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = delim
	// Rows in a real-world export do not all have the same column count; a
	// trailing note or a merged cell should not abort the whole parse.
	r.FieldsPerRecord = -1

	rows, err := r.ReadAll()
	if err != nil {
		return "", fmt.Errorf("cannot read the CSV file: %w", err)
	}

	var b strings.Builder
	writeRows(&b, rows)

	out := strings.TrimSpace(b.String())
	if out == "" {
		return "", fmt.Errorf("the CSV file has no readable rows")
	}
	return out, nil
}

// writeRows treats the first non-empty row as the header and labels every later
// row's cells with it. A row is separated from the next by a blank line so the
// model sees discrete records rather than one run-on block.
func writeRows(b *strings.Builder, rows [][]string) {
	header, headerIdx := firstNonEmptyRow(rows)
	if header == nil {
		return
	}

	for i := headerIdx + 1; i < len(rows); i++ {
		row := rows[i]
		if isEmptyRow(row) {
			continue
		}
		wroteAny := false
		for col, cell := range row {
			cell = strings.TrimSpace(cell)
			if cell == "" {
				continue
			}
			label := columnLabel(header, col)
			fmt.Fprintf(b, "%s: %s\n", label, cell)
			wroteAny = true
		}
		if wroteAny {
			b.WriteString("\n")
		}
	}
}

func firstNonEmptyRow(rows [][]string) ([]string, int) {
	for i, r := range rows {
		if !isEmptyRow(r) {
			return r, i
		}
	}
	return nil, -1
}

func isEmptyRow(row []string) bool {
	for _, c := range row {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

// columnLabel returns the header for a column, falling back to a positional
// name when a data row is wider than the header row (a stray trailing cell).
func columnLabel(header []string, col int) string {
	if col < len(header) {
		if h := strings.TrimSpace(header[col]); h != "" {
			return h
		}
	}
	return fmt.Sprintf("Column %d", col+1)
}
