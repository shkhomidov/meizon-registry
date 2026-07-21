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

// Package ocr turns image-based (scanned) PDFs into text, so a standard that
// exists only as a scan can still go through the ingestion pipeline.
//
// Only Mistral OCR is implemented. It is a strict FALLBACK: the caller tries
// the PDF's own text layer first and comes here only on
// docextract.ErrNoTextLayer. The text layer is free and exact; OCR is slow and
// billable, so running it on a document that did not need it is a cost bug.
package ocr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ProviderMistral is the only implemented provider.
const ProviderMistral = "mistral"

// DefaultModel is pinned, not "-latest", on purpose: a silent model revision
// could change page segmentation, and page alignment is load-bearing — the
// reviewer's "jump to the page this came from" reads these page breaks.
const DefaultModel = "mistral-ocr-2505"

const defaultBaseURL = "https://api.mistral.ai"

// PageBreak must match docextract.PageBreak. OCR output is joined with it so an
// OCR'd document behaves exactly like a text one downstream — same chunker,
// same page jumps, same review UI.
const PageBreak = "\f"

// Timeout is generous: a 49-page scan takes minutes, well beyond any per-request
// LLM default.
const Timeout = 15 * time.Minute

// Client runs OCR against a provider.
type Client interface {
	Provider() string
	// ExtractPDF returns the document's text, pages joined with PageBreak.
	// pages selects which 0-based pages to read; nil means the whole document.
	// onProgress, when set, is called after each batch with pages done so far.
	ExtractPDF(ctx context.Context, filename string, data []byte, pages []int, onProgress Progress) (Result, error)
}

// Progress reports pages completed out of the total requested.
type Progress func(done, total int)

// pageBatch is how many pages go in one OCR request.
//
// Sized for FEEDBACK, not throughput: a 106-page document read in a single call
// leaves the progress counter at 0 for several minutes, which is indistinguishable
// from a hung job — the reason this batching exists. Smaller batches also mean a
// transient failure costs one batch rather than the whole document.
const pageBatch = 20

// Result is the outcome of one OCR pass. ByIndex maps each requested page's
// 0-based index in the ORIGINAL document to its text, so a caller that only
// OCR'd part of a document can splice the results back into the right slots.
type Result struct {
	Text    string
	Pages   int
	Model   string
	ByIndex map[int]string
}

// Config configures a client.
type Config struct {
	Provider string
	APIKey   string
	Model    string
	BaseURL  string
}

// New builds a client for the configured provider.
func New(cfg Config) (Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("an OCR API key is required")
	}
	switch cfg.Provider {
	case ProviderMistral, "":
		return &mistralClient{
			apiKey:  cfg.APIKey,
			model:   orDefault(cfg.Model, DefaultModel),
			baseURL: strings.TrimRight(orDefault(cfg.BaseURL, defaultBaseURL), "/"),
			hc:      &http.Client{Timeout: Timeout},
		}, nil
	default:
		return nil, fmt.Errorf("unknown OCR provider %q", cfg.Provider)
	}
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

type mistralClient struct {
	apiKey  string
	model   string
	baseURL string
	hc      *http.Client
	// lastUpload describes the most recent upload response, so a downstream
	// failure can say what the provider actually accepted.
	lastUpload string
}

func (c *mistralClient) Provider() string { return ProviderMistral }

