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

package authn

import (
	"context"
	"net/http"
	"time"

	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/securecookie"
)

const sessionCookieName = "mz_session"
const sessionTTL = 12 * time.Hour

type identityCtxKey int

const identityKey identityCtxKey = 0

// SessionCookie issues and clears the console session cookie.
type SessionCookie struct {
	codec  *securecookie.Codec
	secure bool
}

// NewSessionCookie builds a session cookie issuer from the signing secret.
func NewSessionCookie(secret []byte, secure bool) (*SessionCookie, error) {
	codec, err := securecookie.NewCodec(secret)
	if err != nil {
		return nil, err
	}
	return &SessionCookie{codec: codec, secure: secure}, nil
}

// Set writes a signed session cookie for the given identity.
func (s *SessionCookie) Set(w http.ResponseWriter, identityID gid.GID) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    s.codec.Encode(identityID.String(), sessionTTL),
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// Clear removes the session cookie.
func (s *SessionCookie) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// Middleware injects the authenticated identity (if any) into the request
// context. It never rejects; handlers decide whether a session is required.
func (s *SessionCookie) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookieName); err == nil {
			if payload, err := s.codec.Decode(c.Value); err == nil {
				if id, err := gid.ParseGID(payload); err == nil {
					r = r.WithContext(context.WithValue(r.Context(), identityKey, id))
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// IdentityFrom returns the authenticated identity id from the context.
func IdentityFrom(ctx context.Context) (gid.GID, bool) {
	id, ok := ctx.Value(identityKey).(gid.GID)
	return id, ok
}
