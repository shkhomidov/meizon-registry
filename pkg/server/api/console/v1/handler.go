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

// Package console_v1 is the authoring console's data API. Every route requires a
// session; mutations run with the session identity as the actor so the service
// enforces RBAC, region scope and separation of duties uniformly.
package console_v1

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/registry"
	"go.meizon.cloud/registry/pkg/server/api/authn"
	"go.meizon.cloud/registry/pkg/server/api/httpx"
)

// Handler serves /api/console/v1.
type Handler struct {
	svc    *registry.Service
	cookie *authn.SessionCookie
}

// NewHandler builds the console handler.
func NewHandler(svc *registry.Service, cookie *authn.SessionCookie) *Handler {
	return &Handler{svc: svc, cookie: cookie}
}

// Routes returns the router, session-gated.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(h.cookie.Middleware)
	r.Use(requireSession)

	r.Get("/frameworks", h.listFrameworks)
	r.Post("/frameworks", h.createFramework)
	r.Post("/frameworks/import", h.importFramework)
	r.Post("/frameworks/generate", h.generateFramework)
	r.Get("/frameworks/generate/status/{jobId}", h.generateStatus)
	r.Get("/jobs", h.listJobs)
	r.Post("/admin/translate-missing", h.translateMissing)
	r.Post("/frameworks/generate/accept", h.acceptGeneratedFramework)
	r.Post("/frameworks/{ref}/next-version/generate", h.generateNextVersion)
	r.Post("/frameworks/{ref}/next-version/accept", h.acceptNextVersion)
	r.Get("/frameworks/{ref}", h.getFramework)
	r.Get("/frameworks/{ref}/export", h.exportFramework)
	r.Get("/frameworks/{ref}/source", h.sourceDocument)
	r.Get("/frameworks/{ref}/translations", h.translations)
	r.Post("/frameworks/{ref}/translations", h.addTranslation)
	r.Post("/frameworks/{ref}/controls", h.addControl)
	r.Post("/frameworks/{ref}/submit", h.transition(transitionSubmit))
	r.Post("/frameworks/{ref}/approve", h.transition(transitionApprove))
	r.Post("/frameworks/{ref}/publish", h.transition(transitionPublish))
	// Reject sends a submission back to DRAFT; deprecate retires a published
	// version and is what tells consumers to stop using it. Both existed in the
	// service with no way to reach them — a moderator could only ever say yes.
	r.Post("/frameworks/{ref}/reject", h.transition(transitionReject))
	r.Post("/frameworks/{ref}/deprecate", h.transition(transitionDeprecate))

	// Universal hierarchy + cross-mappings (all UI-managed).
	r.Get("/frameworks/{ref}/structure", h.getStructure)
	r.Post("/frameworks/{ref}/categories", h.addCategory)
	r.Post("/frameworks/{ref}/requirements", h.addRequirement)
	r.Delete("/frameworks/{ref}/structure/{level}/{code}", h.deleteStructureNode)
	r.Post("/frameworks/{ref}/items/{code}/mappings", h.addItemMapping)
	r.Delete("/frameworks/{ref}/mappings/{id}", h.removeItemMapping)
	r.Get("/coverage", h.coverage)
	r.Get("/mappings", h.mappingsBetween)
	r.Patch("/frameworks/{ref}/mappings/{id}", h.updateMapping)
	r.Delete("/frameworks/{ref}/mappings/{id}/any", h.deleteMapping)

	// LLM-assisted cross-mapping: the model proposes, the auditor decides.
	r.Post("/frameworks/{ref}/automap", h.autoMap)
	r.Get("/frameworks/{ref}/mapping-proposals", h.mappingProposals)
	r.Post("/frameworks/{ref}/mapping-proposals/decide", h.decideProposals)

	// AI-assisted authoring: the LLM proposes, the auditor decides.
	r.Get("/ai/status", h.aiStatus)
	r.Post("/frameworks/{ref}/ai/generate", h.aiGenerate)
	r.Post("/frameworks/{ref}/ai/accept", h.aiAccept)

	// Catalogs: control library, evidence guidance, policy templates.
	r.Get("/frameworks/{ref}/controls-library", h.listControlLibrary)
	r.Post("/frameworks/{ref}/controls-library", h.addControlEntry)
	r.Delete("/frameworks/{ref}/controls-library/{id}", h.deleteControlEntry)
	r.Post("/frameworks/{ref}/controls-library/{id}/evidence", h.addEvidence)
	r.Delete("/frameworks/{ref}/evidence/{id}", h.deleteEvidence)
	r.Get("/frameworks/{ref}/policy-templates", h.listPolicyTemplates)
	r.Post("/frameworks/{ref}/policy-templates", h.upsertPolicyTemplate)
	r.Delete("/frameworks/{ref}/policy-templates/{id}", h.deletePolicyTemplate)

	r.Get("/audit", h.audit)

	// Superadmin governance surface.
	r.Route("/admin", func(ar chi.Router) {
		ar.Use(h.requireSuperAdmin)
		ar.Get("/users", h.listUsers)
		ar.Post("/users", h.createUser)
		ar.Post("/users/role", h.assignRole)
		ar.Get("/keys", h.listKeys)
		ar.Post("/keys", h.generateKey)
		ar.Get("/tokens", h.listTokens)
		ar.Post("/tokens", h.issueToken)
		// Organizations: the keyless-sync review queue. Approving an org lets it
		// sync every published public framework instantly, with no token issued.
		ar.Get("/organizations", h.listOrganizations)
		ar.Post("/organizations", h.registerOrganization)
		ar.Post("/organizations/{tenant}/approve", h.approveOrganization)
		ar.Post("/organizations/{tenant}/suspend", h.suspendOrganization)
		ar.Get("/mappings/unresolved", h.unresolvedMappings)
		ar.Post("/mappings/resolve", h.resolveMappings)
		ar.Get("/settings/llm", h.getLLMSettings)
		ar.Put("/settings/llm", h.putLLMSettings)
		ar.Post("/settings/llm/test", h.testLLMSettings)
	})

	return r
}

