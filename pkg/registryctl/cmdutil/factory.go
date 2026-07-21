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

// Package cmdutil holds the registryctl Factory: shared plumbing that builds a
// database-backed registry service from flags and REGISTRYD_* environment
// variables, and applies migrations on first use so the CLI works against a
// fresh database.
package cmdutil

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.gearno.de/kit/log"
	"go.gearno.de/kit/migrator"
	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/crypto/cipher"
	"go.meizon.cloud/registry/pkg/crypto/passwdhash"
	"go.meizon.cloud/registry/pkg/registry"
)

// Factory constructs shared dependencies for commands.
type Factory struct {
	PgAddr     string
	PgUser     string
	PgPassword string
	PgDatabase string

	Version string

	svc *registry.Service
}

// Service lazily builds the registry service, running migrations on first use.
func (f *Factory) Service(ctx context.Context) (*registry.Service, error) {
	if f.svc != nil {
		return f.svc, nil
	}

	logger := log.NewLogger(log.WithName("registryctl"))

	client, err := pg.NewClient(
		pg.WithAddr(f.PgAddr),
		pg.WithUser(f.PgUser),
		pg.WithPassword(f.PgPassword),
		pg.WithDatabase(f.PgDatabase),
		pg.WithApplicationName("registryctl"),
	)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to postgres: %w", err)
	}

	if err := migrator.NewMigrator(client, coredata.Migrations, logger.Named("migrations")).Run(ctx, "migrations"); err != nil {
		return nil, fmt.Errorf("cannot apply migrations: %w", err)
	}

	var encKey cipher.EncryptionKey
	if err := encKey.UnmarshalText([]byte(os.Getenv("REGISTRYD_ENCRYPTION_KEY"))); err != nil {
		return nil, fmt.Errorf("REGISTRYD_ENCRYPTION_KEY is required and must be base64 of 32 bytes: %w", err)
	}

	pepper := []byte(os.Getenv("REGISTRYD_AUTH_PASSWORD_PEPPER"))
	hashProfile, err := passwdhash.NewProfile(pepper, 600000)
	if err != nil {
		return nil, fmt.Errorf("REGISTRYD_AUTH_PASSWORD_PEPPER is required (>= 32 bytes): %w", err)
	}

	f.svc = registry.NewService(client, hashProfile, registry.Config{
		PlatformTenant: registry.PlatformTenant,
		EncryptionKey:  encKey,
		SuperAdmins:    splitList(os.Getenv("REGISTRYD_SUPER_ADMINS")),
	}, logger)

	return f.svc, nil
}

func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
