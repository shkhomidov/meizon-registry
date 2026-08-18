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
	"testing"

	"go.meizon.cloud/registry/pkg/registry"
)

// TestBootstrapResetsSuperAdminPassword pins the recovery path for a forgotten
// superadmin password: re-running superadmin bootstrap for an existing
// allowlisted identity sets a new password, the new one authenticates, and the
// old one no longer does.
func TestBootstrapResetsSuperAdminPassword(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.BootstrapSuperAdmin(ctx, req(superAdminEmail, "Root")); err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	if _, err := svc.Authenticate(ctx, superAdminEmail, pw); err != nil {
		t.Fatalf("the original password must authenticate: %v", err)
	}

	// Re-run bootstrap with a different password — the reset.
	const newPassword = "reset-password-6789"
	reset := registry.CreateIdentityRequest{Email: superAdminEmail, FullName: "Root", Password: newPassword}
	if _, err := svc.BootstrapSuperAdmin(ctx, reset); err != nil {
		t.Fatalf("re-bootstrap (reset): %v", err)
	}

	if _, err := svc.Authenticate(ctx, superAdminEmail, newPassword); err != nil {
		t.Fatalf("the reset password must authenticate: %v", err)
	}
	if _, err := svc.Authenticate(ctx, superAdminEmail, pw); err == nil {
		t.Fatal("the old password must no longer authenticate after a reset")
	}
}