// ExtractPDF runs the documented three-step flow:
//
//	POST /v1/files            (purpose=ocr)      → file id
//	GET  /v1/files/{id}/url   (expiry=1 hour)    → signed URL
//	POST /v1/ocr              (document_url)     → page-wise markdown
//
// The upload route is used rather than a public URL because the document is
// customer data: it must never be hosted somewhere anonymously reachable. The
// signed URL is short-lived, and the uploaded file is deleted afterwards so the
// copy at the provider does not outlive the request that needed it.
func (c *mistralClient) ExtractPDF(ctx context.Context, filename string, data []byte, pages []int, onProgress Progress) (Result, error) {
	if len(data) == 0 {
		return Result{}, fmt.Errorf("the document is empty")
	}

	// Uploaded ONCE even though the document is read in several batches —
	// re-uploading a 100-page PDF per batch would cost more than the OCR.
	fileID, err := c.uploadFile(ctx, filename, data)
	if err != nil {
		return Result{}, err
	}
	// Best-effort cleanup — a failure here must not fail the extraction the user
	// is waiting on, but it is still attempted on every path.
	defer func() { _ = c.deleteFile(context.WithoutCancel(ctx), fileID) }()

	signedURL, err := c.signedURL(ctx, fileID)
	if err != nil {
		return Result{}, err
	}

	// Whole document in one call: no page list means no way to split it.
	if len(pages) == 0 {
		res, err := c.ocr(ctx, signedURL, nil)
		if err == nil && onProgress != nil {
			onProgress(res.Pages, res.Pages)
		}
		return res, err
	}

	merged := Result{ByIndex: map[int]string{}, Model: c.model}
	for start := 0; start < len(pages); start += pageBatch {
		end := min(start+pageBatch, len(pages))
		batch, err := c.ocr(ctx, signedURL, pages[start:end])
		if err != nil {
			return Result{}, fmt.Errorf("pages %d-%d of %d: %w", start+1, end, len(pages), err)
		}
		for idx, text := range batch.ByIndex {
			merged.ByIndex[idx] = text
		}
		merged.Pages += batch.Pages
		if batch.Model != "" {
			merged.Model = batch.Model
		}
		if onProgress != nil {
			onProgress(end, len(pages))
		}
	}

	merged.Text = joinByIndex(merged.ByIndex)
	return merged, nil
}

// joinByIndex renders pages in document order, keeping gaps as empty pages so
// page numbering is preserved.
func joinByIndex(byIndex map[int]string) string {
	maxIdx := -1
	for idx := range byIndex {
		if idx > maxIdx {
			maxIdx = idx
		}
	}
	pages := make([]string, 0, maxIdx+1)
	for i := 0; i <= maxIdx; i++ {
		pages = append(pages, byIndex[i])
	}
	return strings.Join(pages, PageBreak)
}

func (c *mistralClient) uploadFile(ctx context.Context, filename string, data []byte) (string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("purpose", "ocr"); err != nil {
		return "", err
	}
	part, err := w.CreateFormFile("file", orDefault(filename, "document.pdf"))
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/files", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", w.FormDataContentType())

	var out struct {
		ID       string `json:"id"`
		Object   string `json:"object"`
		Filename string `json:"filename"`
		Purpose  string `json:"purpose"`
		Bytes    int64  `json:"bytes"`
	}
	if err := c.do(req, &out); err != nil {
		return "", fmt.Errorf("ocr upload: %w", err)
	}
	if out.ID == "" {
		return "", fmt.Errorf("ocr upload: the provider returned no file id")
	}
	c.lastUpload = fmt.Sprintf("id=%s object=%s purpose=%s bytes=%d", out.ID, out.Object, out.Purpose, out.Bytes)
	return out.ID, nil
}

// signedURLAttempts covers the gap between an upload succeeding and the file
// becoming queryable. Observed against the live API: the first lookup after an
// upload can return 404 "No file matches the given query", and a moment later
// the same id resolves fine. Without this the whole feature fails intermittently
// on its very first call.
const signedURLAttempts = 4

func (c *mistralClient) signedURL(ctx context.Context, fileID string) (string, error) {
	// One hour: the OCR call happens immediately, so a longer-lived URL to a
	// customer document is exposure with no benefit.
	endpoint := fmt.Sprintf("%s/v1/files/%s/url?expiry=1", c.baseURL, url.PathEscape(fileID))

	var last error
	for attempt := 0; attempt < signedURLAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)

		var out struct {
			URL string `json:"url"`
		}
		if err := c.do(req, &out); err != nil {
			last = err
			if isNotFound(err) {
				continue // the upload has not landed yet
			}
			return "", fmt.Errorf("ocr signed url (%s): %w", c.lastUpload, err)
		}
		if out.URL == "" {
			return "", fmt.Errorf("ocr signed url: the provider returned no url")
		}
		return out.URL, nil
	}
	return "", fmt.Errorf("ocr signed url (%s): file never became available: %w", c.lastUpload, last)
}

// isNotFound reports a 404 from the provider.
func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "returned 404")
}

