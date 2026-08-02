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

package registry_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"go.gearno.de/kit/log"
	"go.gearno.de/kit/migrator"
	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/crypto/cipher"
	"go.meizon.cloud/registry/pkg/crypto/passwdhash"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/registry"
)

const (
	superAdminEmail = "root@meizon.test"
	pw              = "password12345"
)

func newTestService(t *testing.T) (*registry.Service, *pg.Client) {
	t.Helper()

	addr := os.Getenv("REGISTRYD_TEST_PG_ADDR")
	if addr == "" {
		t.Skip("set REGISTRYD_TEST_PG_ADDR to run database-backed tests")
	}

	logger := log.NewLogger(log.WithName("test"))
	client, err := pg.NewClient(
		pg.WithAddr(addr),
		pg.WithUser(envOr("REGISTRYD_TEST_PG_USER", "registryd")),
		pg.WithPassword(envOr("REGISTRYD_TEST_PG_PASSWORD", "registryd")),
		pg.WithDatabase(envOr("REGISTRYD_TEST_PG_DATABASE", "registryd_test")),
		pg.WithApplicationName("registry-test"),
		pg.WithRegisterer(prometheus.NewRegistry()),
	)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	ctx := context.Background()
	if err := migrator.NewMigrator(client, coredata.Migrations, logger.Named("migrations")).Run(ctx, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Clean slate.
	if err := client.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		_, err := conn.Exec(ctx, `TRUNCATE identities, memberships, frameworks, framework_versions, controls, approvals, distribution_tokens, signing_keys, audit_log, downloads, distribution_events, mapping_sets CASCADE;`)
		return err
	}); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	var encKey cipher.EncryptionKey
	if err := encKey.UnmarshalText([]byte(base64.StdEncoding.EncodeToString(make([]byte, 32)))); err != nil {
		t.Fatalf("enc key: %v", err)
	}
	hashProfile, err := passwdhash.NewProfile([]byte("0123456789abcdef0123456789abcdef"), 600000)
	if err != nil {
		t.Fatalf("hash profile: %v", err)
	}

	svc := registry.NewService(client, hashProfile, registry.Config{
		PlatformTenant: registry.PlatformTenant,
		EncryptionKey:  encKey,
		SuperAdmins:    []string{superAdminEmail},
	}, logger)

	return svc, client
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// TestLifecycleEndToEnd walks the full acceptance path: register, role
// assignment, region isolation, separation of duties, publish + sign, and the
// distribution round trip with tamper rejection.
func TestLifecycleEndToEnd(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// Bootstrap governance.
	if _, err := svc.BootstrapSuperAdmin(ctx, req(superAdminEmail, "Root")); err != nil {
		t.Fatalf("bootstrap superadmin: %v", err)
	}
	superID := mustID(t, svc, superAdminEmail)

	mustCreateUser(t, svc, superID, "eu-auditor@meizon.test", "EU Auditor", "auditor", []string{"EU"})
	mustCreateUser(t, svc, superID, "eu-mod@meizon.test", "EU Moderator", "moderator", []string{"EU"})
	mustCreateUser(t, svc, superID, "us-auditor@meizon.test", "US Auditor", "auditor", []string{"US"})

	if err := svc.GenerateSigningKey(ctx, superID, "reg-2026"); err != nil {
		t.Fatalf("generate signing key: %v", err)
	}

	euAuditor := mustID(t, svc, "eu-auditor@meizon.test")
	euMod := mustID(t, svc, "eu-mod@meizon.test")
	usAuditor := mustID(t, svc, "us-auditor@meizon.test")

	// EU auditor authors an EU framework.
	created, err := svc.CreateFramework(ctx, euAuditor, registry.CreateFrameworkRequest{
		ReferenceID: "nist-800-171-r2", Name: "NIST SP 800-171 Rev 2", ShortName: "NIST 800-171",
		Region: "EU", Authority: "NIST", License: "public-domain",
	})
	if err != nil {
		t.Fatalf("create framework: %v", err)
	}

	addControl(t, svc, euAuditor, "nist-800-171-r2", "3.1.1", "Limit system access")
	addControl(t, svc, euAuditor, "nist-800-171-r2", "3.1.2", "Limit transaction functions")

	// A US auditor cannot author in the EU region.
	if _, err := svc.CreateFramework(ctx, usAuditor, registry.CreateFrameworkRequest{
		ReferenceID: "us-only", Name: "US only", Region: "EU", License: "public-domain",
	}); !errors.Is(err, registry.ErrForbidden) {
		t.Fatalf("expected US auditor to be forbidden in EU, got: %v", err)
	}

	if err := svc.Submit(ctx, euAuditor, created.VersionID); err != nil {
		t.Fatalf("submit: %v", err)
	}

	// The auditor cannot approve or publish.
	if err := svc.Approve(ctx, euAuditor, created.VersionID, ""); !errors.Is(err, registry.ErrForbidden) {
		t.Fatalf("expected auditor approve forbidden, got: %v", err)
	}
	if err := svc.Publish(ctx, euAuditor, created.VersionID); !errors.Is(err, registry.ErrForbidden) {
		t.Fatalf("expected auditor publish forbidden, got: %v", err)
	}

	// The EU moderator approves and publishes.
	if err := svc.Approve(ctx, euMod, created.VersionID, "looks good"); err != nil {
		t.Fatalf("moderator approve: %v", err)
	}
	if err := svc.Publish(ctx, euMod, created.VersionID); err != nil {
		t.Fatalf("moderator publish: %v", err)
	}

	// Immutability: cannot add a control to a published version.
	if _, err := svc.AddControl(ctx, euAuditor, registry.AddControlRequest{VersionID: created.VersionID, RefID: "x", Name: "x"}); err == nil {
		t.Fatal("expected adding a control to a published version to fail")
	}

	// Distribution round trip.
	token, err := svc.IssueToken(ctx, superID, "grc-eu", []string{"EU"})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	tc, err := svc.AuthenticateToken(ctx, token)
	if err != nil {
		t.Fatalf("authenticate token: %v", err)
	}

	catalog, err := svc.Catalog(ctx, tc, "", nil)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if len(catalog) != 1 || catalog[0].ID != "nist-800-171-r2" {
		t.Fatalf("unexpected catalog: %+v", catalog)
	}

	bundle, err := svc.Bundle(ctx, tc, "nist-800-171-r2", "latest")
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}

	keys, err := svc.VerificationKeys(ctx)
	if err != nil {
		t.Fatalf("verification keys: %v", err)
	}
	if err := bundle.Verify(keys); err != nil {
		t.Fatalf("published bundle must verify: %v", err)
	}

	// The seed matches the bundle's flatten output exactly (imports unchanged).
	seed, err := svc.Seed(ctx, tc, "nist-800-171-r2", "latest")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	seedJSON, _ := json.Marshal(seed)
	flatJSON, _ := json.Marshal(bundle.Flatten())
	if string(seedJSON) != string(flatJSON) {
		t.Fatalf("seed does not match flatten:\n seed=%s\n flat=%s", seedJSON, flatJSON)
	}

	// A tampered bundle is rejected.
	bundle.Controls[0].Description = "malicious edit"
	if err := bundle.Verify(keys); err == nil {
		t.Fatal("expected tampered bundle to be rejected")
	}
}

