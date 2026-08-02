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
	"encoding/json"
	"errors"
	"testing"

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/fwmap"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/registry"
)

// TestMappingSetPublishAndSecurityGate is the load-bearing test for Part A: a
// mapping set publishes and signs when its target is public, and is REFUSED when
// the target is not — the one decision that cannot be walked back, because a
// signed artifact naming a private framework's codes cannot be unshipped.
func TestMappingSetPublishAndSecurityGate(t *testing.T) {
	svc, client := newTestService(t)
	ctx := context.Background()

	if _, err := svc.BootstrapSuperAdmin(ctx, req(superAdminEmail, "Root")); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	super := mustID(t, svc, superAdminEmail)
	mustCreateUser(t, svc, super, "ms-mod@meizon.test", "Mod", "moderator", []string{"EU"})
	mod := mustID(t, svc, "ms-mod@meizon.test")

	if err := svc.GenerateSigningKey(ctx, super, "reg-ms"); err != nil {
		t.Fatalf("signing key: %v", err)
	}

	// A PUBLIC target framework: created public-domain and taken all the way to
	// PUBLISHED (which is what sets Public = true).
	publishFramework(t, svc, super, mod, "nist-csf", "public-domain")

	// A NON-PUBLIC target: a proprietary framework left as a draft. Public is
	// false, so a mapping set naming its codes must be refused.
	if _, err := svc.CreateFramework(ctx, super, registry.CreateFrameworkRequest{
		ReferenceID: "secret-fw", Name: "Secret", Region: "EU", License: "proprietary",
	}); err != nil {
		t.Fatalf("create secret-fw: %v", err)
	}

	// The SOURCE framework, with one requirement carrying a mapping to each
	// target. It stays a draft — a mapping set publishes independently of its
	// source framework's own lifecycle.
	src, err := svc.CreateFramework(ctx, super, registry.CreateFrameworkRequest{
		ReferenceID: "iso-27001", Name: "ISO 27001", Region: "EU", License: "public-domain",
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if err := svc.AddCategory(ctx, super, src.VersionID, "c1", "Org controls", "", ""); err != nil {
		t.Fatalf("add category: %v", err)
	}
	if err := svc.AddRequirement(ctx, super, src.VersionID, "c1", "A.5.1", "", "Policies", "Define policies.", "", "", ""); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	addMapping(t, svc, super, src.VersionID, "A.5.1", "nist-csf", "2.0", "GV.PO")
	addMapping(t, svc, super, src.VersionID, "A.5.1", "secret-fw", "1.0", "SEC.1")

	// Publish the set to the PUBLIC target: succeeds.
	id, err := svc.PublishMappingSet(ctx, mod, "iso-27001", "nist-csf", "2.0")
	if err != nil {
		t.Fatalf("publish to public target should succeed: %v", err)
	}
	if id == gid.Nil {
		t.Fatal("expected a mapping set id")
	}

	// The stored signed document must verify against the registry's key — the
	// real trust property a consumer relies on, exercised over the exact bytes
	// that will be served.
	keys, err := svc.VerificationKeys(ctx)
	if err != nil {
		t.Fatalf("verification keys: %v", err)
	}
	stored := storedMappingDocument(t, client, "nist-csf")
	var set fwmap.MappingSet
	if err := json.Unmarshal(stored, &set); err != nil {
		t.Fatalf("stored document is not valid JSON: %v", err)
	}
	if err := set.Verify(keys); err != nil {
		t.Fatalf("the stored signed mapping set must verify: %v", err)
	}
	if len(set.Requirements) != 1 || set.Requirements[0].SourceRef != "A.5.1" || set.Requirements[0].TargetRef != "GV.PO" {
		t.Fatalf("mapping content wrong: %+v", set.Requirements)
	}
	if set.Source.Framework != "iso-27001" || set.Target.Framework != "nist-csf" {
		t.Fatalf("endpoints wrong: %s -> %s", set.Source.Framework, set.Target.Framework)
	}

	// A mapping_published event must have been announced, naming both endpoints.
	if !mappingEventEmitted(t, client, "iso-27001", "nist-csf") {
		t.Fatal("no mapping_published event was emitted")
	}

	// Publish the set to the NON-PUBLIC target: refused, and nothing signed.
	if _, err := svc.PublishMappingSet(ctx, mod, "iso-27001", "secret-fw", "1.0"); !errors.Is(err, registry.ErrMappingTargetNotPublic) {
		t.Fatalf("publish to a non-public target must be refused, got: %v", err)
	}

	// Distribution: a consumer token can list and fetch the published set, and
	// the fetched bytes verify — the full path a GRC instance walks.
	token, err := svc.IssueToken(ctx, super, "grc-eu", []string{"EU"})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	tc, err := svc.AuthenticateToken(ctx, token)
	if err != nil {
		t.Fatalf("authenticate token: %v", err)
	}

	cat, err := svc.MappingCatalog(ctx, tc)
	if err != nil {
		t.Fatalf("mapping catalog: %v", err)
	}
	if len(cat) != 1 || cat[0].Source != "iso-27001" || cat[0].Target != "nist-csf" {
		t.Fatalf("catalog should list the one published set, got %+v", cat)
	}

	doc, err := svc.MappingSetDocument(ctx, tc, "iso-27001", "latest", "nist-csf", "2.0")
	if err != nil {
		t.Fatalf("fetch mapping set: %v", err)
	}
	var fetched fwmap.MappingSet
	if err := json.Unmarshal(doc, &fetched); err != nil {
		t.Fatalf("served document is not valid JSON: %v", err)
	}
	if err := fetched.Verify(keys); err != nil {
		t.Fatalf("the served mapping set must verify: %v", err)
	}

	// Region gating: a token scoped to a different region must not see or fetch
	// an EU set.
	usToken, err := svc.IssueToken(ctx, super, "grc-us", []string{"US"})
	if err != nil {
		t.Fatalf("issue us token: %v", err)
	}
	usTC, err := svc.AuthenticateToken(ctx, usToken)
	if err != nil {
		t.Fatalf("authenticate us token: %v", err)
	}
	usCat, err := svc.MappingCatalog(ctx, usTC)
	if err != nil {
		t.Fatalf("us mapping catalog: %v", err)
	}
	if len(usCat) != 0 {
		t.Fatalf("a US token must not see the EU mapping set, got %+v", usCat)
	}
	if _, err := svc.MappingSetDocument(ctx, usTC, "iso-27001", "latest", "nist-csf", "2.0"); !errors.Is(err, registry.ErrNotDistributable) {
		t.Fatalf("a US token must be refused the EU set, got: %v", err)
	}
}

// publishFramework drives a framework to PUBLISHED using distinct author and
// moderator so separation of duties is satisfied.
func publishFramework(t *testing.T, svc *registry.Service, author, moderator gid.GID, ref, license string) {
	t.Helper()
	ctx := context.Background()
	created, err := svc.CreateFramework(ctx, author, registry.CreateFrameworkRequest{
		ReferenceID: ref, Name: ref, Region: "EU", License: license,
	})
	if err != nil {
		t.Fatalf("create %s: %v", ref, err)
	}
	addControl(t, svc, author, ref, "GV.PO", "Governance policy")
	if err := svc.Submit(ctx, author, created.VersionID); err != nil {
		t.Fatalf("submit %s: %v", ref, err)
	}
	if err := svc.Approve(ctx, moderator, created.VersionID, "ok"); err != nil {
		t.Fatalf("approve %s: %v", ref, err)
	}
	if err := svc.Publish(ctx, moderator, created.VersionID); err != nil {
		t.Fatalf("publish %s: %v", ref, err)
	}
}

func addMapping(t *testing.T, svc *registry.Service, actor, versionID gid.GID, itemCode, targetFw, targetVer, targetItem string) {
	t.Helper()
	if _, err := svc.AddItemMapping(context.Background(), actor, registry.AddItemMappingRequest{
		VersionID: versionID, ItemCode: itemCode, Relation: "equivalent",
		TargetFramework: targetFw, TargetVersion: targetVer, TargetItem: targetItem,
	}); err != nil {
		t.Fatalf("add mapping %s->%s: %v", itemCode, targetItem, err)
	}
}

// storedMappingDocument reads the exact signed JSON that distribution will serve.
func storedMappingDocument(t *testing.T, client *pg.Client, targetCode string) []byte {
	t.Helper()
	var doc []byte
	err := client.WithConn(context.Background(), func(ctx context.Context, conn pg.Querier) error {
		return conn.QueryRow(ctx,
			`SELECT document FROM mapping_sets WHERE target_framework_code = $1 AND status = 'PUBLISHED'`,
			targetCode).Scan(&doc)
	})
	if err != nil {
		t.Fatalf("read stored mapping document: %v", err)
	}
	return doc
}

// mappingEventEmitted reports whether a mapping_published event names the given
// source and target.
func mappingEventEmitted(t *testing.T, client *pg.Client, source, target string) bool {
	t.Helper()
	var n int
	err := client.WithConn(context.Background(), func(ctx context.Context, conn pg.Querier) error {
		return conn.QueryRow(ctx,
			`SELECT count(*) FROM distribution_events
			 WHERE kind = 'mapping_published' AND framework_ref = $1 AND target_framework_ref = $2`,
			source, target).Scan(&n)
	})
	if err != nil {
		t.Fatalf("read mapping event: %v", err)
	}
	return n > 0
}
