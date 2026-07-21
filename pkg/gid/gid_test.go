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

package gid_test

import (
	"testing"

	"go.meizon.cloud/registry/pkg/gid"
)

func TestGIDEmbedsTenantAndType(t *testing.T) {
	tenant := gid.NewTenantID()
	const entityType uint16 = 4

	id := gid.New(tenant, entityType)

	if id.TenantID() != tenant {
		t.Fatal("tenant id not recoverable from GID")
	}
	if id.EntityType() != entityType {
		t.Fatalf("entity type = %d, want %d", id.EntityType(), entityType)
	}
	if !id.IsValid() {
		t.Fatal("expected valid GID")
	}
}

func TestGIDStringRoundTrip(t *testing.T) {
	id := gid.New(gid.NewTenantID(), 7)

	parsed, err := gid.ParseGID(id.String())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed != id {
		t.Fatal("GID did not survive string round trip")
	}
}

func TestTenantIsolationDistinct(t *testing.T) {
	a := gid.NewTenantID()
	b := gid.NewTenantID()
	if a == b {
		t.Fatal("expected distinct tenant ids")
	}
	if !a.IsValid() {
		t.Fatal("generated tenant should be valid")
	}
	if gid.NilTenant.IsValid() {
		t.Fatal("nil tenant should be invalid")
	}
}
