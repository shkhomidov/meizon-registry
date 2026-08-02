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
	"errors"
	"testing"

	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/registry"
)

// TestOrgApprovalKeylessSync proves the whole keyless-sync gate against a real
// database: a pending org cannot sync, approval opens access to exactly the
// published PUBLIC frameworks (nothing tenant-private), and suspension closes it
// again immediately — all with no token anywhere.
func TestOrgApprovalKeylessSync(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.BootstrapSuperAdmin(ctx, req(superAdminEmail, "Root")); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	super := mustID(t, svc, superAdminEmail)
	mustCreateUser(t, svc, super, "org-mod@meizon.test", "Mod", "moderator", []string{"EU"})
	mod := mustID(t, svc, "org-mod@meizon.test")

	if err := svc.GenerateSigningKey(ctx, super, "reg-org"); err != nil {
		t.Fatalf("signing key: %v", err)
	}

	// Publish a PUBLIC framework (public-domain → Public=true) and leave a
	// proprietary one as a draft (never public). An approved org must see the
	// first and never the second.
	publishFramework(t, svc, super, mod, "public-fw", "public-domain")
	if _, err := svc.CreateFramework(ctx, super, registry.CreateFrameworkRequest{
		ReferenceID: "private-fw", Name: "Private", Region: "EU", License: "proprietary",
	}); err != nil {
		t.Fatalf("create private-fw: %v", err)
	}

	// An org registers — pending.
	orgTenant, err := svc.RegisterOrganization(ctx, "Acme GRC", "acme-admin@acme.test")
	if err != nil {
		t.Fatalf("register org: %v", err)
	}

	// Pending: no sync context.
	if _, err := svc.SyncContextForOrg(ctx, orgTenant); !errors.Is(err, registry.ErrOrgNotApproved) {
		t.Fatalf("pending org must be denied sync, got: %v", err)
	}

	// A non-superadmin cannot approve.
	if err := svc.ApproveOrganization(ctx, mod, orgTenant); !errors.Is(err, registry.ErrForbidden) {
		t.Fatalf("moderator must not approve orgs, got: %v", err)
	}

	// Superadmin approves — instant.
	if err := svc.ApproveOrganization(ctx, super, orgTenant); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// Approved: a sync context that sees exactly the public framework.
	tc, err := svc.SyncContextForOrg(ctx, orgTenant)
	if err != nil {
		t.Fatalf("approved org should get a sync context: %v", err)
	}
	catalog, err := svc.Catalog(ctx, tc, "", nil)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if len(catalog) != 1 || catalog[0].ID != "public-fw" {
		t.Fatalf("approved org must see only the public framework, got %+v", catalog)
	}
	// The private framework must be unreachable even by direct request.
	if _, err := svc.Bundle(ctx, tc, "private-fw", "latest"); err == nil {
		t.Fatal("approved org must not fetch a non-public framework")
	}

	// The change feed is scoped the same way.
	feed, err := svc.Changes(ctx, tc, 0, 100)
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if len(feed.Events) != 1 || feed.Events[0].Framework != "public-fw" {
		t.Fatalf("feed should carry only the public framework, got %+v", feed.Events)
	}

	// Suspend — access closes immediately, same tenant, next request.
	if err := svc.SuspendOrganization(ctx, super, orgTenant); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if _, err := svc.SyncContextForOrg(ctx, orgTenant); !errors.Is(err, registry.ErrOrgNotApproved) {
		t.Fatalf("a suspended org must be denied sync, got: %v", err)
	}
}

// TestUnknownOrgDeniedWithoutDisclosure: an unknown tenant is refused with the
// same error as a pending/suspended one — the surface never reveals whether an
// org exists.
func TestUnknownOrgDeniedWithoutDisclosure(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	if _, err := svc.BootstrapSuperAdmin(ctx, req(superAdminEmail, "Root")); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	if _, err := svc.SyncContextForOrg(ctx, gid.NewTenantID()); !errors.Is(err, registry.ErrOrgNotApproved) {
		t.Fatalf("unknown org must be denied with ErrOrgNotApproved, got: %v", err)
	}
}
