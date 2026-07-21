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
	"time"

	"github.com/jackc/pgx/v5"
	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/gid"
)

type (
	// SigningKey is an ed25519 key pair used to sign published versions. The
	// private half is stored AES-256-GCM encrypted; only the public half and the
	// stable KeyID are distributed.
	SigningKey struct {
		ID                  gid.GID    `db:"id"`
		KeyID               string     `db:"key_id"`
		PublicKey           []byte     `db:"public_key"`
		EncryptedPrivateKey []byte     `db:"encrypted_private_key"`
		Active              bool       `db:"active"`
		CreatedAt           time.Time  `db:"created_at"`
		RotatedAt           *time.Time `db:"rotated_at"`
	}

	SigningKeys []*SigningKey
)

const signingKeyColumns = `id, key_id, public_key, encrypted_private_key, active, created_at, rotated_at`

func (k SigningKey) Insert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `
INSERT INTO signing_keys (tenant_id, id, key_id, public_key, encrypted_private_key, active, created_at, rotated_at)
VALUES (@tenant_id, @id, @key_id, @public_key, @encrypted_private_key, @active, @created_at, @rotated_at);`

	args := pgx.StrictNamedArgs{
		"tenant_id":             scope.GetTenantID(),
		"id":                    k.ID,
		"key_id":                k.KeyID,
		"public_key":            k.PublicKey,
		"encrypted_private_key": k.EncryptedPrivateKey,
		"active":                k.Active,
		"created_at":            k.CreatedAt,
		"rotated_at":            k.RotatedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	if err != nil {
		if isUniqueViolation(err, "signing_keys_keyid_unique") {
			return ErrResourceAlreadyExists
		}
		return err
	}

	return nil
}

func (k *SigningKey) LoadByKeyID(ctx context.Context, conn pg.Querier, scope Scoper, keyID string) error {
	q := fmt.Sprintf(`SELECT %s FROM signing_keys WHERE %s AND key_id = @key_id LIMIT 1;`, signingKeyColumns, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"key_id": keyID}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query signing key: %w", err)
	}

	key, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[SigningKey])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrResourceNotFound
		}
		return fmt.Errorf("cannot collect signing key: %w", err)
	}

	*k = key

	return nil
}

// LoadActive loads the current active signing key.
func (k *SigningKey) LoadActive(ctx context.Context, conn pg.Querier, scope Scoper) error {
	q := fmt.Sprintf(`SELECT %s FROM signing_keys WHERE %s AND active = TRUE ORDER BY created_at DESC LIMIT 1;`,
		signingKeyColumns, scope.SQLFragment())

	rows, err := conn.Query(ctx, q, scope.SQLArguments())
	if err != nil {
		return fmt.Errorf("cannot query active signing key: %w", err)
	}

	key, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[SigningKey])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrResourceNotFound
		}
		return fmt.Errorf("cannot collect active signing key: %w", err)
	}

	*k = key

	return nil
}

// LoadAllPublic loads the public halves of every signing key, for building the
// verification key set.
func (ks *SigningKeys) LoadAllPublic(ctx context.Context, conn pg.Querier, scope Scoper) error {
	q := fmt.Sprintf(`SELECT %s FROM signing_keys WHERE %s ORDER BY created_at ASC;`, signingKeyColumns, scope.SQLFragment())

	rows, err := conn.Query(ctx, q, scope.SQLArguments())
	if err != nil {
		return fmt.Errorf("cannot query signing keys: %w", err)
	}

	keys, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[SigningKey])
	if err != nil {
		return fmt.Errorf("cannot collect signing keys: %w", err)
	}

	*ks = keys

	return nil
}

// Deactivate marks a key rotated/inactive.
func (k *SigningKey) Deactivate(ctx context.Context, conn pg.Tx, scope Scoper, rotatedAt time.Time) error {
	q := fmt.Sprintf(`UPDATE signing_keys SET active = FALSE, rotated_at = @rotated_at WHERE %s AND id = @id;`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"id": k.ID, "rotated_at": rotatedAt}
	maps.Copy(args, scope.SQLArguments())

	_, err := conn.Exec(ctx, q, args)
	return err
}
