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

// Package registryd is the registryd daemon implementation. It satisfies the
// unit framework's Configurable and Runnable contracts: the framework loads the
// YAML config, then Run wires the database, applies migrations, constructs the
// registry service and serves the HTTP API.
package registryd

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.gearno.de/kit/httpserver"
	"go.gearno.de/kit/log"
	"go.gearno.de/kit/migrator"
	"go.gearno.de/kit/pg"
	"go.gearno.de/kit/unit"
	"go.opentelemetry.io/otel/trace"

	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/crypto/cipher"
	"go.meizon.cloud/registry/pkg/crypto/passwdhash"
	"go.meizon.cloud/registry/pkg/registry"
	"go.meizon.cloud/registry/pkg/registryconfig"
	"go.meizon.cloud/registry/pkg/server"
	"go.meizon.cloud/registry/pkg/server/api"
)

// Implm is the daemon.
type Implm struct {
	cfg registryconfig.Config
}

var (
	_ unit.Configurable = (*Implm)(nil)
	_ unit.Runnable     = (*Implm)(nil)
)

// New returns a daemon with default configuration.
func New() *Implm {
	return &Implm{cfg: registryconfig.Defaults()}
}

// GetConfiguration returns the config for the unit framework to populate.
func (impl *Implm) GetConfiguration() any {
	return &impl.cfg
}

// Run boots the daemon.
func (impl *Implm) Run(parentCtx context.Context, l *log.Logger, r prometheus.Registerer, tp trace.TracerProvider) error {
	ctx := parentCtx

	var encryptionKey cipher.EncryptionKey
	if err := encryptionKey.UnmarshalText([]byte(impl.cfg.EncryptionKey)); err != nil {
		return fmt.Errorf("cannot parse encryption key: %w", err)
	}

	pepper := []byte(impl.cfg.Auth.PasswordPepper)
	hashProfile, err := passwdhash.NewProfile(pepper, uint32(impl.cfg.Auth.PasswordIterations))
	if err != nil {
		return fmt.Errorf("cannot create password hashing profile: %w", err)
	}

	pgClient, err := pg.NewClient(
		pg.WithAddr(impl.cfg.Pg.Addr),
		pg.WithUser(impl.cfg.Pg.Username),
		pg.WithPassword(impl.cfg.Pg.Password),
		pg.WithDatabase(impl.cfg.Pg.Database),
		pg.WithPoolSize(int32(impl.cfg.Pg.PoolSize)),
		pg.WithMinPoolSize(int32(impl.cfg.Pg.MinPoolSize)),
		pg.WithApplicationName("registryd"),
		pg.WithLogger(l),
		pg.WithRegisterer(r),
		pg.WithTracerProvider(tp),
	)
	if err != nil {
		return fmt.Errorf("cannot create postgres client: %w", err)
	}
	defer pgClient.Close()

	if err := migrator.NewMigrator(pgClient, coredata.Migrations, l.Named("migrations")).Run(ctx, "migrations"); err != nil {
		return fmt.Errorf("cannot migrate database schema: %w", err)
	}

	svc := registry.NewService(pgClient, hashProfile, registry.Config{
		PlatformTenant: registry.PlatformTenant,
		EncryptionKey:  encryptionKey,
		SuperAdmins:    impl.cfg.SuperAdmins,
	}, l)

	handler, err := server.NewHandler(server.Config{
		API: api.Config{
			Service:        svc,
			RateLimitRPM:   impl.cfg.API.RateLimitRPM,
			RateLimitBurst: impl.cfg.API.RateLimitBurst,
			AdminToken:     impl.cfg.AdminToken,
			CookieSecret:   []byte(impl.cfg.Auth.CookieSecret),
			CookieSecure:   impl.cfg.API.CookieSecure,
			CorsOrigins:    impl.cfg.API.ConsoleOrigins,
		},
	})
	if err != nil {
		return fmt.Errorf("cannot build HTTP handler: %w", err)
	}

	httpServer := httpserver.NewServer(
		impl.cfg.API.Addr,
		handler,
		httpserver.WithLogger(l),
		httpserver.WithRegisterer(r),
		httpserver.WithTracerProvider(tp),
	)

	// A job whose goroutine died with the previous process will never finish;
	// leaving it flagged "running" makes a user wait for nothing.
	svc.FailOrphanedJobs(ctx)

	l.InfoCtx(ctx, "registryd listening", log.String("addr", impl.cfg.API.Addr))

	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.ListenAndServe() }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("cannot shut down http server: %w", err)
		}
		return ctx.Err()
	case err := <-errCh:
		return fmt.Errorf("http server failed: %w", err)
	}
}
