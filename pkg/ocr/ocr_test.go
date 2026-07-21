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

package ocr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mistralStub implements the three documented endpoints and records what it saw.
type mistralStub struct {
	gotPurpose  string
	gotFilename string
	gotAuth     string
	gotModel    string
	gotDocURL   string
	deleted     bool
	pages       []map[string]any
}

func (s *mistralStub) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/files", func(w http.ResponseWriter, r *http.Request) {
		s.gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			t.Errorf("upload was not multipart: %v", err)
		}
		s.gotPurpose = r.FormValue("purpose")
		if f, hdr, err := r.FormFile("file"); err == nil {
			s.gotFilename = hdr.Filename
			_ = f.Close()
		} else {
			t.Errorf("no \"file\" part: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "file-123"})
	})

	mux.HandleFunc("/v1/files/file-123/url", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("expiry") == "" {
			t.Error("signed URL requested without an expiry")
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"url": "https://storage.example/doc.pdf?X-Amz-Signature=SUPERSECRETSIG",
		})
	})

	mux.HandleFunc("/v1/files/file-123", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			s.deleted = true
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"deleted": true})
	})

	mux.HandleFunc("/v1/ocr", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string            `json:"model"`
			Document map[string]string `json:"document"`
			Images   bool              `json:"include_image_base64"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.gotModel = body.Model
		s.gotDocURL = body.Document["document_url"]
		if body.Images {
			t.Error("include_image_base64 must be false — images bloat the response for no gain")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"pages": s.pages, "model": body.Model})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestClient(t *testing.T, srv *httptest.Server) Client {
	t.Helper()
	c, err := New(Config{Provider: ProviderMistral, APIKey: "sk-test-KEY", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// TestExtractPDFWireFormat pins the request shape against the documented API.
func TestExtractPDFWireFormat(t *testing.T) {
	stub := &mistralStub{pages: []map[string]any{
		{"index": 0, "markdown": "page one"},
		{"index": 1, "markdown": "page two"},
	}}
	srv := stub.server(t)

	res, err := newTestClient(t, srv).ExtractPDF(context.Background(), "scan.pdf", []byte("%PDF-1.4 fake"), nil, nil)
	if err != nil {
		t.Fatalf("ExtractPDF: %v", err)
	}

	if stub.gotPurpose != "ocr" {
		t.Errorf("purpose = %q, want %q", stub.gotPurpose, "ocr")
	}
	if stub.gotFilename != "scan.pdf" {
		t.Errorf("filename = %q, want scan.pdf", stub.gotFilename)
	}
	if stub.gotAuth != "Bearer sk-test-KEY" {
		t.Errorf("auth header = %q — the key must travel in a header, never a query string", stub.gotAuth)
	}
	if stub.gotModel != DefaultModel {
		t.Errorf("model = %q, want the pinned %q", stub.gotModel, DefaultModel)
	}
	if !strings.HasPrefix(stub.gotDocURL, "https://storage.example/") {
		t.Errorf("document_url = %q, want the signed URL from the files API", stub.gotDocURL)
	}
	if res.Text != "page one"+PageBreak+"page two" {
		t.Errorf("pages must be joined with the page break, got %q", res.Text)
	}
	if res.Pages != 2 {
		t.Errorf("pages = %d, want 2", res.Pages)
	}
	if !stub.deleted {
		t.Error("the uploaded copy of a customer document must be deleted after OCR")
	}
}

// TestExtractPDFOrdersByPageIndex: a page landing in the wrong slot would
// silently misalign every source citation in the reviewer UI.
func TestExtractPDFOrdersByPageIndex(t *testing.T) {
	stub := &mistralStub{pages: []map[string]any{
		{"index": 2, "markdown": "third"},
		{"index": 0, "markdown": "first"},
		{"index": 1, "markdown": "second"},
	}}
	srv := stub.server(t)

	res, err := newTestClient(t, srv).ExtractPDF(context.Background(), "s.pdf", []byte("%PDF"), nil, nil)
	if err != nil {
		t.Fatalf("ExtractPDF: %v", err)
	}
	want := strings.Join([]string{"first", "second", "third"}, PageBreak)
	if res.Text != want {
		t.Errorf("pages out of order:\n got %q\nwant %q", res.Text, want)
	}
}

// TestExtractPDFKeepsPageGaps: a missing page must stay an empty page, or every
// later page number shifts by one.
func TestExtractPDFKeepsPageGaps(t *testing.T) {
	stub := &mistralStub{pages: []map[string]any{
		{"index": 0, "markdown": "first"},
		{"index": 2, "markdown": "third"},
	}}
	srv := stub.server(t)

	res, err := newTestClient(t, srv).ExtractPDF(context.Background(), "s.pdf", []byte("%PDF"), nil, nil)
	if err != nil {
		t.Fatalf("ExtractPDF: %v", err)
	}
	if got := strings.Split(res.Text, PageBreak); len(got) != 3 || got[1] != "" {
		t.Errorf("a page gap must be preserved as an empty page, got %q", got)
	}
}

// TestSecretsRedacted: neither the API key nor a URL signature may survive into
// an error a user or a log will see.
func TestSecretsRedacted(t *testing.T) {
	in := `Get "https://storage.example/doc.pdf?X-Amz-Signature=abc123&key=sk-live-9f8": dial tcp: timeout`
	got := RedactSecrets(in)
	for _, leaked := range []string{"abc123", "sk-live-9f8"} {
		if strings.Contains(got, leaked) {
			t.Errorf("secret %q survived redaction: %s", leaked, got)
		}
	}
}

// TestNewRequiresKey: a client with no key must fail loudly at construction,
// not on the first upload of a customer document.
func TestNewRequiresKey(t *testing.T) {
	if _, err := New(Config{Provider: ProviderMistral}); err == nil {
		t.Fatal("expected an error when no API key is configured")
	}
	if _, err := New(Config{Provider: "tesseract", APIKey: "x"}); err == nil {
		t.Fatal("expected an error for an unknown provider")
	}
}

// TestNoPagesIsAnError: an empty result must not silently become an empty
// document that the pipeline then reports as "no requirements found".
func TestNoPagesIsAnError(t *testing.T) {
	stub := &mistralStub{pages: nil}
	srv := stub.server(t)
	if _, err := newTestClient(t, srv).ExtractPDF(context.Background(), "s.pdf", []byte("%PDF"), nil, nil); err == nil {
		t.Fatal("expected an error when the provider returns no pages")
	}
}

// TestSignedURLRetriesUntilFileLands reproduces the live failure: the first
// lookup after an upload returned 404 "No file matches the given query", and the
// same id resolved moments later. Without a retry the feature fails
// intermittently on its first call, which is exactly how it behaved.
func TestSignedURLRetriesUntilFileLands(t *testing.T) {
	var urlCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/files", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "file-123"})
	})
	mux.HandleFunc("/v1/files/file-123/url", func(w http.ResponseWriter, r *http.Request) {
		urlCalls++
		if urlCalls < 3 {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"No file matches the given query."}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"url": "https://storage.example/doc.pdf"})
	})
	mux.HandleFunc("/v1/files/file-123", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"deleted": true})
	})
	mux.HandleFunc("/v1/ocr", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pages": []map[string]any{{"index": 0, "markdown": "ok"}},
			"model": "mistral-ocr-latest",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res, err := newTestClient(t, srv).ExtractPDF(context.Background(), "s.pdf", []byte("%PDF"), nil, nil)
	if err != nil {
		t.Fatalf("ExtractPDF should survive a slow-to-land upload: %v", err)
	}
	if urlCalls < 3 {
		t.Errorf("expected retries, got %d call(s)", urlCalls)
	}
	// The provider echoed an alias for a pinned request — both must be visible,
	// or a silent model change looks like no change at all.
	if !strings.Contains(res.Model, "requested "+DefaultModel) {
		t.Errorf("model = %q, want it to record the requested pin too", res.Model)
	}
}

// TestExtractPDFRequestsOnlySelectedPages: the whole point of per-page OCR is
// not paying to re-read pages whose text was already extracted exactly.
func TestExtractPDFRequestsOnlySelectedPages(t *testing.T) {
	var gotPages []int
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/files", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "file-123"})
	})
	mux.HandleFunc("/v1/files/file-123/url", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"url": "https://storage.example/d.pdf"})
	})
	mux.HandleFunc("/v1/files/file-123", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"deleted": true})
	})
	mux.HandleFunc("/v1/ocr", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Pages []int `json:"pages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotPages = body.Pages
		// Answer with ABSOLUTE page indices, as the documented shape implies.
		_ = json.NewEncoder(w).Encode(map[string]any{"pages": []map[string]any{
			{"index": 3, "markdown": "scanned three"},
			{"index": 7, "markdown": "scanned seven"},
		}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res, err := newTestClient(t, srv).ExtractPDF(context.Background(), "mixed.pdf", []byte("%PDF"), []int{3, 7}, nil)
	if err != nil {
		t.Fatalf("ExtractPDF: %v", err)
	}
	if len(gotPages) != 2 || gotPages[0] != 3 || gotPages[1] != 7 {
		t.Errorf("pages sent = %v, want [3 7] — OCR must not be billed for pages that already had text", gotPages)
	}
	if res.ByIndex[3] != "scanned three" || res.ByIndex[7] != "scanned seven" {
		t.Errorf("ByIndex must key on the ORIGINAL page numbers, got %v", res.ByIndex)
	}
}

// TestExtractPDFHandlesPositionalIndexing: some providers number a partial
// response 0..n-1 over the request rather than absolutely. Guessing wrong
// attributes a page's text to the wrong page — silently.
func TestExtractPDFHandlesPositionalIndexing(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/files", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "file-123"})
	})
	mux.HandleFunc("/v1/files/file-123/url", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"url": "https://storage.example/d.pdf"})
	})
	mux.HandleFunc("/v1/files/file-123", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"deleted": true})
	})
	mux.HandleFunc("/v1/ocr", func(w http.ResponseWriter, r *http.Request) {
		// 0 and 1 were NOT requested — so these must be positional.
		_ = json.NewEncoder(w).Encode(map[string]any{"pages": []map[string]any{
			{"index": 0, "markdown": "scanned three"},
			{"index": 1, "markdown": "scanned seven"},
		}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res, err := newTestClient(t, srv).ExtractPDF(context.Background(), "mixed.pdf", []byte("%PDF"), []int{3, 7}, nil)
	if err != nil {
		t.Fatalf("ExtractPDF: %v", err)
	}
	if res.ByIndex[3] != "scanned three" || res.ByIndex[7] != "scanned seven" {
		t.Errorf("positional response not remapped to original pages, got %v", res.ByIndex)
	}
}

// TestExtractPDFBatchesAndReportsProgress is the fix for a job that looked
// hung: a 106-page scan sat at 0/106 for minutes because the whole document
// was one request. Pages are now read in batches, the counter advances, and
// the file is still uploaded only once.
func TestExtractPDFBatchesAndReportsProgress(t *testing.T) {
	var uploads, ocrCalls int
	var batchSizes []int

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/files", func(w http.ResponseWriter, r *http.Request) {
		uploads++
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "file-123"})
	})
	mux.HandleFunc("/v1/files/file-123/url", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"url": "https://storage.example/d.pdf"})
	})
	mux.HandleFunc("/v1/files/file-123", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"deleted": true})
	})
	mux.HandleFunc("/v1/ocr", func(w http.ResponseWriter, r *http.Request) {
		ocrCalls++
		var body struct {
			Pages []int `json:"pages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		batchSizes = append(batchSizes, len(body.Pages))

		out := make([]map[string]any, 0, len(body.Pages))
		for _, p := range body.Pages {
			out = append(out, map[string]any{"index": p, "markdown": fmt.Sprintf("page %d", p)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"pages": out})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// 45 pages → three batches at the configured size.
	pages := make([]int, 45)
	for i := range pages {
		pages[i] = i
	}

	var reported [][2]int
	res, err := newTestClient(t, srv).ExtractPDF(context.Background(), "big.pdf", []byte("%PDF"), pages,
		func(done, total int) { reported = append(reported, [2]int{done, total}) })
	if err != nil {
		t.Fatalf("ExtractPDF: %v", err)
	}

	if uploads != 1 {
		t.Errorf("uploads = %d, want exactly 1 — re-uploading per batch would cost more than the OCR", uploads)
	}
	if ocrCalls < 3 {
		t.Errorf("ocr calls = %d, want the document split into batches", ocrCalls)
	}
	for _, n := range batchSizes {
		if n > pageBatch {
			t.Errorf("a batch carried %d pages, over the %d limit", n, pageBatch)
		}
	}

	// Progress must actually move, and finish at the total.
	if len(reported) < 3 {
		t.Fatalf("progress reported %d time(s), want one per batch", len(reported))
	}
	if first := reported[0]; first[0] == 0 {
		t.Error("the first progress report must show pages already done, not 0")
	}
	if last := reported[len(reported)-1]; last[0] != 45 || last[1] != 45 {
		t.Errorf("final progress = %v, want 45/45", last)
	}

	// Every page must survive the merge, in the right slot.
	if got := len(res.ByIndex); got != 45 {
		t.Errorf("merged %d page(s), want 45", got)
	}
	if res.ByIndex[0] != "page 0" || res.ByIndex[44] != "page 44" {
		t.Error("batched pages were not merged back at their original indices")
	}
	if strings.Count(res.Text, PageBreak) != 44 {
		t.Errorf("joined text has %d page breaks, want 44", strings.Count(res.Text, PageBreak))
	}
}