// TestSeparationOfDuties verifies a moderator cannot approve a version they
// solely authored.
func TestSeparationOfDuties(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.BootstrapSuperAdmin(ctx, req(superAdminEmail, "Root")); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	superID := mustID(t, svc, superAdminEmail)
	mustCreateUser(t, svc, superID, "eu-mod@meizon.test", "EU Moderator", "moderator", []string{"EU"})
	euMod := mustID(t, svc, "eu-mod@meizon.test")

	// The moderator authors their own framework.
	created, err := svc.CreateFramework(ctx, euMod, registry.CreateFrameworkRequest{
		ReferenceID: "self-authored", Name: "Self Authored", Region: "EU", License: "public-domain",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	addControl(t, svc, euMod, "self-authored", "1.1", "A control")
	if err := svc.Submit(ctx, euMod, created.VersionID); err != nil {
		t.Fatalf("submit: %v", err)
	}

	if err := svc.Approve(ctx, euMod, created.VersionID, ""); !errors.Is(err, registry.ErrSeparationOfDuties) {
		t.Fatalf("expected separation-of-duties error, got: %v", err)
	}
}

// TestCopyrightGate verifies a proprietary framework is not served to a
// non-owning tenant.
func TestCopyrightGate(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.BootstrapSuperAdmin(ctx, req(superAdminEmail, "Root")); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	superID := mustID(t, svc, superAdminEmail)
	mustCreateUser(t, svc, superID, "eu-mod@meizon.test", "EU Moderator", "moderator", []string{"EU"})
	euMod := mustID(t, svc, "eu-mod@meizon.test")

	if err := svc.GenerateSigningKey(ctx, superID, "reg-2026"); err != nil {
		t.Fatalf("signing key: %v", err)
	}

	created, err := svc.CreateFramework(ctx, euMod, registry.CreateFrameworkRequest{
		ReferenceID: "iso-27001", Name: "ISO/IEC 27001", Region: "EU", License: "proprietary",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	addControl(t, svc, euMod, "iso-27001", "A.5.1", "Policies for information security")
	if err := svc.Submit(ctx, euMod, created.VersionID); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// Moderator cannot approve own work; use the superadmin to approve + publish.
	if err := svc.Approve(ctx, superID, created.VersionID, "ok"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := svc.Publish(ctx, superID, created.VersionID); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// The owning tenant (platform) may fetch it.
	owner := registry.TokenContext{TokenID: gid.New(registry.PlatformTenant, coredata.DistributionTokenEntityType), OwnerTenant: registry.PlatformTenant}
	if _, err := svc.Bundle(ctx, owner, "iso-27001", "latest"); err != nil {
		t.Fatalf("owning tenant should fetch proprietary framework: %v", err)
	}

	// A different tenant is gated out.
	otherTenant := gid.NewTenantID()
	stranger := registry.TokenContext{TokenID: gid.New(otherTenant, coredata.DistributionTokenEntityType), OwnerTenant: otherTenant}
	if _, err := svc.Bundle(ctx, stranger, "iso-27001", "latest"); !errors.Is(err, registry.ErrNotDistributable) {
		t.Fatalf("expected proprietary framework gated from other tenant, got: %v", err)
	}
}

func req(email, name string) registry.CreateIdentityRequest {
	return registry.CreateIdentityRequest{Email: email, FullName: name, Password: pw}
}

func mustCreateUser(t *testing.T, svc *registry.Service, actor gid.GID, email, name, role string, regions []string) {
	t.Helper()
	if _, err := svc.CreateUser(context.Background(), actor, req(email, name), role, regions); err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}
}

func mustID(t *testing.T, svc *registry.Service, email string) gid.GID {
	t.Helper()
	id, err := svc.IdentityIDByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("resolve %s: %v", email, err)
	}
	return id
}

func addControl(t *testing.T, svc *registry.Service, actor gid.GID, framework, ref, name string) {
	t.Helper()
	fw, err := svc.FrameworkByReference(context.Background(), framework)
	if err != nil {
		t.Fatalf("framework %s: %v", framework, err)
	}
	versionID, err := svc.LatestVersionID(context.Background(), fw.ID)
	if err != nil {
		t.Fatalf("latest version: %v", err)
	}
	if _, err := svc.AddControl(context.Background(), actor, registry.AddControlRequest{
		VersionID: versionID, RefID: ref, Name: name, Description: name + " description",
	}); err != nil {
		t.Fatalf("add control %s: %v", ref, err)
	}
}
