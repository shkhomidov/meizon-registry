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
	"sort"
	"testing"

	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/registry"
)

func listRefs(t *testing.T, svc *registry.Service, actor gid.GID) []string {
	t.Helper()
	items, err := svc.FrameworkList(context.Background(), actor)
	if err != nil {
		t.Fatalf("list frameworks: %v", err)
	}
	refs := make([]string, 0, len(items))
	for _, it := range items {
		refs = append(refs, it.ID)
	}
	sort.Strings(refs)
	return refs
}

func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	w := append([]string(nil), want...)
	sort.Strings(w)
	for i := range got {
		if got[i] != w[i] {
			return false
		}
	}
	return true
}

// TestFrameworkOwnershipRBAC covers the three role rules: an auditor sees and
// manages only their own frameworks; a superadmin can delete a framework; and
// delete is denied to moderators and auditors.
func TestFrameworkOwnershipRBAC(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.BootstrapSuperAdmin(ctx, req(superAdminEmail, "Root")); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	super := mustID(t, svc, superAdminEmail)
	mustCreateUser(t, svc, super, "aud1@meizon.test", "Aud1", "auditor", []string{"EU"})
	mustCreateUser(t, svc, super, "aud2@meizon.test", "Aud2", "auditor", []string{"EU"})
	mustCreateUser(t, svc, super, "mod@meizon.test", "Mod", "moderator", []string{"EU"})
	aud1 := mustID(t, svc, "aud1@meizon.test")
	aud2 := mustID(t, svc, "aud2@meizon.test")
	mod := mustID(t, svc, "mod@meizon.test")

	// aud1 owns fw-a, aud2 owns fw-b.
	if _, err := svc.CreateFramework(ctx, aud1, registry.CreateFrameworkRequest{ReferenceID: "fw-a", Name: "A", Region: "EU", License: "public-domain"}); err != nil {
		t.Fatalf("aud1 create: %v", err)
	}
	if _, err := svc.CreateFramework(ctx, aud2, registry.CreateFrameworkRequest{ReferenceID: "fw-b", Name: "B", Region: "EU", License: "public-domain"}); err != nil {
		t.Fatalf("aud2 create: %v", err)
	}

	// 1. Listing is owner-scoped for auditors, full for moderator/superadmin.
	if got := listRefs(t, svc, aud1); !sameSet(got, []string{"fw-a"}) {
		t.Fatalf("aud1 should see only fw-a, got %v", got)
	}
	if got := listRefs(t, svc, aud2); !sameSet(got, []string{"fw-b"}) {
		t.Fatalf("aud2 should see only fw-b, got %v", got)
	}
	if got := listRefs(t, svc, mod); !sameSet(got, []string{"fw-a", "fw-b"}) {
		t.Fatalf("moderator should see both, got %v", got)
	}
	if got := listRefs(t, svc, super); !sameSet(got, []string{"fw-a", "fw-b"}) {
		t.Fatalf("superadmin should see both, got %v", got)
	}

	// 2. EnsureFrameworkAccess: auditor own ok, auditor other forbidden; mod/super any.
	fwA, err := svc.FrameworkByReference(ctx, "fw-a")
	if err != nil {
		t.Fatalf("load fw-a: %v", err)
	}
	fwB, err := svc.FrameworkByReference(ctx, "fw-b")
	if err != nil {
		t.Fatalf("load fw-b: %v", err)
	}
	if err := svc.EnsureFrameworkAccess(ctx, aud1, fwA); err != nil {
		t.Fatalf("aud1 should access own fw-a: %v", err)
	}
	if err := svc.EnsureFrameworkAccess(ctx, aud1, fwB); !errors.Is(err, registry.ErrForbidden) {
		t.Fatalf("aud1 must NOT access fw-b, got %v", err)
	}
	if err := svc.EnsureFrameworkAccess(ctx, mod, fwB); err != nil {
		t.Fatalf("moderator should access any framework: %v", err)
	}
	if err := svc.EnsureFrameworkAccess(ctx, super, fwB); err != nil {
		t.Fatalf("superadmin should access any framework: %v", err)
	}

	// 3. Delete: only superadmin. Auditor (even own) and moderator are denied.
	if err := svc.DeleteFramework(ctx, aud1, "fw-a"); !errors.Is(err, registry.ErrForbidden) {
		t.Fatalf("auditor must not delete, got %v", err)
	}
	if err := svc.DeleteFramework(ctx, mod, "fw-a"); !errors.Is(err, registry.ErrForbidden) {
		t.Fatalf("moderator must not delete, got %v", err)
	}
	if err := svc.DeleteFramework(ctx, super, "fw-a"); err != nil {
		t.Fatalf("superadmin should delete: %v", err)
	}
	// fw-a and its version are gone (cascade).
	if _, err := svc.FrameworkByReference(ctx, "fw-a"); !errors.Is(err, coredata.ErrResourceNotFound) {
		t.Fatalf("fw-a should be deleted, got %v", err)
	}
}
