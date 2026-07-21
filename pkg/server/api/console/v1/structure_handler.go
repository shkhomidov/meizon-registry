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
	"go.meizon.cloud/registry/pkg/fwschema"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/registry"
	"go.meizon.cloud/registry/pkg/server/api/authn"
	"go.meizon.cloud/registry/pkg/server/api/httpx"
)

// latestVersionFor resolves a framework reference to its newest version id.
func (h *Handler) latestVersionFor(r *http.Request) (gid.GID, error) {
	framework, err := h.svc.FrameworkByReference(r.Context(), chi.URLParam(r, "ref"))
	if err != nil {
		return gid.Nil, err
	}
	return h.svc.LatestVersionID(r.Context(), framework.ID)
}

func (h *Handler) getStructure(w http.ResponseWriter, r *http.Request) {
	versionID, err := h.latestVersionFor(r)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	tree, err := h.svc.StructureOf(r.Context(), versionID)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, tree)
}

func (h *Handler) addCategory(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	versionID, err := h.latestVersionFor(r)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	var body struct{ Code, Name, Description string }
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	if err := h.svc.AddCategory(r.Context(), actor, versionID, body.Code, body.Name, body.Description, "manual"); err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

func (h *Handler) addRequirement(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	versionID, err := h.latestVersionFor(r)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	var body struct{ Category, Code, Number, Title, Description, ItemType, Guidance string }
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	if err := h.svc.AddRequirement(r.Context(), actor, versionID, body.Category, body.Code, body.Number, body.Title, body.Description, body.ItemType, body.Guidance, "manual"); err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

func (h *Handler) deleteStructureNode(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	versionID, err := h.latestVersionFor(r)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	if err := h.svc.DeleteStructureNode(r.Context(), actor, versionID, chi.URLParam(r, "level"), chi.URLParam(r, "code")); err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) addItemMapping(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	versionID, err := h.latestVersionFor(r)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	var body struct{ Relation, Framework, Version, Item, Notes string }
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	id, err := h.svc.AddItemMapping(r.Context(), actor, registry.AddItemMappingRequest{
		VersionID: versionID, ItemCode: chi.URLParam(r, "code"),
		Relation: body.Relation, TargetFramework: body.Framework,
		TargetVersion: body.Version, TargetItem: body.Item, Notes: body.Notes,
	})
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{"id": id.String()})
}

func (h *Handler) removeItemMapping(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	versionID, err := h.latestVersionFor(r)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	mappingID, err := gid.ParseGID(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid mapping id")
		return
	}
	if err := h.svc.RemoveItemMapping(r.Context(), actor, versionID, mappingID); err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// importFramework accepts a v2 exchange JSON document (UI upload) and creates a
// framework with a full draft.
func (h *Handler) importFramework(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	var doc fwschema.Framework
	if !httpx.DecodeJSON(w, r, &doc) {
		return
	}
	out, err := h.svc.ImportFrameworkDoc(r.Context(), actor, &doc)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{
		"id": doc.ID, "version": out.Version, "versionId": out.VersionID.String(),
	})
}

func (h *Handler) coverage(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	if source == "" {
		httpx.Error(w, http.StatusBadRequest, "source is required")
		return
	}
	report, err := h.svc.CoverageFor(r.Context(), source, r.URL.Query().Get("target"))
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, report)
}

func (h *Handler) unresolvedMappings(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.UnresolvedStubSummary(r.Context())
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, rows)
}

func (h *Handler) resolveMappings(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	n, err := h.svc.ResolveAllStubs(r.Context(), actor)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]int{"resolved": n})
}
