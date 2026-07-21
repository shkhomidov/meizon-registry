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

// Package connect_v1 is the authentication surface for the console: sign in,
// sign out, and viewer. Sign-in verifies credentials via the registry service
// and issues a signed session cookie.
package connect_v1

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.meizon.cloud/registry/pkg/registry"
	"go.meizon.cloud/registry/pkg/server/api/authn"
	"go.meizon.cloud/registry/pkg/server/api/httpx"
)

// Handler serves /api/connect/v1.
type Handler struct {
	svc    *registry.Service
	cookie *authn.SessionCookie
}

// NewHandler builds the connect handler.
func NewHandler(svc *registry.Service, cookie *authn.SessionCookie) *Handler {
	return &Handler{svc: svc, cookie: cookie}
}

// Routes returns the router. The session middleware is applied so /viewer can
// read the current session.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(h.cookie.Middleware)
	r.Post("/signin", h.signIn)
	r.Post("/signout", h.signOut)
	r.Get("/viewer", h.viewer)
	return r
}

func (h *Handler) signIn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}

	id, err := h.svc.Authenticate(r.Context(), body.Email, body.Password)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}

	h.cookie.Set(w, id)

	viewer, err := h.svc.Viewer(r.Context(), id)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, viewer)
}

func (h *Handler) signOut(w http.ResponseWriter, r *http.Request) {
	h.cookie.Clear(w)
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) viewer(w http.ResponseWriter, r *http.Request) {
	id, ok := authn.IdentityFrom(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	viewer, err := h.svc.Viewer(r.Context(), id)
	if err != nil {
		httpx.ServiceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, viewer)
}
