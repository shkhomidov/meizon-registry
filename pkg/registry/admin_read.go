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

package registry

import (
	"context"
	"encoding/base64"
	"time"

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/gid"
)

// UserInfo is the admin view of an identity and its role assignment.
type UserInfo struct {
	ID       gid.GID  `json:"id"`
	Email    string   `json:"email"`
	FullName string   `json:"fullName"`
	Status   string   `json:"status"`
	Role     string   `json:"role"`
	Regions  []string `json:"regions"`
}

// ListUsers returns every identity with its role/region scope.
func (s *Service) ListUsers(ctx context.Context) ([]UserInfo, error) {
	var out []UserInfo
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		var identities coredata.Identities
		if err := identities.LoadAll(ctx, conn, s.platformScope()); err != nil {
			return err
		}
		var memberships coredata.Memberships
		if err := memberships.LoadAll(ctx, conn, s.platformScope()); err != nil {
			return err
		}

		byIdentity := make(map[gid.GID]*coredata.Membership, len(memberships))
		for _, m := range memberships {
			byIdentity[m.IdentityID] = m
		}

		out = make([]UserInfo, 0, len(identities))
		for _, i := range identities {
			u := UserInfo{ID: i.ID, Email: i.Email, FullName: i.FullName, Status: string(i.Status)}
			if m := byIdentity[i.ID]; m != nil {
				u.Role = m.Role
				u.Regions = m.RegionList()
			}
			out = append(out, u)
		}
		return nil
	})
	return out, err
}

// SigningKeyInfo is the admin view of a signing key (public material only).
type SigningKeyInfo struct {
	KeyID        string     `json:"keyId"`
	Active       bool       `json:"active"`
	PublicKeyB64 string     `json:"publicKey"`
	CreatedAt    time.Time  `json:"createdAt"`
	RotatedAt    *time.Time `json:"rotatedAt,omitempty"`
}

// ListSigningKeys returns every signing key's public material.
func (s *Service) ListSigningKeys(ctx context.Context) ([]SigningKeyInfo, error) {
	var out []SigningKeyInfo
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		var keys coredata.SigningKeys
		if err := keys.LoadAllPublic(ctx, conn, s.platformScope()); err != nil {
			return err
		}
		out = make([]SigningKeyInfo, 0, len(keys))
		for _, k := range keys {
			out = append(out, SigningKeyInfo{
				KeyID: k.KeyID, Active: k.Active, CreatedAt: k.CreatedAt, RotatedAt: k.RotatedAt,
				PublicKeyB64: base64.StdEncoding.EncodeToString(k.PublicKey),
			})
		}
		return nil
	})
	return out, err
}

// TokenInfo is the admin view of a distribution token (never the secret).
type TokenInfo struct {
	ID         gid.GID    `json:"id"`
	Name       string     `json:"name"`
	Regions    []string   `json:"regions"`
	Revoked    bool       `json:"revoked"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}

// ListTokens returns every distribution token's metadata.
func (s *Service) ListTokens(ctx context.Context) ([]TokenInfo, error) {
	var out []TokenInfo
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		var tokens coredata.DistributionTokens
		if err := tokens.LoadAll(ctx, conn, s.platformScope()); err != nil {
			return err
		}
		out = make([]TokenInfo, 0, len(tokens))
		for _, t := range tokens {
			out = append(out, TokenInfo{
				ID: t.ID, Name: t.Name, Regions: t.RegionList(), Revoked: t.Revoked,
				CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt,
			})
		}
		return nil
	})
	return out, err
}
