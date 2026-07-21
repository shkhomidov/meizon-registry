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

// Package admin_v1 exposes a gated superadmin surface. In this build it offers a
// read-only audit-log endpoint protected by a static admin bearer token
// (REGISTRYD_ADMIN_TOKEN); when the token is empty the surface is disabled.
package admin_v1

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.meizon.cloud/registry/pkg/registry"
)

// Handler serves /api/admin/v1.
type Handler struct {
	svc        *registry.Service
	adminToken string
}

// NewHandler builds the admin handler. An empty adminToken disables the surface.
func NewHandler(svc *registry.Service, adminToken string) *Handler {
	return &Handler{svc: svc, adminToken: adminToken}
}

// Routes returns the chi router for the admin API.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(h.requireAdmin)
	r.Get("/audit", h.audit)
	return r
}

func (h *Handler) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.adminToken == "" {
			http.Error(w, "admin surface disabled", http.StatusNotFound)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), []byte(h.adminToken)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) audit(w http.ResponseWriter, r *http.Request) {
	entries, err := h.svc.RecentAudit(r.Context(), 200)
	if err != nil {
		http.Error(w, "cannot load audit log", http.StatusInternalServerError)
		return
	}

	type view struct {
		Actor  string `json:"actor"`
		Action string `json:"action"`
		Target string `json:"target"`
		Detail string `json:"detail"`
		At     string `json:"at"`
	}
	out := make([]view, 0, len(entries))
	for _, e := range entries {
		out = append(out, view{Actor: e.ActorID, Action: e.Action, Target: e.TargetID, Detail: e.Detail, At: e.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00")})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(out)
}
