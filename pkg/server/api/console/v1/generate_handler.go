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

package console_v1

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.meizon.cloud/registry/pkg/fwflat"
	"go.meizon.cloud/registry/pkg/registry"
	"go.meizon.cloud/registry/pkg/server/api/authn"
	"go.meizon.cloud/registry/pkg/server/api/httpx"
)

const maxUploadBytes = 25 << 20 // 25 MiB

// generateFramework is the document-first entry point: upload a source document
// (multipart "document" file) or POST {text, brief} JSON, and the model returns
// a full v2 framework proposal for review. Nothing is persisted here.
func (h *Handler) generateFramework(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())

	docText, brief, _, ok := readGenerationInput(w, r, h.svc)
	if !ok {
		return
	}

	jobID, err := h.svc.StartGenerateJob(r.Context(), actor, docText, brief)
	if err == nil && len(docText.Raw) > 0 {
		h.svc.StageSourceDocument(r.Context(), actor, jobID, docText, docText.Filename, docText.Raw)
	}
	if err != nil {
		if errors.Is(err, registry.ErrLLMNotConfigured) {
			httpx.Error(w, http.StatusPreconditionFailed, "LLM is not configured — ask a superadmin to set it up in Settings")
			return
		}
		httpx.ServiceError(w, err)
		return
	}
	// Report the OCR split up front: how many pages will be read by OCR, out of
	// how many total. Waiting for the job to say so hides the cost until it has
	// already been incurred.
	httpx.JSON(w, http.StatusAccepted, map[string]any{
		"jobId":    jobID,
		"pages":    docText.PageCount(),
		"ocrPages": len(docText.OCRPages),
	})
}

// generateStatus reports the progress of a staged generation job (new-framework
// or next-version). The finished status carries the reviewable proposal.
func (h *Handler) generateStatus(w http.ResponseWriter, r *http.Request) {
	st, err := h.svc.IngestJobStatus(r.Context(), chi.URLParam(r, "jobId"))
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, st)
}

// readGenerationInput reads the source document from either a multipart upload
// (file "document") or a JSON body {text, brief, version}. It returns the
// extracted text, the brief, and the optional target version. On error it has
// already written the response and returns ok=false.
func readGenerationInput(w http.ResponseWriter, r *http.Request, svc *registry.Service) (in registry.DocInput, brief, version string, ok bool) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
			httpx.Error(w, http.StatusBadRequest, "cannot read upload")
			return in, "", "", false
		}
		brief = r.FormValue("brief")
		version = r.FormValue("version")
		file, header, err := r.FormFile("document")
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "a document file is required")
			return in, "", "", false
		}
		defer func() { _ = file.Close() }()

		data, err := io.ReadAll(io.LimitReader(file, maxUploadBytes))
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "cannot read the document")
			return in, "", "", false
		}
		// Text layer first; a scan falls back to OCR inside the job, and says so
		// here if OCR is not configured.
		prepared, err := svc.PrepareDocument(r.Context(), header.Filename, data)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, err.Error())
			return in, "", "", false
		}
		return prepared, brief, version, true
	}

	var body struct{ Text, Brief, Version string }
	if !httpx.DecodeJSON(w, r, &body) {
		return in, "", "", false
	}
	return registry.DocInput{Text: body.Text}, body.Brief, body.Version, true
}

// exportFramework downloads the framework as a flat meizon-framework/v2 file —
// the shareable, re-importable artifact.
func (h *Handler) exportFramework(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "ref")
	// ?lang= selects a translation; absent means the canonical record.
	lang := r.URL.Query().Get("lang")
	doc, err := h.svc.ExportFlatIn(r.Context(), ref, lang)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "cannot encode framework")
		return
	}
	filename := ref
	if doc.Version != "" {
		filename += "-" + doc.Version
	}
	if doc.Language != "" {
		filename += "-" + doc.Language
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename+".json"))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// generateNextVersion produces the next version of an existing framework from a
// revised document, diffed (added/modified/removed/unchanged) against the
// current latest version. Nothing is persisted here.
func (h *Handler) generateNextVersion(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	ref := chi.URLParam(r, "ref")

	docText, brief, version, ok := readGenerationInput(w, r, h.svc)
	if !ok {
		return
	}
	if strings.TrimSpace(version) == "" {
		httpx.Error(w, http.StatusBadRequest, "a target version is required")
		return
	}

	jobID, err := h.svc.StartNextVersionJob(r.Context(), actor, ref, docText, brief, version)
	if err != nil {
		if errors.Is(err, registry.ErrLLMNotConfigured) {
			httpx.Error(w, http.StatusPreconditionFailed, "LLM is not configured — ask a superadmin to set it up in Settings")
			return
		}
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, map[string]string{"jobId": jobID})
}

// acceptNextVersion imports the reviewed next-version document as a new DRAFT
// version (origin=ai) on the existing framework.
func (h *Handler) acceptNextVersion(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	ref := chi.URLParam(r, "ref")

	var body struct {
		Version  string           `json:"version"`
		Document fwflat.Framework `json:"document"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	out, err := h.svc.AcceptNextVersion(r.Context(), actor, ref, body.Version, &body.Document)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{
		"id": ref, "version": out.Version, "versionId": out.VersionID.String(),
	})
}

// acceptGeneratedFramework imports the reviewed (possibly edited) generated
// document as a DRAFT with origin=ai.
func (h *Handler) acceptGeneratedFramework(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	var doc fwflat.Framework
	if !httpx.DecodeJSON(w, r, &doc) {
		return
	}
	// jobId links the framework to the source file staged at upload time.
	out, err := h.svc.AcceptGeneratedFlat(r.Context(), actor, &doc, r.URL.Query().Get("jobId"))
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{
		"id": doc.ID, "version": out.Version, "versionId": out.VersionID.String(),
	})
}

// translations lists the languages a framework carries.
func (h *Handler) translations(w http.ResponseWriter, r *http.Request) {
	view, err := h.svc.TranslationsOf(r.Context(), chi.URLParam(r, "ref"))
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, view)
}

// addTranslation starts an async translation into the requested language and
// returns a job id; the console polls the shared ingest status endpoint.
func (h *Handler) addTranslation(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	var body struct {
		Language string `json:"language"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	jobID, err := h.svc.StartTranslateJob(r.Context(), actor, chi.URLParam(r, "ref"), body.Language)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, map[string]string{"jobId": jobID})
}

// sourceDocument serves the file a framework was generated from. Inline
// disposition by default so the console can preview it in place; ?download=1
// forces a save.
func (h *Handler) sourceDocument(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "ref")
	view, content, err := h.svc.SourceDocumentFor(r.Context(), ref)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}

	disposition := "inline"
	if r.URL.Query().Get("download") != "" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Type", view.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", disposition, view.Filename))
	w.Header().Set("Content-Length", strconv.FormatInt(view.ByteSize, 10))
	// The stored file never changes, and its digest is already the identity of
	// the bytes — so it caches on the hash rather than being re-sent per view.
	w.Header().Set("ETag", `"`+view.SHA256+`"`)
	if match := r.Header.Get("If-None-Match"); match == `"`+view.SHA256+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

// listJobs returns recent pipeline runs so a user can check on work started in
// another tab, or before a restart.
func (h *Handler) listJobs(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	jobs, err := h.svc.JobList(r.Context(), limit)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, jobs)
}

// translateMissing starts an English translation for every framework that has
// no canonical record yet. Frameworks already in English are skipped.
func (h *Handler) translateMissing(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	started, err := h.svc.TranslateMissingCanonical(r.Context(), actor)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, map[string]any{
		"started": started,
		"count":   len(started),
	})
}
