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

// Package console serves the built React console from inside registryd.
//
// One binary, one origin. The alternative — a separate web server for the SPA —
// puts the console on a different origin from /api, which means CORS and
// cross-site cookie rules for a session cookie that must stay SameSite. Serving
// both from here removes that whole class of deployment problem.
package console

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// dist is the built console. The committed placeholder keeps `go build` working
// without Node installed; the Docker build overwrites it with the real bundle,
// so a dev build and a release build differ only in content.
//
//go:embed all:dist
var dist embed.FS

// Built reports whether a real console bundle is embedded, as opposed to the
// placeholder. Deployments without it still serve the API — the console is
// optional, and pretending otherwise would break API-only installs.
func Built() bool {
	f, err := dist.Open("dist/assets")
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// Handler serves the SPA: real files when they exist, index.html otherwise.
//
// The fallback is what makes client-side routing work. A deep link like
// /frameworks/iso-27001 is not a file on disk, and without this it would 404 on
// refresh even though the route is valid inside the app.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}

		if f, err := sub.Open(name); err == nil {
			_ = f.Close()
			// Hashed asset filenames change whenever content does, so they are
			// safe to cache hard. index.html must not be, or a deploy leaves
			// browsers on the old bundle.
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			files.ServeHTTP(w, r)
			return
		}

		index, err := dist.ReadFile("dist/index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(index)
	})
}
