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

// Package server builds the top-level HTTP handler for registryd. The console
// SPA is a later deliverable; for now the root simply mounts the /api families.
package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.meizon.cloud/registry/pkg/server/api"
	"go.meizon.cloud/registry/pkg/server/console"
)

// Config aggregates everything the HTTP layer needs.
type Config struct {
	API api.Config
}

// NewHandler builds the outer router.
func NewHandler(cfg Config) (http.Handler, error) {
	apiMux, err := api.NewMux(cfg.API)
	if err != nil {
		return nil, err
	}

	root := chi.NewRouter()
	root.Mount("/api", http.StripPrefix("/api", apiMux))

	// Liveness probe target (there is no dedicated /healthz, matching the GRC).
	// Kept on its own path because "/" now serves the console when one is built.
	root.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	if console.Built() {
		// Console and API share an origin, so the session cookie needs no
		// cross-site relaxation and CORS is unnecessary.
		root.Mount("/", console.Handler())
	} else {
		root.Get("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("meizon registry\n"))
		})
	}

	return root, nil
}
