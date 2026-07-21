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
	"go.meizon.cloud/registry/pkg/server/api/authn"
	"go.meizon.cloud/registry/pkg/server/api/httpx"
)

func (h *Handler) listControlLibrary(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.ControlsOfFramework(r.Context(), chi.URLParam(r, "ref"))
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (h *Handler) addControlEntry(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	var body struct {
		Code, Name, Description, Domain string
		Items                           []string
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	id, err := h.svc.AddControlEntry(r.Context(), actor, chi.URLParam(r, "ref"), body.Code, body.Name, body.Description, body.Domain, body.Items)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{"id": id.String()})
}

func (h *Handler) deleteControlEntry(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	id, err := gid.ParseGID(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid control id")
		return
	}
	if err := h.svc.DeleteControlEntry(r.Context(), actor, chi.URLParam(r, "ref"), id); err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) addEvidence(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	controlID, err := gid.ParseGID(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid control id")
		return
	}
	var body struct {
		Type                 string `json:"type"`
		Hint                 string `json:"hint"`
		RenewalCadenceMonths *int   `json:"renewalCadenceMonths"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	id, err := h.svc.AddEvidence(r.Context(), actor, chi.URLParam(r, "ref"), controlID, body.Type, body.Hint, body.RenewalCadenceMonths)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{"id": id.String()})
}

func (h *Handler) deleteEvidence(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	id, err := gid.ParseGID(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid evidence id")
		return
	}
	if err := h.svc.DeleteEvidence(r.Context(), actor, chi.URLParam(r, "ref"), id); err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) listPolicyTemplates(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.PolicyTemplatesOfFramework(r.Context(), chi.URLParam(r, "ref"))
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (h *Handler) upsertPolicyTemplate(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	var body struct {
		ID       string   `json:"id"`
		Name     string   `json:"name"`
		Body     string   `json:"body"`
		Controls []string `json:"controls"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	templateID := gid.Nil
	if body.ID != "" {
		parsed, err := gid.ParseGID(body.ID)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid template id")
			return
		}
		templateID = parsed
	}
	id, err := h.svc.UpsertPolicyTemplate(r.Context(), actor, chi.URLParam(r, "ref"), templateID, body.Name, body.Body, body.Controls)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"id": id.String()})
}

func (h *Handler) deletePolicyTemplate(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	id, err := gid.ParseGID(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid template id")
		return
	}
	if err := h.svc.DeletePolicyTemplate(r.Context(), actor, chi.URLParam(r, "ref"), id); err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}
