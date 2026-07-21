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

// Package authn holds request authentication middleware. The distribution API
// authenticates per-instance bearer tokens; the resolved token context is
// stashed on the request context for handlers.
package authn

import (
	"context"
	"net/http"
	"strings"

	"go.meizon.cloud/registry/pkg/registry"
)

type ctxKey int

const tokenContextKey ctxKey = 0

// TokenAuthenticator resolves a bearer token to a distribution context.
type TokenAuthenticator interface {
	AuthenticateToken(ctx context.Context, plaintext string) (registry.TokenContext, error)
}

// NewBearerTokenMiddleware authenticates the Authorization: Bearer header and,
// on success, injects the token context. Missing or invalid tokens get 401.
func NewBearerTokenMiddleware(auth TokenAuthenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := bearerToken(r)
			if token == "" {
				unauthorized(w)
				return
			}

			tc, err := auth.AuthenticateToken(r.Context(), token)
			if err != nil {
				unauthorized(w)
				return
			}

			ctx := context.WithValue(r.Context(), tokenContextKey, tc)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// TokenContextFrom returns the token context injected by the middleware.
func TokenContextFrom(ctx context.Context) (registry.TokenContext, bool) {
	tc, ok := ctx.Value(tokenContextKey).(registry.TokenContext)
	return tc, ok
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}
