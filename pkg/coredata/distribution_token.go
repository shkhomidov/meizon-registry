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

package coredata

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/gid"
)

type (
	// DistributionToken is a per-GRC-instance bearer credential for the
	// read-only distribution API. Only the SHA-256 digest of the token is stored;
	// the plaintext is shown once at issuance. regions scopes which regions the
	// instance may pull (empty = all).
	DistributionToken struct {
		ID          gid.GID    `db:"id"`
		Name        string     `db:"name"`
		HashedToken string     `db:"hashed_token"`
		Regions     string     `db:"regions"`
		Revoked     bool       `db:"revoked"`
		CreatedAt   time.Time  `db:"created_at"`
		LastUsedAt  *time.Time `db:"last_used_at"`
	}

	DistributionTokens []*DistributionToken
)

const distributionTokenColumns = `id, name, hashed_token, regions, revoked, created_at, last_used_at`

// RegionList splits the stored comma-joined regions.
func (t *DistributionToken) RegionList() []string {
	if strings.TrimSpace(t.Regions) == "" {
		return nil
	}
	parts := strings.Split(t.Regions, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func (t DistributionToken) Insert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `
INSERT INTO distribution_tokens (tenant_id, id, name, hashed_token, regions, revoked, created_at, last_used_at)
VALUES (@tenant_id, @id, @name, @hashed_token, @regions, @revoked, @created_at, @last_used_at);`

	args := pgx.StrictNamedArgs{
		"tenant_id":    scope.GetTenantID(),
		"id":           t.ID,
		"name":         t.Name,
		"hashed_token": t.HashedToken,
		"regions":      t.Regions,
		"revoked":      t.Revoked,
		"created_at":   t.CreatedAt,
		"last_used_at": t.LastUsedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	if err != nil {
		if isUniqueViolation(err, "distribution_tokens_hash_unique") {
			return ErrResourceAlreadyExists
		}
		return err
	}

	return nil
}

// LoadByHashedToken loads an active (non-revoked) token by its digest. It reads
// unscoped because the distribution middleware resolves the token before any
// tenant context exists.
func (t *DistributionToken) LoadByHashedToken(ctx context.Context, conn pg.Querier, hashedToken string) error {
	q := fmt.Sprintf(`SELECT %s FROM distribution_tokens WHERE hashed_token = @hashed_token AND revoked = FALSE LIMIT 1;`, distributionTokenColumns)

	rows, err := conn.Query(ctx, q, pgx.StrictNamedArgs{"hashed_token": hashedToken})
	if err != nil {
		return fmt.Errorf("cannot query distribution token: %w", err)
	}

	token, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[DistributionToken])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrResourceNotFound
		}
		return fmt.Errorf("cannot collect distribution token: %w", err)
	}

	*t = token

	return nil
}

// LoadAll returns every distribution token in the scope, newest first.
func (ts *DistributionTokens) LoadAll(ctx context.Context, conn pg.Querier, scope Scoper) error {
	q := fmt.Sprintf(`SELECT %s FROM distribution_tokens WHERE %s ORDER BY created_at DESC;`, distributionTokenColumns, scope.SQLFragment())

	rows, err := conn.Query(ctx, q, scope.SQLArguments())
	if err != nil {
		return fmt.Errorf("cannot query distribution tokens: %w", err)
	}

	tokens, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[DistributionToken])
	if err != nil {
		return fmt.Errorf("cannot collect distribution tokens: %w", err)
	}

	*ts = tokens

	return nil
}

// TouchLastUsed records that the token was just used.
func (t *DistributionToken) TouchLastUsed(ctx context.Context, conn pg.Querier, at time.Time) error {
	_, err := conn.Exec(ctx, `UPDATE distribution_tokens SET last_used_at = @at WHERE id = @id;`,
		pgx.StrictNamedArgs{"id": t.ID, "at": at})
	return err
}

// Revoke marks a token revoked. A revoked token fails authentication
// (LoadByHashedToken filters revoked = FALSE) but its row is kept, so its name
// and last-used time stay visible for audit.
func (t *DistributionToken) Revoke(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := fmt.Sprintf(`UPDATE distribution_tokens SET revoked = TRUE WHERE %s AND id = @id;`, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"id": t.ID}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}

// Reinstate clears the revoked flag, re-enabling a token that was inactivated.
// The plaintext is unchanged — reinstating re-enables the same secret, so this
// is only appropriate for a deliberate pause, not for a leaked credential
// (delete and re-issue in that case).
func (t *DistributionToken) Reinstate(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := fmt.Sprintf(`UPDATE distribution_tokens SET revoked = FALSE WHERE %s AND id = @id;`, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"id": t.ID}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}

// Delete removes a token row entirely. Unlike Revoke it leaves no audit trail of
// the token itself; the audit log records who deleted it.
func (t *DistributionToken) Delete(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := fmt.Sprintf(`DELETE FROM distribution_tokens WHERE %s AND id = @id;`, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"id": t.ID}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}
