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

package coredata_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.gearno.de/kit/log"
	"go.gearno.de/kit/migrator"
	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/gid"
)

func testClient(t *testing.T) *pg.Client {
	t.Helper()
	addr := os.Getenv("REGISTRYD_TEST_PG_ADDR")
	if addr == "" {
		t.Skip("set REGISTRYD_TEST_PG_ADDR to run database-backed tests")
	}
	logger := log.NewLogger(log.WithName("coredata-test"))
	client, err := pg.NewClient(
		pg.WithAddr(addr),
		pg.WithUser("registryd"), pg.WithPassword("registryd"), pg.WithDatabase("registryd"),
		pg.WithApplicationName("coredata-test"),
		pg.WithRegisterer(prometheus.NewRegistry()),
	)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	ctx := context.Background()
	if err := migrator.NewMigrator(client, coredata.Migrations, logger).Run(ctx, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := client.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		// CASCADE so referencing rows (versions, controls, …) don't block the
		// truncate when the database already holds data from another run.
		_, err := conn.Exec(ctx, `TRUNCATE frameworks CASCADE;`)
		return err
	}); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return client
}

// TestTenantIsolation verifies a framework created under one tenant is invisible
// to a scope bound to a different tenant, while a matching scope finds it.
func TestTenantIsolation(t *testing.T) {
	client := testClient(t)
	ctx := context.Background()

	tenantA := gid.NewTenantID()
	tenantB := gid.NewTenantID()

	fw := coredata.Framework{
		ID:          gid.New(tenantA, coredata.FrameworkEntityType),
		ReferenceID: "iso-test",
		Name:        "Tenant A framework",
		Region:      "EU",
		License:     "public-domain",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := client.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		return fw.Insert(ctx, tx, coredata.NewScope(tenantA))
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Tenant A sees it.
	if err := client.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		var got coredata.Framework
		return got.LoadByID(ctx, conn, coredata.NewScope(tenantA), fw.ID)
	}); err != nil {
		t.Fatalf("tenant A should see its framework: %v", err)
	}

	// Tenant B does not.
	err := client.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		var got coredata.Framework
		return got.LoadByID(ctx, conn, coredata.NewScope(tenantB), fw.ID)
	})
	if !errors.Is(err, coredata.ErrResourceNotFound) {
		t.Fatalf("tenant B must not see tenant A's framework, got: %v", err)
	}
}

// TestCatalogCopyrightGate verifies LoadCatalog excludes non-public frameworks
// owned by other tenants but includes public ones.
func TestCatalogCopyrightGate(t *testing.T) {
	client := testClient(t)
	ctx := context.Background()

	ownerTenant := gid.NewTenantID()
	strangerTenant := gid.NewTenantID()

	public := coredata.Framework{
		ID: gid.New(ownerTenant, coredata.FrameworkEntityType), ReferenceID: "nist-public",
		Name: "Public", Region: "EU", License: "public-domain", Public: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	proprietary := coredata.Framework{
		ID: gid.New(ownerTenant, coredata.FrameworkEntityType), ReferenceID: "iso-proprietary",
		Name: "Proprietary", Region: "EU", License: "proprietary", Public: false,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}

	if err := client.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		if err := public.Insert(ctx, tx, coredata.NewScope(ownerTenant)); err != nil {
			return err
		}
		return proprietary.Insert(ctx, tx, coredata.NewScope(ownerTenant))
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// A stranger tenant sees only the public framework.
	var got coredata.Frameworks
	if err := client.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		return got.LoadCatalog(ctx, conn, strangerTenant, nil)
	}); err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if len(got) != 1 || got[0].ReferenceID != "nist-public" {
		t.Fatalf("stranger tenant should only see the public framework, got: %d entries", len(got))
	}

	// The owner sees both.
	var ownerView coredata.Frameworks
	if err := client.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		return ownerView.LoadCatalog(ctx, conn, ownerTenant, nil)
	}); err != nil {
		t.Fatalf("owner catalog: %v", err)
	}
	if len(ownerView) != 2 {
		t.Fatalf("owner should see both frameworks, got: %d", len(ownerView))
	}
}