func (h *Handler) requireSuperAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := authn.IdentityFrom(r.Context())
		viewer, err := h.svc.Viewer(r.Context(), id)
		if err != nil || viewer.Role != "superadmin" {
			httpx.Error(w, http.StatusForbidden, "superadmin only")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.svc.ListUsers(r.Context())
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, users)
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	var body struct {
		Email    string   `json:"email"`
		FullName string   `json:"fullName"`
		Password string   `json:"password"`
		Role     string   `json:"role"`
		Regions  []string `json:"regions"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	id, err := h.svc.CreateUser(r.Context(), actor,
		registry.CreateIdentityRequest{Email: body.Email, FullName: body.FullName, Password: body.Password},
		body.Role, body.Regions)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{"id": id.String()})
}

func (h *Handler) listOrganizations(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	orgs, err := h.svc.ListOrganizations(r.Context(), actor, r.URL.Query().Get("status"))
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, orgs)
}

func (h *Handler) registerOrganization(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	var body struct{ Name string }
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	// requestedBy is the superadmin creating it here; a future self-serve signup
	// would pass the registering org's own contact.
	tenantID, err := h.svc.RegisterOrganization(r.Context(), body.Name, actor.String())
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{"tenantId": tenantID.String()})
}

func (h *Handler) approveOrganization(w http.ResponseWriter, r *http.Request) {
	h.orgTransition(w, r, func(ctx context.Context, actor gid.GID, tenant gid.TenantID) error {
		return h.svc.ApproveOrganization(ctx, actor, tenant)
	})
}

func (h *Handler) suspendOrganization(w http.ResponseWriter, r *http.Request) {
	h.orgTransition(w, r, func(ctx context.Context, actor gid.GID, tenant gid.TenantID) error {
		return h.svc.SuspendOrganization(ctx, actor, tenant)
	})
}

// orgTransition parses the {tenant} param and applies a status change, so
// approve and suspend share one parse-and-dispatch path.
func (h *Handler) orgTransition(w http.ResponseWriter, r *http.Request, apply func(context.Context, gid.GID, gid.TenantID) error) {
	actor, _ := authn.IdentityFrom(r.Context())
	var tenant gid.TenantID
	if err := tenant.UnmarshalText([]byte(chi.URLParam(r, "tenant"))); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid organization id")
		return
	}
	if err := apply(r.Context(), actor, tenant); err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) assignRole(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	var body struct {
		Email   string   `json:"email"`
		Role    string   `json:"role"`
		Regions []string `json:"regions"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	if err := h.svc.AssignRole(r.Context(), actor, body.Email, body.Role, body.Regions); err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.svc.ListSigningKeys(r.Context())
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, keys)
}

func (h *Handler) generateKey(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	var body struct {
		KeyID string `json:"keyId"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	if err := h.svc.GenerateSigningKey(r.Context(), actor, body.KeyID); err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

func (h *Handler) listTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := h.svc.ListTokens(r.Context())
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, tokens)
}

func (h *Handler) issueToken(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	var body struct {
		Name    string   `json:"name"`
		Regions []string `json:"regions"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	token, err := h.svc.IssueToken(r.Context(), actor, body.Name, body.Regions)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{"token": token})
}

func requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authn.IdentityFrom(r.Context()); !ok {
			httpx.Error(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type frameworkView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ShortName string `json:"shortName"`
	Region    string `json:"region"`
	Authority string `json:"authority"`
	License   string `json:"license"`
	Public    bool   `json:"public"`
}

func toFrameworkView(f *coredata.Framework) frameworkView {
	return frameworkView{
		ID: f.ReferenceID, Name: f.Name, ShortName: f.ShortName, Region: f.Region,
		Authority: f.Authority, License: f.License, Public: f.Public,
	}
}

func (h *Handler) listFrameworks(w http.ResponseWriter, r *http.Request) {
	frameworks, err := h.svc.FrameworkList(r.Context())
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	// FrameworkList already returns the console shape, including author,
	// timestamps and source-document metadata.
	httpx.JSON(w, http.StatusOK, frameworks)
}

func (h *Handler) createFramework(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())
	var body struct {
		Reference   string `json:"reference"`
		Name        string `json:"name"`
		ShortName   string `json:"shortName"`
		Region      string `json:"region"`
		Authority   string `json:"authority"`
		License     string `json:"license"`
		Description string `json:"description"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	req := registry.CreateFrameworkRequest{
		ReferenceID: body.Reference, Name: body.Name, ShortName: body.ShortName,
		Region: body.Region, Authority: body.Authority, License: body.License,
		Description: body.Description,
	}
	out, err := h.svc.CreateFramework(r.Context(), actor, req)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{
		"id": req.ReferenceID, "versionId": out.VersionID.String(), "version": out.Version,
	})
}

func (h *Handler) getFramework(w http.ResponseWriter, r *http.Request) {
	framework, err := h.svc.FrameworkByReference(r.Context(), chi.URLParam(r, "ref"))
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	versions, err := h.svc.VersionsOf(r.Context(), framework.ID)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}

	type versionView struct {
		ID          string     `json:"id"`
		Version     string     `json:"version"`
		Status      string     `json:"status"`
		ContentHash string     `json:"contentHash"`
		PublishedAt *time.Time `json:"publishedAt,omitempty"`
	}
	type controlView struct {
		RefID       string `json:"refId"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Section     string `json:"section"`
	}

	vv := make([]versionView, 0, len(versions))
	for _, v := range versions {
		vv = append(vv, versionView{ID: v.ID.String(), Version: v.Version, Status: string(v.Status), ContentHash: v.ContentHash, PublishedAt: v.PublishedAt})
	}

	var cv []controlView
	if len(versions) > 0 {
		controls, err := h.svc.ControlsOf(r.Context(), versions[0].ID)
		if err != nil {
			httpx.ServiceError(w, err)
			return
		}
		for _, c := range controls {
			cv = append(cv, controlView{RefID: c.RefID, Name: c.Name, Description: c.Description, Section: c.Section})
		}
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"framework":      toFrameworkView(&framework),
		"versions":       vv,
		"latestControls": cv,
	})
}

func (h *Handler) addControl(w http.ResponseWriter, r *http.Request) {
	actor, _ := authn.IdentityFrom(r.Context())

	framework, err := h.svc.FrameworkByReference(r.Context(), chi.URLParam(r, "ref"))
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	versionID, err := h.svc.LatestVersionID(r.Context(), framework.ID)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}

	var body struct {
		Ref         string `json:"ref"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Section     string `json:"section"`
		Guidance    string `json:"guidance"`
		Parent      string `json:"parent"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}

	id, err := h.svc.AddControl(r.Context(), actor, registry.AddControlRequest{
		VersionID: versionID, RefID: body.Ref, Name: body.Name, Description: body.Description,
		Section: body.Section, Guidance: body.Guidance, ParentRefID: body.Parent,
	})
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{"id": id.String()})
}

type transitionKind int

const (
	transitionSubmit transitionKind = iota
	transitionApprove
	transitionPublish
	transitionReject
	transitionDeprecate
)

func (h *Handler) transition(kind transitionKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, _ := authn.IdentityFrom(r.Context())

		framework, err := h.svc.FrameworkByReference(r.Context(), chi.URLParam(r, "ref"))
		if err != nil {
			httpx.ServiceError(w, err)
			return
		}
		versionID, err := h.svc.LatestVersionID(r.Context(), framework.ID)
		if err != nil {
			httpx.ServiceError(w, err)
			return
		}

		switch kind {
		case transitionSubmit:
			err = h.svc.Submit(r.Context(), actor, versionID)
		case transitionApprove:
			var body struct {
				Comment string `json:"comment"`
			}
			_ = httpx.DecodeOptional(r, &body)
			err = h.svc.Approve(r.Context(), actor, versionID, body.Comment)
		case transitionPublish:
			err = h.svc.Publish(r.Context(), actor, versionID)
		case transitionReject:
			var body struct {
				Comment string `json:"comment"`
			}
			_ = httpx.DecodeOptional(r, &body)
			err = h.svc.Reject(r.Context(), actor, versionID, body.Comment)
		case transitionDeprecate:
			err = h.svc.Deprecate(r.Context(), actor, versionID)
		}
		if err != nil {
			httpx.ServiceError(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func (h *Handler) audit(w http.ResponseWriter, r *http.Request) {
	entries, err := h.svc.RecentAudit(r.Context(), 100)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	type view struct {
		Action string    `json:"action"`
		Target string    `json:"target"`
		Detail string    `json:"detail"`
		At     time.Time `json:"at"`
	}
	out := make([]view, 0, len(entries))
	for _, e := range entries {
		out = append(out, view{Action: e.Action, Target: e.TargetID, Detail: e.Detail, At: e.CreatedAt})
	}
	httpx.JSON(w, http.StatusOK, out)
}
