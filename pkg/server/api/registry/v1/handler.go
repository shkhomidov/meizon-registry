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

// Package registry_v1 serves the machine-to-machine distribution API. It is
// read-only, bearer-token authenticated, region-scoped, ETag/HEAD aware and
// enforces the copyright gate via the registry service.
package registry_v1

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/registry"
	"go.meizon.cloud/registry/pkg/server/api/authn"
)

// Handler serves /api/registry/v1.
type Handler struct {
	svc *registry.Service
}

// NewHandler builds the distribution handler.
func NewHandler(svc *registry.Service) *Handler {
	return &Handler{svc: svc}
}

// Routes returns the chi router for the distribution API, already wrapped in the
// bearer-token middleware.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(authn.NewBearerTokenMiddleware(h.svc))

	r.Get("/catalog", h.catalog)
	r.Head("/catalog", h.catalog)
	r.Get("/changes", h.changes)
	r.Get("/frameworks/{id}", h.framework)
	r.Get("/frameworks/{id}/versions/{version}", h.bundle)
	r.Head("/frameworks/{id}/versions/{version}", h.bundle)
	r.Get("/frameworks/{id}/versions/{version}/seed", h.seed)
	// The published version's audit (QA) template; ?lang selects a translation.
	r.Get("/frameworks/{id}/versions/{version}/qa", h.qa)

	// Cross-mapping sets: a catalog for cold discovery, and fetch-by-pair, which
	// the consumer also reaches from mapping_published feed events.
	r.Get("/mappings", h.mappingCatalog)
	r.Get("/mappings/{source}/{sourceVersion}/{target}/{targetVersion}", h.mappingSet)

	return r
}

func (h *Handler) catalog(w http.ResponseWriter, r *http.Request) {
	tc, ok := authn.TokenContextFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	region := r.URL.Query().Get("region")

	var since *time.Time
	if raw := r.URL.Query().Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid since timestamp; expected RFC3339")
			return
		}
		since = &t
	}

	entries, err := h.svc.Catalog(r.Context(), tc, region, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot load catalog")
		return
	}
	if entries == nil {
		entries = []registry.CatalogEntry{}
	}

	writeJSON(w, r, http.StatusOK, entries)
}

// changes serves the cursor-based change feed: "what happened after seq N that I
// am allowed to see". A consumer persists nextSeq only after it has successfully
// imported the page, so a crash re-delivers rather than skips.
func (h *Handler) changes(w http.ResponseWriter, r *http.Request) {
	tc, ok := authn.TokenContextFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var since int64
	if raw := r.URL.Query().Get("since"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid since cursor; expected a non-negative integer sequence number")
			return
		}
		since = n
	}

	var limit int
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit; expected a positive integer")
			return
		}
		limit = n
	}

	feed, err := h.svc.Changes(r.Context(), tc, since, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot load changes")
		return
	}

	writeJSON(w, r, http.StatusOK, feed)
}

// mappingCatalog lists the published cross-mapping sets this token may see.
func (h *Handler) mappingCatalog(w http.ResponseWriter, r *http.Request) {
	tc, ok := authn.TokenContextFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	entries, err := h.svc.MappingCatalog(r.Context(), tc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot load mapping catalog")
		return
	}
	writeJSON(w, r, http.StatusOK, entries)
}

// mappingSet serves the exact signed bytes of one mapping set. The response is
// the stored document verbatim, so the consumer verifies precisely what was
// signed.
func (h *Handler) mappingSet(w http.ResponseWriter, r *http.Request) {
	tc, ok := authn.TokenContextFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	doc, err := h.svc.MappingSetDocument(r.Context(), tc,
		chi.URLParam(r, "source"), chi.URLParam(r, "sourceVersion"),
		chi.URLParam(r, "target"), chi.URLParam(r, "targetVersion"))
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/json")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(doc)
}

func (h *Handler) framework(w http.ResponseWriter, r *http.Request) {
	tc, ok := authn.TokenContextFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	framework, versions, err := h.svc.Versions(r.Context(), tc, chi.URLParam(r, "id"))
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	type versionView struct {
		Version     string     `json:"version"`
		Status      string     `json:"status"`
		ContentHash string     `json:"contentHash"`
		PublishedAt *time.Time `json:"publishedAt,omitempty"`
		// Languages this version can be fetched in (source + translations); pull
		// the localized seed/QA with ?lang=<code>.
		Languages []string `json:"languages,omitempty"`
	}
	out := struct {
		ID        string        `json:"id"`
		Name      string        `json:"name"`
		Region    string        `json:"region"`
		Authority string        `json:"authority"`
		License   string        `json:"license"`
		Versions  []versionView `json:"versions"`
	}{
		ID: framework.ReferenceID, Name: framework.Name, Region: framework.Region,
		Authority: framework.Authority, License: framework.License,
	}
	for _, v := range versions {
		if v.Status != coredata.FrameworkVersionStatusPublished {
			continue
		}
		langs, _ := h.svc.VersionLanguages(r.Context(), framework, v.ID) // best-effort discovery
		out.Versions = append(out.Versions, versionView{
			Version: v.Version, Status: string(v.Status), ContentHash: v.ContentHash,
			PublishedAt: v.PublishedAt, Languages: langs,
		})
	}

	writeJSON(w, r, http.StatusOK, out)
}

func (h *Handler) bundle(w http.ResponseWriter, r *http.Request) {
	tc, ok := authn.TokenContextFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	bundle, err := h.svc.Bundle(r.Context(), tc, chi.URLParam(r, "id"), chi.URLParam(r, "version"))
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	if bundle.Signature != nil {
		etag := `"` + bundle.Signature.ContentHash + `"`
		w.Header().Set("ETag", etag)
		if match := r.Header.Get("If-None-Match"); match == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	writeJSON(w, r, http.StatusOK, bundle)
}

func (h *Handler) seed(w http.ResponseWriter, r *http.Request) {
	tc, ok := authn.TokenContextFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	seed, err := h.svc.Seed(r.Context(), tc, chi.URLParam(r, "id"), chi.URLParam(r, "version"), r.URL.Query().Get("lang"))
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	writeJSON(w, r, http.StatusOK, seed)
}

// qa serves the published version's audit (QA) template. `?lang` selects a
// translation, falling back to the canonical template. 404 if the version has no
// template marked ready.
func (h *Handler) qa(w http.ResponseWriter, r *http.Request) {
	tc, ok := authn.TokenContextFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	tpl, err := h.svc.QATemplateDistribution(r.Context(), tc,
		chi.URLParam(r, "id"), chi.URLParam(r, "version"), r.URL.Query().Get("lang"))
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	writeJSON(w, r, http.StatusOK, tpl)
}

func (h *Handler) writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, coredata.ErrResourceNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, registry.ErrNotDistributable):
		writeError(w, http.StatusForbidden, "not distributable")
	default:
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
