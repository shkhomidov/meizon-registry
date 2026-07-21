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
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/crypto/hash"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/iam"
)

// tokenPrefix identifies a distribution token in bearer headers.
const tokenPrefix = "mzt_"

// IssueToken creates a per-GRC-instance distribution token. Superadmin only. The
// plaintext token is returned once and never stored; only its digest is kept.
func (s *Service) IssueToken(ctx context.Context, actorID gid.GID, name string, regions []string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("token name is required")
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("cannot generate token: %w", err)
	}
	plaintext := tokenPrefix + base64.RawURLEncoding.EncodeToString(raw)

	err := s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		if err := s.authorize(ctx, tx, actorID, iam.ActionTokenManage, "", gid.Nil); err != nil {
			return err
		}

		token := coredata.DistributionToken{
			ID:          gid.New(s.cfg.PlatformTenant, coredata.DistributionTokenEntityType),
			Name:        strings.TrimSpace(name),
			HashedToken: hash.SHA256Hex([]byte(plaintext)),
			Regions:     strings.Join(regions, ","),
			CreatedAt:   time.Now(),
		}
		if err := token.Insert(ctx, tx, s.platformScope()); err != nil {
			return fmt.Errorf("cannot store token: %w", err)
		}

		return s.recordAudit(ctx, tx, s.platformScope(), actorID, "token.issue", token.ID.String(),
			fmt.Sprintf("%s regions=%s", name, strings.Join(regions, ",")))
	})
	if err != nil {
		return "", err
	}

	return plaintext, nil
}

// resolveToken authenticates a bearer token and marks it used. It returns the
// token record (with its region scope) for the distribution layer.
func (s *Service) resolveToken(ctx context.Context, conn pg.Querier, plaintext string) (*coredata.DistributionToken, error) {
	plaintext = strings.TrimSpace(plaintext)
	if !strings.HasPrefix(plaintext, tokenPrefix) {
		return nil, coredata.ErrResourceNotFound
	}

	var token coredata.DistributionToken
	if err := token.LoadByHashedToken(ctx, conn, hash.SHA256Hex([]byte(plaintext))); err != nil {
		return nil, err
	}

	_ = token.TouchLastUsed(ctx, conn, time.Now())

	return &token, nil
}
