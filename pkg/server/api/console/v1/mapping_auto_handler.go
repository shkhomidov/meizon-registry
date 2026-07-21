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
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/registry"
	"go.meizon.cloud/registry/pkg/server/api/authn"
	"go.meizon.cloud/registry/pkg/server/api/httpx"
)

// autoMap starts an LLM cross-mapping pass against a target framework and
// returns a job id; the console polls the shared generate-status endpoint.
func (h *Handler) autoMap(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	var body struct {
		Target   string `json:"target"`
		NodeKind string `json:"nodeKind"`
		Remap    bool   `json:"remap"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	jobID, err := h.svc.StartAutoMapJob(r.Context(), actor, registry.AutoMapRequest{
		SourceRef: chi.URLParam(r, "ref"),
		TargetRef: body.Target,
		NodeKind:  body.NodeKind,
		Remap:     body.Remap,
	})
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, map[string]string{"jobId": jobID})
}

// mappingProposals returns proposals awaiting review plus the run history.
func (h *Handler) mappingProposals(w http.ResponseWriter, r *http.Request) {
	view, err := h.svc.MappingReviewOf(r.Context(), chi.URLParam(r, "ref"), r.URL.Query().Get("status"))
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, view)
}

// decideProposals accepts or rejects a batch of proposals.
func (h *Handler) decideProposals(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	var body struct {
		IDs    []string `json:"ids"`
		Reject bool     `json:"reject"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	ids := make([]gid.GID, 0, len(body.IDs))
	for _, raw := range body.IDs {
		id, err := gid.ParseGID(raw)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid proposal id")
			return
		}
		ids = append(ids, id)
	}

	ref := chi.URLParam(r, "ref")
	var n int
	var err error
	if body.Reject {
		n, err = h.svc.RejectMappingProposals(r.Context(), actor, ref, ids)
	} else {
		n, err = h.svc.AcceptMappingProposals(r.Context(), actor, ref, ids)
	}
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]int{"decided": n})
}

// mappingsBetween lists saved mappings from one framework to another.
func (h *Handler) mappingsBetween(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.MappingsBetween(r.Context(), r.URL.Query().Get("source"), r.URL.Query().Get("target"))
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, rows)
}

// updateMapping edits a saved mapping's relation and notes.
func (h *Handler) updateMapping(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	var body struct {
		NodeKind string `json:"nodeKind"`
		Relation string `json:"relation"`
		Notes    string `json:"notes"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	id, err := gid.ParseGID(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid mapping id")
		return
	}
	if err := h.svc.UpdateMapping(r.Context(), actor, chi.URLParam(r, "ref"), body.NodeKind, id, body.Relation, body.Notes); err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// deleteMapping removes a saved mapping of either kind.
func (h *Handler) deleteMapping(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	id, err := gid.ParseGID(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid mapping id")
		return
	}
	kind := r.URL.Query().Get("nodeKind")
	if err := h.svc.DeleteMapping(r.Context(), actor, chi.URLParam(r, "ref"), kind, id); err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}