func (c *mistralClient) ocr(ctx context.Context, documentURL string, requested []int) (Result, error) {
	payload := map[string]any{
		"model": c.model,
		"document": map[string]string{
			"type":         "document_url",
			"document_url": documentURL,
		},
		// Images would balloon the response for no gain: only text is wanted.
		"include_image_base64": false,
	}
	// Only the pages that actually need reading. Page numbers are 0-based, the
	// same basis docextract reports, so no translation is needed.
	if len(requested) > 0 {
		payload["pages"] = requested
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Result{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/ocr", bytes.NewReader(raw))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	var out struct {
		Pages []struct {
			Index    int    `json:"index"`
			Markdown string `json:"markdown"`
		} `json:"pages"`
		Model string `json:"model"`
	}
	if err := c.do(req, &out); err != nil {
		return Result{}, fmt.Errorf("ocr: %w", err)
	}
	if len(out.Pages) == 0 {
		return Result{}, fmt.Errorf("ocr: the provider returned no pages")
	}

	// Order by the provider's own page index rather than trusting array order:
	// a page landing in the wrong slot would silently misalign every source
	// citation in the reviewer UI.
	byIndex := map[int]string{}
	maxIdx := -1
	for _, p := range out.Pages {
		byIndex[p.Index] = p.Markdown
		if p.Index > maxIdx {
			maxIdx = p.Index
		}
	}

	// When a subset was requested, the provider may index the response either
	// absolutely (the original page numbers) or positionally (0..n-1 over the
	// request). Detect which, because guessing wrong silently attributes a
	// page's text to the wrong page.
	if len(requested) > 0 && !indicesMatch(byIndex, requested) {
		positional := map[int]string{}
		for i, want := range requested {
			if text, ok := byIndex[i]; ok {
				positional[want] = text
			}
		}
		byIndex = positional
		maxIdx = -1
		for idx := range byIndex {
			if idx > maxIdx {
				maxIdx = idx
			}
		}
	}

	pageTexts := make([]string, 0, len(byIndex))
	for i := 0; i <= maxIdx; i++ {
		pageTexts = append(pageTexts, byIndex[i]) // a gap stays an empty page, keeping numbering
	}
	pages := pageTexts

	// The live API echoes an alias ("mistral-ocr-latest") even for a pinned
	// request, so record both: what was requested is what a future run can be
	// compared against, and hiding the difference would make a silent model
	// change look like no change at all.
	model := c.model
	if out.Model != "" && out.Model != c.model {
		model = fmt.Sprintf("%s (requested %s)", out.Model, c.model)
	}
	return Result{
		Text:    strings.Join(pages, PageBreak),
		Pages:   len(out.Pages),
		Model:   model,
		ByIndex: byIndex,
	}, nil
}

func (c *mistralClient) deleteFile(ctx context.Context, fileID string) error {
	endpoint := fmt.Sprintf("%s/v1/files/%s", c.baseURL, url.PathEscape(fileID))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	var discard map[string]any
	return c.do(req, &discard)
}

// do executes a request and decodes JSON, scrubbing anything credential-shaped
// from errors. The key travels in a header, never a query string, but a signed
// URL carries its own token — and transport errors embed the full URL.
func (c *mistralClient) do(req *http.Request, out any) error {
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %s", RedactSecrets(err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))

	if resp.StatusCode >= 300 {
		return fmt.Errorf("provider returned %d: %s", resp.StatusCode,
			RedactSecrets(strings.TrimSpace(string(data[:min(len(data), 300)]))))
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("cannot decode response: %w", err)
	}
	return nil
}

// RedactSecrets strips credential-shaped values out of text that may reach a
// user or a log. Exported so callers wrapping OCR errors can apply it too.
func RedactSecrets(text string) string {
	return secretPattern.ReplaceAllString(text, "${1}=REDACTED")
}

var secretPattern = regexp.MustCompile(`(?i)\b(key|api_key|apikey|access_token|token|signature|Signature|X-Amz-Signature)=[^\s&"']+`)

// indicesMatch reports whether every returned page index is one that was
// requested — the signal that the provider indexed its response absolutely.
func indicesMatch(byIndex map[int]string, requested []int) bool {
	want := make(map[int]bool, len(requested))
	for _, r := range requested {
		want[r] = true
	}
	for idx := range byIndex {
		if !want[idx] {
			return false
		}
	}
	return true
}
