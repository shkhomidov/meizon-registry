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
)

// TestTokenRevokeReinstateDelete covers the lifecycle a superadmin drives from
// the tokens page: a live token stops authenticating when inactivated, works
// again when reinstated, and is gone for good once deleted.
func TestTokenRevokeReinstateDelete(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.BootstrapSuperAdmin(ctx, req(superAdminEmail, "Root")); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	superID := mustID(t, svc, superAdminEmail)

	plaintext, err := svc.IssueToken(ctx, superID, "grc-eu", nil)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// Find the token's id via the admin listing (issue returns only the secret).
	tokens, err := svc.ListTokens(ctx)
	if err != nil || len(tokens) != 1 {
		t.Fatalf("list: err=%v n=%d", err, len(tokens))
	}
	id := tokens[0].ID

	// Live: authenticates.
	if _, err := svc.AuthenticateToken(ctx, plaintext); err != nil {
		t.Fatalf("fresh token must authenticate: %v", err)
	}

	// Inactivate: stops authenticating, row kept and flagged.
	if err := svc.RevokeToken(ctx, superID, id); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := svc.AuthenticateToken(ctx, plaintext); err == nil {
		t.Fatal("a revoked token must not authenticate")
	}
	if tokens, _ := svc.ListTokens(ctx); len(tokens) != 1 || !tokens[0].Revoked {
		t.Fatalf("revoked token must still be listed and flagged, got %+v", tokens)
	}

	// Reinstate: the same secret works again.
	if err := svc.ReinstateToken(ctx, superID, id); err != nil {
		t.Fatalf("reinstate: %v", err)
	}
	if _, err := svc.AuthenticateToken(ctx, plaintext); err != nil {
		t.Fatalf("a reinstated token must authenticate again: %v", err)
	}

	// Delete: gone from the listing and unusable forever.
	if err := svc.DeleteToken(ctx, superID, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.AuthenticateToken(ctx, plaintext); err == nil {
		t.Fatal("a deleted token must not authenticate")
	}
	if tokens, _ := svc.ListTokens(ctx); len(tokens) != 0 {
		t.Fatalf("deleted token must be gone, got %d", len(tokens))
	}
}
