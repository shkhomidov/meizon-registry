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

// Package api composes the versioned API families under /api into one handler:
// the distribution API (/registry/v1, token auth), the console + connect APIs
// (/console/v1, /connect/v1, session-cookie auth) and the gated admin API
// (/admin/v1), wrapped in a shared rate limiter, CORS and security headers.
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"go.meizon.cloud/registry/pkg/registry"
	admin_v1 "go.meizon.cloud/registry/pkg/server/api/admin/v1"
	"go.meizon.cloud/registry/pkg/server/api/authn"
	connect_v1 "go.meizon.cloud/registry/pkg/server/api/connect/v1"
	console_v1 "go.meizon.cloud/registry/pkg/server/api/console/v1"
	registry_v1 "go.meizon.cloud/registry/pkg/server/api/registry/v1"
)

// Config configures the API mux.
type Config struct {
	Service        *registry.Service
	RateLimitRPM   int
	RateLimitBurst int
	AdminToken     string

	// CookieSecret signs console session cookies. When empty, the console and
	// connect APIs are not mounted (distribution and admin still work).
	CookieSecret []byte
	CookieSecure bool

	// CorsOrigins are the browser origins allowed to call the console with
	// credentials (e.g. the Vite dev server). Empty means same-origin only.
	CorsOrigins []string
}

// NewMux builds the /api handler (mounted with the /api prefix stripped).
func NewMux(cfg Config) (http.Handler, error) {
	limiter := newRateLimiter(cfg.RateLimitRPM, cfg.RateLimitBurst)

	distribution := registry_v1.NewHandler(cfg.Service)
	admin := admin_v1.NewHandler(cfg.Service, cfg.AdminToken)

	r := chi.NewRouter()
	r.Use(securityHeaders)
	if len(cfg.CorsOrigins) > 0 {
		r.Use(cors.Handler(cors.Options{
			AllowedOrigins:   cfg.CorsOrigins,
			AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
			AllowedHeaders:   []string{"Content-Type", "Authorization"},
			AllowCredentials: true,
			MaxAge:           300,
		}))
	}
	r.Use(limiter.middleware)

	r.Mount("/registry/v1", http.StripPrefix("/registry/v1", distribution.Routes()))
	r.Mount("/admin/v1", http.StripPrefix("/admin/v1", admin.Routes()))

	if len(cfg.CookieSecret) > 0 {
		cookie, err := authn.NewSessionCookie(cfg.CookieSecret, cfg.CookieSecure)
		if err != nil {
			return nil, err
		}
		connect := connect_v1.NewHandler(cfg.Service, cookie)
		console := console_v1.NewHandler(cfg.Service, cookie)
		r.Mount("/connect/v1", http.StripPrefix("/connect/v1", connect.Routes()))
		r.Mount("/console/v1", http.StripPrefix("/console/v1", console.Routes()))
	}

	return r, nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
